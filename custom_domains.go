package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

// customDomainResolver abstracts DNS lookups for deterministic testing.
type customDomainResolver interface {
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupTXT(ctx context.Context, host string) ([]string, error)
}

type netCustomDomainResolver struct{}

func (netCustomDomainResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return net.DefaultResolver.LookupCNAME(ctx, host)
}

func (netCustomDomainResolver) LookupTXT(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, host)
}

// customDomainRecord stores a verified custom domain binding.
type customDomainRecord struct {
	Domain   string `json:"domain"`
	TenantID string `json:"tenant_id"`
	Slug     string `json:"slug"`
	Method   string `json:"method"` // "cname" or "txt"
}

// customDomainRegistry verifies DNS ownership and serves Caddy on-demand TLS allowlist checks.
type customDomainRegistry struct {
	mu         sync.RWMutex
	baseDomain string
	resolver   customDomainResolver
	verified   map[string]customDomainRecord
}

func newCustomDomainRegistry(baseDomain string, resolver customDomainResolver) *customDomainRegistry {
	if resolver == nil {
		resolver = netCustomDomainResolver{}
	}
	return &customDomainRegistry{
		baseDomain: strings.ToLower(strings.TrimSuffix(strings.TrimSpace(baseDomain), ".")),
		resolver:   resolver,
		verified:   make(map[string]customDomainRecord),
	}
}

func normalizeDomain(domain string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
}

// VerifyAndRegister checks CNAME (pointing to expectedTarget) or TXT (_mw-verify.<domain> containing
// "mw-verify=<expectedToken>") and registers the domain in the on-demand TLS allowlist.
func (reg *customDomainRegistry) VerifyAndRegister(ctx context.Context, domain, tenantID, slug, expectedTarget, expectedToken string) (customDomainRecord, error) {
	normalized := normalizeDomain(domain)
	if err := caddyValidateCustomDomain(normalized); err != nil {
		return customDomainRecord{}, err
	}
	if !templateSlugPattern.MatchString(tenantID) || !templateSlugPattern.MatchString(slug) {
		return customDomainRecord{}, fmt.Errorf("invalid tenant_id or slug")
	}

	target := normalizeDomain(expectedTarget)
	if target != "" {
		if cname, err := reg.resolver.LookupCNAME(ctx, normalized); err == nil {
			if normalizeDomain(cname) == target {
				rec := customDomainRecord{
					Domain:   normalized,
					TenantID: tenantID,
					Slug:     slug,
					Method:   "cname",
				}
				reg.mu.Lock()
				reg.verified[normalized] = rec
				reg.mu.Unlock()
				return rec, nil
			}
		}
	}

	if token := strings.TrimSpace(expectedToken); token != "" {
		wantTXT := "mw-verify=" + token
		if records, err := reg.resolver.LookupTXT(ctx, "_mw-verify."+normalized); err == nil {
			for _, txt := range records {
				if strings.TrimSpace(txt) == wantTXT {
					rec := customDomainRecord{
						Domain:   normalized,
						TenantID: tenantID,
						Slug:     slug,
						Method:   "txt",
					}
					reg.mu.Lock()
					reg.verified[normalized] = rec
					reg.mu.Unlock()
					return rec, nil
				}
			}
		}
	}

	return customDomainRecord{}, fmt.Errorf("dns verification failed for %q", normalized)
}

// IsAllowed returns true if domain is a verified custom domain or a valid <slug>.paas.<baseDomain>.
func (reg *customDomainRegistry) IsAllowed(domain string) bool {
	normalized := normalizeDomain(domain)
	if err := caddyValidateCustomDomain(normalized); err != nil {
		return false
	}

	reg.mu.RLock()
	_, ok := reg.verified[normalized]
	base := reg.baseDomain
	reg.mu.RUnlock()
	if ok {
		return true
	}

	if base != "" {
		suffix := ".paas." + base
		if strings.HasSuffix(normalized, suffix) {
			slug := strings.TrimSuffix(normalized, suffix)
			if caddyHostLabelPattern.MatchString(slug) {
				return true
			}
		}
	}
	return false
}

// HandleCaddyAsk implements GET /paas/caddy/ask?domain=... for Caddy on-demand TLS.
func (reg *customDomainRegistry) HandleCaddyAsk() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := strings.TrimSpace(r.URL.Query().Get("domain"))
		if domain == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing domain"})
			return
		}
		if !reg.IsAllowed(domain) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "domain not allowed"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"allowed": true, "domain": normalizeDomain(domain)})
	}
}
