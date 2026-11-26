package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTemplateRender(t *testing.T) {
	out, network, err := templateRender("a={{RAND_HEX_24}}\nb={{RAND_HEX_24}}\nn={{TENANT_NETWORK}}", "acme")
	if err != nil || network != "mw-tenant-acme" {
		t.Fatalf("%v %s", err, network)
	}
	matches := regexp.MustCompile(`=([0-9a-f]{24})\n`).FindAllStringSubmatch(out, -1)
	if len(matches) != 2 || matches[0][1] == matches[1][1] {
		t.Fatalf("secrets not distinct: %s", out)
	}
	if strings.Contains(out, "{{") {
		t.Fatalf("unrendered: %s", out)
	}
	if _, _, err := templateRender("x", "../evil"); err == nil {
		t.Fatal("expected slug rejection")
	}
}

func TestTemplateDeploy(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "catalog-manifest.json"), []byte(`{"version":1,"packs":{"p":{"fileCount":1,"digest":"d"}}}`), 0o600)
	os.MkdirAll(filepath.Join(root, "packs", "p", "app"), 0o700)
	os.WriteFile(filepath.Join(root, "packs", "p", "app", "docker-compose.yml"), []byte("s: {{RAND_HEX_24}}\nnet: {{TENANT_NETWORK}}\n"), 0o600)
	target, err := templateDeploy(root, "p", "app", "acme", filepath.Join(root, "out"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(target)
	if !strings.Contains(string(raw), "mw-tenant-acme") || strings.Contains(string(raw), "{{") {
		t.Fatalf("bad render %s", raw)
	}
	if _, err := templateDeploy(root, "missing", "app", "acme", root); err == nil {
		t.Fatal("expected unlisted pack rejection")
	}
	if _, err := templateDeploy(root, "p", "../app", "acme", root); err == nil {
		t.Fatal("expected name rejection")
	}
}
