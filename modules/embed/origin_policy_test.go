// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestEmbedOriginDenialBoundsAndSamplesAudit(t *testing.T) {
	db, closeDB := testutil.TestDB(t)
	t.Cleanup(closeDB)
	var output bytes.Buffer
	m := New()
	m.allowedOrigins = map[string]struct{}{"https://allowed.example": {}}
	m.ctx = &module.Context{Events: service.NewEventService(db), Logger: slog.New(slog.NewJSONHandler(&output, nil))}
	path := "/embed/dify/messages/" + strings.Repeat("x", 5000)
	for range 2 {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Origin", "https://blocked.example/"+strings.Repeat("o", 5000))
		r.Header.Set("Referer", "https://blocked.example/"+strings.Repeat("r", 5000))
		w := httptest.NewRecorder()
		m.handleDifySuggestedProxy(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("denial returned %d", w.Code)
		}
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected one sampled event, got %d, err=%v", count, err)
	}
	var raw, storedPath string
	if err := db.QueryRow("SELECT metadata, request_url FROM events").Scan(&raw, &storedPath); err != nil {
		t.Fatal(err)
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil || storedPath != path[:256] ||
		metadata["path"] != storedPath || len(metadata["origin"]) != 256 || len(metadata["referer"]) != 256 {
		t.Fatal("unbounded or missing embed audit fields")
	}
	if strings.Contains(output.String(), strings.Repeat("o", 257)) ||
		strings.Contains(output.String(), strings.Repeat("r", 257)) || strings.Contains(output.String(), strings.Repeat("x", 257)) {
		t.Fatal("unbounded field reached the server log")
	}
}

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

func TestParseAllowedHosts(t *testing.T) {
	t.Run("empty config", func(t *testing.T) {
		allowed, err := parseAllowedHosts("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(allowed) != 0 {
			t.Fatalf("expected empty allowlist, got %d", len(allowed))
		}
	})

	t.Run("valid list", func(t *testing.T) {
		allowed, err := parseAllowedHosts("api.dify.ai, dify.internal.example")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(allowed) != 2 {
			t.Fatalf("expected 2 hosts, got %d", len(allowed))
		}
		if _, ok := allowed["api.dify.ai"]; !ok {
			t.Fatal("missing api.dify.ai")
		}
		if _, ok := allowed["dify.internal.example"]; !ok {
			t.Fatal("missing dify.internal.example")
		}
	})

	t.Run("invalid host with scheme", func(t *testing.T) {
		if _, err := parseAllowedHosts("https://api.dify.ai"); err == nil {
			t.Fatal("expected error for host with scheme")
		}
	})

	t.Run("invalid host with port", func(t *testing.T) {
		if _, err := parseAllowedHosts("api.dify.ai:443"); err == nil {
			t.Fatal("expected error for host with port")
		}
	})
}

func TestIsUpstreamHostAllowed(t *testing.T) {
	mod := New()
	mod.allowedUpstreamHosts = map[string]struct{}{
		"api.dify.ai": {},
	}

	if !mod.isUpstreamHostAllowed("api.dify.ai") {
		t.Fatal("expected api.dify.ai to be allowed")
	}
	if mod.isUpstreamHostAllowed("evil.example") {
		t.Fatal("expected evil.example to be blocked")
	}
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

	t.Run("block when token is configured but header is missing", func(t *testing.T) {
		mod := New()
		mod.requireProxyToken = false
		mod.proxyToken = "configured-token"

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set("Origin", "https://example.com")
		if mod.isProxyTokenAuthorized(req) {
			t.Fatal("expected proxy token check to fail when token is configured but header is missing")
		}
	})

	t.Run("allow when configured signed token matches even if policy flag is disabled", func(t *testing.T) {
		mod := New()
		mod.requireProxyToken = false
		mod.proxyToken = "configured-token"

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set("Origin", "https://example.com")
		token, _, err := mod.issueSignedProxyToken("https://example.com", time.Now())
		if err != nil {
			t.Fatalf("issueSignedProxyToken() error: %v", err)
		}
		req.Header.Set(embedProxyTokenHeader, token)
		if !mod.isProxyTokenAuthorized(req) {
			t.Fatal("expected proxy token check to pass with matching configured token")
		}
	})

	t.Run("block when token is missing", func(t *testing.T) {
		mod := New()
		mod.requireProxyToken = true
		mod.proxyToken = "secret-token"

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set("Origin", "https://example.com")
		if mod.isProxyTokenAuthorized(req) {
			t.Fatal("expected proxy token check to fail when token is missing")
		}
	})

	t.Run("allow when raw proxy secret matches", func(t *testing.T) {
		mod := New()
		mod.requireProxyToken = true
		mod.proxyToken = "secret-token"

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set(embedProxyTokenHeader, "secret-token")
		if !mod.isProxyTokenAuthorized(req) {
			t.Fatal("expected proxy token check to pass with raw secret token")
		}
	})

	t.Run("block when signed token origin mismatches", func(t *testing.T) {
		mod := New()
		mod.requireProxyToken = true
		mod.proxyToken = "secret-token"

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set("Origin", "https://example.com")
		token, _, err := mod.issueSignedProxyToken("https://other.example", time.Now())
		if err != nil {
			t.Fatalf("issueSignedProxyToken() error: %v", err)
		}
		req.Header.Set(embedProxyTokenHeader, token)
		if mod.isProxyTokenAuthorized(req) {
			t.Fatal("expected proxy token check to fail when token origin mismatches")
		}
	})

	t.Run("allow when signed token matches", func(t *testing.T) {
		mod := New()
		mod.requireProxyToken = true
		mod.proxyToken = "secret-token"

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set("Origin", "https://example.com")
		token, _, err := mod.issueSignedProxyToken("https://example.com", time.Now())
		if err != nil {
			t.Fatalf("issueSignedProxyToken() error: %v", err)
		}
		req.Header.Set(embedProxyTokenHeader, token)
		if !mod.isProxyTokenAuthorized(req) {
			t.Fatal("expected proxy token check to pass with matching token")
		}
	})

	t.Run("block when signed token is expired", func(t *testing.T) {
		mod := New()
		mod.requireProxyToken = true
		mod.proxyToken = "secret-token"

		issuedAt := time.Now().Add(-2 * embedProxyTokenTTL)
		token, _, err := mod.issueSignedProxyToken("https://example.com", issuedAt)
		if err != nil {
			t.Fatalf("issueSignedProxyToken() error: %v", err)
		}

		req := httptest.NewRequest("POST", "/embed/dify/chat-messages", nil)
		req.Header.Set("Origin", "https://example.com")
		req.Header.Set(embedProxyTokenHeader, token)
		if mod.isProxyTokenAuthorized(req) {
			t.Fatal("expected proxy token check to fail with expired token")
		}
	})
}
