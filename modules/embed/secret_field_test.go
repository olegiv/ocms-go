// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
	"github.com/olegiv/ocms-go/modules/embed/providers"
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

// TestStoredSecretNeverReachesTheView fails when the saved API key is copied
// into the form schema, or when the rendered page contains it.
func TestStoredSecretNeverReachesTheView(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()
	schema := schemaWithStoredValues(m.getProvider("dify").SettingsSchema(), map[string]string{"api_key": "app-secret-9", "bot_name": "Bot"})
	var keyField *providers.SettingField
	for i := range schema {
		if schema[i].ID == "api_key" {
			keyField = &schema[i]
		}
		if schema[i].ID == "bot_name" && schema[i].Default != "Bot" {
			t.Fatalf("plain field default = %q", schema[i].Default)
		}
	}
	if keyField == nil || keyField.Default != "" || !keyField.HasStoredValue {
		t.Fatalf("api_key schema = %+v; want empty Default and HasStoredValue", keyField)
	}

	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	// Even a schema that carries the secret in Default must not render it.
	leaky := schema
	for i := range leaky {
		if leaky[i].ID == "api_key" {
			leaky[i].Default = "app-secret-9"
		}
	}
	pc := &adminviews.PageContext{AdminLang: "en", User: adminviews.UserInfo{Role: "admin"}}
	var buffer bytes.Buffer
	if err := EmbedProviderPage(pc, EmbedProviderViewData{ProviderID: "dify", ProviderName: "Dify", Schema: leaky}).Render(templ.WithNonce(t.Context(), "n"), &buffer); err != nil {
		t.Fatal(err)
	}
	html := buffer.String()
	if strings.Contains(html, "app-secret-9") {
		t.Fatal("rendered page contains the stored secret")
	}
	for _, want := range []string{`name="api_key"`, `type="password"`, `autocomplete="new-password"`, "leave blank to keep"} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page lacks %s", want)
		}
	}
	if m := regexp.MustCompile(`<input[^>]*name="api_key"[^>]*>`).FindString(html); strings.Contains(m, "required") {
		t.Fatalf("password field must not be required while a value is stored: %s", m)
	}
}

// TestSaveProviderSettingsRefusesWhenStoredSettingsUnreadable fails when a
// blank secret submission overwrites the key because the existing row could
// not be loaded.
func TestSaveProviderSettingsRefusesWhenStoredSettingsUnreadable(t *testing.T) {
	m, cleanup := testModuleDB(t)
	defer cleanup()
	saveDifyForm(t, m, url.Values{"api_endpoint": {"https://api.dify.ai/v1"}, "api_key": {"app-secret-1"}, "bot_name": {"Bot"}})
	if _, err := m.ctx.DB.Exec(`UPDATE embed_settings SET settings = 'not json' WHERE provider = 'dify'`); err != nil {
		t.Fatal(err)
	}
	saveDifyForm(t, m, url.Values{"api_endpoint": {"https://api.dify.ai/v1"}, "api_key": {""}, "bot_name": {"Renamed"}})
	var raw string
	if err := m.ctx.DB.QueryRow(`SELECT settings FROM embed_settings WHERE provider = 'dify'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "not json" {
		t.Fatalf("settings row was overwritten although it could not be loaded: %s", raw)
	}
}
