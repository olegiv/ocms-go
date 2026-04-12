// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/middleware"
)

func TestEmbedClientIP(t *testing.T) {
	t.Run("uses remote address when peer is untrusted", func(t *testing.T) {
		if err := middleware.SetTrustedProxies(nil); err != nil {
			t.Fatalf("SetTrustedProxies() error: %v", err)
		}
		t.Cleanup(func() {
			_ = middleware.SetTrustedProxies(nil)
		})

		req := httptest.NewRequest(http.MethodGet, "/embed/dify/chat-messages", nil)
		req.RemoteAddr = "192.168.1.50:12345"
		req.Header.Set("X-Forwarded-For", "198.51.100.9")

		if got := embedClientIP(req); got != "192.168.1.50" {
			t.Errorf("embedClientIP() = %q, want %q", got, "192.168.1.50")
		}
	})

	t.Run("uses trusted proxy aware forwarded chain", func(t *testing.T) {
		if err := middleware.SetTrustedProxies([]string{"127.0.0.1/32", "10.0.0.0/8"}); err != nil {
			t.Fatalf("SetTrustedProxies() error: %v", err)
		}
		t.Cleanup(func() {
			_ = middleware.SetTrustedProxies(nil)
		})

		req := httptest.NewRequest(http.MethodGet, "/embed/dify/chat-messages", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.4")

		if got := embedClientIP(req); got != "203.0.113.5" {
			t.Errorf("embedClientIP() = %q, want %q", got, "203.0.113.5")
		}
	})
}

func TestShouldAuditUpstreamStatus(t *testing.T) {
	testCases := []struct {
		name   string
		status int
		want   bool
	}{
		{name: "200", status: http.StatusOK, want: false},
		{name: "400", status: http.StatusBadRequest, want: false},
		{name: "401", status: http.StatusUnauthorized, want: true},
		{name: "403", status: http.StatusForbidden, want: true},
		{name: "404", status: http.StatusNotFound, want: false},
		{name: "429", status: http.StatusTooManyRequests, want: true},
		{name: "500", status: http.StatusInternalServerError, want: true},
		{name: "503", status: http.StatusServiceUnavailable, want: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldAuditUpstreamStatus(tc.status)
			if got != tc.want {
				t.Fatalf("shouldAuditUpstreamStatus(%d) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestHandleDifyProxyToken(t *testing.T) {
	// setup builds a module configured to accept https://example.com, plus
	// a helper that mints a fresh signed token for that origin (simulating
	// the render-time injection done by providers.DifyProvider.RenderBody).
	setup := func() (*Module, func() string) {
		mod := New()
		mod.proxyToken = "test-secret"
		mod.allowedOrigins = map[string]struct{}{
			"https://example.com": {},
		}
		mod.requireOriginPolicy = true
		mintFresh := func() string {
			tok, _, err := mod.issueSignedProxyToken("https://example.com", time.Now())
			if err != nil {
				t.Fatalf("issueSignedProxyToken: %v", err)
			}
			return tok
		}
		return mod, mintFresh
	}

	t.Run("refreshes when presented with a fresh render-time token", func(t *testing.T) {
		mod, mint := setup()

		req := httptest.NewRequest(http.MethodGet, "/embed/dify/token", nil)
		req.Header.Set("Origin", "https://example.com")
		req.Header.Set(embedProxyTokenHeader, mint())
		w := httptest.NewRecorder()

		mod.handleDifyProxyToken(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("cache-control = %q, want %q", got, "no-store")
		}
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Fatalf("vary = %q, want %q", got, "Origin")
		}

		var payload map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		token, _ := payload["token"].(string)
		if token == "" {
			t.Fatal("expected token in response")
		}
		if err := mod.validateSignedProxyToken(token, "https://example.com", time.Now()); err != nil {
			t.Fatalf("refreshed token should validate: %v", err)
		}
	})

	t.Run("rejects missing X-Embed-Proxy-Token header (the codex attack path)", func(t *testing.T) {
		mod, _ := setup()

		req := httptest.NewRequest(http.MethodGet, "/embed/dify/token", nil)
		req.Header.Set("Origin", "https://example.com")
		w := httptest.NewRecorder()

		mod.handleDifyProxyToken(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d (no prior token → no mint)", w.Code, http.StatusForbidden)
		}
	})

	t.Run("rejects bogus X-Embed-Proxy-Token header", func(t *testing.T) {
		mod, _ := setup()

		req := httptest.NewRequest(http.MethodGet, "/embed/dify/token", nil)
		req.Header.Set("Origin", "https://example.com")
		req.Header.Set(embedProxyTokenHeader, "not-a-real-token")
		w := httptest.NewRecorder()

		mod.handleDifyProxyToken(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d (invalid signature)", w.Code, http.StatusForbidden)
		}
	})

	t.Run("rejects token from a different origin", func(t *testing.T) {
		mod, mint := setup()
		// Mint for allowed origin, then try to use it from a different
		// origin that also happens to be allowlisted.
		mod.allowedOrigins["https://other.example"] = struct{}{}

		req := httptest.NewRequest(http.MethodGet, "/embed/dify/token", nil)
		req.Header.Set("Origin", "https://other.example")
		req.Header.Set(embedProxyTokenHeader, mint())
		w := httptest.NewRecorder()

		mod.handleDifyProxyToken(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d (origin mismatch)", w.Code, http.StatusForbidden)
		}
	})

	t.Run("rejects recently-expired token past grace window", func(t *testing.T) {
		mod, _ := setup()
		// Forge claims with Iat/Exp well outside the grace window.
		staleIssuedAt := time.Now().Add(-2 * (embedProxyTokenTTL + embedProxyTokenRefreshGrace))
		staleToken, _, err := mod.issueSignedProxyToken("https://example.com", staleIssuedAt)
		if err != nil {
			t.Fatalf("issueSignedProxyToken: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/embed/dify/token", nil)
		req.Header.Set("Origin", "https://example.com")
		req.Header.Set(embedProxyTokenHeader, staleToken)
		w := httptest.NewRecorder()

		mod.handleDifyProxyToken(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d (beyond grace)", w.Code, http.StatusForbidden)
		}
	})

	t.Run("accepts just-expired token within grace window", func(t *testing.T) {
		mod, _ := setup()
		// Issue a token backdated so it is expired by ~30s (well under the
		// 5 minute grace). The refresh handler should accept it.
		backdate := time.Now().Add(-(embedProxyTokenTTL + 30*time.Second))
		graceToken, _, err := mod.issueSignedProxyToken("https://example.com", backdate)
		if err != nil {
			t.Fatalf("issueSignedProxyToken: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/embed/dify/token", nil)
		req.Header.Set("Origin", "https://example.com")
		req.Header.Set(embedProxyTokenHeader, graceToken)
		w := httptest.NewRecorder()

		mod.handleDifyProxyToken(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d (within grace)", w.Code, http.StatusOK)
		}
	})

	t.Run("blocks disallowed origin even with valid header", func(t *testing.T) {
		mod, mint := setup()

		req := httptest.NewRequest(http.MethodGet, "/embed/dify/token", nil)
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set(embedProxyTokenHeader, mint())
		w := httptest.NewRecorder()

		mod.handleDifyProxyToken(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})

	t.Run("returns 404 when proxy token is not configured", func(t *testing.T) {
		mod := New()
		mod.proxyToken = ""
		mod.allowedOrigins = map[string]struct{}{
			"https://example.com": {},
		}
		mod.requireOriginPolicy = true

		req := httptest.NewRequest(http.MethodGet, "/embed/dify/token", nil)
		req.Header.Set("Origin", "https://example.com")
		w := httptest.NewRecorder()

		mod.handleDifyProxyToken(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})
}
