package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCaddyValidateCustomDomain(t *testing.T) {
	for _, valid := range []string{"example.com", "a.b-c.io"} {
		if err := caddyValidateCustomDomain(valid); err != nil {
			t.Fatalf("%s: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "localhost", "-a.com", "a..com", "a_b.com", "evil.com/x"} {
		if caddyValidateCustomDomain(invalid) == nil {
			t.Fatalf("expected rejection for %q", invalid)
		}
	}
}

func TestCaddyTenantHost(t *testing.T) {
	host, err := caddyTenantHost("acme", "Example.com")
	if err != nil || host != "acme.paas.example.com" {
		t.Fatalf("got %q %v", host, err)
	}
	if _, err := caddyTenantHost("Bad Slug", "example.com"); err == nil {
		t.Fatal("expected slug rejection")
	}
}

func TestCaddySyncRoute(t *testing.T) {
	var methods []string
	var postedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			postedBody = string(raw)
		}
	}))
	defer server.Close()
	spec := caddyRouteSpec{ID: "tenant-acme", Hosts: []string{"*.paas.example.com", "shop.acme.id"}, Upstream: "mw-acme:8080"}
	if err := caddySyncRoute(server.Client(), server.URL, "srv0", spec); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[0] != "DELETE /id/tenant-acme" || methods[1] != "POST /config/apps/http/servers/srv0/routes" {
		t.Fatalf("unexpected calls %v", methods)
	}
	if !strings.Contains(postedBody, "*.paas.example.com") || !strings.Contains(postedBody, "mw-acme:8080") {
		t.Fatalf("bad body %s", postedBody)
	}
	if caddySyncRoute(server.Client(), server.URL, "srv0", caddyRouteSpec{ID: "x", Hosts: []string{"bad_host"}, Upstream: "u:1"}) == nil {
		t.Fatal("expected invalid host rejection")
	}
}
