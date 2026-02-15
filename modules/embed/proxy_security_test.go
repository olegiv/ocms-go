// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
