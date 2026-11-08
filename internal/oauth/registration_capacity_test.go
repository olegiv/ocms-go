// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func requireDCRRows(t *testing.T, f *fixture, want int64) {
	t.Helper()
	if got, err := f.s.q.CountOAuthClientsBySource(t.Context(), "dcr"); err != nil || got != want {
		t.Fatalf("DCR registry count=%d, want %d: %v", got, want, err)
	}
}

func TestDCRCapacityReclaimsOverrepresentedSourceAtomically(t *testing.T) {
	f, now := registrationFixture(t)
	var victim, consented string
	for i := range 50 {
		peer := fmt.Sprintf("192.0.2.%d:3000", i+1)
		for j := range 20 {
			id := requireRegistration(t, registerFrom(f.s, peer, registrationMetadata, nil))
			if i == 0 && j == 0 {
				consented = id
				f.values.Set("client_id", id)
				f.redeem(t, f.code(t))
				// Retention can remove a grant before the client expires.
				retentionExec(t, f, "DELETE FROM oauth_grants WHERE client_id = ?", id)
			}
			if i == 1 && j == 0 {
				victim = id
			}
			*now = now.Add(time.Millisecond)
		}
	}
	requireDCRRows(t, f, 1000)
	f.values.Set("client_id", victim)
	staleConsent, err := f.s.Prepare(t.Context(), f.values)
	if err != nil {
		t.Fatal(err)
	}
	const newcomer = "198.51.100.1:3000"
	retentionExec(t, f, `CREATE TRIGGER reject_dcr_budget BEFORE INSERT ON oauth_dcr_registrations
		BEGIN SELECT RAISE(ABORT, 'registration evidence write failed'); END`)
	if w := registerFrom(f.s, newcomer, registrationMetadata, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed registration evidence: %d %s", w.Code, w.Body.String())
	}
	requireDCRRows(t, f, 1000)
	if _, err := f.s.q.GetOAuthClient(t.Context(), victim); err != nil {
		t.Fatalf("failed registration removed victim: %v", err)
	}
	retentionExec(t, f, "DROP TRIGGER reject_dcr_budget")
	requireRegistration(t, registerFrom(f.s, newcomer, registrationMetadata, nil))
	requireDCRRows(t, f, 1000)
	if _, err := f.s.q.GetOAuthClient(t.Context(), victim); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("oldest client from an overrepresented source was not reclaimed: %v", err)
	}
	if _, err := f.s.Authorize(t.Context(), staleConsent, f.user.ID); problemCode(err) != "invalid_client" {
		t.Fatalf("consent for reclaimed client: %v", err)
	}
	if _, err := f.s.clients.resolve(t.Context(), consented); err != nil {
		t.Fatalf("previously redeemed client became unavailable: %v", err)
	}
	requireRegistrationLimit(t, registerFrom(f.s, "192.0.2.2:3000", registrationMetadata, nil), 3600)
}

func TestDCRDistinctSourcesKeepGrace(t *testing.T) {
	f, now := registrationFixture(t)
	start := *now
	for i := range 1000 {
		requireRegistration(t, registerFrom(f.s, fmt.Sprintf("198.18.%d.%d:3000", i/256, i%256), registrationMetadata, nil))
	}
	const newcomer = "192.0.2.1:3000"
	if w := registerFrom(f.s, newcomer, registrationMetadata, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("recent sole clients lost their grace period: %d %s", w.Code, w.Body.String())
	}
	*now = start.Add(dcrSingleClientGrace)
	requireRegistration(t, registerFrom(f.s, newcomer, registrationMetadata, nil))
	requireDCRRows(t, f, 1000)
}

func TestDCRAdmissionMigrationPreservesLegacyClient(t *testing.T) {
	f, now := registrationFixture(t)
	if err := store.RunMigrationCommand(t.Context(), f.db, "down-to", "20261106120000"); err != nil {
		t.Fatal(err)
	}
	legacy := registeredClient{ID: "legacy-client", Name: "Legacy", Redirects: []string{"https://client.test/callback"}}
	if err := f.s.saveClient(t.Context(), legacy, "dcr", now.Add(DCRUnusedClientLifetime).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(f.db); err != nil {
		t.Fatal(err)
	}
	f.values.Set("client_id", legacy.ID)
	f.redeem(t, f.code(t))
	requireRegistration(t, registerFrom(f.s, "192.0.2.1:3000", registrationMetadata, nil))
	requireDCRRows(t, f, 2)
}

func TestAbandonedDCRConsentsReleaseRegistryCapacity(t *testing.T) {
	f, now := registrationFixture(t)
	for i := range 50 {
		peer := fmt.Sprintf("192.0.2.%d:3000", i+1)
		for range 20 {
			id := requireRegistration(t, registerFrom(f.s, peer, registrationMetadata, nil))
			f.values.Set("client_id", id)
			f.code(t)
		}
	}
	requireDCRRows(t, f, 1000)
	*now = now.Add(DCRUnusedClientLifetime + time.Minute)
	for range 1000 / CleanupBatchSize {
		if _, err := cleanupAt(t.Context(), f.db, *now); err != nil {
			t.Fatal(err)
		}
	}
	requireRegistration(t, registerFrom(f.s, "198.51.100.1:3000", registrationMetadata, nil))
	requireDCRRows(t, f, 1)
}

func TestDCRRefusalWarningsAreBounded(t *testing.T) {
	f, _ := registrationFixture(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	const peer = "192.0.2.1:3000"
	fillRegistrationBudget(t, f.s, peer, nil)
	for range 2 {
		requireRegistrationLimit(t, registerFrom(f.s, peer, registrationMetadata, nil), 3600)
	}
	retentionExec(t, f, `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<980)
		INSERT INTO oauth_clients SELECT 'legacy' || x, 'Client', '[]', 'dcr', 9999999999 FROM n`)
	// All provisional rows share one source, so that source has no donor
	// when its own hourly budget expires and the registry is still full.
	now := f.s.now().Add(time.Hour)
	f.s.now = func() time.Time { return now }
	for range 2 {
		if w := registerFrom(f.s, peer, registrationMetadata, nil); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("registry refusal: %d %s", w.Code, w.Body.String())
		}
	}
	for _, reason := range []string{"source_limit", "registry_full"} {
		if got := strings.Count(logs.String(), "reason="+reason); got != 1 {
			t.Fatalf("reason %s logged %d times: %s", reason, got, logs.String())
		}
	}
	if strings.Contains(logs.String(), "192.0.2.1") {
		t.Fatal("refusal warning included the peer address")
	}
}

func TestCancelledDCRRequestDoesNotLogStorageFault(t *testing.T) {
	f, _ := registrationFixture(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	synctest.Test(t, func(t *testing.T) {
		f.s.dcrWrites = make(chan struct{}, 1)
		f.s.dcrWrites <- struct{}{}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/oauth/register", strings.NewReader(registrationMetadata))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			f.s.Register(w, req)
		}()
		synctest.Wait()
		cancel()
		<-done
		if w.Code != http.StatusServiceUnavailable || strings.Contains(logs.String(), "level=ERROR") {
			t.Fatalf("cancelled registration status=%d logs=%q", w.Code, logs.String())
		}
	})
	requireDCRRows(t, f, 0)
}
