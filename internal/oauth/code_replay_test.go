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

// TestReplayWithoutProofOfPossessionDoesNotRevoke pins the review finding on
// A-1: authorization codes travel in redirect URLs, so a spent code plus the
// public client_id is knowable by an observer. Revoking on that alone lets
// anyone disconnect the user. Only a replay that also carries the PKCE
// verifier and the redirect_uri the code was issued for revokes the grant;
// every other presentation is a plain invalid_grant that leaves the grant and
// its tokens working.
func TestReplayWithoutProofOfPossessionDoesNotRevoke(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	first := f.redeem(t, code)

	wrong := url.Values{"code_verifier": {f.verifier[:50] + "b"}}
	missing := url.Values{"code_verifier": {""}}
	otherRedirect := url.Values{"redirect_uri": {"http://127.0.0.1/callback"}}
	unregisteredRedirect := url.Values{"redirect_uri": {"https://cms.test/not-the-redirect"}}
	// The alternate callback must pass client validation so the replay reaches
	// provesCodePossession's binding to the callback used to issue the code.
	client, resolveErr := f.s.clients.resolve(t.Context(), f.values.Get("client_id"))
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if !client.acceptsRedirect(otherRedirect.Get("redirect_uri")) {
		t.Fatal("replay fixture needs a second registered redirect")
	}
	for name, extra := range map[string]url.Values{
		"wrong verifier":                 wrong,
		"missing verifier":               missing,
		"mismatched registered redirect": otherRedirect,
		"unregistered redirect":          unregisteredRedirect,
	} {
		status, body := f.exchangeCode(code, extra)
		if status != http.StatusBadRequest || body["error"] != "invalid_grant" {
			t.Fatalf("%s: status=%d error=%v", name, status, body["error"])
		}
		if _, err := f.s.VerifyAccess(t.Context(), access(first)); err != nil {
			t.Fatalf("%s: a replay without proof of possession revoked the access token: %v", name, err)
		}
		var revoked int64
		if err := f.db.QueryRowContext(t.Context(), `SELECT revoked_at FROM oauth_grants`).Scan(&revoked); err != nil || revoked != 0 {
			t.Fatalf("%s: grant revoked_at=%d err=%v", name, revoked, err)
		}
	}
	// The family still refreshes.
	status, body := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}})
	if status != http.StatusOK {
		t.Fatalf("refresh after the unproven replays: status=%d error=%v", status, body["error"])
	}
	for _, e := range authEvents(t, f) {
		if e.level == "warning" {
			t.Fatalf("an unproven replay recorded a reuse event: %+v", e)
		}
	}

	// The same code replayed with the verifier and redirect_uri still revokes.
	second := f.code(t)
	secondTokens := f.redeem(t, second)
	if status, _ = f.exchangeCode(second, nil); status != http.StatusBadRequest {
		t.Fatalf("proven replay status=%d", status)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(secondTokens)); err == nil {
		t.Fatal("a replay with the verifier and redirect_uri did not revoke the grant")
	}
}

// TestReplayedCodeFromAnotherClientCannotRevoke keeps the revocation tied to
// the client the grant belongs to, as for refresh tokens.
func TestReplayedCodeFromAnotherClientCannotRevoke(t *testing.T) {
	f := newFixture(t)
	code := f.code(t)
	first := f.redeem(t, code)
	// Share the callback and send the correct verifier so only the spent
	// code's client binding can prevent revocation of the original grant.
	other := registeredClient{ID: "https://other.test/metadata", Name: "Other", AuthMethod: "none", Redirects: []string{f.values.Get("redirect_uri")}}
	if err := f.s.saveClient(t.Context(), other, "cimd", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	status, denied := f.exchangeCode(code, url.Values{"client_id": {other.ID}})
	if status != http.StatusBadRequest || denied["error"] != "invalid_grant" {
		t.Fatalf("foreign replay status=%d error=%v", status, denied["error"])
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
