package main

import (
	"testing"
	"time"
)

func TestAutosleepLifecycle(t *testing.T) {
	manager := newAutosleepManager(10 * time.Minute)
	base := time.Unix(1000, 0)
	if manager.Touch("c1", base) {
		t.Fatal("fresh container must not need wake")
	}
	if got := manager.Sweep(base.Add(9 * time.Minute)); len(got) != 0 {
		t.Fatalf("early sleep %v", got)
	}
	got := manager.Sweep(base.Add(10 * time.Minute))
	if len(got) != 1 || got[0] != "c1" || manager.State("c1") != autosleepAsleep {
		t.Fatalf("expected sleep, got %v", got)
	}
	if len(manager.Sweep(base.Add(time.Hour))) != 0 {
		t.Fatal("already asleep must not repeat")
	}
	if !manager.Touch("c1", base.Add(2*time.Hour)) || manager.State("c1") != autosleepAwake {
		t.Fatal("expected wake")
	}
}
