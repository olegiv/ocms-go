// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
)

func saveDifyForm(t *testing.T, m *Module, fields url.Values) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/embed/dify", strings.NewReader(fields.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", "dify")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, store.User{ID: 1, Email: "admin@example.com", Role: "admin"})
	w := httptest.NewRecorder()
	m.handleSaveProviderSettings(w, req.WithContext(ctx))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save: status = %d, body %s", w.Code, w.Body.String())
	}
}

// TestSaveProviderSettingsKeepsStoredSecretWhenBlank fails when an empty
// password field overwrites the stored API key: the form never echoes the key
// back, so a blank submission must keep it (finding L-4, 2026-10-11).
func TestSaveProviderSettingsKeepsStoredSecretWhenBlank(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()

	saveDifyForm(t, m, url.Values{"api_endpoint": {"https://api.dify.ai/v1"}, "api_key": {"app-secret-1"}, "bot_name": {"Bot"}})
	saveDifyForm(t, m, url.Values{"api_endpoint": {"https://api.dify.ai/v1"}, "api_key": {""}, "bot_name": {"Bot renamed"}})
	ps, err := loadProviderSettings(m.ctx.DB, "dify")
	if err != nil {
		t.Fatal(err)
	}
	if ps.Settings["api_key"] != "app-secret-1" || ps.Settings["bot_name"] != "Bot renamed" {
		t.Fatalf("blank secret submission changed settings: %v", ps.Settings)
	}

	saveDifyForm(t, m, url.Values{"api_endpoint": {"https://api.dify.ai/v1"}, "api_key": {"app-secret-2"}, "bot_name": {"Bot"}})
	ps, err = loadProviderSettings(m.ctx.DB, "dify")
	if err != nil {
		t.Fatal(err)
	}
	if ps.Settings["api_key"] != "app-secret-2" {
		t.Fatalf("new secret was not stored: %v", ps.Settings)
	}
}

// TestDifyAPIKeyIsPasswordField fails when the Dify key goes back to a plain
// text field.
func TestDifyAPIKeyIsPasswordField(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()
	for _, field := range m.getProvider("dify").SettingsSchema() {
		if field.ID == "api_key" && field.Type != "password" {
			t.Fatalf("api_key field type = %q, want password", field.Type)
		}
	}
	if inputType("password") != "password" {
		t.Fatal("password fields must render as <input type=password>")
	}
}
