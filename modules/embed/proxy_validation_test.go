// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/module"
)

func TestValidateDifyIdentifier(t *testing.T) {
	t.Run("valid identifier", func(t *testing.T) {
		err := validateDifyIdentifier("user_123-abc@example.com", maxDifyUserIDLen, "user")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("missing identifier", func(t *testing.T) {
		err := validateDifyIdentifier("", maxDifyUserIDLen, "user")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("too long identifier", func(t *testing.T) {
		err := validateDifyIdentifier(strings.Repeat("a", maxDifyUserIDLen+1), maxDifyUserIDLen, "user")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid characters", func(t *testing.T) {
		err := validateDifyIdentifier("user with spaces", maxDifyUserIDLen, "user")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestExtractAndValidateDifyChatUser(t *testing.T) {
	t.Run("valid body", func(t *testing.T) {
		user, err := extractAndValidateDifyChatUser([]byte(`{"user":"user-1","query":"hello"}`))
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if user != "user-1" {
			t.Fatalf("user = %q, want %q", user, "user-1")
		}
	})

	t.Run("missing user", func(t *testing.T) {
		_, err := extractAndValidateDifyChatUser([]byte(`{"query":"hello"}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid user format", func(t *testing.T) {
		_, err := extractAndValidateDifyChatUser([]byte(`{"user":"bad user","query":"hello"}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("query too long", func(t *testing.T) {
		body := `{"user":"user-1","query":"` + strings.Repeat("x", maxDifyQueryLen+1) + `"}`
		_, err := extractAndValidateDifyChatUser([]byte(body))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("query must be string", func(t *testing.T) {
		_, err := extractAndValidateDifyChatUser([]byte(`{"user":"user-1","query":123}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("query must not be empty", func(t *testing.T) {
		_, err := extractAndValidateDifyChatUser([]byte(`{"user":"user-1","query":"   "}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("rejects unsupported response mode", func(t *testing.T) {
		_, err := extractAndValidateDifyChatUser([]byte(`{"user":"user-1","query":"hello","response_mode":"blocking"}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("rejects invalid conversation id", func(t *testing.T) {
		_, err := extractAndValidateDifyChatUser([]byte(`{"user":"user-1","query":"hello","conversation_id":"bad id"}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestValidateAndSanitizeDifyChatPayload(t *testing.T) {
	body := []byte(`{
		"user": "user-1",
		"query": "hello",
		"response_mode": "",
		"inputs": {"plan":"pro","flag":true,"score":1}
	}`)

	sanitizedBody, user, err := validateAndSanitizeDifyChatPayload(body)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if user != "user-1" {
		t.Fatalf("user = %q, want %q", user, "user-1")
	}

	var payload map[string]any
	if err := json.Unmarshal(sanitizedBody, &payload); err != nil {
		t.Fatalf("unmarshal sanitized payload: %v", err)
	}
	if payload["response_mode"] != "streaming" {
		t.Fatalf("response_mode = %v, want streaming", payload["response_mode"])
	}

	inputs, _ := payload["inputs"].(map[string]any)
	if len(inputs) != 3 {
		t.Fatalf("inputs length = %d, want 3", len(inputs))
	}

	t.Run("rejects unknown field", func(t *testing.T) {
		_, _, err := validateAndSanitizeDifyChatPayload([]byte(`{"user":"user-1","query":"hello","unexpected":true}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("rejects nested inputs", func(t *testing.T) {
		_, _, err := validateAndSanitizeDifyChatPayload([]byte(`{"user":"user-1","query":"hello","inputs":{"nested":{"a":1}}}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("rejects malformed input key", func(t *testing.T) {
		_, _, err := validateAndSanitizeDifyChatPayload([]byte(`{"user":"user-1","query":"hello","inputs":{"bad key":"x"}}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestGetDifyProxyConfig_RejectsInsecureEndpoint(t *testing.T) {
	mod := New()
	mod.ctx = &module.Context{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mod.settings = []*ProviderSettings{
		{
			ProviderID: "dify",
			IsEnabled:  true,
			Settings: map[string]string{
				"api_endpoint": "http://8.8.8.8/v1",
				"api_key":      "app-test-key",
			},
		},
	}

	_, _, ok := mod.getDifyProxyConfig()
	if ok {
		t.Fatal("expected insecure http endpoint to be rejected")
	}
}

func TestGetDifyProxyConfig_AllowsHTTPS(t *testing.T) {
	mod := New()
	mod.ctx = &module.Context{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mod.settings = []*ProviderSettings{
		{
			ProviderID: "dify",
			IsEnabled:  true,
			Settings: map[string]string{
				"api_endpoint": "https://api.dify.ai/v1",
				"api_key":      "app-test-key",
			},
		},
	}

	endpoint, apiKey, ok := mod.getDifyProxyConfig()
	if !ok {
		t.Fatal("expected https endpoint to be accepted")
	}
	if endpoint != "https://api.dify.ai/v1" {
		t.Errorf("endpoint = %q, want %q", endpoint, "https://api.dify.ai/v1")
	}
	if apiKey != "app-test-key" {
		t.Errorf("apiKey = %q, want %q", apiKey, "app-test-key")
	}
}

func TestGetDifyProxyConfig_EnforcesUpstreamHostAllowlist(t *testing.T) {
	mod := New()
	mod.ctx = &module.Context{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mod.allowedUpstreamHosts = map[string]struct{}{
		"api.dify.ai": {},
	}
	mod.settings = []*ProviderSettings{
		{
			ProviderID: "dify",
			IsEnabled:  true,
			Settings: map[string]string{
				"api_endpoint": "https://evil.example/v1",
				"api_key":      "app-test-key",
			},
		},
	}

	_, _, ok := mod.getDifyProxyConfig()
	if ok {
		t.Fatal("expected endpoint host outside allowlist to be rejected")
	}
}
