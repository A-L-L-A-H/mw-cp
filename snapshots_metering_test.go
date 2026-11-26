package main

import (
	"testing"
	"time"
)

func TestSnapshotPlanAndRestore(t *testing.T) {
	plan, err := snapshotPlanFor("acme", "data", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil || plan.ArchiveKey != "snapshots/acme/data/20260102T030405Z.tar.zst.age" {
		t.Fatalf("%v %+v", err, plan)
	}
	if err := snapshotRestorePlan("acme", plan.ArchiveKey); err != nil {
		t.Fatal(err)
	}
	if snapshotRestorePlan("other", plan.ArchiveKey) == nil {
		t.Fatal("cross-tenant restore must fail")
	}
	if snapshotRestorePlan("acme", "snapshots/acme/../other/x") == nil {
		t.Fatal("traversal must fail")
	}
	if _, err := snapshotPlanFor("../x", "data", time.Now()); err == nil {
		t.Fatal("expected tenant rejection")
	}
}

func TestMeteringRecord(t *testing.T) {
	ledger := map[string]meteringUsage{}
	meteringRecord(ledger, "b", 256, 2, time.Hour)
	meteringRecord(ledger, "a", 512, 10, 30*time.Minute)
	meteringRecord(ledger, "b", 256, 2, time.Hour)
	if ledger["b"].MemoryMBSeconds != 256*7200 || ledger["b"].DiskGBSeconds != 2*7200 {
		t.Fatalf("%+v", ledger["b"])
	}
	tenants := meteringTenants(ledger)
	if len(tenants) != 2 || tenants[0] != "a" {
		t.Fatalf("%v", tenants)
	}
}
