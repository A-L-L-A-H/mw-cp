package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// paasCommercialHandler wires commercial PaaS endpoints (T30/T31/T32/T33).
type paasCommercialHandler struct {
	templatesRoot string
	outputBaseDir string
	driver        DockerDriver
	domains       *customDomainRegistry
	autosleep     *autosleepManager

	ledgerMu    sync.RWMutex
	ledger      map[string]meteringUsage
	snapshotsMu sync.RWMutex
	snapshots   map[string][]snapshotPlan
	nowFn       func() time.Time
}

func newPaasCommercialHandler(templatesRoot, outputBaseDir string, driver DockerDriver, domains *customDomainRegistry) *paasCommercialHandler {
	if strings.TrimSpace(templatesRoot) == "" {
		templatesRoot = env("MW_TEMPLATES_ROOT", "../mw-templates")
	}
	if strings.TrimSpace(outputBaseDir) == "" {
		outputBaseDir = env("MW_PAAS_DEPLOY_DIR", filepath.Join(os.TempDir(), "mw-paas-deploys"))
	}
	if driver == nil {
		driver = newCLIDockerDriver(env("DOCKER_BIN", "docker"), os.Getenv("DOCKER_HOST"))
	}
	if domains == nil {
		domains = newCustomDomainRegistry(env("MW_PAAS_BASE_DOMAIN", "moonwitness.app"), nil)
	}
	return &paasCommercialHandler{
		templatesRoot: templatesRoot,
		outputBaseDir: outputBaseDir,
		driver:        driver,
		domains:       domains,
		autosleep:     newAutosleepManager(15 * time.Minute),
		ledger:        make(map[string]meteringUsage),
		snapshots:     make(map[string][]snapshotPlan),
		nowFn:         time.Now,
	}
}

type paasTemplateDeployRequest struct {
	Tenant    string `json:"tenant"`
	Plan      string `json:"plan"`
	Pack      string `json:"pack"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	MemoryMB  int    `json:"memory_mb"`
	VCPUMilli int    `json:"vcpu_milli"`
	DiskGB    int    `json:"disk_gb"`
}

// handleTemplateDeploy handles POST /paas/templates/deploy.
func (h *paasCommercialHandler) handleTemplateDeploy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req paasTemplateDeployRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writePaasJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json payload"})
			return
		}

		if claims, ok := ridTenantClaimsFromContext(r.Context()); ok {
			if req.Tenant != "" && req.Tenant != claims.TenantID {
				writePaasJSON(w, http.StatusForbidden, map[string]string{"error": "tenant mismatch"})
				return
			}
			req.Tenant = claims.TenantID
			if req.Plan == "" {
				req.Plan = claims.PlanTier
			}
		}

		if !templateSlugPattern.MatchString(req.Tenant) || !templateSlugPattern.MatchString(req.Slug) {
			writePaasJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant or slug"})
			return
		}
		if req.Plan == "" {
			req.Plan = "starter"
		}
		if req.MemoryMB == 0 || req.VCPUMilli == 0 || req.DiskGB == 0 {
			if defaults, ok := paasPlanQuotas[req.Plan]; ok {
				if req.MemoryMB == 0 {
					req.MemoryMB = defaults.MemoryMB
				}
				if req.VCPUMilli == 0 {
					req.VCPUMilli = defaults.VCPUMilli
				}
				if req.DiskGB == 0 {
					req.DiskGB = defaults.DiskGB
				}
			}
		}

		requested := paasPlanQuota{
			MemoryMB:  req.MemoryMB,
			VCPUMilli: req.VCPUMilli,
			DiskGB:    req.DiskGB,
		}
		if err := paasQuotaEnforce(req.Plan, requested); err != nil {
			writePaasJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}

		outDir := filepath.Join(h.outputBaseDir, req.Tenant, req.Slug)
		composePath, err := templateDeploy(h.templatesRoot, req.Pack, req.Name, req.Slug, outDir)
		if err != nil {
			writePaasJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		if err := h.driver.DeployCompose(r.Context(), req.Slug, composePath); err != nil {
			writePaasJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		network, _ := templateTenantNetwork(req.Slug)
		h.ledgerMu.Lock()
		meteringRecord(h.ledger, req.Tenant, requested.MemoryMB, requested.DiskGB, time.Minute)
		h.ledgerMu.Unlock()

		writePaasJSON(w, http.StatusCreated, map[string]any{
			"status":       "deployed",
			"tenant":       req.Tenant,
			"plan":         req.Plan,
			"slug":         req.Slug,
			"network":      network,
			"compose_path": composePath,
		})
	}
}

// handleTenantQuotas handles GET /paas/quotas/:tenant.
func (h *paasCommercialHandler) handleTenantQuotas() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant := strings.TrimPrefix(r.URL.Path, "/paas/quotas/")
		tenant = strings.Trim(tenant, "/")
		if !templateSlugPattern.MatchString(tenant) {
			writePaasJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant"})
			return
		}

		plan := strings.TrimSpace(r.URL.Query().Get("plan"))
		if claims, ok := ridTenantClaimsFromContext(r.Context()); ok {
			if tenant != claims.TenantID {
				writePaasJSON(w, http.StatusForbidden, map[string]string{"error": "tenant mismatch"})
				return
			}
			if plan == "" {
				plan = claims.PlanTier
			}
		}
		if plan == "" {
			plan = "starter"
		}

		quota, known := paasPlanQuotas[plan]
		if !known {
			writePaasJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown plan"})
			return
		}

		writePaasJSON(w, http.StatusOK, map[string]any{
			"tenant":     tenant,
			"plan":       plan,
			"memory_mb":  quota.MemoryMB,
			"vcpu_milli": quota.VCPUMilli,
			"disk_gb":    quota.DiskGB,
		})
	}
}

// handleTenantUsage handles GET /paas/usage/:tenant.
func (h *paasCommercialHandler) handleTenantUsage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant := strings.TrimPrefix(r.URL.Path, "/paas/usage/")
		tenant = strings.Trim(tenant, "/")
		if !templateSlugPattern.MatchString(tenant) {
			writePaasJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant"})
			return
		}
		if claims, ok := ridTenantClaimsFromContext(r.Context()); ok && tenant != claims.TenantID {
			writePaasJSON(w, http.StatusForbidden, map[string]string{"error": "tenant mismatch"})
			return
		}

		h.ledgerMu.RLock()
		usage := h.ledger[tenant]
		h.ledgerMu.RUnlock()

		writePaasJSON(w, http.StatusOK, map[string]any{
			"tenant":            tenant,
			"memory_mb_seconds": usage.MemoryMBSeconds,
			"disk_gb_seconds":   usage.DiskGBSeconds,
		})
	}
}

// handleAppSnapshot handles POST /paas/apps/:slug/snapshots.
func (h *paasCommercialHandler) handleAppSnapshot(w http.ResponseWriter, r *http.Request, slug string) {
	var body struct {
		Tenant string `json:"tenant"`
		Volume string `json:"volume"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if claims, ok := ridTenantClaimsFromContext(r.Context()); ok {
		if body.Tenant != "" && body.Tenant != claims.TenantID {
			writePaasJSON(w, http.StatusForbidden, map[string]string{"error": "tenant mismatch"})
			return
		}
		body.Tenant = claims.TenantID
	}
	if body.Tenant == "" {
		body.Tenant = slug
	}
	if body.Volume == "" {
		body.Volume = slug + "-data"
	}

	plan, err := snapshotPlanFor(body.Tenant, body.Volume, h.nowFn())
	if err != nil {
		writePaasJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	h.snapshotsMu.Lock()
	h.snapshots[body.Tenant] = append(h.snapshots[body.Tenant], plan)
	h.snapshotsMu.Unlock()

	writePaasJSON(w, http.StatusCreated, map[string]any{
		"status":      "snapshot_planned",
		"slug":        slug,
		"tenant":      plan.Tenant,
		"volume":      plan.Volume,
		"archive_key": plan.ArchiveKey,
		"cipher":      plan.Cipher,
	})
}

// handleAppRestore handles POST /paas/apps/:slug/restore.
func (h *paasCommercialHandler) handleAppRestore(w http.ResponseWriter, r *http.Request, slug string) {
	var body struct {
		Tenant     string `json:"tenant"`
		ArchiveKey string `json:"archive_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writePaasJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json payload"})
		return
	}
	if claims, ok := ridTenantClaimsFromContext(r.Context()); ok {
		if body.Tenant != "" && body.Tenant != claims.TenantID {
			writePaasJSON(w, http.StatusForbidden, map[string]string{"error": "tenant mismatch"})
			return
		}
		body.Tenant = claims.TenantID
	}
	if body.Tenant == "" {
		body.Tenant = slug
	}

	if err := snapshotRestorePlan(body.Tenant, body.ArchiveKey); err != nil {
		writePaasJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}

	writePaasJSON(w, http.StatusOK, map[string]any{
		"status":      "restore_accepted",
		"slug":        slug,
		"tenant":      body.Tenant,
		"archive_key": body.ArchiveKey,
	})
}
