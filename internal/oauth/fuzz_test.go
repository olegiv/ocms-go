// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"net/url"
	"testing"
)

func FuzzAuthorizationRedirectBinding(f *testing.F) {
	fixture := newFixture(f)
	f.Add("https://client.test/callback", "state&code=attacker", "S256")
	f.Add("https://attacker.test/callback", "state", "plain")
	f.Add("http://127.0.0.1:49152/callback", "#fragment", "S256")
	f.Fuzz(func(t *testing.T, redirectURI, state, method string) {
		values := url.Values{}
		for key, entries := range fixture.values {
			values[key] = append([]string(nil), entries...)
		}
		values.Set("redirect_uri", redirectURI)
		values.Set("state", state)
		values.Set("code_challenge_method", method)
		req, prepareErr := fixture.s.Prepare(t.Context(), values)
		response := fixture.s.Redirect(req, url.Values{"error": {"access_denied"}})
		if response == "" {
			return
		}
		u, err := url.Parse(response)
		if err != nil {
			t.Fatal(err)
		}
		// A protocol error can be redirected only to a registered callback;
		// client state must remain one query value, never become a code/fragment.
		registeredTarget := (u.Scheme == "https" && u.Host == "client.test") || (u.Scheme == "http" && u.Hostname() == "127.0.0.1")
		if u.User != nil || u.Fragment != "" || u.Path != "/callback" || !registeredTarget {
			t.Fatal("authorization escaped registered callback")
		}
		if u.Query().Get("code") != "" || u.Query().Get("iss") != fixture.s.Issuer() || u.Query().Get("state") != state {
			t.Fatal("response parameters were injected")
		}
		if prepareErr == nil && req.RedirectURI != redirectURI {
			t.Fatal("prepared redirect changed")
		}
	})
}
