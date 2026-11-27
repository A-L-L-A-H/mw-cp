package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeDNSResolver struct {
	cnames map[string]string
	txts   map[string][]string
}

func (f fakeDNSResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	if target, ok := f.cnames[host]; ok {
		return target, nil
	}
	return "", errors.New("no cname")
}

func (f fakeDNSResolver) LookupTXT(_ context.Context, host string) ([]string, error) {
	if vals, ok := f.txts[host]; ok {
		return vals, nil
	}
	return nil, errors.New("no txt")
}

func TestCustomDomainVerificationAndCaddyAsk(t *testing.T) {
	resolver := fakeDNSResolver{
		cnames: map[string]string{
			"app.acme.io": "acme.paas.moonwitness.app.",
		},
		txts: map[string][]string{
			"_mw-verify.shop.acme.io": {"mw-verify=tok-123"},
		},
	}
	reg := newCustomDomainRegistry("moonwitness.app", resolver)

	// Unverified custom domain is denied.
	if reg.IsAllowed("app.acme.io") {
		t.Fatal("expected unverified domain to be denied")
	}
	// Wildcard tenant subdomain is allowed.
	if !reg.IsAllowed("acme.paas.moonwitness.app") {
		t.Fatal("expected tenant paas subdomain to be allowed")
	}

	// Verify via CNAME.
	rec, err := reg.VerifyAndRegister(context.Background(), "app.acme.io", "acme", "web", "acme.paas.moonwitness.app", "")
	if err != nil || rec.Method != "cname" {
		t.Fatalf("cname verify failed: rec=%+v err=%v", rec, err)
	}

	// Verify via TXT.
	recTXT, err := reg.VerifyAndRegister(context.Background(), "shop.acme.io", "acme", "shop", "", "tok-123")
	if err != nil || recTXT.Method != "txt" {
		t.Fatalf("txt verify failed: rec=%+v err=%v", recTXT, err)
	}

	// Wrong token fails closed.
	if _, err := reg.VerifyAndRegister(context.Background(), "evil.acme.io", "acme", "evil", "acme.paas.moonwitness.app", "wrong"); err == nil {
		t.Fatal("expected failed dns verification")
	}

	// HTTP /paas/caddy/ask checks.
	handler := reg.HandleCaddyAsk()

	reqOK := httptest.NewRequest(http.MethodGet, "/paas/caddy/ask?domain=app.acme.io", nil)
	resOK := httptest.NewRecorder()
	handler.ServeHTTP(resOK, reqOK)
	if resOK.Code != http.StatusOK {
		t.Fatalf("expected 200 for verified domain, got %d", resOK.Code)
	}

	reqDeny := httptest.NewRequest(http.MethodGet, "/paas/caddy/ask?domain=evil.acme.io", nil)
	resDeny := httptest.NewRecorder()
	handler.ServeHTTP(resDeny, reqDeny)
	if resDeny.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unverified domain, got %d", resDeny.Code)
	}

	reqMissing := httptest.NewRequest(http.MethodGet, "/paas/caddy/ask", nil)
	resMissing := httptest.NewRecorder()
	handler.ServeHTTP(resMissing, reqMissing)
	if resMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing domain, got %d", resMissing.Code)
	}
}
