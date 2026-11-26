package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var templateSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// templateCatalogManifest mirrors mw-templates catalog-manifest.json.
type templateCatalogManifest struct {
	Version int `json:"version"`
	Packs   map[string]struct {
		FileCount int    `json:"fileCount"`
		Digest    string `json:"digest"`
	} `json:"packs"`
}

func templateReadManifest(templatesRoot string) (templateCatalogManifest, error) {
	var manifest templateCatalogManifest
	raw, err := os.ReadFile(filepath.Join(templatesRoot, "catalog-manifest.json"))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func templateRandomHex(byteCount int) (string, error) {
	buffer := make([]byte, byteCount)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// templateTenantNetwork returns the isolated network name for a tenant.
func templateTenantNetwork(slug string) (string, error) {
	if !templateSlugPattern.MatchString(slug) {
		return "", fmt.Errorf("invalid tenant slug %q", slug)
	}
	return "mw-tenant-" + slug, nil
}

// templateRender substitutes every {{RAND_HEX_24}} with a distinct crypto-random 24-hex secret
// and returns the rendered compose plus the network assigned to the tenant.
func templateRender(compose, slug string) (string, string, error) {
	network, err := templateTenantNetwork(slug)
	if err != nil {
		return "", "", err
	}
	const placeholder = "{{RAND_HEX_24}}"
	var rendered strings.Builder
	remaining := compose
	for {
		index := strings.Index(remaining, placeholder)
		if index < 0 {
			rendered.WriteString(remaining)
			break
		}
		secret, err := templateRandomHex(12)
		if err != nil {
			return "", "", err
		}
		rendered.WriteString(remaining[:index])
		rendered.WriteString(secret)
		remaining = remaining[index+len(placeholder):]
	}
	output := strings.ReplaceAll(rendered.String(), "{{TENANT_NETWORK}}", network)
	return output, network, nil
}

// templateDeploy renders packs/<pack>/<name>/docker-compose.yml into outputDir (mode 0600).
func templateDeploy(templatesRoot, pack, name, slug, outputDir string) (string, error) {
	manifest, err := templateReadManifest(templatesRoot)
	if err != nil {
		return "", err
	}
	if _, listed := manifest.Packs[pack]; !listed {
		return "", fmt.Errorf("pack %q not in catalog manifest", pack)
	}
	if strings.ContainsAny(name, `/\.`) {
		return "", fmt.Errorf("invalid template name %q", name)
	}
	source, err := os.ReadFile(filepath.Join(templatesRoot, "packs", pack, name, "docker-compose.yml"))
	if err != nil {
		return "", err
	}
	rendered, _, err := templateRender(string(source), slug)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(outputDir, "docker-compose.yml")
	return target, os.WriteFile(target, []byte(rendered), 0o600)
}
