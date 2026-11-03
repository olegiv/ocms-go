// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDCRCapacityCannotDenyTrustedClientAdmission(t *testing.T) {
	f := metadataFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(registeredClient{ID: "https://client.test" + r.URL.Path, Name: "Fetched", AuthMethod: "none", Redirects: []string{"https://client.test/callback"}})
	})
	if _, err := f.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1000)
		INSERT INTO oauth_clients SELECT 'public' || x, 'Public', '["https://client.test/callback"]', 'dcr', 9999999999 FROM n`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.clients.resolve(t.Context(), "https://client.test/fresh"); err != nil {
		t.Fatalf("public registrations denied new fetched client: %v", err)
	}
	pinned := registeredClient{ID: "https://client.test/pinned", Name: "Pinned", AuthMethod: "none", Redirects: []string{"https://client.test/callback"}}
	raw, err := json.Marshal([]registeredClient{pinned})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.clients.loadPreRegistered(string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.clients.resolve(t.Context(), pinned.ID); err != nil {
		t.Fatalf("public registrations denied first-use owner pin: %v", err)
	}
	f.s.options.AllowDCR = true
	r := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(`{"client_name":"New public","redirect_uris":["https://client.test/callback"]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.s.Register(w, r)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "temporarily_unavailable") {
		t.Fatalf("public cap response = %d %s", w.Code, w.Body.String())
	}
	if _, err := f.s.clients.resolve(t.Context(), "public1"); err != nil {
		t.Fatalf("existing public client stopped resolving: %v", err)
	}
	// Existing authorization/token behavior is independent of either cap.
	f.redeem(t, f.code(t))
	if _, err := f.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<997)
		INSERT INTO oauth_clients SELECT 'trusted' || x, 'Trusted', '["https://client.test/callback"]', 'cimd', 9999999999 FROM n`); err != nil {
		t.Fatal(err)
	}
	pinned.ID = "https://client.test/over-cap"
	if err := f.s.saveClient(t.Context(), pinned, "cimd", time.Now().Add(time.Hour).Unix()); err == nil {
		t.Fatal("trusted registry grew without bound")
	}
	if _, err := f.db.Exec(`UPDATE oauth_clients SET expires_at = 1 WHERE id = 'trusted1'`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.saveClient(t.Context(), pinned, "cimd", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatalf("expired unused capacity was not reclaimed: %v", err)
	}
}

func TestDCRCapacityRemainsBoundedUnderConcurrentAdmission(t *testing.T) {
	f := newFixture(t)
	if _, err := f.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<999)
		INSERT INTO oauth_clients SELECT 'public' || x, 'Public', '["https://client.test/callback"]', 'dcr', 9999999999 FROM n`); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			client := registeredClient{ID: fmt.Sprintf("concurrent%d", i), Name: "Public", AuthMethod: "none", Redirects: []string{"https://client.test/callback"}}
			if f.s.saveClient(t.Context(), client, "dcr", time.Now().Add(time.Hour).Unix()) == nil {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	var count int64
	err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM oauth_clients WHERE source = 'dcr'`).Scan(&count)
	if err != nil || count != 1000 || accepted.Load() != 1 {
		t.Fatalf("concurrent admission: count=%d accepted=%d error=%v", count, accepted.Load(), err)
	}
}
