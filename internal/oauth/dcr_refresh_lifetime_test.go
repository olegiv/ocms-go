// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestDCRClientOutlivesTheLongestRefreshFamily answers R1 P3-2: with the
// longest refresh lifetime the service accepts (MaxRefreshTTL, what
// OCMS_OAUTH_REFRESH_TTL_DAYS=90 configures), a refresh at the very end of the
// family still resolves its dynamically registered client. The client is
// extended on redemption to DCRClientLifetime, counted from the same moment as the
// family's own expiry, so it can never lapse first (at most at the same
// instant). MaxRefreshTTL is the bound New enforces, so a longer lifetime
// cannot be configured without this test noticing.
func TestDCRClientOutlivesTheLongestRefreshFamily(t *testing.T) {
	if DCRClientLifetime < MaxRefreshTTL {
		t.Fatalf("DCRClientLifetime %v is shorter than the longest refresh lifetime %v: a used client would lapse before its family ends",
			DCRClientLifetime, MaxRefreshTTL)
	}
	f := newFixture(t)
	f.s.options.AllowDCR = true
	f.s.options.RefreshTTL = MaxRefreshTTL
	start := time.Now().UTC().Truncate(time.Second)
	clock := start
	f.s.now = func() time.Time { return clock }

	client := f.registerDCR(t, "Long lived")
	f.values.Set("client_id", client)
	clock = start.Add(time.Hour)
	consent := clock
	code := f.code(t)
	clock = clock.Add(time.Second)
	tokens := f.redeem(t, code)
	grantEnd := consent.Add(MaxRefreshTTL)

	// The very last second of the family: client and grant both resolve.
	clock = grantEnd.Add(-time.Second)
	if _, err := f.s.clients.resolve(t.Context(), client); err != nil {
		t.Fatalf("the client lapsed before its refresh family ended: %v", err)
	}
	status, next := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(tokens)}, "client_id": {client}})
	if status != http.StatusOK || access(next) == "" {
		t.Fatalf("refresh at the end of the family: status=%d error=%v", status, next["error"])
	}

	// One second later the family is over. With the longest lifetime the client
	// and the family end at the same instant, so which of the two refusals is
	// reported is immaterial; the refresh must be refused.
	clock = grantEnd.Add(time.Second)
	if status, denied := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(next)}, "client_id": {client}}); status != http.StatusBadRequest {
		t.Fatalf("refresh past the family's end: status=%d error=%v, want a refusal", status, denied["error"])
	}
}

func TestDCRCodeCanRedeemAcrossRegistrationExpiry(t *testing.T) {
	f, now := registrationFixture(t)
	client := f.registerDCR(t, "End of registration")
	f.values.Set("client_id", client)
	shortExpiry := now.Add(DCRUnusedClientLifetime)
	*now = shortExpiry.Add(-time.Second)
	code := f.code(t)
	assertState := func(wantExpiry int64, wantProvisional bool) {
		t.Helper()
		var provisional bool
		if err := f.db.QueryRowContext(t.Context(), "SELECT provisional FROM oauth_dcr_registrations WHERE client_id = ?", client).Scan(&provisional); err != nil {
			t.Fatal(err)
		}
		if got := clientExpiry(t, f, client); got != wantExpiry || provisional != wantProvisional {
			t.Fatalf("client expiry=%d, provisional=%t, want %d and %t", got, provisional, wantExpiry, wantProvisional)
		}
	}
	assertState(shortExpiry.Unix(), true)
	*now = shortExpiry.Add(time.Second)
	if status, body := f.exchangeCode(code, url.Values{"code_verifier": {strings.Repeat("b", 64)}}); status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatalf("wrong PKCE status=%d error=%v", status, body["error"])
	}
	assertState(shortExpiry.Unix(), true)
	retentionExec(t, f, `CREATE TRIGGER reject_dcr_extension BEFORE UPDATE OF expires_at ON oauth_clients
		BEGIN SELECT RAISE(ABORT, 'extension fixture failure'); END`)
	if status, body := f.exchangeCode(code, nil); status != http.StatusServiceUnavailable || access(body) != "" {
		t.Fatalf("failed promotion status=%d error=%v", status, body["error"])
	}
	assertState(shortExpiry.Unix(), true)
	retentionExec(t, f, "DROP TRIGGER reject_dcr_extension")
	f.redeem(t, code)
	assertState(shortExpiry.Add(-time.Second).Add(DCRClientLifetime).Unix(), false)
}

func TestDCRCodeReplayRevokesAcrossRegistrationExpiry(t *testing.T) {
	f, now := registrationFixture(t)
	client := f.registerDCR(t, "Concurrent exchange")
	f.values.Set("client_id", client)
	shortExpiry := now.Add(DCRUnusedClientLifetime)
	*now = shortExpiry.Add(-time.Second)
	code := f.code(t)
	*now = shortExpiry.Add(time.Second)

	// Commit another exchange after the resolver reads the short-lived client
	// row, before it checks the current registration and code state.
	clock := f.s.now
	var winner string
	f.s.now = func() time.Time {
		f.s.now = clock
		winner = access(f.redeem(t, code))
		return clock()
	}
	status, body := f.exchangeCode(code, nil)
	if winner == "" || status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatalf("interleaved replay status=%d error=%v", status, body["error"])
	}
	if _, err := f.s.VerifyAccess(t.Context(), winner); err == nil {
		t.Fatal("the winning redemption's token survived the replay")
	}
}
