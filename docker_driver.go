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
