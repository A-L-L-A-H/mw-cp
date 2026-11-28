package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPaasCommercialRoutesEndToEnd(t *testing.T) {
	templatesRoot := t.TempDir()
	manifest := `{"version":1,"packs":{"curated":{"fileCount":1,"digest":"abc"}}}`
	if err := os.WriteFile(filepath.Join(templatesRoot, "catalog-manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	composeDir := filepath.Join(templatesRoot, "packs", "curated", "ghost")
	if err := os.MkdirAll(composeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	composeSrc := "services:\n  app:\n    image: ghost:5-alpine\n    environment:\n      SECRET: {{RAND_HEX_24}}\n    networks:\n      - {{TENANT_NETWORK}}\n"
	if err := os.WriteFile(filepath.Join(composeDir, "docker-compose.yml"), []byte(composeSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	driver := newMockDockerDriver()
	domains := newCustomDomainRegistry("moonwitness.app", nil)
	h := newPaasCommercialHandler(templatesRoot, t.TempDir(), driver, domains)
	h.nowFn = func() time.Time { return time.Date(2026, 11, 27, 8, 0, 0, 0, time.UTC) }

	// 1. POST /paas/templates/deploy (within starter quota)
	deployBody, _ := json.Marshal(paasTemplateDeployRequest{
		Tenant:    "acme",
		Plan:      "starter",
		Pack:      "curated",
		Name:      "ghost",
		Slug:      "acme-blog",
		MemoryMB:  256,
		VCPUMilli: 500,
		DiskGB:    2,
	})
	reqDeploy := httptest.NewRequest(http.MethodPost, "/paas/templates/deploy", bytes.NewReader(deployBody))
	resDeploy := httptest.NewRecorder()
	h.handleTemplateDeploy().ServeHTTP(resDeploy, reqDeploy)
	if resDeploy.Code != http.StatusCreated {
		t.Fatalf("expected 201 deploy, got %d: %s", resDeploy.Code, resDeploy.Body.String())
	}
	if driver.States["acme-blog"] != "running" {
		t.Fatalf("expected mock driver state running, got %q", driver.States["acme-blog"])
	}

	// 2. POST /paas/templates/deploy over quota -> 403
	overBody, _ := json.Marshal(paasTemplateDeployRequest{
		Tenant:    "acme",
		Plan:      "starter",
		Pack:      "curated",
		Name:      "ghost",
		Slug:      "acme-blog",
		MemoryMB:  1024,
		VCPUMilli: 500,
		DiskGB:    2,
	})
	reqOver := httptest.NewRequest(http.MethodPost, "/paas/templates/deploy", bytes.NewReader(overBody))
	resOver := httptest.NewRecorder()
	h.handleTemplateDeploy().ServeHTTP(resOver, reqOver)
	if resOver.Code != http.StatusForbidden {
		t.Fatalf("expected 403 over-quota, got %d", resOver.Code)
	}

	// 3. GET /paas/quotas/:tenant
	reqQuota := httptest.NewRequest(http.MethodGet, "/paas/quotas/acme?plan=pro", nil)
	resQuota := httptest.NewRecorder()
	h.handleTenantQuotas().ServeHTTP(resQuota, reqQuota)
	if resQuota.Code != http.StatusOK {
		t.Fatalf("expected 200 quotas, got %d", resQuota.Code)
	}

	// 4. GET /paas/usage/:tenant
	reqUsage := httptest.NewRequest(http.MethodGet, "/paas/usage/acme", nil)
	resUsage := httptest.NewRecorder()
	h.handleTenantUsage().ServeHTTP(resUsage, reqUsage)
	if resUsage.Code != http.StatusOK {
		t.Fatalf("expected 200 usage, got %d", resUsage.Code)
	}

	// 5. POST /paas/apps/:slug/snapshots & /restore
	reqSnap := httptest.NewRequest(http.MethodPost, "/paas/apps/acme-blog/snapshots", bytes.NewBufferString(`{"tenant":"acme","volume":"blog-data"}`))
	resSnap := httptest.NewRecorder()
	h.handleAppSnapshot(resSnap, reqSnap, "acme-blog")
	if resSnap.Code != http.StatusCreated {
		t.Fatalf("expected 201 snapshot, got %d: %s", resSnap.Code, resSnap.Body.String())
	}
	var snapResp map[string]string
	_ = json.Unmarshal(resSnap.Body.Bytes(), &snapResp)

	restorePayload, _ := json.Marshal(map[string]string{"tenant": "acme", "archive_key": snapResp["archive_key"]})
	reqRestore := httptest.NewRequest(http.MethodPost, "/paas/apps/acme-blog/restore", bytes.NewReader(restorePayload))
	resRestore := httptest.NewRecorder()
	h.handleAppRestore(resRestore, reqRestore, "acme-blog")
	if resRestore.Code != http.StatusOK {
		t.Fatalf("expected 200 restore, got %d: %s", resRestore.Code, resRestore.Body.String())
	}

	// Cross-tenant restore is rejected (403).
	badRestore, _ := json.Marshal(map[string]string{"tenant": "other", "archive_key": snapResp["archive_key"]})
	reqBadRestore := httptest.NewRequest(http.MethodPost, "/paas/apps/acme-blog/restore", bytes.NewReader(badRestore))
	resBadRestore := httptest.NewRecorder()
	h.handleAppRestore(resBadRestore, reqBadRestore, "acme-blog")
	if resBadRestore.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-tenant restore, got %d", resBadRestore.Code)
	}
}

func TestPaasQuotasApplyAuthenticatedAndPlanLimits(t *testing.T) {
	dockerBin := writeDockerMock(t)
	handler, token := newTestServer(t, dockerBin)

	validPayload := `{"tenant":"acme","plan":"pro","memory_mb":512,"vcpu_milli":1000,"disk_gb":10,"pids_limit":300}`

	// 1. Unauthenticated request is rejected (401).
	reqNoAuth := httptest.NewRequest(http.MethodPost, "/paas/quotas/apply", bytes.NewBufferString(validPayload))
	resNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(resNoAuth, reqNoAuth)
	if resNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated POST /paas/quotas/apply, got %d", resNoAuth.Code)
	}

	// 2. Wrong bearer token is rejected (401).
	reqBadToken := httptest.NewRequest(http.MethodPost, "/paas/quotas/apply", bytes.NewBufferString(validPayload))
	reqBadToken.Header.Set("Authorization", "Bearer wrong-token")
	resBadToken := httptest.NewRecorder()
	handler.ServeHTTP(resBadToken, reqBadToken)
	if resBadToken.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid token, got %d", resBadToken.Code)
	}

	// 3. Invalid / non-positive / unknown plan limits are rejected (400).
	for _, invalidBody := range []string{
		`{"tenant":"acme","plan":"free","memory_mb":128,"vcpu_milli":250,"disk_gb":1}`,
		`{"tenant":"acme","plan":"pro","memory_mb":0,"vcpu_milli":500,"disk_gb":5}`,
		`{"tenant":"acme","plan":"pro","memory_mb":256,"vcpu_milli":-100,"disk_gb":5}`,
		`{"tenant":"acme","plan":"pro","memory_mb":256,"vcpu_milli":500,"disk_gb":0}`,
		`{"tenant":"../evil","plan":"pro","memory_mb":256,"vcpu_milli":500,"disk_gb":5}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/paas/quotas/apply", bytes.NewBufferString(invalidBody))
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid payload %s, got %d: %s", invalidBody, res.Code, res.Body.String())
		}
	}

	// 4. Over-quota CPU/RAM/disk limits for paid plan are rejected (403).
	for _, overQuotaBody := range []string{
		`{"tenant":"acme","plan":"starter","memory_mb":512,"vcpu_milli":500,"disk_gb":2}`,
		`{"tenant":"acme","plan":"pro","memory_mb":512,"vcpu_milli":1500,"disk_gb":10}`,
		`{"tenant":"acme","plan":"business","memory_mb":1024,"vcpu_milli":2000,"disk_gb":30}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/paas/quotas/apply", bytes.NewBufferString(overQuotaBody))
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for over-quota payload %s, got %d: %s", overQuotaBody, res.Code, res.Body.String())
		}
	}

	// 5. Valid authenticated paid plan quota application succeeds (200) and persists hardened profile.
	reqApply := httptest.NewRequest(http.MethodPost, "/paas/quotas/apply", bytes.NewBufferString(validPayload))
	reqApply.Header.Set("Authorization", "Bearer "+token)
	resApply := httptest.NewRecorder()
	handler.ServeHTTP(resApply, reqApply)
	if resApply.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid POST /paas/quotas/apply, got %d: %s", resApply.Code, resApply.Body.String())
	}

	var applied map[string]any
	if err := json.Unmarshal(resApply.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if applied["status"] != "applied" || applied["plan"] != "pro" || int(applied["memory_mb"].(float64)) != 512 {
		t.Fatalf("unexpected applied quota response: %+v", applied)
	}

	// 6. GET /paas/quotas/acme reflects the applied quota.
	reqGet := httptest.NewRequest(http.MethodGet, "/paas/quotas/acme", nil)
	reqGet.Header.Set("Authorization", "Bearer "+token)
	resGet := httptest.NewRecorder()
	handler.ServeHTTP(resGet, reqGet)
	if resGet.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET /paas/quotas/acme, got %d", resGet.Code)
	}
	var fetched map[string]any
	_ = json.Unmarshal(resGet.Body.Bytes(), &fetched)
	if fetched["plan"] != "pro" || int(fetched["pids_limit"].(float64)) != 300 {
		t.Fatalf("expected persisted pro quota with pids_limit=300, got %+v", fetched)
	}
}
