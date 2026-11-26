package main

import "fmt"

// paasPlanQuota is the resource ceiling of one PaaS plan tier.
type paasPlanQuota struct {
	MemoryMB  int
	VCPUMilli int
	DiskGB    int
}

var paasPlanQuotas = map[string]paasPlanQuota{
	"starter":  {MemoryMB: 256, VCPUMilli: 500, DiskGB: 2},
	"pro":      {MemoryMB: 512, VCPUMilli: 1000, DiskGB: 10},
	"business": {MemoryMB: 1024, VCPUMilli: 2000, DiskGB: 25},
}

// paasQuotaEnforce fails closed: unknown plans and any over-quota dimension are rejected.
func paasQuotaEnforce(plan string, requested paasPlanQuota) error {
	limit, known := paasPlanQuotas[plan]
	if !known {
		return fmt.Errorf("unknown plan %q", plan)
	}
	if requested.MemoryMB <= 0 || requested.VCPUMilli <= 0 || requested.DiskGB <= 0 {
		return fmt.Errorf("non-positive resource request")
	}
	if requested.MemoryMB > limit.MemoryMB {
		return fmt.Errorf("memory %dMB exceeds %s quota %dMB", requested.MemoryMB, plan, limit.MemoryMB)
	}
	if requested.VCPUMilli > limit.VCPUMilli {
		return fmt.Errorf("cpu %dm exceeds %s quota %dm", requested.VCPUMilli, plan, limit.VCPUMilli)
	}
	if requested.DiskGB > limit.DiskGB {
		return fmt.Errorf("disk %dGB exceeds %s quota %dGB", requested.DiskGB, plan, limit.DiskGB)
	}
	return nil
}
