// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

type fixture struct {
	s        *Service
	db       *sql.DB
	dbPath   string
	user     store.User
	verifier string
	values   url.Values
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "oauth.db")
	db, err := store.NewDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	user, err := store.New(db).CreateUser(context.Background(), store.CreateUserParams{
		Email: "author@example.test", PasswordHash: "unused", Name: "Author", Role: "editor",
		CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db, Options{Issuer: "https://cms.test"})
	if err != nil {
		t.Fatal(err)
	}
	client := registeredClient{ID: "https://client.test/metadata", Name: "Test client", AuthMethod: "none",
		Redirects: []string{"https://client.test/callback", "http://127.0.0.1/callback"}}
	if err := s.saveClient(context.Background(), client, "cimd", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 64)
	digest := sha256.Sum256([]byte(verifier))
	return &fixture{s: s, db: db, dbPath: dbPath, user: user, verifier: verifier, values: url.Values{
		"client_id": {client.ID}, "redirect_uri": {client.Redirects[0]}, "response_type": {"code"},
		"scope": {"mcp:access mcp:drafts pages:read"}, "resource": {s.Resource()}, "state": {"opaque-state"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"},
	}}
}

func (f *fixture) code(t *testing.T) string {
	t.Helper()
	request, err := f.s.Prepare(context.Background(), f.values)
	if err != nil {
		t.Fatal(err)
	}
	code, err := f.s.Authorize(context.Background(), request, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func (f *fixture) exchange(values url.Values) (int, map[string]any) {
	if values.Get("client_id") == "" {
		values.Set("client_id", f.values.Get("client_id"))
	}
	if values.Get("resource") == "" {
		values.Set("resource", f.s.Resource())
	}
	r := httptest.NewRequest(http.MethodPost, "https://cms.test/oauth/token", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.s.Token(w, r)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func (f *fixture) redeem(t *testing.T, code string) map[string]any {
	t.Helper()
	status, body := f.exchange(url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {f.values.Get("redirect_uri")}, "code_verifier": {f.verifier}})
	if status != http.StatusOK {
		t.Fatalf("exchange status=%d error=%v", status, body["error"])
	}
	return body
}

func access(body map[string]any) string  { v, _ := body["access_token"].(string); return v }
func refresh(body map[string]any) string { v, _ := body["refresh_token"].(string); return v }

func TestCodeExchangePKCERetryAndSingleUse(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	status, _ := f.exchange(url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {f.values.Get("redirect_uri")}, "code_verifier": {strings.Repeat("b", 64)}})
	if status != 400 {
		t.Fatalf("wrong verifier: %d", status)
	}
	body := f.redeem(t, code)
	p, err := f.s.VerifyAccess(context.Background(), access(body))
	if err != nil || p.UserID != f.user.ID || len(p.Scopes) != 3 {
		t.Fatalf("principal=%+v err=%v", p, err)
	}
	status, _ = f.exchange(url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {f.values.Get("redirect_uri")}, "code_verifier": {f.verifier}})
	if status != 400 {
		t.Fatalf("code reused: %d", status)
	}
	var stored string
	if err := f.db.QueryRow("SELECT access_hash FROM oauth_tokens LIMIT 1").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == access(body) || stored != tokenHash(access(body)) {
		t.Fatal("access credential not hashed")
	}
}

func TestRefreshRotationScopeBindingAndDurableReuseRevocation(t *testing.T) {
	f := newFixture(t)
	body := f.redeem(t, f.code(t))
	status, denied := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(body)}, "scope": {"pages:write"}})
	if status != 400 || denied["error"] != "invalid_scope" {
		t.Fatalf("scope escalation status=%d", status)
	}
	status, next := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(body)}, "scope": {"mcp:access"}})
	if status != 200 {
		t.Fatalf("refresh status=%d error=%v", status, next["error"])
	}
	if _, err := f.s.VerifyAccess(context.Background(), access(body)); err == nil {
		t.Fatal("old access still active")
	}
	p, err := f.s.VerifyAccess(context.Background(), access(next))
	if err != nil || len(p.Scopes) != 1 {
		t.Fatal("reduced scope not preserved")
	}
	status, denied = f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(body)}})
	if status != 400 || denied["error"] != "invalid_grant" {
		t.Fatalf("replay status=%d", status)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.NewDB(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted, err := New(reopened, Options{Issuer: "https://cms.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.VerifyAccess(context.Background(), access(next)); err == nil {
		t.Fatal("family revocation was rolled back")
	}
}

func TestWrongRefreshClientCannotUseOrRevokeFamily(t *testing.T) {
	f := newFixture(t)
	body := f.redeem(t, f.code(t))
	other := registeredClient{ID: "https://other.test/metadata", Name: "Other", AuthMethod: "none", Redirects: []string{"https://other.test/callback"}}
	if err := f.s.saveClient(context.Background(), other, "cimd", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	status, _ := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(body)}, "client_id": {other.ID}})
	if status != 400 {
		t.Fatalf("wrong client status=%d", status)
	}
	if _, err := f.s.VerifyAccess(context.Background(), access(body)); err != nil {
		t.Fatal("wrong client revoked another grant")
	}
}

func TestConcurrentCodeRedemption(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			status, _ := f.exchange(url.Values{"grant_type": {"authorization_code"}, "code": {code},
				"redirect_uri": {f.values.Get("redirect_uri")}, "code_verifier": {f.verifier}})
			results <- status
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for status := range results {
		if status == 200 {
			success++
		} else if status != 400 {
			t.Errorf("status=%d", status)
		}
	}
	if success != 1 {
		t.Fatalf("successful redemptions=%d", success)
	}
}

func TestTokenInsertFailureRollsBackCodeConsumption(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	_, err := f.db.Exec("CREATE TRIGGER reject_token BEFORE INSERT ON oauth_tokens BEGIN SELECT RAISE(ABORT,'fixture'); END")
	if err != nil {
		t.Fatal(err)
	}
	status, _ := f.exchange(url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {f.values.Get("redirect_uri")}, "code_verifier": {f.verifier}})
	if status != 503 {
		t.Fatalf("storage failure status=%d", status)
	}
	if _, err := f.db.Exec("DROP TRIGGER reject_token"); err != nil {
		t.Fatal(err)
	}
	f.redeem(t, code)
}

func TestAuthorizationRejectsUnsafeRequests(t *testing.T) {
	for _, tc := range []struct{ field, value string }{
		{"redirect_uri", "https://client.test/other"}, {"redirect_uri", "https://evilclient.test/callback"},
		{"redirect_uri", "https://client.test:444/callback"}, {"code_challenge_method", "plain"},
		{"response_type", "token"}, {"resource", "https://elsewhere.test/api/mcp"}, {"scope", "openid"},
		{"code_challenge", "short"},
	} {
		t.Run(tc.field+"/"+tc.value, func(t *testing.T) {
			f := newFixture(t)
			f.values.Set(tc.field, tc.value)
			if _, err := f.s.Prepare(context.Background(), f.values); err == nil {
				t.Fatal("unsafe request accepted")
			}
		})
	}
	f := newFixture(t)
	f.values.Set("redirect_uri", "http://127.0.0.1:49152/callback")
	req, err := f.s.Prepare(context.Background(), f.values)
	if err != nil {
		t.Fatal("valid loopback:", err)
	}
	redirect := f.s.Redirect(req, url.Values{"error": {"access_denied"}})
	u, _ := url.Parse(redirect)
	if u.Query().Get("iss") != f.s.Issuer() || u.Query().Get("state") != "opaque-state" {
		t.Fatal("issuer/state omitted")
	}
}

func TestAccessChecksCurrentRoleSessionAndExpiry(t *testing.T) {
	for _, kind := range []string{"role", "credential_version", "expired", "revoked"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			body := f.redeem(t, f.code(t))
			p, err := f.s.VerifyAccess(context.Background(), access(body))
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "role":
				_, err = f.db.Exec("UPDATE users SET role='public' WHERE id=?", f.user.ID)
			case "credential_version":
				_, err = f.db.Exec("UPDATE users SET session_version=session_version+1 WHERE id=?", f.user.ID)
			case "expired":
				_, err = f.db.Exec("UPDATE oauth_tokens SET access_expires_at=0")
			case "revoked":
				err = f.s.RevokeGrant(context.Background(), f.user.ID, p.GrantID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.VerifyAccess(context.Background(), access(body)); err == nil {
				t.Fatal("stale access accepted")
			}
		})
	}
}
