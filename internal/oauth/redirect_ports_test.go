// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import "testing"

// TestRedirectPortsAreBounded pins audit A-15: url.Parse accepts any numeric
// port, so ":0" and ":99999" used to be registered, matched and redirected to.
// Ports outside 1-65535 are refused wherever a redirect URI is validated.
func TestRedirectPortsAreBounded(t *testing.T) {
	for _, tc := range []struct {
		uri  string
		want bool
	}{
		{"https://client.test/callback", true},
		{"https://client.test:1/callback", true},
		{"https://client.test:443/callback", true},
		{"https://client.test:65535/callback", true},
		{"http://127.0.0.1:1/callback", true},
		{"http://127.0.0.1:65535/callback", true},
		{"http://localhost:65535/callback", true},
		{"https://client.test:0/callback", false},
		{"https://client.test:65536/callback", false},
		{"https://client.test:99999/callback", false},
		{"http://127.0.0.1:0/callback", false},
		{"http://127.0.0.1:65536/callback", false},
		{"http://127.0.0.1:99999/callback", false},
		{"http://localhost:0/callback", false},
		{"http://[::1]:99999/callback", false},
	} {
		if got := validRedirect(tc.uri); got != tc.want {
			t.Errorf("validRedirect(%q) = %v, want %v", tc.uri, got, tc.want)
		}
	}

	// Registered metadata: the document is refused as a whole.
	for _, redirect := range []string{"http://127.0.0.1:0/callback", "https://client.test:99999/callback"} {
		client := registeredClient{Name: "Native client", AuthMethod: "none", Redirects: []string{redirect}}
		if validCIMDClient(client) || validClient(client) {
			t.Errorf("client metadata with %q accepted", redirect)
		}
	}

	// Request time: a loopback client registered without a port may choose
	// one at login, but only a real one.
	client := registeredClient{Name: "Native client", AuthMethod: "none", Redirects: []string{"http://127.0.0.1/callback"}}
	for port, want := range map[string]bool{"1": true, "49152": true, "65535": true, "0": false, "65536": false, "99999": false} {
		if got := client.acceptsRedirect("http://127.0.0.1:" + port + "/callback"); got != want {
			t.Errorf("acceptsRedirect(port %s) = %v, want %v", port, got, want)
		}
	}
	f := newFixture(t)
	for _, port := range []string{"0", "99999"} {
		f.values.Set("redirect_uri", "http://127.0.0.1:"+port+"/callback")
		if _, err := f.s.Prepare(t.Context(), f.values); err == nil {
			t.Errorf("authorization request accepted loopback port %s", port)
		}
	}
	f.values.Set("redirect_uri", "http://127.0.0.1:49152/callback")
	if _, err := f.s.Prepare(t.Context(), f.values); err != nil {
		t.Fatalf("ordinary loopback port refused: %v", err)
	}
}
