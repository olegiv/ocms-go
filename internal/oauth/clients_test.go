// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestCIMDClaudeMetadataSupportsCodeFlow(t *testing.T) {
	// Public Claude metadata captured on 2026-10-31. Extra advertised grant
	// capabilities must not enable them at this authorization server.
	const metadata = `{"client_id":"https://claude.ai/oauth/mcp-oauth-client-metadata","client_name":"Claude","client_uri":"https://claude.ai","redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"grant_types":["authorization_code","refresh_token","urn:ietf:params:oauth:grant-type:jwt-bearer"],"response_types":["code"],"token_endpoint_auth_method":"none"}`
	f := metadataFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(metadata))
	})
	f.s.options.AllowedHosts = []string{"claude.ai"}
	f.values.Set("client_id", "https://claude.ai/oauth/mcp-oauth-client-metadata")
	f.values.Set("redirect_uri", "https://claude.ai/api/mcp/auth_callback")
	body := f.redeem(t, f.code(t))
	p, err := f.s.VerifyAccess(t.Context(), access(body))
	if err != nil || p.ClientID != f.values.Get("client_id") || len(p.Scopes) != 3 {
		t.Fatalf("Claude code flow failed: principal=%+v error=%v", p, err)
	}
	status, denied := f.exchange(url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}})
	if status != http.StatusBadRequest || denied["error"] != "unsupported_grant_type" {
		t.Fatalf("advertised JWT grant accepted: status=%d error=%v", status, denied["error"])
	}
}

func TestCIMDChatGPTMetadataNegotiatesPublicClient(t *testing.T) {
	// ChatGPT's public metadata captured on 2026-10-31 advertises both
	// methods. Its legacy preference must not override the supported set.
	const metadata = `{"client_id":"https://chatgpt.com/oauth/client.json","client_name":"ChatGPT","redirect_uris":["https://chatgpt.com/connector_platform_oauth_redirect"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"private_key_jwt","token_endpoint_auth_methods_supported":["none","private_key_jwt"]}`
	f := metadataFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(metadata))
	})
	f.s.options.AllowedHosts = []string{"chatgpt.com"}
	f.values.Set("client_id", "https://chatgpt.com/oauth/client.json")
	f.values.Set("redirect_uri", "https://chatgpt.com/connector_platform_oauth_redirect")
	client, err := f.s.clients.resolve(t.Context(), f.values.Get("client_id"))
	if err != nil || client.AuthMethod != "none" {
		t.Fatalf("public-client method not selected: method=%q error=%v", client.AuthMethod, err)
	}
	body := f.redeem(t, f.code(t))
	status, rotated := f.exchange(url.Values{"grant_type": {"refresh_token"},
		"client_id": {client.ID}, "resource": {f.s.Resource()}, "refresh_token": {refresh(body)}})
	if status != http.StatusOK || access(rotated) == "" || refresh(rotated) == refresh(body) {
		t.Fatalf("ChatGPT public-client refresh failed: status=%d", status)
	}
	status, denied := f.exchange(url.Values{"grant_type": {"authorization_code"},
		"client_id": {client.ID}, "resource": {f.s.Resource()}, "client_assertion": {"unsupported-assertion"}})
	if status != http.StatusBadRequest || denied["error"] != "invalid_client" {
		t.Fatalf("client assertion was accepted: status=%d error=%v", status, denied["error"])
	}
}

func TestCIMDAuthMethodCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name, methods string
		want          bool
	}{
		{"legacy public", `"token_endpoint_auth_method":"none"`, true},
		{"legacy assertion only", `"token_endpoint_auth_method":"private_key_jwt"`, false},
		{"public supported with assertion preference", `"token_endpoint_auth_method":"private_key_jwt","token_endpoint_auth_methods_supported":["none","private_key_jwt"]`, true},
		{"public supported without preference", `"token_endpoint_auth_methods_supported":["none"]`, true},
		{"assertion only with public preference", `"token_endpoint_auth_method":"none","token_endpoint_auth_methods_supported":["private_key_jwt"]`, false},
		{"empty capabilities", `"token_endpoint_auth_method":"none","token_endpoint_auth_methods_supported":[]`, false},
		{"no method", `"unrelated":true`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var client registeredClient
			if err := json.Unmarshal([]byte(`{"client_name":"Client","redirect_uris":["https://client.test/callback"],`+tc.methods+`}`), &client); err != nil {
				t.Fatal(err)
			}
			if got := validCIMDClient(client); got != tc.want {
				t.Fatalf("CIMD method capabilities accepted=%v, want %v", got, tc.want)
			}
			if tc.name == "public supported with assertion preference" && validClient(client) {
				t.Fatal("CIMD capability negotiation relaxed DCR authentication")
			}
		})
	}
}

func TestPreRegisteredClientSurvivesMetadataFetchFailure(t *testing.T) {
	f := newFixture(t)
	client := registeredClient{ID: "https://client.test/pinned", Name: "Pinned client", AuthMethod: "none",
		Redirects: []string{"https://client.test/callback"}, GrantTypes: []string{"authorization_code", "refresh_token"},
		Responses: []string{"code"}}
	options := Options{Issuer: f.s.Issuer(), AllowedHosts: []string{"client.test"}}
	var calls atomic.Int64
	denyFetch := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls.Add(1)
		w := httptest.NewRecorder()
		w.WriteHeader(http.StatusForbidden)
		return w.Result(), nil
	})
	service, err := New(f.db, options)
	if err != nil {
		t.Fatal(err)
	}
	service.clients.http.Transport = denyFetch
	f.values.Set("client_id", client.ID)
	f.values.Set("redirect_uri", client.Redirects[0])
	if _, err := service.Prepare(t.Context(), f.values); err == nil || calls.Load() != 1 {
		t.Fatal("failed live discovery did not fail closed")
	}
	raw, err := json.Marshal([]registeredClient{client})
	if err != nil {
		t.Fatal(err)
	}
	options.PreRegisteredClients = string(raw)
	f.s, err = New(f.db, options)
	if err != nil {
		t.Fatal(err)
	}
	f.s.clients.http.Transport = denyFetch
	body := f.redeem(t, f.code(t))
	p, err := f.s.VerifyAccess(t.Context(), access(body))
	if err != nil || p.ClientID != client.ID || len(p.Scopes) != 3 {
		t.Fatalf("pinned client code flow failed: principal=%+v error=%v", p, err)
	}
	now := time.Now().Add(16 * time.Minute)
	f.s.now = func() time.Time { return now }
	status, next := f.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh(body)}})
	if status != http.StatusOK || access(next) == "" || calls.Load() != 1 {
		t.Fatalf("pinned refresh failed or performed live discovery: status=%d calls=%d", status, calls.Load())
	}
	if err := f.s.RevokeGrant(t.Context(), f.user.ID, p.GrantID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.VerifyAccess(t.Context(), access(next)); err == nil {
		t.Fatal("pinned client token survived grant revocation")
	}
	options.PreRegisteredClients = ""
	unpinned, err := New(f.db, options)
	if err != nil {
		t.Fatal(err)
	}
	unpinned.clients.http.Transport = denyFetch
	unpinned.now = func() time.Time { return now.Add(16 * time.Minute) }
	if _, err := unpinned.Prepare(t.Context(), f.values); err == nil || calls.Load() != 2 {
		t.Fatal("removed pin outlived the normal metadata cache")
	}
}

func TestPreRegisteredClientConfigurationRejectsUnsafeMetadata(t *testing.T) {
	f := newFixture(t)
	const valid = `[{"client_id":"https://client.test/pinned","client_name":"Pinned","redirect_uris":["https://client.test/callback"],"token_endpoint_auth_method":"none","grant_types":["authorization_code"]}]`
	for name, raw := range map[string]string{
		"malformed": "[", "trailing_json": valid + " {}", "oversized": valid + strings.Repeat(" ", 64<<10),
		"unlisted_host":     strings.ReplaceAll(valid, "client.test", "other.test"),
		"http_id":           strings.Replace(valid, "https://client.test/pinned", "http://client.test/pinned", 1),
		"private_id":        strings.Replace(valid, "https://client.test/pinned", "https://127.0.0.1/pinned", 1),
		"id_credentials":    strings.Replace(valid, "https://client.test/pinned", "https://user:pass@client.test/pinned", 1),
		"id_query":          strings.Replace(valid, "/pinned", "/pinned?x=1", 1),
		"id_fragment":       strings.Replace(valid, "/pinned", "/pinned#x", 1),
		"id_port":           strings.Replace(valid, "client.test/pinned", "client.test:8443/pinned", 1),
		"confidential":      strings.Replace(valid, `"none"`, `"private_key_jwt"`, 1),
		"unsafe_redirect":   strings.Replace(valid, "https://client.test/callback", "http://client.test/callback", 1),
		"no_code":           strings.Replace(valid, "authorization_code", "client_credentials", 1),
		"implicit_response": strings.Replace(valid, `"grant_types"`, `"response_types":["token"],"grant_types"`, 1),
		"duplicate":         "[" + strings.Trim(valid, "[]") + "," + strings.Trim(valid, "[]") + "]",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(f.db, Options{Issuer: f.s.Issuer(), AllowedHosts: []string{"client.test", "127.0.0.1"}, PreRegisteredClients: raw})
			if err == nil || strings.Contains(err.Error(), raw) {
				t.Fatal("unsafe pin accepted or error disclosed raw configuration")
			}
		})
	}
	for _, count := range []int{20, 21} {
		clients := make([]registeredClient, count)
		for i := range clients {
			clients[i] = registeredClient{ID: fmt.Sprintf("https://client.test/pin%d", i), Name: "Pinned", AuthMethod: "none",
				Redirects: []string{"https://client.test/callback"}}
		}
		raw, err := json.Marshal(clients)
		if err != nil {
			t.Fatal(err)
		}
		_, err = New(f.db, Options{Issuer: f.s.Issuer(), AllowedHosts: []string{"client.test"}, PreRegisteredClients: string(raw)})
		if (err == nil) != (count == 20) {
			t.Fatalf("pin count=%d error=%v", count, err)
		}
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
		"no_code_grant": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"client_id":"https://client.test/cimd","client_name":"Other","token_endpoint_auth_method":"none","redirect_uris":["https://client.test/callback"],"grant_types":["urn:ietf:params:oauth:grant-type:jwt-bearer"]}`))
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
		strings.Replace(valid, `"none"`, `"none","grant_types":["authorization_code","urn:ietf:params:oauth:grant-type:jwt-bearer"]`, 1),
		strings.Replace(valid, "127.0.0.1", "localhost", 1),
	} {
		if w := register(body); w.Code != 400 {
			t.Fatalf("invalid registration status=%d", w.Code)
		}
	}
}
