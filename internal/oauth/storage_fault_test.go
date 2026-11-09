// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// breakTable makes every query on table fail with a non-ErrNoRows error until
// the returned restore runs, standing in for SQLITE_BUSY or an I/O fault.
func (f *fixture) breakTable(t *testing.T, table string) (restore func()) {
	t.Helper()
	if _, err := f.db.Exec("ALTER TABLE " + table + " RENAME TO " + table + "_unavailable"); err != nil {
		t.Fatal(err)
	}
	restored := false
	restore = func() {
		if restored {
			return
		}
		restored = true
		if _, err := f.db.Exec("ALTER TABLE " + table + "_unavailable RENAME TO " + table); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(restore)
	return restore
}

func (f *fixture) revoke(clientID, token string) (int, map[string]any) {
	values := url.Values{"client_id": {clientID}, "token": {token}}
	r := httptest.NewRequest(http.MethodPost, "https://cms.test/oauth/revoke", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	f.s.Revoke(w, r)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func problemCode(err error) string {
	var p *Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return ""
}

func TestRevokeValidatesClientIDBeforeLookingUpToken(t *testing.T) {
	f := newFixture(t)
	tokens := f.redeem(t, f.code(t))
	raw := access(tokens)
	restore := f.breakTable(t, "oauth_tokens")
	for name, id := range map[string]string{"empty": "", "too long": strings.Repeat("x", 2049)} {
		t.Run(name, func(t *testing.T) {
			status, response := f.revoke(id, raw)
			if status != http.StatusBadRequest || response["error"] != "invalid_client" {
				t.Fatalf("status=%d error=%v, want 400 invalid_client", status, response["error"])
			}
		})
	}
	restore()
	if status, response := f.revoke(strings.Repeat("x", 2048), raw); status != http.StatusOK || len(response) != 0 {
		t.Fatalf("maximum-length mismatched client: status=%d fields=%d", status, len(response))
	}
	if _, err := f.s.VerifyAccess(t.Context(), raw); err != nil {
		t.Fatalf("invalid or unmatched client revoked the token: %v", err)
	}
}

func TestRevokeUsesStoredGrantWithoutClientMetadata(t *testing.T) {
	f := newFixture(t)
	body := f.redeem(t, f.code(t))
	clientID := f.values.Get("client_id")
	f.s.options.AllowedHosts = []string{"client.test"}
	var fetches atomic.Int64
	f.s.clients.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		fetches.Add(1)
		return nil, errors.New("unexpected client metadata fetch")
	})

	const otherClient = "https://client.test/other-metadata"
	for _, tc := range []struct{ name, token string }{
		{"malformed token", "x"},
		{"unknown token", randomCredential(AccessPrefix)},
		{"wrong client for a known token", refresh(body)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, response := f.revoke(otherClient, tc.token)
			if status != http.StatusOK || len(response) != 0 {
				t.Fatalf("expected indistinguishable 200 response, got status=%d body=%v", status, response)
			}
		})
	}

	// Revocation of a valid grant must work even after its metadata expires.
	if _, err := f.db.ExecContext(t.Context(), "UPDATE oauth_clients SET expires_at = 0 WHERE id = ?", clientID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(body)); err != nil {
		t.Fatalf("wrong client or metadata expiry invalidated the grant: %v", err)
	}
	status, response := f.revoke(clientID, refresh(body))
	if status != http.StatusOK || len(response) != 0 {
		t.Errorf("revoke with expired metadata: status=%d body=%v", status, response)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(body)); problemCode(err) != "invalid_token" {
		t.Errorf("revoked grant remained usable: %v", err)
	}
	if got := fetches.Load(); got != 0 {
		t.Errorf("revocation made %d client metadata requests", got)
	}
}

// A revocation the server could not perform must not be reported as done:
// the client would discard its token while the grant stayed active.
func TestRevokeStorageFaultIsNotReportedAsSuccess(t *testing.T) {
	for _, table := range []string{"oauth_tokens", "oauth_grants"} {
		t.Run(table, func(t *testing.T) {
			f := newFixture(t)
			body := f.redeem(t, f.code(t))
			restore := f.breakTable(t, table)
			clientID := f.values.Get("client_id")
			status, problemBody := f.revoke(clientID, refresh(body))
			if status != http.StatusServiceUnavailable || problemBody["error"] != "temporarily_unavailable" {
				t.Fatalf("revoke during storage fault: status=%d error=%v", status, problemBody["error"])
			}
			restore()
			if _, err := f.s.VerifyAccess(t.Context(), access(body)); err != nil {
				t.Fatalf("failed revocation changed the grant: %v", err)
			}
			if status, _ := f.revoke(clientID, refresh(body)); status != http.StatusOK {
				t.Fatalf("retried revoke status=%d", status)
			}
			if _, err := f.s.VerifyAccess(t.Context(), access(body)); err == nil {
				t.Fatal("retried revocation ineffective")
			}
		})
	}
	f := newFixture(t)
	unknown := randomCredential(refreshPrefix)
	if status, _ := f.revoke(f.values.Get("client_id"), unknown); status != http.StatusOK {
		t.Fatalf("unknown token revoke status=%d, RFC 7009 requires 200", status)
	}
}

func TestRevokeLiveAccessWriteFaultIsRetryable(t *testing.T) {
	f := newFixture(t)
	first := f.redeem(t, f.code(t))
	if _, err := f.db.ExecContext(t.Context(), `CREATE TRIGGER fail_revoke BEFORE UPDATE OF revoked_at ON oauth_grants
		BEGIN SELECT RAISE(ABORT, 'revocation write failed'); END`); err != nil {
		t.Fatal(err)
	}
	clientID := f.values.Get("client_id")
	status, response := f.revoke(clientID, access(first))
	if status != http.StatusServiceUnavailable || response["error"] != "temporarily_unavailable" {
		t.Fatalf("failed revoke: status=%d error=%v", status, response["error"])
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(first)); err != nil {
		t.Fatalf("failed revoke changed the grant: %v", err)
	}
	if _, err := f.db.ExecContext(t.Context(), `DROP TRIGGER fail_revoke`); err != nil {
		t.Fatal(err)
	}
	status, response = f.revoke(clientID, access(first))
	if status != http.StatusOK || len(response) != 0 {
		t.Fatalf("retry revoke: status=%d body=%v", status, response)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(first)); problemCode(err) != "invalid_token" {
		t.Fatalf("access after retrying revoke: %v", err)
	}
}

// Storage faults are retryable: they must never tell a client that a token,
// code or refresh token is dead, or the client throws away live credentials.
func TestStorageFaultsDoNotInvalidateCredentials(t *testing.T) {
	for _, table := range []string{"oauth_tokens", "oauth_grants", "users"} {
		t.Run("access/"+table, func(t *testing.T) {
			f := newFixture(t)
			body := f.redeem(t, f.code(t))
			restore := f.breakTable(t, table)
			_, err := f.s.VerifyAccess(t.Context(), access(body))
			if !Retryable(err) || problemCode(err) != "temporarily_unavailable" {
				t.Fatalf("access check during storage fault: %v", err)
			}
			restore()
			if _, err := f.s.VerifyAccess(t.Context(), access(body)); err != nil {
				t.Fatalf("token unusable after fault cleared: %v", err)
			}
		})
	}
	t.Run("refresh", func(t *testing.T) {
		f := newFixture(t)
		body := f.redeem(t, f.code(t))
		restore := f.breakTable(t, "oauth_grants")
		status, failed := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(body)}})
		if status != http.StatusServiceUnavailable || failed["error"] == "invalid_grant" {
			t.Fatalf("refresh during storage fault: status=%d error=%v", status, failed["error"])
		}
		restore()
		if status, next := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(body)}}); status != http.StatusOK || access(next) == "" {
			t.Fatalf("refresh token lost to a storage fault: status=%d", status)
		}
	})
	t.Run("code", func(t *testing.T) {
		f := newFixture(t)
		code := f.code(t)
		restore := f.breakTable(t, "oauth_codes")
		status, failed := f.exchange(url.Values{"grant_type": {"authorization_code"}, "code": {code},
			"redirect_uri": {f.values.Get("redirect_uri")}, "code_verifier": {f.verifier}})
		if status != http.StatusServiceUnavailable || failed["error"] == "invalid_grant" {
			t.Fatalf("code exchange during storage fault: status=%d error=%v", status, failed["error"])
		}
		restore()
		f.redeem(t, code)
	})
	t.Run("recheck", func(t *testing.T) {
		f := newFixture(t)
		p, err := f.s.VerifyAccess(t.Context(), access(f.redeem(t, f.code(t))))
		if err != nil {
			t.Fatal(err)
		}
		f.breakTable(t, "oauth_tokens")
		if _, err := f.s.RecheckAccess(t.Context(), f.s.q, p); !Retryable(err) {
			t.Fatalf("recheck during storage fault: %v", err)
		}
	})
}

// The last-used timestamp is bookkeeping; failing to write it must not turn
// a valid access token into a 401.
func TestGrantTouchFailureKeepsAccessValid(t *testing.T) {
	f := newFixture(t)
	body := f.redeem(t, f.code(t))
	for _, statement := range []string{
		`UPDATE oauth_grants SET last_used_at = 0`,
		`CREATE TRIGGER fail_touch BEFORE UPDATE OF last_used_at ON oauth_grants BEGIN SELECT RAISE(ABORT, 'database is locked'); END`,
	} {
		if _, err := f.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(body)); err != nil {
		t.Fatalf("touch failure rejected a valid token: %v", err)
	}
}

func TestUnclassifiedErrorsAreServerErrors(t *testing.T) {
	if code := protocolProblem(errors.New("database is locked")).Code; code != "server_error" {
		t.Fatalf("unclassified error mapped to %q", code)
	}
	w := httptest.NewRecorder()
	WriteProblem(w, errors.New("database is locked"))
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "locked") {
		t.Fatalf("unclassified error response: status=%d body=%s", w.Code, w.Body.String())
	}
}

// Rewriting a pinned client's cache row can fail transiently; the token
// endpoint must answer with a retryable error, not invalid_grant.
func TestPreRegisteredClientCacheFaultIsRetryable(t *testing.T) {
	const id = "https://claude.ai/oauth/claude-code-client-metadata"
	const pin = `[{"client_id":"https://claude.ai/oauth/claude-code-client-metadata","client_name":"Claude Code","redirect_uris":["http://localhost/callback"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none"}]`
	f := newFixture(t)
	s, err := New(f.db, Options{Issuer: f.s.Issuer(), AllowedHosts: []string{"claude.ai"}, PreRegisteredClients: pin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER fail_client_cache BEFORE INSERT ON oauth_clients BEGIN SELECT RAISE(ABORT, 'database is locked'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.clients.resolve(t.Context(), id); !Retryable(err) {
		t.Fatalf("pinned client cache fault: %v", err)
	}
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {randomCredential(refreshPrefix)},
		"client_id": {id}, "resource": {s.Resource()}}
	r := httptest.NewRequest(http.MethodPost, "https://cms.test/oauth/token", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Token(w, r)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "invalid_grant") {
		t.Fatalf("token endpoint during client cache fault: status=%d body=%s", w.Code, w.Body.String())
	}
}

// A metadata host that cannot be reached says nothing about the client's
// identity; only a document that fails validation is invalid_client.
func TestClientMetadataFetchFaultsAreRetryable(t *testing.T) {
	const id = "https://claude.ai/oauth/live-client"
	cases := []struct {
		name      string
		transport roundTripFunc
		retryable bool
	}{
		{"network", func(*http.Request) (*http.Response, error) { return nil, errors.New("dial tcp: i/o timeout") }, true},
		{"5xx", statusTransport(http.StatusBadGateway), true},
		{"429", statusTransport(http.StatusTooManyRequests), true},
		{"408", statusTransport(http.StatusRequestTimeout), true},
		{"3xx", statusTransport(http.StatusFound), false},
		{"4xx", statusTransport(http.StatusForbidden), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			s, err := New(f.db, Options{Issuer: f.s.Issuer(), AllowedHosts: []string{"claude.ai"}})
			if err != nil {
				t.Fatal(err)
			}
			s.clients.http.Transport = tc.transport
			_, err = s.clients.resolve(t.Context(), id)
			if Retryable(err) != tc.retryable {
				t.Fatalf("fetch failure classified as %q", problemCode(err))
			}
			if !tc.retryable && problemCode(err) != "invalid_client" {
				t.Fatalf("rejected document classified as %q", problemCode(err))
			}
		})
	}
}

func statusTransport(status int) roundTripFunc {
	return func(*http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		w.WriteHeader(status)
		return w.Result(), nil
	}
}
