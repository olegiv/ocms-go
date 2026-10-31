// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentRefreshRevokesWinningFamily(t *testing.T) {
	f := newFixture(t)
	first := f.redeem(t, f.code(t))
	type response struct {
		status int
		body   map[string]any
	}
	results := make(chan response, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			status, body := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}})
			results <- response{status, body}
		})
	}
	wg.Wait()
	close(results)
	var winner string
	var successes, replays int
	for r := range results {
		switch r.status {
		case 200:
			successes++
			winner = access(r.body)
		case 400:
			if r.body["error"] != "invalid_grant" {
				t.Fatalf("replay error=%v", r.body["error"])
			}
			replays++
		default:
			t.Fatalf("unexpected refresh status=%d", r.status)
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("successes=%d replays=%d", successes, replays)
	}
	if _, err := f.s.VerifyAccess(t.Context(), winner); err == nil {
		t.Fatal("replayed family still usable")
	}
}

func TestCommitFailureReturnsNoCredentialsAndLeavesCodeUsable(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	// A deferred foreign-key error occurs at COMMIT, after the adapter has
	// generated token data. It must not escape in the HTTP response.
	for _, statement := range []string{
		`CREATE TABLE commit_guard (grant_id TEXT REFERENCES oauth_grants(id) DEFERRABLE INITIALLY DEFERRED)`,
		`CREATE TRIGGER fail_commit AFTER INSERT ON oauth_tokens BEGIN INSERT INTO commit_guard VALUES ('nonexistent'); END`,
	} {
		if _, err := f.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	status, body := f.exchange(url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {f.values.Get("redirect_uri")}, "code_verifier": {f.verifier}})
	if status != 503 || access(body) != "" || refresh(body) != "" {
		t.Fatalf("commit failure status=%d error=%v", status, body["error"])
	}
	if _, err := f.db.Exec(`DROP TRIGGER fail_commit`); err != nil {
		t.Fatal(err)
	}
	f.redeem(t, code)
}

func TestCancelledExchangeCannotConsumeCode(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {f.verifier},
		"client_id": {f.values.Get("client_id")}, "redirect_uri": {f.values.Get("redirect_uri")}, "resource": {f.s.Resource()}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(values.Encode())).WithContext(ctx)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.s.Token(w, r)
	if w.Code == 200 || strings.Contains(w.Body.String(), "access_token") {
		t.Fatalf("cancelled exchange status=%d", w.Code)
	}
	f.redeem(t, code)
}

func TestReplayWithEscalatedScopeStillRevokesGrant(t *testing.T) {
	f := newFixture(t)
	first := f.redeem(t, f.code(t))
	_, next := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}})
	status, body := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}, "scope": {"pages:write"}})
	if status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("replay status=%d error=%v", status, body["error"])
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(next)); err == nil {
		t.Fatal("scope check masked replay")
	}
}

func TestGrantOwnershipAndStoredDataContainNoCredentials(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	body := f.redeem(t, code)
	p, err := f.s.VerifyAccess(t.Context(), access(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.RevokeGrant(t.Context(), f.user.ID+1, p.GrantID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(body)); err != nil {
		t.Fatal("foreign account revoked grant")
	}
	if err := f.s.RevokeGrant(t.Context(), f.user.ID, p.GrantID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(body)); err == nil {
		t.Fatal("owner revocation ineffective")
	}
	for _, query := range []string{
		`SELECT code_hash || challenge || redirect_uri FROM oauth_codes`,
		`SELECT access_hash || refresh_hash FROM oauth_tokens`,
		`SELECT metadata || request_url || message FROM events WHERE category = 'auth'`,
	} {
		rows, err := f.db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{code, access(body), refresh(body), f.verifier, "opaque-state"} {
				if strings.Contains(value, secret) {
					t.Fatal("credential or client state persisted in OAuth/audit storage")
				}
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
	}
}
