package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestOpenAPIContractCoversAutomationSurface keeps the published contract
// aligned with the public, API-token authenticated surface. Console routes
// intentionally remain an owner-console implementation detail.
func TestOpenAPIContractCoversAutomationSurface(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read OpenAPI contract: %v", err)
	}
	var document struct {
		OpenAPI string `yaml:"openapi"`
		Servers []struct {
			URL string `yaml:"url"`
		} `yaml:"servers"`
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			SecuritySchemes map[string]map[string]any `yaml:"securitySchemes"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse OpenAPI contract: %v", err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("unexpected OpenAPI version %q", document.OpenAPI)
	}
	if len(document.Servers) != 1 || document.Servers[0].URL != "/" {
		t.Fatalf("OpenAPI server must use the current deployment origin, got %#v", document.Servers)
	}
	if _, ok := document.Components.SecuritySchemes["bearerAuth"]; !ok {
		t.Fatal("OpenAPI contract must expose bearerAuth for API Tokens")
	}

	expected := map[string][]string{
		"/health":                          {"get"},
		"/public/v1/settings":             {"get"},
		"/public/v1/logo":                 {"get"},
		"/public/v1/domains/submit":       {"post"},
		"/public/v1/domains/status":       {"post"},
		"/api/v1/domains":                 {"get"},
		"/api/v1/donations":               {"post"},
		"/api/v1/mailboxes":               {"get", "post"},
		"/api/v1/mailboxes/retention/batch": {"post"},
		"/api/v1/mailboxes/cleanup":       {"post"},
		"/api/v1/mailboxes/{id}":          {"get", "delete"},
		"/api/v1/mailboxes/{id}/retention": {"put"},
		"/api/v1/mailboxes/{id}/emails":   {"get"},
		"/api/v1/mailboxes/{id}/emails/{email_id}": {"get", "delete"},
		"/api/v1/mailboxes/{id}/events":  {"get"},
		"/api/v1/emails/cleanup":          {"post"},
		"/api/v1/lookup/mailbox":          {"get"},
		"/api/v1/lookup/latest":           {"get"},
		"/api/v1/lookup/latest-code":      {"get"},
		"/api/v1/lookup/latest-link":      {"get"},
	}
	for path, methods := range expected {
		operations, ok := document.Paths[path]
		if !ok {
			t.Errorf("OpenAPI contract is missing %s", path)
			continue
		}
		for _, method := range methods {
			if _, ok := operations[method]; !ok {
				t.Errorf("OpenAPI contract is missing %s %s", strings.ToUpper(method), path)
			}
		}
	}
	for path := range document.Paths {
		if strings.HasPrefix(path, "/console/") {
			t.Errorf("Admin console route must not be published as an automation contract: %s", path)
		}
	}
	if strings.Contains(string(data), "127.0.0.1") || strings.Contains(string(data), "mail.your-host.example") {
		t.Fatal("OpenAPI contract must not hard-code a deployment address")
	}
}
