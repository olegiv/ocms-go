// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"
)

// TestConcurrentCodeRedemptionLeavesNoWorkingToken documents the consequence
// of A-1 for a client that sends one code twice at once: one request wins, the
// other is the replay, and the replay revokes what the winner was issued.
func TestConcurrentCodeRedemptionLeavesNoWorkingToken(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	type response struct {
		status int
		body   map[string]any
	}
	results := make(chan response, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			status, body := f.exchangeCode(code, nil)
			results <- response{status, body}
		})
	}
	wg.Wait()
	close(results)
	var winner string
	successes := 0
	for r := range results {
		if r.status == http.StatusOK {
			successes++
			winner = access(r.body)
		}
	}
	if successes != 1 {
		t.Fatalf("successful redemptions = %d, want 1", successes)
	}
	if _, err := f.s.VerifyAccess(t.Context(), winner); err == nil {
		t.Fatal("the winning redemption's token survived the replay")
	}
}

type auditEvent struct{ level, message, ip, requestURL string }

// authEvents returns the Event Log's authentication entries, oldest first.
func authEvents(t testing.TB, f *fixture) []auditEvent {
	t.Helper()
	rows, err := f.db.QueryContext(t.Context(), `SELECT level, message, ip_address, request_url FROM events WHERE category = 'auth' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var events []auditEvent
	for rows.Next() {
		var e auditEvent
		if err := rows.Scan(&e.level, &e.message, &e.ip, &e.requestURL); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func (f *fixture) exchangeCode(code string, extra url.Values) (int, map[string]any) {
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {f.values.Get("redirect_uri")}, "code_verifier": {f.verifier}}
	for key, entries := range extra {
		values[key] = entries
	}
	return f.exchange(values)
}

// TestReplayedAuthorizationCodeRevokesTheGrantItIssued pins audit A-1: a spent
// code presented again used to fail without touching the tokens its first
// redemption issued, so a thief who raced the client kept a working access
// token and refresh token. RFC 6749 4.1.2 asks for the family to be revoked,
// and the revocation has to be committed although the response is an error.
func TestReplayedAuthorizationCodeRevokesTheGrantItIssued(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	first := f.redeem(t, code)
	if _, err := f.s.VerifyAccess(t.Context(), access(first)); err != nil {
		t.Fatalf("first redemption did not issue a working token: %v", err)
	}

	status, denied := f.exchangeCode(code, nil)
	if status != http.StatusBadRequest || denied["error"] != "invalid_grant" {
		t.Fatalf("replay status=%d error=%v", status, denied["error"])
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(first)); err == nil {
		t.Fatal("access token issued by the replayed code still works")
	}
	status, denied = f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}})
	if status != http.StatusBadRequest || denied["error"] != "invalid_grant" {
		t.Fatalf("refresh token issued by the replayed code still works: status=%d error=%v", status, denied["error"])
	}
	var revoked int64
	if err := f.db.QueryRowContext(t.Context(), `SELECT revoked_at FROM oauth_grants`).Scan(&revoked); err != nil || revoked == 0 {
		t.Fatalf("grant was not revoked durably: revoked_at=%d err=%v", revoked, err)
	}

	// Replaying again finds nothing left to revoke and records nothing new.
	if status, _ = f.exchangeCode(code, nil); status != http.StatusBadRequest {
		t.Fatalf("second replay status=%d", status)
	}
	var warnings int
	for _, e := range authEvents(t, f) {
		if e.message == "OAuth authorization code reuse revoked connection" && e.level == "warning" {
			warnings++
		}
	}
	if warnings != 1 {
		t.Fatalf("code reuse audit events = %d, want 1", warnings)
	}
}

// TestReplayedCodeFromAnotherClientCannotRevoke keeps the revocation tied to
// the client the grant belongs to, as for refresh tokens.
func TestReplayedCodeFromAnotherClientCannotRevoke(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	first := f.redeem(t, code)
	other := registeredClient{ID: "https://other.test/metadata", Name: "Other", AuthMethod: "none", Redirects: []string{"https://other.test/callback"}}
	if err := f.s.saveClient(t.Context(), other, "cimd", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	status, _ := f.exchangeCode(code, url.Values{"client_id": {other.ID}})
	if status != http.StatusBadRequest {
		t.Fatalf("foreign replay status=%d", status)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(first)); err != nil {
		t.Fatalf("another client's replay revoked the grant: %v", err)
	}
}

// TestUnredeemedCodeProbesDoNotRevoke covers the failures that are not a
// replay: a code that was never redeemed is not burned by a wrong verifier
// (documented behaviour), and an unknown code revokes nothing.
func TestUnredeemedCodeProbesDoNotRevoke(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	if status, _ := f.exchangeCode(code, url.Values{"code_verifier": {f.verifier[:50] + "b"}}); status != http.StatusBadRequest {
		t.Fatalf("wrong verifier status=%d", status)
	}
	if status, _ := f.exchangeCode(randomCredential(codePrefix), nil); status != http.StatusBadRequest {
		t.Fatalf("unknown code status=%d", status)
	}
	body := f.redeem(t, code)
	if _, err := f.s.VerifyAccess(t.Context(), access(body)); err != nil {
		t.Fatalf("probes revoked an unredeemed grant: %v", err)
	}
	for _, e := range authEvents(t, f) {
		if e.level == "warning" {
			t.Fatalf("probe recorded a reuse event: %+v", e)
		}
	}
}
