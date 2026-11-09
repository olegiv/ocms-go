// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

var changedKeyFixtureID atomic.Uint64

// insertKeyOwner adds the administrator that created the keys of a test.
func insertKeyOwner(t *testing.T, db *sql.DB) store.User {
	t.Helper()
	res, err := db.Exec(`INSERT INTO users (email, name, role, password_hash) VALUES ('owner@example.com', 'Owner', 'admin', 'x')`)
	if err != nil {
		t.Fatalf("insert key owner: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.New(db).GetUserByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

// TestAPIKeyAuthNamesAKeyThatChangedMidRequest pins R1 P3-11. A key row that
// changes between the credential read and the source-policy check is denied,
// but the denial was reported through the "no source CIDR" branch, so the
// caller (and the log) were told the key had no source restriction when it had
// one. The denial stays; its reason is now the real one.
func TestAPIKeyAuthNamesAKeyThatChangedMidRequest(t *testing.T) {
	for _, tc := range []struct{ name, change string }{
		{"permissions", `permissions = '["media:read"]'`},
		{"deactivated", `is_active = 0`},
		{"new owner", `created_by = (SELECT id FROM users WHERE email = 'new-owner@example.test')`},
		{"expiration changed", `expires_at = '2000-01-01 00:00:00'`},
		{"expiration removed", `expires_at = NULL`},
	} {
		t.Run(tc.name, func(t *testing.T) { testAPIKeyChangedMidRequest(t, tc.change) })
	}
}

func testAPIKeyChangedMidRequest(t *testing.T, change string) {
	t.Helper()
	loaded, resume := make(chan struct{}), make(chan struct{})
	var armed, paused atomic.Bool
	function := fmt.Sprintf("pause_changed_key_%d", changedKeyFixtureID.Add(1))
	if err := sqlite.RegisterScalarFunction(function, 1, func(_ *sqlite.FunctionContext, values []driver.Value) (driver.Value, error) {
		if armed.Load() && paused.CompareAndSwap(false, true) {
			close(loaded)
			<-resume
		}
		return values[0], nil
	}); err != nil {
		t.Fatal(err)
	}
	db, err := store.NewDB(filepath.Join(t.TempDir(), "changed.db"))
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
	if err = store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	SetRequireAPIKeySourceCIDRs(true)
	t.Cleanup(func() { SetRequireAPIKeySourceCIDRs(false) })

	user := insertKeyOwner(t, db)
	if _, err = db.Exec(`INSERT INTO users (email, name, role, password_hash) VALUES ('new-owner@example.test', 'New owner', 'admin', 'x')`); err != nil {
		t.Fatal(err)
	}
	raw, prefix, err := model.GenerateAPIAccessKey()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := model.HashAPIKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	q := store.New(db)
	key, err := q.CreateAPIKey(t.Context(), store.CreateAPIKeyParams{Name: "Changing", KeyHash: hash, KeyPrefix: prefix,
		KeyLookup: sql.NullString{String: model.APIKeyLookup(raw), Valid: true}, Permissions: `["pages:read"]`, IsActive: true,
		ExpiresAt: sql.NullTime{Time: now.Add(time.Hour), Valid: true}, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err = q.ReplaceAPIKeySourceCIDRs(t.Context(), key.ID, []string{"198.51.100.1/32"}); err != nil {
		t.Fatal(err)
	}
	// A view in front of the table pauses the credential read at a known point
	// and lets the test change the row before the policy check re-reads it.
	if _, err = db.Exec(`ALTER TABLE api_keys RENAME TO api_key_rows;
		CREATE VIEW api_keys AS SELECT id,name,` + function + `(key_hash) AS key_hash,key_prefix,permissions,last_used_at,expires_at,is_active,created_by,created_at,updated_at,key_lookup FROM api_key_rows;
		CREATE TRIGGER update_key_view INSTEAD OF UPDATE ON api_keys BEGIN
		UPDATE api_key_rows SET name=NEW.name, permissions=NEW.permissions, expires_at=NEW.expires_at, is_active=NEW.is_active,
		updated_at=NEW.updated_at,last_used_at=NEW.last_used_at,key_lookup=NEW.key_lookup WHERE id=OLD.id; END;`); err != nil {
		t.Fatal(err)
	}
	protected := APIKeyAuth(db)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/pages", nil)
		req.RemoteAddr = "198.51.100.1:1234"
		req.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, req)
		return w
	}

	// Control: with the key left alone this request is allowed, so the denial
	// below can only come from the change.
	if w := request(); w.Code != http.StatusOK {
		t.Fatalf("control request status = %d (%s)", w.Code, w.Body.String())
	}

	// A successful request records last_used_at in a background goroutine; let
	// it finish so the barrier below is hit by the credential read, not by it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var lastUsed sql.NullTime
		if err = db.QueryRow(`SELECT last_used_at FROM api_key_rows WHERE id = ?`, key.ID).Scan(&lastUsed); err != nil {
			t.Fatal(err)
		}
		if lastUsed.Valid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("last_used_at was never recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}

	armed.Store(true)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request() }()
	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("credential read did not reach the barrier")
	}
	if _, err = db.Exec(`UPDATE api_key_rows SET `+change+` WHERE id = ?`, key.ID); err != nil {
		t.Fatal(err)
	}
	close(resume)
	var w *httptest.ResponseRecorder
	select {
	case w = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("authentication did not resume")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("a key changed mid-request: status = %d, want 401", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "source CIDR") {
		t.Fatalf("the denial blames the key's source CIDRs, which it has: %s", body)
	}
	if !strings.Contains(body, "changed while it was being authenticated") {
		t.Fatalf("the denial does not say the key changed: %s", body)
	}
}

// TestRecheckAPIKeyPolicyDeniesAChangedOrMissingKeyWithoutError keeps the
// queued-writer contract: a key that changed or vanished is a plain denial
// (false, nil), never an error that a caller would report as a server fault.
func TestRecheckAPIKeyPolicyDeniesAChangedOrMissingKeyWithoutError(t *testing.T) {
	db, err := store.NewDB(filepath.Join(t.TempDir(), "recheck.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err = store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	user := insertKeyOwner(t, db)
	now := time.Now()
	q := store.New(db)
	key, err := q.CreateAPIKey(t.Context(), store.CreateAPIKeyParams{Name: "Recheck", KeyHash: "hash", KeyPrefix: "rchk",
		Permissions: `["pages:read"]`, IsActive: true, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := RecheckAPIKeyPolicy(t.Context(), q, key, "198.51.100.1"); err != nil || !allowed {
		t.Fatalf("unchanged key: allowed=%v err=%v", allowed, err)
	}
	changed := key
	changed.Permissions = `["pages:write"]`
	if allowed, err := RecheckAPIKeyPolicy(t.Context(), q, changed, "198.51.100.1"); err != nil || allowed {
		t.Fatalf("changed key: allowed=%v err=%v, want a denial without an error", allowed, err)
	}
	missing := key
	missing.ID += 100
	if allowed, err := RecheckAPIKeyPolicy(t.Context(), q, missing, "198.51.100.1"); err != nil || allowed {
		t.Fatalf("missing key: allowed=%v err=%v, want a denial without an error", allowed, err)
	}
}
