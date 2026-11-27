package main

import (
	"context"
	"errors"
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
