// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// TestDCRClientOutlivesTheLongestRefreshFamily answers R1 P3-2: with the
// longest refresh lifetime the service accepts (MaxRefreshTTL, what
// OCMS_OAUTH_REFRESH_TTL_DAYS=90 configures), a refresh at the very end of the
// family still resolves its dynamically registered client. The client is
// extended at consent to DCRClientLifetime, counted from the same moment as the
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
	tokens := f.redeem(t, f.code(t))
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
