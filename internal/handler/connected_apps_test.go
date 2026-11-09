// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestConnectedAppsPageAndRevocationThroughSessionAndCSRF(t *testing.T) {
	env := newMFATestEnv(t, false)
	router := env.router.(*chi.Mux)
	values := enableTestOAuth(t, env)
	router.Group(func(r chi.Router) {
		r.Use(middleware.Auth(env.sm), middleware.LoadUser(env.sm, env.db), middleware.LoadSiteConfig(env.db, nil))
		r.Get(RouteAccountConnectedApps, env.h.ConnectedApps)
		r.Post(RouteAccountConnectedApps+"/revoke", env.h.RevokeConnectedApp)
	})
	user := env.createUser(t, "editor")
	q := store.New(env.db)
	now := time.Now().UTC()
	grant, err := q.CreateOAuthGrant(t.Context(), store.CreateOAuthGrantParams{ID: "own-connection", UserID: user.ID, ClientID: values.Get("client_id"), Scopes: "mcp:access mcp:drafts", Resource: env.h.oauth.Resource(), SessionVersion: user.SessionVersion, CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	// Only a grant that issued a token is a connection.
	if err = q.CreateOAuthToken(t.Context(), store.CreateOAuthTokenParams{ID: "own-connection-token", GrantID: grant.ID,
		AccessHash: "own-access-hash", RefreshHash: "own-refresh-hash", Scopes: grant.Scopes, CreatedAt: now.Unix(),
		AccessExpiresAt: grant.ExpiresAt}); err != nil {
		t.Fatal(err)
	}
	key, err := q.CreateAPIKey(t.Context(), store.CreateAPIKeyParams{Name: "Own MCP key", KeyHash: "PRIVATE_CONNECTION_HASH", KeyPrefix: "PRIVATE_PREFIX", KeyLookup: sql.NullString{String: "PRIVATE_LOOKUP", Valid: true}, Permissions: `["mcp:access"]`, IsActive: true, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if rr := authRequest(env.router, http.MethodGet, RouteAccountConnectedApps, nil); rr.Header().Get("Location") != RouteLogin {
		t.Fatal("anonymous account access was not denied")
	}
	login := authRequest(env.router, http.MethodPost, RouteLogin, url.Values{"email": {user.Email}, "password": {mfaTestPassword}})
	cookie := sessionCookie(t, login, env.sm)
	rr := authRequest(env.router, http.MethodGet, RouteAccountConnectedApps, nil, cookie)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Own MCP key") || !strings.Contains(rr.Body.String(), grant.ID) || !strings.Contains(rr.Header().Get("Cache-Control"), "no-store") || rr.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatalf("account overview failed: status=%d", rr.Code)
	}
	for _, forbidden := range []string{"PRIVATE_CONNECTION_HASH", "PRIVATE_PREFIX", "PRIVATE_LOOKUP", "<script>client</script>"} {
		if strings.Contains(rr.Body.String(), forbidden) {
			t.Fatalf("page exposed unsafe value %s", forbidden)
		}
	}
	if !strings.Contains(rr.Body.String(), "&lt;script&gt;client&lt;/script&gt;") {
		t.Fatal("registered client name not escaped")
	}
	form := url.Values{"kind": {"api_key"}, "id": {strconv.FormatInt(key.ID, 10)}}
	req := httptest.NewRequest(http.MethodPost, RouteAccountConnectedApps+"/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatal("cross-site revoke accepted")
	}
	still, _ := q.GetAPIKeyByID(t.Context(), key.ID)
	if !still.IsActive {
		t.Fatal("denied cross-site request changed key")
	}
	other := env.createUser(t, "admin")
	foreign, err := q.CreateAPIKey(t.Context(), store.CreateAPIKeyParams{Name: "Other MCP key", KeyHash: "foreign-hash", KeyPrefix: "foreign", Permissions: `["mcp:access"]`, IsActive: true, CreatedBy: other.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	foreignForm := url.Values{"kind": {"api_key"}, "id": {strconv.FormatInt(foreign.ID, 10)}}
	rr = authRequest(env.router, http.MethodPost, RouteAccountConnectedApps+"/revoke", foreignForm, cookie)
	if rr.Code != http.StatusNotFound || rr.Header().Get("Location") != "" {
		t.Fatalf("foreign key revoke status=%d, want 404", rr.Code)
	}
	if stored, loadErr := q.GetAPIKeyByID(t.Context(), foreign.ID); loadErr != nil || !stored.IsActive {
		t.Fatalf("foreign key did not stay active: %v", loadErr)
	}
	if _, err = env.db.Exec(`CREATE TRIGGER fail_connection_revoke BEFORE UPDATE ON api_keys
		WHEN OLD.is_active = 1 AND NEW.is_active = 0 BEGIN SELECT RAISE(ABORT, 'connection fixture fault'); END`); err != nil {
		t.Fatal(err)
	}
	logs := captureLogs(t)
	rr = authRequest(env.router, http.MethodPost, RouteAccountConnectedApps+"/revoke", form, cookie)
	if rr.Code != http.StatusInternalServerError || strings.Contains(rr.Body.String(), "connection fixture fault") ||
		!strings.Contains(logs.String(), "connection fixture fault") {
		t.Fatalf("storage fault status=%d, want a logged 500 with a generic response", rr.Code)
	}
	if stored, loadErr := q.GetAPIKeyByID(t.Context(), key.ID); loadErr != nil || !stored.IsActive {
		t.Fatalf("failed revoke did not leave the key active: %v", loadErr)
	}
	var revokeEvents int
	if err = env.db.QueryRow(`SELECT COUNT(*) FROM events WHERE message='Account revoked connected app access'`).Scan(&revokeEvents); err != nil || revokeEvents != 0 {
		t.Fatalf("failed revocations logged %d successes: %v", revokeEvents, err)
	}
	if _, err = env.db.Exec(`DROP TRIGGER fail_connection_revoke`); err != nil {
		t.Fatal(err)
	}
	rr = authRequest(env.router, http.MethodPost, RouteAccountConnectedApps+"/revoke", form, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != RouteAccountConnectedApps {
		t.Fatalf("own key revoke status=%d", rr.Code)
	}
	still, _ = q.GetAPIKeyByID(t.Context(), key.ID)
	if still.IsActive {
		t.Fatal("key revoke did not persist")
	}
	rr = authRequest(env.router, http.MethodPost, RouteAccountConnectedApps+"/revoke", url.Values{"kind": {"oauth"}, "id": {grant.ID}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatal("own grant revoke failed")
	}
	ended, _ := q.GetOAuthGrant(t.Context(), grant.ID)
	if ended.RevokedAt == 0 {
		t.Fatal("grant revoke did not persist")
	}
	if rr := authRequest(env.router, http.MethodGet, RouteAccountConnectedApps+"?key_page=0", nil, cookie); rr.Code != http.StatusBadRequest {
		t.Fatal("invalid page accepted")
	}
}

func TestConnectedAppsDemoGuard(t *testing.T) {
	const marker = "connected-app demo mutation denied"
	if os.Getenv("OCMS_CONNECTION_DEMO_CHILD") != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary, "-test.run=^TestConnectedAppsDemoGuard$", "-test.v")
		cmd.Env = append(os.Environ(), "OCMS_DEMO_MODE=true", "OCMS_CONNECTION_DEMO_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), marker) {
			t.Fatalf("demo guard check: %v\n%s", err, out)
		}
		return
	}
	env := newMFATestEnv(t, false)
	u := env.createUser(t, "editor")
	now := time.Now()
	k, err := store.New(env.db).CreateAPIKey(t.Context(), store.CreateAPIKeyParams{Name: "Demo key", KeyHash: "test-hash", KeyPrefix: "test", Permissions: `["mcp:access"]`, IsActive: true, CreatedBy: u.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, RouteAccountConnectedApps+"/revoke", strings.NewReader("kind=api_key&id="+strconv.FormatInt(k.ID, 10)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = addUserToContext(requestWithSession(env.sm, req), &u)
	w := httptest.NewRecorder()
	env.h.RevokeConnectedApp(w, req)
	after, err := store.New(env.db).GetAPIKeyByID(t.Context(), k.ID)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != RouteAccountConnectedApps || err != nil || !after.IsActive {
		t.Fatal("demo account revoke changed credential")
	}
	t.Log(marker)
}
