// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"net/http/httptest"
	"testing"
)

func TestParseAllowedOrigins(t *testing.T) {
	t.Run("empty config", func(t *testing.T) {
		allowed, err := parseAllowedOrigins("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(allowed) != 0 {
			t.Fatalf("expected empty allowlist, got %d", len(allowed))
		}
	})

	t.Run("valid list", func(t *testing.T) {
		allowed, err := parseAllowedOrigins("https://example.com, https://app.example.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(allowed) != 2 {
			t.Fatalf("expected 2 origins, got %d", len(allowed))
		}
		if _, ok := allowed["https://example.com"]; !ok {
			t.Fatal("missing https://example.com")
		}
		if _, ok := allowed["https://app.example.com"]; !ok {
			t.Fatal("missing https://app.example.com")
		}
	})

	t.Run("invalid entry", func(t *testing.T) {
		if _, err := parseAllowedOrigins("javascript:alert(1)"); err == nil {
			t.Fatal("expected error for invalid origin")
		}
	})
}

func TestIsRequestOriginAllowed(t *testing.T) {
	mod := New()
	mod.allowedOrigins = map[string]struct{}{
		"https://example.com": {},
	}

	t.Run("allow by origin header", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set("Origin", "https://example.com")
		if !mod.isRequestOriginAllowed(req) {
			t.Fatal("expected request to be allowed")
		}
	})

	t.Run("block by origin header", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set("Origin", "https://evil.example")
		if mod.isRequestOriginAllowed(req) {
			t.Fatal("expected request to be blocked")
		}
	})

	t.Run("allow by referer fallback", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set("Referer", "https://example.com/chat/widget")
		if !mod.isRequestOriginAllowed(req) {
			t.Fatal("expected request to be allowed by referer")
		}
	})

	t.Run("block missing headers when policy enabled", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		if mod.isRequestOriginAllowed(req) {
			t.Fatal("expected request to be blocked")
		}
	})
}

func TestIsRequestOriginAllowed_NoAllowlist(t *testing.T) {
	t.Run("allow when origin policy is not required", func(t *testing.T) {
		mod := New()
		mod.allowedOrigins = nil
		mod.requireOriginPolicy = false

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		if !mod.isRequestOriginAllowed(req) {
			t.Fatal("expected request to be allowed without allowlist in non-production mode")
		}
	})

	t.Run("block when origin policy is required", func(t *testing.T) {
		mod := New()
		mod.allowedOrigins = nil
		mod.requireOriginPolicy = true

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		if mod.isRequestOriginAllowed(req) {
			t.Fatal("expected request to be blocked without allowlist in production mode")
		}
	})
}

func TestIsProxyTokenAuthorized(t *testing.T) {
	t.Run("allow when policy is disabled", func(t *testing.T) {
		mod := New()
		mod.requireProxyToken = false

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		if !mod.isProxyTokenAuthorized(req) {
			t.Fatal("expected proxy token check to pass when policy is disabled")
		}
	})

	t.Run("block when token is missing", func(t *testing.T) {
		mod := New()
		mod.requireProxyToken = true
		mod.proxyToken = "secret-token"

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		if mod.isProxyTokenAuthorized(req) {
			t.Fatal("expected proxy token check to fail when token is missing")
		}
	})

	t.Run("block when token is incorrect", func(t *testing.T) {
		mod := New()
		mod.requireProxyToken = true
		mod.proxyToken = "secret-token"

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set(embedProxyTokenHeader, "wrong-token")
		if mod.isProxyTokenAuthorized(req) {
			t.Fatal("expected proxy token check to fail when token is incorrect")
		}
	})

	t.Run("allow when token matches", func(t *testing.T) {
		mod := New()
		mod.requireProxyToken = true
		mod.proxyToken = "secret-token"

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set(embedProxyTokenHeader, "secret-token")
		if !mod.isProxyTokenAuthorized(req) {
			t.Fatal("expected proxy token check to pass with matching token")
		}
	})
}
