package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMockDockerDriverLifecycle(t *testing.T) {
	ctx := context.Background()
	driver := newMockDockerDriver()
	driver.LogData["tenant-a"] = "server started on :8080\n"

	if err := driver.DeployCompose(ctx, "tenant-a", "/tmp/tenant-a/docker-compose.yml"); err != nil {
		t.Fatalf("deploy failed: %v", err)
	}
	if driver.States["tenant-a"] != "running" {
		t.Fatalf("expected running, got %q", driver.States["tenant-a"])
	}

	if err := driver.Pause(ctx, "tenant-a"); err != nil {
		t.Fatalf("pause failed: %v", err)
	}
	if driver.States["tenant-a"] != "paused" {
		t.Fatalf("expected paused, got %q", driver.States["tenant-a"])
	}

	if err := driver.Unpause(ctx, "tenant-a"); err != nil {
		t.Fatalf("unpause failed: %v", err)
	}
	if driver.States["tenant-a"] != "running" {
		t.Fatalf("expected running after unpause, got %q", driver.States["tenant-a"])
	}

	logs, err := driver.Logs(ctx, "tenant-a", 50)
	if err != nil || logs != "server started on :8080\n" {
		t.Fatalf("unexpected logs: %q err=%v", logs, err)
	}
	if len(driver.Calls) != 4 {
		t.Fatalf("expected 4 recorded calls, got %d", len(driver.Calls))
	}

	driver.FailOn["pause"] = errors.New("daemon unavailable")
	if err := driver.Pause(ctx, "tenant-a"); err == nil {
		t.Fatal("expected injected pause error")
	}
}

func TestCLIDockerDriverValidation(t *testing.T) {
	driver := newCLIDockerDriver("docker", "unix:///var/run/docker.sock")
	if err := driver.DeployCompose(context.Background(), "../bad", "/tmp/compose.yml"); err == nil {
		t.Fatal("expected invalid project slug error")
	}
	if err := driver.Pause(context.Background(), ""); err == nil {
		t.Fatal("expected empty container error")
	}
}

func TestBuildPaasContainerSpecEnforcesSecurityAndResourceHardLimits(t *testing.T) {
	// Default request without explicit MemoryLimit or PIDsLimit still gets hard limits + cap-drop=ALL + no-new-privileges.
	req := PaasDeployRequest{
		Slug:     "billing-web",
		TenantID: "acme",
		Image:    "nginx:1.27-alpine",
		Port:     8080,
		Env:      map[string]string{"B_VAR": "2", "A_VAR": "1"},
	}
	spec := BuildPaasContainerSpec(req, "mw-paas-billing-web", "mw-tenant-acme")
	if len(spec.CapDrop) != 1 || spec.CapDrop[0] != "ALL" {
		t.Fatalf("expected CapDrop=[\"ALL\"], got %+v", spec.CapDrop)
	}
	if len(spec.SecurityOpt) != 1 || spec.SecurityOpt[0] != "no-new-privileges:true" {
		t.Fatalf("expected SecurityOpt=[\"no-new-privileges:true\"], got %+v", spec.SecurityOpt)
	}
	if spec.MemoryLimit != "256m" || spec.MemorySwap != "256m" {
		t.Fatalf("expected default hard RAM limit 256m/256m, got %s/%s", spec.MemoryLimit, spec.MemorySwap)
	}
	if spec.PIDsLimit != 256 {
		t.Fatalf("expected default PIDsLimit 256, got %d", spec.PIDsLimit)
	}

	args := spec.DockerRunArgs()
	joined := " " + strings.Join(args, " ") + " "
	for _, required := range []string{
		" --cap-drop=ALL ",
		" --security-opt=no-new-privileges:true ",
		" --pids-limit=256 ",
		" --memory 256m ",
		" --memory-swap 256m ",
		" --network mw-tenant-acme ",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("expected %q in docker run args: %s", required, joined)
		}
	}

	// Quota-derived spec enforces plan quota ceiling and maps to hardened container spec.
	qSpec, err := BuildQuotaContainerSpec("acme", "pro", "acme-api", "node:22-alpine", paasPlanQuota{
		MemoryMB:  512,
		VCPUMilli: 1000,
		DiskGB:    10,
	}, 512)
	if err != nil {
		t.Fatalf("unexpected BuildQuotaContainerSpec error: %v", err)
	}
	if qSpec.MemoryLimit != "512m" || qSpec.CPULimit != "1.00" || qSpec.PIDsLimit != 512 {
		t.Fatalf("unexpected quota container spec limits: %+v", qSpec)
	}
	if _, err := BuildQuotaContainerSpec("acme", "starter", "acme-api", "node:22-alpine", paasPlanQuota{
		MemoryMB:  1024,
		VCPUMilli: 500,
		DiskGB:    2,
	}, 256); err == nil {
		t.Fatal("expected over-quota container spec to fail closed")
	}
}
