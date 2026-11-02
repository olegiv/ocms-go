// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package connectedapps

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/oauth"
	"github.com/olegiv/ocms-go/internal/store"
)

type connectionFixture struct {
	db                    *sql.DB
	q                     *store.Queries
	s                     *Service
	owner, foreign, admin store.User
	now                   time.Time
}

func newConnectionFixture(t testing.TB) *connectionFixture {
	t.Helper()
	db, err := store.NewDB(filepath.Join(t.TempDir(), "connections.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	f := &connectionFixture{db: db, q: store.New(db), s: New(db), now: time.Now().UTC().Truncate(time.Second)}
	for i, target := range []*store.User{&f.owner, &f.foreign, &f.admin} {
		role := "editor"
		if i == 2 {
			role = "admin"
		}
		u, err := f.q.CreateUser(t.Context(), store.CreateUserParams{Email: fmt.Sprintf("user-%d@example.test", i),
			Name: fmt.Sprintf("Account %d", i), PasswordHash: "not-used", Role: role, CreatedAt: f.now, UpdatedAt: f.now})
		if err != nil {
			t.Fatal(err)
		}
		*target = u
	}
	if err := f.q.UpsertOAuthClient(t.Context(), store.UpsertOAuthClientParams{ID: "https://client.test/metadata", Name: "Registered <script>name</script>", RedirectUris: `["https://client.test/callback"]`, Source: "cimd", ExpiresAt: f.now.Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	return f
}

func actor(u store.User) Actor { return Actor{UserID: u.ID, SessionVersion: u.SessionVersion} }

func (f *connectionFixture) grant(t testing.TB, u store.User, id string) store.OauthGrant {
	t.Helper()
	g, err := f.q.CreateOAuthGrant(t.Context(), store.CreateOAuthGrantParams{ID: id, UserID: u.ID, ClientID: "https://client.test/metadata",
		Scopes: "mcp:access mcp:drafts", Resource: "https://cms.test/api/mcp", SessionVersion: u.SessionVersion, CreatedAt: f.now.Unix(), ExpiresAt: f.now.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func (f *connectionFixture) key(t testing.TB, u store.User, n int, permissions string) store.ApiKey {
	t.Helper()
	k, err := f.q.CreateAPIKey(t.Context(), store.CreateAPIKeyParams{Name: fmt.Sprintf("MCP key %d", n),
		KeyHash: fmt.Sprintf("PRIVATE_HASH_%d", n), KeyPrefix: "PRIVATE_PREFIX", KeyLookup: sql.NullString{String: fmt.Sprintf("PRIVATE_LOOKUP_%d", n), Valid: true},
		Permissions: permissions, IsActive: true, CreatedBy: u.ID, CreatedAt: f.now.Add(time.Duration(n) * time.Millisecond), UpdatedAt: f.now})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestConnectionsOwnerIsolationPaginationAndSafeProjection(t *testing.T) {
	f := newConnectionFixture(t)
	f.grant(t, f.owner, "own-grant")
	f.grant(t, f.foreign, "foreign-grant")
	for i := range PageSize + 1 {
		f.key(t, f.owner, i, `["mcp:access","mcp:drafts"]`)
	}
	f.key(t, f.foreign, 99, `["mcp:access"]`)
	// Newer irrelevant and malformed keys cannot push older MCP keys off a page.
	for i := 100; i < 140; i++ {
		f.key(t, f.owner, i, `["pages:read"]`)
	}
	for i, p := range []string{`not json`, `"mcp:access"`, `["mcp:access",null]`, `["mcp:access",7]`} {
		f.key(t, f.owner, 200+i, p)
	}
	page, err := f.s.List(t.Context(), actor(f.owner), false, Pagination{1, 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.OAuth) != 1 || page.OAuth[0].ID != "own-grant" || page.OAuth[0].Host != "client.test" || len(page.APIKeys) != PageSize || !page.KeysMore {
		t.Fatalf("unexpected own page: %+v", page)
	}
	encoded, _ := json.Marshal(page)
	for _, secret := range []string{"PRIVATE_HASH", "PRIVATE_PREFIX", "PRIVATE_LOOKUP", "foreign-grant"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("metadata exposed %s", secret)
		}
	}
	next, err := f.s.List(t.Context(), actor(f.owner), false, Pagination{1, 2}, true)
	if err != nil || len(next.APIKeys) != 1 || next.KeysMore || next.APIKeys[0].Name != "MCP key 0" {
		t.Fatalf("older key inaccessible: %+v %v", next, err)
	}
	if _, err := f.s.List(t.Context(), actor(f.owner), true, Pagination{1, 1}, true); !errors.Is(err, ErrForbidden) {
		t.Fatal("editor obtained admin overview")
	}
	all, err := f.s.List(t.Context(), actor(f.admin), true, Pagination{1, 1}, true)
	if err != nil || len(all.OAuth) != 2 {
		t.Fatalf("admin overview: %+v %v", all, err)
	}
}

func TestConnectionsRevocationRechecksCurrentActorAndOwnership(t *testing.T) {
	f := newConnectionFixture(t)
	for _, u := range []store.User{f.owner, f.foreign} {
		f.grant(t, u, fmt.Sprint(u.ID))
	}
	own := f.key(t, f.owner, 1, `["mcp:access"]`)
	foreign := f.key(t, f.foreign, 2, `["mcp:access"]`)
	rest := f.key(t, f.owner, 3, `["pages:read"]`)
	for _, item := range []struct{ kind, id string }{{"oauth", fmt.Sprint(f.foreign.ID)}, {"api_key", fmt.Sprint(foreign.ID)}, {"api_key", fmt.Sprint(rest.ID)}} {
		if err := f.s.Revoke(t.Context(), actor(f.owner), false, item.kind, item.id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign/unrelated credential accepted: %v", err)
		}
	}
	if err := f.s.Revoke(t.Context(), actor(f.owner), true, "api_key", fmt.Sprint(foreign.ID)); !errors.Is(err, ErrForbidden) {
		t.Fatal("editor obtained administrative revocation")
	}
	for range 2 {
		if err := f.s.Revoke(t.Context(), actor(f.owner), false, "api_key", fmt.Sprint(own.ID)); err != nil {
			t.Fatal(err)
		}
		if err := f.s.Revoke(t.Context(), actor(f.owner), false, "oauth", fmt.Sprint(f.owner.ID)); err != nil {
			t.Fatal(err)
		}
	}
	k, _ := f.q.GetAPIKeyByID(t.Context(), own.ID)
	g, _ := f.q.GetOAuthGrant(t.Context(), fmt.Sprint(f.owner.ID))
	if k.IsActive || g.RevokedAt == 0 {
		t.Fatal("own revocation did not persist")
	}
	if err := f.s.Revoke(t.Context(), actor(f.admin), true, "api_key", fmt.Sprint(foreign.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE users SET role='editor' WHERE id=?`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Revoke(t.Context(), actor(f.admin), true, "oauth", fmt.Sprint(f.foreign.ID)); !errors.Is(err, ErrForbidden) {
		t.Fatal("stale admin role used")
	}
	if _, err := f.db.Exec(`UPDATE users SET session_version=session_version+1 WHERE id=?`, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Revoke(t.Context(), actor(f.owner), false, "api_key", fmt.Sprint(own.ID)); !errors.Is(err, ErrForbidden) {
		t.Fatal("stale session version used")
	}
}

func TestConnectionRevocationDeniesNextOAuthRequestAndInvalidatesShares(t *testing.T) {
	f := newConnectionFixture(t)
	g := f.grant(t, f.owner, "live-grant")
	raw := oauth.AccessPrefix + strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(raw))
	if err := f.q.CreateOAuthToken(t.Context(), store.CreateOAuthTokenParams{ID: "test-token", GrantID: g.ID, AccessHash: hex.EncodeToString(hash[:]), RefreshHash: "synthetic-refresh-hash", Scopes: g.Scopes, CreatedAt: f.now.Unix(), AccessExpiresAt: g.ExpiresAt}); err != nil {
		t.Fatal(err)
	}
	s, err := oauth.New(f.db, oauth.Options{Issuer: "https://cms.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyAccess(t.Context(), raw); err != nil {
		t.Fatal("normal token access failed", err)
	}
	p, err := f.q.CreatePage(t.Context(), store.CreatePageParams{Title: "Synthetic", Slug: "connected-share", Status: "draft", AuthorID: f.owner.ID, LanguageCode: "en", PageType: "post", CreatedAt: f.now, UpdatedAt: f.now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO preview_links (token_hash,page_id,revision,created_by,session_version,issuing_grant_id,created_at,expires_at) VALUES ('synthetic-share',?,1,?,?,?, ?,?)`, p.ID, f.owner.ID, f.owner.SessionVersion, g.ID, f.now.Unix(), g.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Revoke(t.Context(), actor(f.owner), false, "oauth", g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyAccess(t.Context(), raw); err == nil {
		t.Fatal("revoked OAuth grant still authorizes the next request")
	}
	var revoked int64
	if err := f.db.QueryRow(`SELECT revoked_at FROM preview_links WHERE token_hash='synthetic-share'`).Scan(&revoked); err != nil || revoked == 0 {
		t.Fatal("issuer revocation did not invalidate preview", err)
	}
}

func TestConnectionsWithDisabledOAuthAndChangedRole(t *testing.T) {
	f := newConnectionFixture(t)
	f.grant(t, f.owner, "grant")
	f.key(t, f.owner, 1, `["mcp:access"]`)
	page, err := f.s.List(t.Context(), actor(f.owner), false, Pagination{1, 1}, false)
	if err != nil || page.OAuth[0].Status != "unavailable" || page.APIKeys[0].Status != "active" {
		t.Fatalf("disabled issuer: %+v %v", page, err)
	}
	if _, err := f.db.Exec(`UPDATE users SET role='public' WHERE id=?`, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	page, err = f.s.List(t.Context(), actor(f.owner), false, Pagination{1, 1}, true)
	if err != nil || page.OAuth[0].Status != "unavailable" || page.APIKeys[0].Status != "unavailable" {
		t.Fatalf("role loss: %+v %v", page, err)
	}
	if err := f.s.Revoke(t.Context(), actor(f.owner), false, "oauth", "grant"); err != nil {
		t.Fatal("public owner cannot remove old access", err)
	}
}

func TestConnectionRequestParsing(t *testing.T) {
	for _, query := range []string{"oauth_page=0", "key_page=-1", "key_page=9223372036854775807", "oauth_page=2&oauth_page=3"} {
		v, _ := url.ParseQuery(query)
		if _, err := ParsePagination(v); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid pages accepted: %s", query)
		}
	}
	for _, body := range []string{"kind=oauth&id=owned&id=foreign", "kind=oauth&kind=api_key&id=x", "kind=api_key", "kind=unknown&id=x", strings.Repeat("x", 4097)} {
		r := httptest.NewRequest(http.MethodPost, "/account/connected-apps/revoke", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, _, err := ParseRevoke(httptest.NewRecorder(), r); !errors.Is(err, ErrInvalid) {
			t.Error("invalid revoke form accepted")
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/account/connected-apps/revoke?id=foreign", strings.NewReader("kind=oauth&id=own"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, _, err := ParseRevoke(httptest.NewRecorder(), r); err == nil {
		t.Fatal("query selector accepted")
	}
	r = httptest.NewRequest(http.MethodPost, "/account/connected-apps/revoke", strings.NewReader("kind=oauth&id=own"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if kind, id, err := ParseRevoke(httptest.NewRecorder(), r); err != nil || kind != "oauth" || id != "own" {
		t.Fatal("normal form rejected")
	}
}

func BenchmarkConnectedAppsOverview(b *testing.B) {
	f := newConnectionFixture(b)
	for i := range 100 {
		f.grant(b, f.owner, strconv.Itoa(i))
		f.key(b, f.owner, i, `["mcp:access"]`)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := f.s.List(b.Context(), actor(f.owner), false, Pagination{1, 1}, true); err != nil {
			b.Fatal(err)
		}
	}
}
