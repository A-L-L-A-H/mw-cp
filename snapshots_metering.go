package main

import (
	"fmt"
	"path"
	"sort"
	"time"
)

// snapshotPlan describes an encrypted volume snapshot for a tenant.
type snapshotPlan struct {
	Tenant     string
	Volume     string
	ArchiveKey string
	Cipher     string
}

// snapshotPlanFor builds a tenant-scoped, encrypted snapshot plan.
func snapshotPlanFor(tenant, volume string, at time.Time) (snapshotPlan, error) {
	if !templateSlugPattern.MatchString(tenant) {
		return snapshotPlan{}, fmt.Errorf("invalid tenant %q", tenant)
	}
	if !templateSlugPattern.MatchString(volume) {
		return snapshotPlan{}, fmt.Errorf("invalid volume %q", volume)
	}
	key := path.Join("snapshots", tenant, volume, at.UTC().Format("20060102T150405Z")+".tar.zst.age")
	return snapshotPlan{Tenant: tenant, Volume: volume, ArchiveKey: key, Cipher: "age-x25519"}, nil
}

// snapshotRestorePlan validates that an archive key belongs to the tenant before restore.
func snapshotRestorePlan(tenant, archiveKey string) error {
	prefix := path.Join("snapshots", tenant) + "/"
	if !templateSlugPattern.MatchString(tenant) || len(archiveKey) <= len(prefix) || archiveKey[:len(prefix)] != prefix || path.Clean(archiveKey) != archiveKey {
		return fmt.Errorf("archive %q not owned by tenant %q", archiveKey, tenant)
	}
	return nil
}

// meteringUsage accumulates per-tenant usage.
type meteringUsage struct {
	MemoryMBSeconds float64
	DiskGBSeconds   float64
}

// meteringRecord accumulates usage over a duration.
func meteringRecord(ledger map[string]meteringUsage, tenant string, memoryMB, diskGB int, duration time.Duration) {
	entry := ledger[tenant]
	entry.MemoryMBSeconds += float64(memoryMB) * duration.Seconds()
	entry.DiskGBSeconds += float64(diskGB) * duration.Seconds()
	ledger[tenant] = entry
}

func meteringTenants(ledger map[string]meteringUsage) []string {
	tenants := make([]string, 0, len(ledger))
	for tenant := range ledger {
		tenants = append(tenants, tenant)
	}
	sort.Strings(tenants)
	return tenants
}
