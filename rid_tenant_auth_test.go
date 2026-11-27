package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRIDTenantAuthFailClosed(t *testing.T) {
	secret := []byte("super-secret-rid-key")
	now := time.Unix(1_800_000_000, 0)

	validToken, err := SignRIDToken(RIDTenantClaims{
		Subject:   "user-1",
		Issuer:    "mw-rid",
		TenantID:  "acme-inc",
		PlanTier:  "pro",
		ExpiresAt: now.Add(time.Hour).Unix(),
	}, secret)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	claims, err := ParseAndVerifyRIDToken(validToken, secret, now)
	if err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
	if claims.TenantID != "acme-inc" || claims.PlanTier != "pro" {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	// Wrong secret fails closed.
	if _, err := ParseAndVerifyRIDToken(validToken, []byte("wrong-secret"), now); err == nil {
		t.Fatal("expected signature failure")
	}

	// Expired token fails closed.
	expiredToken, _ := SignRIDToken(RIDTenantClaims{
		TenantID:  "acme-inc",
		PlanTier:  "pro",
		ExpiresAt: now.Add(-time.Minute).Unix(),
	}, secret)
	if _, err := ParseAndVerifyRIDToken(expiredToken, secret, now); err == nil {
		t.Fatal("expected expired token failure")
	}

	// Unknown plan_tier fails closed.
	badPlanToken, _ := SignRIDToken(RIDTenantClaims{
		TenantID:  "acme-inc",
		PlanTier:  "enterprise-unlisted",
		ExpiresAt: now.Add(time.Hour).Unix(),
	}, secret)
	if _, err := ParseAndVerifyRIDToken(badPlanToken, secret, now); err == nil {
		t.Fatal("expected unknown plan_tier failure")
	}

	// Middleware test.
	var captured RIDTenantClaims
	protected := requireRIDTenantAuth(secret, func() time.Time { return now }, func(w http.ResponseWriter, r *http.Request) {
		c, ok := ridTenantClaimsFromContext(r.Context())
		if !ok {
			t.Fatal("missing claims in context")
		}
		captured = c
		w.WriteHeader(http.StatusOK)
	})

	reqNoAuth := httptest.NewRequest(http.MethodGet, "/paas/quotas/acme-inc", nil)
	resNoAuth := httptest.NewRecorder()
	protected.ServeHTTP(resNoAuth, reqNoAuth)
	if resNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resNoAuth.Code)
	}

	reqAuth := httptest.NewRequest(http.MethodGet, "/paas/quotas/acme-inc", nil)
	reqAuth.Header.Set("X-MW-RID-Token", validToken)
	resAuth := httptest.NewRecorder()
	protected.ServeHTTP(resAuth, reqAuth)
	if resAuth.Code != http.StatusOK || captured.TenantID != "acme-inc" {
		t.Fatalf("expected 200 with claims, got %d %+v", resAuth.Code, captured)
	}
}
