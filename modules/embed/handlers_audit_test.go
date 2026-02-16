// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import "testing"

func TestBuildEmbedSettingsAuditMetadata(t *testing.T) {
	metadata := buildEmbedSettingsAuditMetadata("dify", true, map[string]string{
		"api_endpoint": "https://api.dify.ai/v1",
		"api_key":      "secret-value",
	})

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
	if _, ok := metadata["api_key"]; ok {
		t.Fatal("metadata must not include raw api_key")
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
