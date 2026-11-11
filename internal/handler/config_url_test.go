// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestConfigUpdateURLValues drives the config save for site_url and site_logo:
// a valid value is stored normalized, and an invalid one is refused with the
// stored value kept, so the frontend never renders a link templ blocks.
func TestConfigUpdateURLValues(t *testing.T) {
	// Fully migrated: the save writes config_translations, which testDB lacks.
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	sm := testSessionManager(t)
	queries := store.New(db)
	h := NewConfigHandler(db, newStatusTestRenderer(t, db, sm), sm, cache.NewManager(queries))
	admin := createTestAdminUser(t, db)
	ctx := context.Background()

	// site_logo is edited only when its row exists, as on older installs.
	if _, err := queries.UpsertConfig(ctx, store.UpsertConfigParams{
		Key: model.SiteLogoConfigKey, Value: "/uploads/old.png", Type: model.ConfigTypeString,
	}); err != nil {
		t.Fatalf("UpsertConfig: %v", err)
	}

	for _, tc := range []struct {
		name, key, value, want string
		accepted               bool
	}{
		{"site_url origin", model.ConfigKeySiteURL, " https://example.com/ ", "https://example.com", true},
		{"site_url with path", model.ConfigKeySiteURL, "https://example.com/blog", "https://example.com", false},
		{"site_url without scheme", model.ConfigKeySiteURL, "example.com:8443", "https://example.com", false},
		{"site_logo data URI", model.SiteLogoConfigKey, " data:image/svg+xml;base64,PHN2Zz48L3N2Zz4= ", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", true},
		{"site_logo javascript", model.SiteLogoConfigKey, "javascript:alert(1)", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", false},
		{"site_logo relative without slash", model.SiteLogoConfigKey, "uploads/logo.png", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Post both fields so the one under test is the only change.
			form := url.Values{}
			for _, key := range []string{model.ConfigKeySiteURL, model.SiteLogoConfigKey} {
				cfg, err := queries.GetConfigByKey(ctx, key)
				if err != nil {
					t.Fatalf("GetConfigByKey(%s): %v", key, err)
				}
				form.Set(key, cfg.Value)
			}
			form.Set(tc.key, tc.value)

			req := httptest.NewRequest(http.MethodPost, "/admin/config", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = addUserToContext(requestWithSession(sm, req), &admin)
			w := httptest.NewRecorder()
			h.Update(w, req)

			redirected := w.Code == http.StatusSeeOther || w.Code == http.StatusFound
			if redirected != tc.accepted {
				t.Errorf("status = %d, accepted = %v", w.Code, tc.accepted)
			}
			cfg, err := queries.GetConfigByKey(ctx, tc.key)
			if err != nil || cfg.Value != tc.want {
				t.Errorf("stored %s = %q, %v; want %q", tc.key, cfg.Value, err, tc.want)
			}
		})
	}
}
