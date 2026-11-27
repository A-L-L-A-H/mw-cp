package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

const (
	defaultPaasMemoryLimit = "256m"
	defaultPaasPIDsLimit   = 256
	maxPaasPIDsLimit       = 4096
)

// PaasContainerSpec is the hardened launch specification for a PaaS container.
type PaasContainerSpec struct {
	ContainerName string   `json:"container_name"`
	Image         string   `json:"image"`
	Restart       string   `json:"restart"`
	Network       string   `json:"network,omitempty"`
	Labels        []string `json:"labels,omitempty"`
	Volumes       []string `json:"volumes,omitempty"`
	Env           []string `json:"env,omitempty"`
	CapDrop       []string `json:"cap_drop"`
	SecurityOpt   []string `json:"security_opt"`
	MemoryLimit   string   `json:"memory_limit"`
	MemorySwap    string   `json:"memory_swap"`
	CPULimit      string   `json:"cpu_limit,omitempty"`
	PIDsLimit     int      `json:"pids_limit"`
}

// BuildPaasContainerSpec constructs a hardened PaaS container launch spec enforcing
// cap-drop=ALL, no-new-privileges, and PID/RAM hard limits fail-closed.
func BuildPaasContainerSpec(req PaasDeployRequest, containerName, dockerNetwork string) PaasContainerSpec {
	memLimit := strings.TrimSpace(req.MemoryLimit)
	if memLimit == "" {
		memLimit = defaultPaasMemoryLimit
	}
	pidsLimit := req.PIDsLimit
	if pidsLimit <= 0 {
		pidsLimit = defaultPaasPIDsLimit
	} else if pidsLimit > maxPaasPIDsLimit {
		pidsLimit = maxPaasPIDsLimit
	}

	envEntries := make([]string, 0, len(req.Env))
	if len(req.Env) > 0 {
		keys := make([]string, 0, len(req.Env))
		for k := range req.Env {
			keys = append(keys, k)
		}
		// Keep deterministic order matching sort.Strings in paas.go.
		for i := 0; i < len(keys); i++ {
			for j := i + 1; j < len(keys); j++ {
				if keys[j] < keys[i] {
					keys[i], keys[j] = keys[j], keys[i]
				}
			}
		}
		for _, k := range keys {
			envEntries = append(envEntries, k+"="+req.Env[k])
		}
	}

	return PaasContainerSpec{
		ContainerName: containerName,
		Image:         req.Image,
		Restart:       "unless-stopped",
		Network:       strings.TrimSpace(dockerNetwork),
		Labels: []string{
			"mw.paas.app=" + req.Slug,
			"mw.paas.tenant=" + req.TenantID,
			"mw.paas.managed=true",
		},
		Volumes:     append([]string(nil), req.Volumes...),
		Env:         envEntries,
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
		MemoryLimit: memLimit,
		MemorySwap:  memLimit,
		CPULimit:    strings.TrimSpace(req.CPULimit),
		PIDsLimit:   pidsLimit,
	}
}

// BuildQuotaContainerSpec validates paid plan quota ceilings and builds a hardened PaaS container spec.
func BuildQuotaContainerSpec(tenant, plan, slug, image string, quota paasPlanQuota, pidsLimit int) (PaasContainerSpec, error) {
	if err := paasQuotaEnforce(plan, quota); err != nil {
		return PaasContainerSpec{}, err
	}
	req := PaasDeployRequest{
		Slug:        slug,
		TenantID:    tenant,
		Image:       image,
		MemoryLimit: fmt.Sprintf("%dm", quota.MemoryMB),
		CPULimit:    fmt.Sprintf("%.2f", float64(quota.VCPUMilli)/1000.0),
		PIDsLimit:   pidsLimit,
	}
	netName, _ := templateTenantNetwork(slug)
	return BuildPaasContainerSpec(req, PaasContainerName(slug), netName), nil
}

// DockerRunArgs renders hardened `docker run` CLI arguments from the container spec.
func (s PaasContainerSpec) DockerRunArgs() []string {
	restart := s.Restart
	if restart == "" {
		restart = "unless-stopped"
	}
	memLimit := strings.TrimSpace(s.MemoryLimit)
	if memLimit == "" {
		memLimit = defaultPaasMemoryLimit
	}
	memSwap := strings.TrimSpace(s.MemorySwap)
	if memSwap == "" {
		memSwap = memLimit
	}
	pidsLimit := s.PIDsLimit
	if pidsLimit <= 0 {
		pidsLimit = defaultPaasPIDsLimit
	}

	args := []string{
		"run", "-d",
		"--name", s.ContainerName,
		"--restart", restart,
	}

	capDrop := s.CapDrop
	if len(capDrop) == 0 {
		capDrop = []string{"ALL"}
	}
	for _, capName := range capDrop {
		args = append(args, "--cap-drop="+capName)
	}

	secOpts := s.SecurityOpt
	if len(secOpts) == 0 {
		secOpts = []string{"no-new-privileges:true"}
	}
	for _, opt := range secOpts {
		args = append(args, "--security-opt="+opt)
	}

	args = append(args,
		"--pids-limit="+strconv.Itoa(pidsLimit),
		"--memory", memLimit,
		"--memory-swap", memSwap,
	)
	if s.CPULimit != "" {
		args = append(args, "--cpus", s.CPULimit)
	}
	for _, lbl := range s.Labels {
		args = append(args, "--label", lbl)
	}
	if s.Network != "" {
		args = append(args, "--network", s.Network)
	}
	for _, vol := range s.Volumes {
		args = append(args, "-v", vol)
	}
	for _, envKV := range s.Env {
		args = append(args, "-e", envKV)
	}
	args = append(args, s.Image)
	return args
}

// DockerDriver abstracts container lifecycle operations for PaaS workloads.
type DockerDriver interface {
	DeployCompose(ctx context.Context, project, composePath string) error
	Pause(ctx context.Context, container string) error
	Unpause(ctx context.Context, container string) error
	Logs(ctx context.Context, container string, tail int) (string, error)
}

// CLIDockerDriver executes Docker operations via the Docker CLI (respecting optional socket/host).
type CLIDockerDriver struct {
	DockerBin  string
	DockerHost string
}

func newCLIDockerDriver(dockerBin, dockerHost string) *CLIDockerDriver {
	if strings.TrimSpace(dockerBin) == "" {
		dockerBin = "docker"
	}
	return &CLIDockerDriver{
		DockerBin:  dockerBin,
		DockerHost: strings.TrimSpace(dockerHost),
	}
}

func (d *CLIDockerDriver) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, d.DockerBin, args...)
	if d.DockerHost != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+d.DockerHost)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (d *CLIDockerDriver) DeployCompose(ctx context.Context, project, composePath string) error {
	if !templateSlugPattern.MatchString(project) {
		return fmt.Errorf("invalid compose project %q", project)
	}
	if strings.TrimSpace(composePath) == "" {
		return fmt.Errorf("missing compose path")
	}
	_, err := d.run(ctx, "compose", "-p", project, "-f", composePath, "up", "-d")
	return err
}

func (d *CLIDockerDriver) Pause(ctx context.Context, container string) error {
	if strings.TrimSpace(container) == "" {
		return fmt.Errorf("missing container name")
	}
	_, err := d.run(ctx, "pause", container)
	return err
}

func (d *CLIDockerDriver) Unpause(ctx context.Context, container string) error {
	if strings.TrimSpace(container) == "" {
		return fmt.Errorf("missing container name")
	}
	_, err := d.run(ctx, "unpause", container)
	return err
}

func (d *CLIDockerDriver) Logs(ctx context.Context, container string, tail int) (string, error) {
	if strings.TrimSpace(container) == "" {
		return "", fmt.Errorf("missing container name")
	}
	if tail <= 0 {
		tail = 100
	}
	return d.run(ctx, "logs", "--tail", strconv.Itoa(tail), container)
}

// DockerCall records one operation invoked on MockDockerDriver.
type DockerCall struct {
	Action string
	Target string
	Extra  string
}

// MockDockerDriver is a deterministic in-memory DockerDriver for CI and unit tests.
type MockDockerDriver struct {
	mu      sync.Mutex
	Calls   []DockerCall
	States  map[string]string
	LogData map[string]string
	FailOn  map[string]error
}

func newMockDockerDriver() *MockDockerDriver {
	return &MockDockerDriver{
		States:  make(map[string]string),
		LogData: make(map[string]string),
		FailOn:  make(map[string]error),
	}
}

func (m *MockDockerDriver) DeployCompose(_ context.Context, project, composePath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, DockerCall{Action: "deploy", Target: project, Extra: composePath})
	if err := m.FailOn["deploy"]; err != nil {
		return err
	}
	m.States[project] = "running"
	return nil
}

func (m *MockDockerDriver) Pause(_ context.Context, container string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, DockerCall{Action: "pause", Target: container})
	if err := m.FailOn["pause"]; err != nil {
		return err
	}
	m.States[container] = "paused"
	return nil
}

func (m *MockDockerDriver) Unpause(_ context.Context, container string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, DockerCall{Action: "unpause", Target: container})
	if err := m.FailOn["unpause"]; err != nil {
		return err
	}
	m.States[container] = "running"
	return nil
}

func (m *MockDockerDriver) Logs(_ context.Context, container string, tail int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, DockerCall{Action: "logs", Target: container, Extra: strconv.Itoa(tail)})
	if err := m.FailOn["logs"]; err != nil {
		return "", err
	}
	return m.LogData[container], nil
}
