// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"modernc.org/sqlite"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

var authPolicyFixtureID atomic.Uint64

func TestAPIKeyAuthCannotMixOldPermissionsWithNewSourcePolicy(t *testing.T) {
	loaded, resume := make(chan struct{}), make(chan struct{})
	var paused atomic.Bool
	function := fmt.Sprintf("pause_auth_policy_%d", authPolicyFixtureID.Add(1))
	if err := sqlite.RegisterScalarFunction(function, 1, func(_ *sqlite.FunctionContext, values []driver.Value) (driver.Value, error) {
		if paused.CompareAndSwap(false, true) {
			close(loaded)
			<-resume
		}
		return values[0], nil
	}); err != nil {
		t.Fatal(err)
	}
	db, err := store.NewDB(filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	user := createTestAdminUser(t, db)
	q := store.New(db)
	raw, prefix, err := model.GenerateAPIAccessKey()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := model.HashAPIKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	key, err := q.CreateAPIKey(t.Context(), store.CreateAPIKeyParams{Name: "Race fixture", KeyHash: hash, KeyPrefix: prefix, KeyLookup: sql.NullString{String: model.APIKeyLookup(raw), Valid: true}, Permissions: `["pages:read"]`, IsActive: true, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.ReplaceAPIKeySourceCIDRs(t.Context(), key.ID, []string{"198.51.100.1/32"}); err != nil {
		t.Fatal(err)
	}
	// A view supplies a deterministic pause in the actual credential SELECT,
	// without adding a production hook or sleeping during Argon2 verification.
	if _, err := db.Exec(`ALTER TABLE api_keys RENAME TO api_key_rows;
		CREATE VIEW api_keys AS SELECT id,name,` + function + `(key_hash) AS key_hash,key_prefix,permissions,last_used_at,expires_at,is_active,created_by,created_at,updated_at,key_lookup FROM api_key_rows;
		CREATE TRIGGER update_key_view INSTEAD OF UPDATE ON api_keys BEGIN
		UPDATE api_key_rows SET name=NEW.name, permissions=NEW.permissions, expires_at=NEW.expires_at, is_active=NEW.is_active,
		updated_at=NEW.updated_at,last_used_at=NEW.last_used_at,key_lookup=NEW.key_lookup WHERE id=OLD.id; END;`); err != nil {
		t.Fatal(err)
	}
	protected := middleware.APIKeyAuth(db)(middleware.RequirePermission(model.PermissionPagesRead)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/pages/private-draft", nil)
		req.RemoteAddr = "203.0.113.1:1234"
		req.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, req)
		done <- w
	}()
	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("credential read did not reach barrier")
	}
	sm := scs.New()
	h := NewAPIKeysHandler(db, newStatusTestRenderer(t, db, sm), sm)
	form := url.Values{"name": {"Changed"}, "permissions": {"media:read"}, "source_cidrs": {"203.0.113.1/32"}, "is_active": {"on"}}
	req := httptest.NewRequest(http.MethodPut, "/admin/api-keys/"+strconv.FormatInt(key.ID, 10), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = addUserToContext(requestWithSession(sm, req), &user)
	route := chi.NewRouteContext()
	route.URLParams.Add("id", strconv.FormatInt(key.ID, 10))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	h.Update(httptest.NewRecorder(), req)
	if sm.GetString(req.Context(), "flash_type") != "success" {
		t.Fatal("concurrent administrator update did not commit")
	}
	close(resume)
	select {
	case w := <-done:
		if w.Code == http.StatusOK {
			t.Fatal("request combined old pages:read permission with newly allowed source")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("authentication did not resume")
	}
	// A new request has exactly the new committed policy: media allowed,
	// pages denied. No second hash or owner interaction is required.
	for _, permission := range []string{model.PermissionPagesRead, model.PermissionMediaRead} {
		r := httptest.NewRequest(http.MethodGet, "/api/v2/check", nil)
		r.RemoteAddr = "203.0.113.1:1234"
		r.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		middleware.APIKeyAuth(db)(middleware.RequirePermission(permission)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))).ServeHTTP(w, r)
		if (w.Code == http.StatusOK) != (permission == model.PermissionMediaRead) {
			t.Fatalf("new policy failed for %s: %d", permission, w.Code)
		}
	}
}
