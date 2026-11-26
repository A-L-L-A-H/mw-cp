package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var caddyHostLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// caddyValidateCustomDomain validates a fully qualified custom domain name.
func caddyValidateCustomDomain(domain string) error {
	normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if normalized == "" || len(normalized) > 253 {
		return fmt.Errorf("invalid domain length")
	}
	labels := strings.Split(normalized, ".")
	if len(labels) < 2 {
		return fmt.Errorf("domain requires at least two labels: %q", domain)
	}
	for _, label := range labels {
		if !caddyHostLabelPattern.MatchString(label) {
			return fmt.Errorf("invalid domain label %q", label)
		}
	}
	return nil
}

// caddyTenantHost builds <slug>.paas.<baseDomain>.
func caddyTenantHost(slug, baseDomain string) (string, error) {
	if !caddyHostLabelPattern.MatchString(slug) {
		return "", fmt.Errorf("invalid tenant slug %q", slug)
	}
	if err := caddyValidateCustomDomain(baseDomain); err != nil {
		return "", err
	}
	return slug + ".paas." + strings.ToLower(baseDomain), nil
}

// caddyRouteSpec describes one reverse-proxy route.
type caddyRouteSpec struct {
	ID       string
	Hosts    []string
	Upstream string
}

func caddyBuildRoute(spec caddyRouteSpec) (map[string]any, error) {
	if spec.ID == "" || len(spec.Hosts) == 0 || spec.Upstream == "" {
		return nil, fmt.Errorf("route requires id, hosts and upstream")
	}
	for _, host := range spec.Hosts {
		trimmed := strings.TrimPrefix(host, "*.")
		if err := caddyValidateCustomDomain(trimmed); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"@id":   spec.ID,
		"match": []any{map[string]any{"host": spec.Hosts}},
		"handle": []any{map[string]any{
			"handler":   "reverse_proxy",
			"upstreams": []any{map[string]any{"dial": spec.Upstream}},
		}},
		"terminal": true,
	}, nil
}

// caddySyncRoute upserts a route through the Caddy Admin API (DELETE by @id, then append).
func caddySyncRoute(client *http.Client, adminURL, serverName string, spec caddyRouteSpec) error {
	route, err := caddyBuildRoute(spec)
	if err != nil {
		return err
	}
	base := strings.TrimRight(adminURL, "/")
	deleteRequest, err := http.NewRequest(http.MethodDelete, base+"/id/"+spec.ID, nil)
	if err != nil {
		return err
	}
	if deleteResponse, deleteErr := client.Do(deleteRequest); deleteErr == nil {
		deleteResponse.Body.Close()
	}
	payload, err := json.Marshal(route)
	if err != nil {
		return err
	}
	postURL := fmt.Sprintf("%s/config/apps/http/servers/%s/routes", base, serverName)
	response, err := client.Post(postURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("caddy admin returned %d", response.StatusCode)
	}
	return nil
}
