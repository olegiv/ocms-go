// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestAdminConnectionsOverviewAndRevocation(t *testing.T) {
	env := newTestEnv(t)
	otherID := env.createUser("other-app@example.test", "Other account")
	raw := env.createKeyWith(func(p *store.CreateAPIKeyParams) { p.CreatedBy = otherID; p.Name = "Other user's MCP key" }, model.PermissionMCPAccess)
	keys, err := env.q.ListAPIKeysByUser(t.Context(), store.ListAPIKeysByUserParams{CreatedBy: otherID, Limit: 10})
	if err != nil || len(keys) != 1 {
		t.Fatal("missing test key", err)
	}
	admin, err := env.q.GetUserByID(t.Context(), env.userID)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(middleware.CSRF(middleware.DefaultCSRFConfig([]byte("test-secret-key-32-bytes-long!!!"), true)))
	r.Route("/admin", env.module.RegisterAdminRoutes)
	request := func(method, path string, form url.Values, user *store.User, crossSite bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if user != nil {
			req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, *user))
		}
		if crossSite {
			req.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := request(http.MethodGet, adminPath, nil, nil, false); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous admin overview=%d", w.Code)
	}
	w := request(http.MethodGet, adminPath, nil, &admin, false)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Other user&#39;s MCP key") || !strings.Contains(w.Body.String(), `action="/admin/mcp/revoke"`) || strings.Contains(w.Body.String(), raw) {
		t.Fatalf("admin overview status=%d", w.Code)
	}
	form := url.Values{"kind": {"api_key"}, "id": {strconv.FormatInt(keys[0].ID, 10)}}
	if w := request(http.MethodPost, adminPath+"/revoke", form, &admin, true); w.Code != http.StatusForbidden {
		t.Fatal("admin revoke lacks CSRF protection")
	}
	if w := request(http.MethodPost, adminPath+"/revoke", form, &admin, false); w.Code != http.StatusSeeOther {
		t.Fatalf("admin revoke=%d", w.Code)
	}
	k, err := env.q.GetAPIKeyByID(t.Context(), keys[0].ID)
	if err != nil || k.IsActive {
		t.Fatal("admin could not revoke another account's MCP key")
	}
	if _, err := env.db.Exec(`UPDATE users SET role='editor' WHERE id=?`, admin.ID); err != nil {
		t.Fatal(err)
	}
	// Even a stale middleware principal cannot perform administrative writes.
	if w := request(http.MethodPost, adminPath+"/revoke", form, &admin, false); w.Code != http.StatusForbidden {
		t.Fatal("stale admin principal accepted")
	}
}
