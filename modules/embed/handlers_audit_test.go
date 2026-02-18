// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/modules/embed/providers"
)

func TestBuildEmbedSettingsAuditMetadata(t *testing.T) {
	metadata := buildEmbedSettingsAuditMetadata("dify", true, map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "secret-value",
	}, true, "https", "old.example", "https", "api.dify.ai")

	if metadata["provider"] != "dify" {
		t.Errorf("provider = %v, want %q", metadata["provider"], "dify")
	}
	if metadata["enabled"] != true {
		t.Errorf("enabled = %v, want %v", metadata["enabled"], true)
	}
	if metadata["api_endpoint"] != "https://api.dify.ai/v1" {
		t.Errorf("api_endpoint = %v, want %q", metadata["api_endpoint"], "https://api.dify.ai/v1")
	}
	if metadata["has_api_key"] != true {
		t.Errorf("has_api_key = %v, want %v", metadata["has_api_key"], true)
	}
	if metadata["endpoint_changed"] != true {
		t.Errorf("endpoint_changed = %v, want %v", metadata["endpoint_changed"], true)
	}
	if metadata["old_host"] != "old.example" {
		t.Errorf("old_host = %v, want %q", metadata["old_host"], "old.example")
	}
	if metadata["new_host"] != "api.dify.ai" {
		t.Errorf("new_host = %v, want %q", metadata["new_host"], "api.dify.ai")
	}
	if _, ok := metadata["api_key"]; ok {
		t.Fatal("metadata must not include raw api_key")
	}
}

func TestEmbedEndpointMetadata(t *testing.T) {
	scheme, host := embedEndpointMetadata("https://API.Dify.AI/v1")
	if scheme != "https" {
		t.Fatalf("scheme = %q, want %q", scheme, "https")
	}
	if host != "api.dify.ai" {
		t.Fatalf("host = %q, want %q", host, "api.dify.ai")
	}

	scheme, host = embedEndpointMetadata("://bad-url")
	if scheme != "" || host != "" {
		t.Fatalf("invalid URL metadata = (%q, %q), want empty values", scheme, host)
	}
}

func TestValidateProviderRuntimePolicy(t *testing.T) {
	mod := New()
	mod.allowedUpstreamHosts = map[string]struct{}{
		"api.dify.ai": {},
	}

	if err := mod.validateProviderRuntimePolicy("dify", map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
	}); err != nil {
		t.Fatalf("expected allowed host to pass, got %v", err)
	}

	if err := mod.validateProviderRuntimePolicy("dify", map[string]string{
		"api_endpoint": "https://evil.example/v1",
	}); err == nil {
		t.Fatal("expected disallowed host to be rejected")
	}
}

func TestValidateProviderRuntimePolicy_RequireAllowlistConfigured(t *testing.T) {
	mod := New()
	mod.requireUpstreamHostPolicy = true
	mod.allowedUpstreamHosts = nil

	err := mod.validateProviderRuntimePolicy("dify", map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
	})
	if err == nil {
		t.Fatal("expected required allowlist policy to reject empty host allowlist")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateProviderRuntimePolicy_AllowlistOptionalWhenNotRequired(t *testing.T) {
	mod := New()
	mod.requireUpstreamHostPolicy = false
	mod.allowedUpstreamHosts = nil

	if err := mod.validateProviderRuntimePolicy("dify", map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
	}); err != nil {
		t.Fatalf("expected optional allowlist mode to pass, got %v", err)
	}
}

func TestValidateProviderEnableSettings(t *testing.T) {
	mod := New()
	mod.allowedUpstreamHosts = map[string]struct{}{
		"api.dify.ai": {},
	}

	provider := providers.NewDify()
	validSettings := map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "app-test-key",
	}
	if err := mod.validateProviderEnableSettings("dify", provider, validSettings); err != nil {
		t.Fatalf("expected valid settings to pass, got %v", err)
	}

	disallowedHostSettings := map[string]string{
		"api_endpoint": "https://evil.example/v1",
		"api_key":      "app-test-key",
	}
	if err := mod.validateProviderEnableSettings("dify", provider, disallowedHostSettings); err == nil {
		t.Fatal("expected disallowed host to fail provider enable validation")
	}
}
