// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/middleware"
)

const registrationMetadata = `{"client_name":"Desktop","redirect_uris":["https://client.test/callback"]}`

func registerFrom(s *Service, remote, body string, headers http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))
	r.RemoteAddr = remote
	r.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		r.Header[http.CanonicalHeaderKey(key)] = values
	}
	w := httptest.NewRecorder()
	s.Register(w, r)
	return w
}

func requireRegistration(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var c registeredClient
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || w.Code != http.StatusCreated || c.ID == "" {
		t.Fatalf("registration: status=%d body=%s error=%v", w.Code, w.Body.String(), err)
	}
	return c.ID
}

func requireRegistrationLimit(t *testing.T, w *httptest.ResponseRecorder, seconds int) {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusTooManyRequests ||
		body["error"] != "rate_limit_exceeded" || len(body) != 2 || body["error_description"] == "" {
		t.Fatalf("registration limit: status=%d body=%s error=%v", w.Code, w.Body.String(), err)
	}
	if w.Header().Get("Retry-After") != strconv.Itoa(seconds) || w.Header().Get("Cache-Control") != "no-store" ||
		w.Header().Get("Pragma") != "no-cache" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("registration limit headers = %v, want retry in %d seconds", w.Header(), seconds)
	}
}

func registrationFixture(t *testing.T) (*fixture, *time.Time) {
	t.Helper()
	f := newFixture(t)
	f.s.options.AllowDCR = true
	now := time.Now().UTC().Truncate(time.Second)
	f.s.now = func() time.Time { return now }
	return f, &now
}

func fillRegistrationBudget(t *testing.T, s *Service, remote string, headers http.Header) string {
	t.Helper()
	var first string
	for range 20 {
		id := requireRegistration(t, registerFrom(s, remote, registrationMetadata, headers))
		if first == "" {
			first = id
		}
	}
	return first
}

func TestProtectedDCRRejectsBeforeAdmission(t *testing.T) {
	f, _ := registrationFixture(t)
	const token = "a4bc19e5307480df69b6528f7253ee3346de303ff40d9408ee3cbd61a29c5f74"
	s, err := New(f.db, Options{Issuer: f.s.Issuer(), AllowDCR: true, DCRInitialAccessToken: token})
	if err != nil {
		t.Fatal(err)
	}
	s.now = f.s.now
	f.s = s
	if s.options.DCRInitialAccessToken != "" {
		t.Fatal("issuer retained the plaintext registration token")
	}
	const bearer = "Bearer " + token
	for _, tc := range []struct {
		name, query string
		headers     []string
		status      int
		code        string
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "query only", query: "?access_token=" + token, status: http.StatusUnauthorized},
		{name: "unsupported scheme", headers: []string{"Basic " + token}, status: http.StatusUnauthorized},
		{name: "wrong token", headers: []string{"Bearer " + strings.ToUpper(token)}, status: http.StatusUnauthorized, code: "invalid_token"},
		{name: "empty bearer", headers: []string{"Bearer"}, status: http.StatusUnauthorized, code: "invalid_token"},
		{name: "multiple lines", headers: []string{bearer, bearer}, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "combined", headers: []string{bearer + ", " + bearer}, status: http.StatusBadRequest, code: "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.NewReader(registrationMetadata)
			r := httptest.NewRequest(http.MethodPost, "/oauth/register"+tc.query, body)
			r.Header.Set("Content-Type", "application/json")
			for _, header := range tc.headers {
				r.Header.Add("Authorization", header)
			}
			w := httptest.NewRecorder()
			s.Register(w, r)
			challenge := `Bearer realm="client-registration"`
			if tc.code != "" {
				challenge += `, error="` + tc.code + `"`
			}
			if w.Code != tc.status || w.Header().Get("WWW-Authenticate") != challenge || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("registration refusal = %d with challenge %q", w.Code, w.Header().Get("WWW-Authenticate"))
			}
			if body.Len() != len(registrationMetadata) || strings.Contains(w.Body.String(), token) {
				t.Fatal("refusal read metadata or returned the token")
			}
		})
	}
	for i := range dcrRegistrationsTotal + 1 {
		remote := fmt.Sprintf("198.18.%d.%d:12345", i/256, i%256)
		if w := registerFrom(s, remote, registrationMetadata, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated registration from %s returned %d", remote, w.Code)
		}
	}
	requireDCRRows(t, f, 0)
	var ledger int
	if err := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM oauth_dcr_registrations").Scan(&ledger); err != nil || ledger != 0 {
		t.Fatalf("unauthenticated requests left %d admissions: %v", ledger, err)
	}

	headers := http.Header{"Authorization": {"bEaReR  " + token}}
	const peer = "192.0.2.3:3000"
	discovery := httptest.NewRecorder()
	s.Discovery(discovery, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	if !strings.Contains(discovery.Body.String(), s.Issuer()+"/oauth/register") || strings.Contains(discovery.Body.String(), token) {
		t.Fatal("protected registration discovery changed or disclosed the token")
	}
	f.redeem(t, f.code(t)) // Cached CIMD remains available.
	clientID := fillRegistrationBudget(t, s, peer, headers)
	f.values.Set("client_id", clientID)
	requireRegistrationLimit(t, registerFrom(s, peer, registrationMetadata, headers), 3600)
	first := f.redeem(t, f.code(t)) // The registered public client uses its usual OAuth flow.
	status, rotated := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}})
	if status != http.StatusOK {
		t.Fatalf("registered client refresh = %d", status)
	}
	if status, _ := f.revoke(clientID, refresh(rotated)); status != http.StatusOK {
		t.Fatalf("registered client revocation = %d", status)
	}
	s.options.AllowDCR = false
	if w := registerFrom(s, peer, registrationMetadata, headers); w.Code != http.StatusNotFound {
		t.Fatalf("token enabled a disabled registration endpoint: %d", w.Code)
	}
}

func TestDCRRegistrationBudgetAndRecovery(t *testing.T) {
	f, now := registrationFixture(t)
	const peer = "192.0.2.1:3000"
	requireRegistration(t, registerFrom(f.s, peer, registrationMetadata, nil))
	*now = now.Add(30 * time.Minute)
	for range 19 {
		requireRegistration(t, registerFrom(f.s, peer, registrationMetadata, nil))
	}
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 1800)
	// A refusal creates no row, and another IP has its own quota.
	count, err := f.s.q.CountOAuthClientsBySource(t.Context(), "dcr")
	if err != nil || count != 20 {
		t.Fatalf("refused registration wrote a row: count=%d error=%v", count, err)
	}
	requireRegistration(t, registerFrom(f.s, "192.0.2.2:3000", registrationMetadata, nil))
	*now = now.Add(30*time.Minute - 500*time.Millisecond)
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 1)
	*now = now.Add(500 * time.Millisecond)
	requireRegistration(t, registerFrom(f.s, peer, registrationMetadata, nil))
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 1800)
	*now = now.Add(30 * time.Minute)
	for range 19 {
		requireRegistration(t, registerFrom(f.s, peer, registrationMetadata, nil))
	}
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 1800)
}

func TestDCRRegistrationBudgetUsesTrustedClientIP(t *testing.T) {
	t.Cleanup(func() {
		if err := middleware.ConfigureTrustedProxies(""); err != nil {
			t.Error(err)
		}
	})
	for _, tc := range []struct {
		name, proxies, first, second string
		firstHeaders, secondHeaders  http.Header
	}{
		{name: "untrusted spoofed forwarding", first: "192.0.2.1:1", second: "192.0.2.1:2",
			firstHeaders:  http.Header{"X-Forwarded-For": {"198.51.100.1"}, "X-Real-IP": {"198.51.100.2"}},
			secondHeaders: http.Header{"X-Forwarded-For": {"198.51.100.3"}, "X-Real-IP": {"198.51.100.4"}}},
		{name: "mapped IPv4", first: "192.0.2.1:1", second: "[::ffff:192.0.2.1]:2"},
		{name: "IPv6 spelling", first: "[2001:db8::1]:1", second: "[2001:0db8:0:0:0:0:0:1]:2"},
		{name: "IPv6 privacy address rotation", first: "[2001:db8:1234:5678::1]:1", second: "[2001:db8:1234:5678:ffff::2]:2"},
		{name: "trusted XFF chain", proxies: "127.0.0.1,10.0.0.0/8", first: "127.0.0.1:1", second: "127.0.0.1:2",
			firstHeaders:  http.Header{"X-Forwarded-For": {"198.51.100.1, 192.0.2.1, 10.0.0.1"}},
			secondHeaders: http.Header{"X-Forwarded-For": {"198.51.100.2, ::ffff:192.0.2.1, 10.0.0.2"}}},
		{name: "trusted real IP and XFF", proxies: "127.0.0.1", first: "127.0.0.1:1", second: "127.0.0.1:2",
			firstHeaders:  http.Header{"X-Real-IP": {"192.0.2.1"}},
			secondHeaders: http.Header{"X-Forwarded-For": {"192.0.2.1"}, "X-Real-IP": {"198.51.100.1"}}},
		{name: "invalid XFF falls back", proxies: "127.0.0.1", first: "127.0.0.1:1", second: "127.0.0.1:2",
			firstHeaders:  http.Header{"X-Forwarded-For": {"invalid"}, "X-Real-IP": {"198.51.100.1"}},
			secondHeaders: http.Header{"X-Forwarded-For": {"also-invalid"}, "X-Real-IP": {"198.51.100.2"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := middleware.ConfigureTrustedProxies(tc.proxies); err != nil {
				t.Fatal(err)
			}
			f, _ := registrationFixture(t)
			fillRegistrationBudget(t, f.s, tc.first, tc.firstHeaders)
			requireRegistrationLimit(t, registerFrom(f.s, tc.second, registrationMetadata, tc.secondHeaders), 3600)
			// A different resolved source behind the same trusted proxy is independent.
			if tc.proxies != "" {
				requireRegistration(t, registerFrom(f.s, tc.second, registrationMetadata,
					http.Header{"X-Forwarded-For": {"203.0.113.99"}}))
			}
		})
	}
}

func TestDCRIPv6BudgetSurvivesRestart(t *testing.T) {
	f, now := registrationFixture(t)
	for i := range 20 {
		requireRegistration(t, registerFrom(f.s, fmt.Sprintf("[2001:db8:1234:5678::%x]:1234", i+1), registrationMetadata, nil))
	}
	s, err := New(f.db, f.s.options)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return *now }
	requireRegistrationLimit(t, registerFrom(s, "[2001:db8:1234:5678:ffff::2]:4321", registrationMetadata, nil), 3600)
	requireRegistration(t, registerFrom(s, "[2001:db8:1234:5679::1]:1234", registrationMetadata, nil))
}

func TestDCRIPv6NetworkBudgetAcrossSubnetsAndServices(t *testing.T) {
	f, now := registrationFixture(t)
	for i := range 99 {
		// The zero fourth hextet has the shortened ledger key 2001:db8::/64.
		peer := fmt.Sprintf("[2001:db8:0:%x::1]:1234", i)
		requireRegistration(t, registerFrom(f.s, peer, registrationMetadata, nil))
	}
	second, err := New(f.db, f.s.options)
	if err != nil {
		t.Fatal(err)
	}
	second.now = f.s.now
	services := []*Service{f.s, second}
	responses := make([]*httptest.ResponseRecorder, 8)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Go(func() {
			peer := fmt.Sprintf("[2001:0db8:0000:%x:ffff::2]:4321", 256+i)
			responses[i] = registerFrom(services[i%len(services)], peer, registrationMetadata, nil)
		})
	}
	wg.Wait()
	accepted := 0
	for _, w := range responses {
		if w.Code == http.StatusCreated {
			accepted++
		} else {
			requireRegistrationLimit(t, w, 3600)
		}
	}
	if accepted != 1 {
		t.Fatalf("IPv6 network admitted %d concurrent registrations with one slot left", accepted)
	}
	requireDCRRows(t, f, 100)
	requireRegistration(t, registerFrom(second, "[2001:db8:1::1]:1234", registrationMetadata, nil))
	requireRegistration(t, registerFrom(second, "[::ffff:192.0.2.1]:1234", registrationMetadata, nil))
	*now = now.Add(time.Hour)
	requireRegistration(t, registerFrom(second, "[2001:db8::1]:1234", registrationMetadata, nil))
}

func TestDCRIPv6NetworkRetryAfterUsesLegacyLedger(t *testing.T) {
	f, now := registrationFixture(t)
	retentionExec(t, f, `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<120)
		INSERT INTO oauth_dcr_registrations (client_id, source_key, registered_at)
		SELECT 'legacy' || x, printf('2001:db8:abcd:%x::/64', x), CASE WHEN x<=20 THEN ? ELSE ? END FROM n`,
		now.Add(-50*time.Minute).UnixNano(), now.Add(-30*time.Minute).UnixNano())
	const peer = "[2001:db8:abcd::1]:1234"
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 1800)
	*now = now.Add(10 * time.Minute)
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 1200)
	*now = now.Add(20 * time.Minute)
	requireRegistration(t, registerFrom(f.s, peer, registrationMetadata, nil))
}

func TestDCRRetryAfterCombinesNetworkAndGlobalBudgets(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		firstNetwork, outsiders time.Duration
		otherCount, want        int
	}{
		{"global outlasts network", -59 * time.Minute, -time.Minute, 1901, 540},
		{"network outlasts global", -time.Minute, -9 * time.Minute, 1900, 3540},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, now := registrationFixture(t)
			retentionExec(t, f, `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1901)
				INSERT INTO oauth_dcr_registrations (client_id, source_key, registered_at)
				SELECT 'other' || x, printf('198.18.%d.%d', x / 256, x % 256), ? FROM n WHERE x<=?
				UNION ALL SELECT 'net' || x, printf('2001:db8:abcd:%x::/64', x), CASE WHEN x=1 THEN ? ELSE ? END FROM n WHERE x<=100`,
				now.Add(tc.outsiders).UnixNano(), tc.otherCount, now.Add(tc.firstNetwork).UnixNano(), now.Add(-time.Minute).UnixNano())
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			const peer = "[2001:db8:abcd::1]:1234"
			requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), tc.want)
			if !strings.Contains(logs.String(), "reason=global_limit") {
				t.Fatalf("overlapping budgets hid global saturation: %s", logs.String())
			}
			*now = now.Add(time.Duration(tc.want) * time.Second)
			requireRegistration(t, registerFrom(f.s, peer, registrationMetadata, nil))
		})
	}
}

func TestDCRRateRefusalDoesNotWaitForSQLiteWriter(t *testing.T) {
	f, _ := registrationFixture(t)
	const peer = "192.0.2.1:3000"
	fillRegistrationBudget(t, f.s, peer, nil)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- registerFrom(f.s, peer, registrationMetadata, nil) }()
	select {
	case w := <-done:
		requireRegistrationLimit(t, w, 3600)
	case <-time.After(2 * time.Second):
		rollback(tx)
		<-done
		t.Fatal("exhausted source waited behind a SQLite writer")
	}
}

func TestDCRQueuedWritesLeaveDatabaseConnectionsAvailable(t *testing.T) {
	f, _ := registrationFixture(t)
	f.db.SetMaxOpenConns(3)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var waiters sync.WaitGroup
	defer func() {
		rollback(tx)
		waiters.Wait()
	}()
	for range 12 {
		waiters.Go(func() { registerFrom(f.s, "192.0.2.1:3000", registrationMetadata, nil) })
	}
	deadline := time.Now().Add(time.Second)
	for f.db.Stats().InUse < 2 {
		if time.Now().After(deadline) {
			t.Fatal("DCR writers did not reach the connection pool")
		}
		time.Sleep(time.Millisecond)
	}
	// Keep making ordinary database reads while the admitted writer waits.
	// Without the write queue, the waiting transactions consume the pool.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for range 10 {
		if _, err = f.s.q.GetUserByID(ctx, f.user.ID); err != nil {
			t.Fatalf("DCR writers exhausted the connection pool: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDCRRegistrationBudgetConcurrent(t *testing.T) {
	f, _ := registrationFixture(t)
	second, err := New(f.db, f.s.options)
	if err != nil {
		t.Fatal(err)
	}
	second.now = f.s.now
	services := []*Service{f.s, second}
	var wg sync.WaitGroup
	responses := make([]*httptest.ResponseRecorder, 60)
	for i := range responses {
		wg.Go(func() {
			responses[i] = registerFrom(services[i%len(services)], "192.0.2.1:3000", registrationMetadata, nil)
		})
	}
	wg.Wait()
	accepted := make(map[string]bool)
	for _, w := range responses {
		if w.Code == http.StatusCreated {
			id := requireRegistration(t, w)
			if accepted[id] {
				t.Fatal("duplicate registration ID")
			}
			accepted[id] = true
		} else {
			requireRegistrationLimit(t, w, 3600)
		}
	}
	count, err := f.s.q.CountOAuthClientsBySource(t.Context(), "dcr")
	if err != nil || count != 20 || len(accepted) != 20 {
		t.Fatalf("concurrent budget: rows=%d successes=%d error=%v", count, len(accepted), err)
	}
}

func TestDCRRegistrationFailuresDoNotConsumeBudget(t *testing.T) {
	f, _ := registrationFixture(t)
	const peer = "192.0.2.1:3000"
	for range 21 {
		if w := registerFrom(f.s, peer, `{"redirect_uris":["http://evil.test/callback"]}`, nil); w.Code != 400 {
			t.Fatalf("invalid metadata: %d", w.Code)
		}
	}
	if _, err := f.db.Exec(`CREATE TRIGGER fail_registration BEFORE INSERT ON oauth_clients BEGIN SELECT RAISE(ABORT, 'injected registration failure'); END`); err != nil {
		t.Fatal(err)
	}
	for range 21 {
		w := registerFrom(f.s, peer, registrationMetadata, nil)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "temporarily_unavailable") || w.Header().Get("Retry-After") != "" {
			t.Fatalf("storage failure: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err := f.db.Exec(`DROP TRIGGER fail_registration`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1000)
		INSERT INTO oauth_clients SELECT 'full' || x, 'Client', '["https://client.test/callback"]', 'dcr', 9999999999 FROM n`); err != nil {
		t.Fatal(err)
	}
	for range 21 {
		if w := registerFrom(f.s, peer, registrationMetadata, nil); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("full registry: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err := f.db.Exec(`DELETE FROM oauth_clients WHERE source = 'dcr'`); err != nil {
		t.Fatal(err)
	}
	fillRegistrationBudget(t, f.s, peer, nil)
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 3600)
}

func TestDCRRegistrationLimitPreservesConnections(t *testing.T) {
	f, _ := registrationFixture(t)
	const peer = "192.0.2.1:3000"
	clientID := fillRegistrationBudget(t, f.s, peer, nil)
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 3600)
	// Cached CIMD authorization and token issuance use their existing paths.
	f.redeem(t, f.code(t))
	f.values.Set("client_id", clientID)
	first := f.redeem(t, f.code(t))
	f.s.options.AllowDCR = false
	if w := registerFrom(f.s, peer, registrationMetadata, nil); w.Code != http.StatusNotFound {
		t.Fatalf("disabled registration: %d", w.Code)
	}
	status, rotated := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(first)}})
	if status != http.StatusOK {
		t.Fatalf("existing refresh: %d %v", status, rotated["error"])
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(rotated)); err != nil {
		t.Fatal(err)
	}
	if status, _ = f.revoke(clientID, refresh(rotated)); status != http.StatusOK {
		t.Fatalf("existing revocation: %d", status)
	}
	f.s.options.AllowDCR = true
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 3600)
}

func TestDCRGlobalRateRetainsLiveBudgets(t *testing.T) {
	f, now := registrationFixture(t)
	const peer = "192.0.2.1:3000"
	requireRegistration(t, registerFrom(f.s, peer, registrationMetadata, nil))
	*now = now.Add(5 * time.Minute)
	for range 19 {
		requireRegistration(t, registerFrom(f.s, peer, registrationMetadata, nil))
	}
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 3300)
	// Reclaim fixture clients to isolate the burst rate from the registry
	// cap. More than 1,000 distinct sources can use the shared rate budget.
	for i := range 1980 {
		if _, err := f.db.Exec(`DELETE FROM oauth_clients WHERE source = 'dcr'`); err != nil {
			t.Fatal(err)
		}
		peerIP := fmt.Sprintf("198.18.%d.%d:3000", i/256, i%256)
		requireRegistration(t, registerFrom(f.s, peerIP, registrationMetadata, nil))
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 3300)
	requireRegistrationLimit(t, registerFrom(f.s, "203.0.113.1:3000", registrationMetadata, nil), 300)
	requireRegistrationLimit(t, registerFrom(f.s, "203.0.113.2:3000", registrationMetadata, nil), 300)
	if strings.Count(logs.String(), "reason=global_limit") != 1 || strings.Contains(logs.String(), "203.0.113.") {
		t.Fatalf("global refusal diagnostics: %s", logs.String())
	}
	*now = now.Add(5 * time.Minute)
	requireRegistration(t, registerFrom(f.s, "203.0.113.1:3000", registrationMetadata, nil))
	requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 3000)
	requireRegistrationLimit(t, registerFrom(f.s, "203.0.113.2:3000", registrationMetadata, nil), 300)
	*now = now.Add(5 * time.Minute)
	requireRegistration(t, registerFrom(f.s, "203.0.113.2:3000", registrationMetadata, nil))
}

// TestDCRSourceCannotExhaustRegistry pins the capacity invariant over multiple
// unused-client lifetimes, including cleanup at the persistent 1,000-row cap.
func TestDCRSourceCannotExhaustRegistry(t *testing.T) {
	f, now := registrationFixture(t)
	for range 72 {
		fillRegistrationBudget(t, f.s, "192.0.2.1:3000", nil)
		requireRegistrationLimit(t, registerFrom(f.s, "192.0.2.1:3000", registrationMetadata, nil), 3600)
		*now = now.Add(time.Hour)
	}
	requireRegistration(t, registerFrom(f.s, "203.0.113.1:3000", registrationMetadata, nil))
	var active int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM oauth_clients WHERE source = 'dcr' AND expires_at > ?`, now.Unix()).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active > 481 {
		t.Fatalf("one source consumed the registry: %d live clients", active)
	}
}
