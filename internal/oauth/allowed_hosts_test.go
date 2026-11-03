// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestAllowedHostsUnsetMeansDefaultsAndEmptyMeansNone pins the first half of
// audit A-4: an empty allowlist used to be replaced by claude.ai and
// chatgpt.com, so "pre-registered clients only" could not be configured.
func TestAllowedHostsUnsetMeansDefaultsAndEmptyMeansNone(t *testing.T) {
	f := newFixture(t)
	unset, err := New(f.db, Options{Issuer: "https://cms.test"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(unset.options.AllowedHosts, []string{"claude.ai", "chatgpt.com"}) {
		t.Fatalf("unset allowlist = %v, want the documented defaults", unset.options.AllowedHosts)
	}

	for name, hosts := range map[string][]string{"empty slice": {}, "empty setting": SplitAllowedHosts(""), "blank setting": SplitAllowedHosts("  ,  ")} {
		t.Run(name, func(t *testing.T) {
			s, err := New(f.db, Options{Issuer: "https://cms.test", AllowedHosts: hosts})
			if err != nil {
				t.Fatal(err)
			}
			if len(s.options.AllowedHosts) != 0 {
				t.Fatalf("empty allowlist = %v, want none", s.options.AllowedHosts)
			}
			s.clients.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("live metadata discovery ran with an empty allowlist")
				return nil, nil
			})
			for _, id := range []string{"https://claude.ai/oauth/mcp-oauth-client-metadata", "https://chatgpt.com/oauth/client.json"} {
				if got := problemCode(second(s.clients.resolve(t.Context(), id))); got != "invalid_client" {
					t.Fatalf("resolve(%s) = %q, want invalid_client", id, got)
				}
			}
		})
	}
}

// TestEmptyAllowlistStillServesPinnedClientsAndDCR is the point of an empty
// allowlist: owner-reviewed clients and registrations keep working, and
// nothing is discovered live.
func TestEmptyAllowlistStillServesPinnedClientsAndDCR(t *testing.T) {
	f := newFixture(t)
	const pin = `[{"client_id":"https://claude.ai/oauth/claude-code-client-metadata","client_name":"Claude Code","redirect_uris":["http://localhost/callback"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"]}]`
	s, err := New(f.db, Options{Issuer: "https://cms.test", AllowedHosts: []string{}, PreRegisteredClients: pin, AllowDCR: true})
	if err != nil {
		t.Fatalf("pinned client refused with an empty allowlist: %v", err)
	}
	s.clients.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("live metadata discovery ran with an empty allowlist")
		return nil, nil
	})
	f.s = s
	f.values.Set("client_id", "https://claude.ai/oauth/claude-code-client-metadata")
	f.values.Set("redirect_uri", "http://localhost:65101/callback")
	if _, err := s.Prepare(t.Context(), f.values); err != nil {
		t.Fatalf("pinned client unusable: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(`{"client_name":"Desktop","redirect_uris":["https://client.test/callback"],"token_endpoint_auth_method":"none"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Register(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("dynamic registration status=%d", w.Code)
	}

	// A malformed pin is still refused, and a pin must still be a metadata
	// document URL.
	for _, id := range []string{"http://claude.ai/pinned", "https://127.0.0.1/pinned", "https://claude.ai:8443/pinned"} {
		raw := strings.Replace(pin, "https://claude.ai/oauth/claude-code-client-metadata", id, 1)
		if _, err := New(f.db, Options{Issuer: "https://cms.test", AllowedHosts: []string{}, PreRegisteredClients: raw}); err == nil {
			t.Errorf("pinned client id %q accepted", id)
		}
	}
}

// TestAllowedHostsMustBeBareLowercaseHostnames pins the second half of A-4:
// entries were never validated, so "https://claude.ai", "claude.ai:443" or
// "*.claude.ai" silently matched nothing.
func TestAllowedHostsMustBeBareLowercaseHostnames(t *testing.T) {
	f := newFixture(t)
	for _, host := range []string{
		"claude.ai", "chatgpt.com", "a-b.example.co.uk", "xn--bcher-kva.example", "client.test", "1password.com",
		strings.Repeat("a", 63) + ".com",
	} {
		if _, err := New(f.db, Options{Issuer: "https://cms.test", AllowedHosts: []string{host}}); err != nil {
			t.Errorf("valid host %q refused: %v", host, err)
		}
	}
	for _, host := range []string{
		"", " ", " claude.ai", "claude.ai ", "https://claude.ai", "https://claude.ai/", "claude.ai:443", "claude.ai/path", "claude.ai/",
		"*.claude.ai", "*", "claude.*", "user@claude.ai", "claude.ai?x=1", "claude.ai#x", "a b.com",
		"127.0.0.1", "10.0.0.1", "169.254.169.254", "[::1]", "::1", "2001:db8::1", "127.1", "1.2.3.4",
		"claude", "localhost", "claude..ai", ".claude.ai", "claude.ai.", "-claude.ai", "claude-.ai", "cla_ude.ai",
		"Claude.AI", "CLAUDE.ai", "bücher.example", strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 127) + "com",
	} {
		_, err := New(f.db, Options{Issuer: "https://cms.test", AllowedHosts: []string{"claude.ai", host}})
		if err == nil {
			t.Errorf("invalid host %q accepted", host)
		}
	}
	// A bad entry anywhere in a list is refused, whatever the others are.
	if _, err := New(f.db, Options{Issuer: "https://cms.test", AllowedHosts: SplitAllowedHosts("claude.ai, https://chatgpt.com")}); err == nil {
		t.Error("list with one malformed entry accepted")
	}
}

func TestSplitAllowedHosts(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{"", []string{}},
		{"   ", []string{}},
		{",,", []string{}},
		{"claude.ai", []string{"claude.ai"}},
		{"claude.ai,chatgpt.com", []string{"claude.ai", "chatgpt.com"}},
		{" Claude.AI ,\tchatgpt.com\n", []string{"claude.ai", "chatgpt.com"}},
		{"claude.ai chatgpt.com", []string{"claude.ai", "chatgpt.com"}},
	} {
		got := SplitAllowedHosts(tc.raw)
		if got == nil || !slices.Equal(got, tc.want) {
			t.Errorf("SplitAllowedHosts(%q) = %#v, want %#v", tc.raw, got, tc.want)
		}
	}
}

// TestDefaultAllowedHostsAreIndependentCopies keeps one Service's allowlist
// from being edited through another's.
func TestDefaultAllowedHostsAreIndependentCopies(t *testing.T) {
	first := DefaultAllowedHosts()
	first[0] = "attacker.test"
	if got := DefaultAllowedHosts(); got[0] != "claude.ai" {
		t.Fatalf("defaults were shared: %v", got)
	}
}
