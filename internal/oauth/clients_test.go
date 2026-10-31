// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func metadataFixture(t *testing.T, handler http.HandlerFunc) *fixture {
	t.Helper()
	f := newFixture(t)
	f.s.options.AllowedHosts = []string{"client.test"}
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)
	// Only this fixture transport redirects a permitted test hostname to the
	// local TLS server; the production transport keeps its safe DNS dialer.
	f.s.clients.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Host = ts.Listener.Addr().String()
		clone.URL = &u
		return ts.Client().Transport.RoundTrip(clone)
	})
	return f
}

func TestCIMDFetchCacheAndExpiry(t *testing.T) {
	const id = "https://client.test/cimd"
	var calls atomic.Int64
	f := metadataFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(registeredClient{ID: id, Name: "Client", AuthMethod: "none", Redirects: []string{"https://client.test/callback"}})
	})
	for range 2 {
		if _, err := f.s.clients.resolve(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("metadata cache was not used")
	}
	now := time.Now().Add(16 * time.Minute)
	f.s.now = func() time.Time { return now }
	if _, err := f.s.clients.resolve(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("expired metadata was trusted")
	}
}

func TestCIMDRejectsUnsafeDocuments(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://other.test/doc", http.StatusFound)
		},
		"oversized": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", (64<<10)+1))) },
		"mismatch": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"client_id":"https://other.test/doc","client_name":"Other","token_endpoint_auth_method":"none","redirect_uris":["https://client.test/callback"]}`))
		},
		"secret_auth": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"client_id":"https://client.test/cimd","client_name":"Other","token_endpoint_auth_method":"client_secret_basic","redirect_uris":["https://client.test/callback"]}`))
		},
		"trailing_json": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{} {}`)) },
		"slow":          func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
	} {
		t.Run(name, func(t *testing.T) {
			f := metadataFixture(t, handler)
			if f.s.clients.http.Timeout != 5*time.Second {
				t.Fatal("production timeout is not bounded at 5s")
			}
			if name == "slow" {
				f.s.clients.http.Timeout = 50 * time.Millisecond
			}
			if _, err := f.s.clients.resolve(t.Context(), "https://client.test/cimd"); err == nil {
				t.Fatal("unsafe metadata accepted")
			}
		})
	}
}

func TestCIMDURLAndNetworkRestrictions(t *testing.T) {
	f := newFixture(t)
	f.s.options.AllowedHosts = []string{"client.test", "localhost", "127.0.0.1", "169.254.169.254"}
	for _, id := range []string{
		"http://client.test/doc", "https://other.test/doc", "https://client.test:8443/doc",
		"https://user:password@client.test/doc", "https://client.test/doc?query=1", "https://client.test/doc#fragment",
		"https://127.0.0.1/doc", "https://169.254.169.254/doc", "https://localhost/doc",
	} {
		if _, err := f.s.clients.resolve(t.Context(), id); err == nil {
			t.Errorf("accepted %s", id)
		}
	}
}

func TestClientCapAllowsRefreshButRefusesNewRegistration(t *testing.T) {
	f := newFixture(t)
	if _, err := f.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<999)
		INSERT INTO oauth_clients SELECT 'cap' || x, 'Client', '["https://client.test/callback"]', 'dcr', 9999999999 FROM n`); err != nil {
		t.Fatal(err)
	}
	c := registeredClient{ID: f.values.Get("client_id"), Name: "Updated", AuthMethod: "none", Redirects: []string{f.values.Get("redirect_uri")}}
	if err := f.s.saveClient(t.Context(), c, "cimd", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatalf("existing client could not refresh: %v", err)
	}
	c.ID = "new-client"
	if err := f.s.saveClient(t.Context(), c, "dcr", time.Now().Add(time.Hour).Unix()); err == nil {
		t.Fatal("client cap exceeded")
	}
}

func TestRegistrationDisabledAndBounded(t *testing.T) {
	f := newFixture(t)
	valid := `{"client_name":"Desktop","redirect_uris":["http://127.0.0.1/callback"],"token_endpoint_auth_method":"none"}`
	register := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.s.Register(w, r)
		return w
	}
	if w := register(valid); w.Code != 404 {
		t.Fatalf("DCR enabled by default: %d", w.Code)
	}
	f.s.options.AllowDCR = true
	w := register(valid)
	if w.Code != 201 || strings.Contains(w.Body.String(), "client_secret") {
		t.Fatalf("public registration status=%d", w.Code)
	}
	for _, body := range []string{valid + " {}", valid + strings.Repeat(" ", 16<<10),
		strings.Replace(valid, `"none"`, `"client_secret_basic"`, 1),
		strings.Replace(valid, `"none"`, `"none","grant_types":["password"]`, 1),
		strings.Replace(valid, "127.0.0.1", "localhost", 1),
	} {
		if w := register(body); w.Code != 400 {
			t.Fatalf("invalid registration status=%d", w.Code)
		}
	}
}
