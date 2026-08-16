// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util_test

import (
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/util"
)

// TestValidateCanonicalURLAccepts pins the values an editor or API client may
// store. The empty string is load-bearing: it is the "unset" sentinel in the
// pages table and the only value a PATCH can send to clear the field.
func TestValidateCanonicalURLAccepts(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want string
	}{
		"empty clears the field": {"", ""},
		"whitespace only":        {"   ", ""},
		"https":                  {"https://example.com/a", "https://example.com/a"},
		"http":                   {"http://example.com/a", "http://example.com/a"},
		"bare host":              {"https://example.com", "https://example.com"},
		"explicit port":          {"https://example.com:8443/a", "https://example.com:8443/a"},
		"query and fragment":     {"https://example.com/a?b=1#c", "https://example.com/a?b=1#c"},
		"uppercase scheme":       {"HTTPS://example.com/a", "HTTPS://example.com/a"},
		"surrounding whitespace": {"  https://example.com/a  ", "https://example.com/a"},
		"internationalized host": {"https://пример.рф/a", "https://пример.рф/a"},
		"at maximum length":      {"https://example.com/" + strings.Repeat("a", util.MaxCanonicalURLLength-20), "https://example.com/" + strings.Repeat("a", util.MaxCanonicalURLLength-20)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := util.ValidateCanonicalURL(tc.raw)
			if err != nil {
				t.Fatalf("ValidateCanonicalURL(%q) error = %v, want it accepted", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("ValidateCanonicalURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestValidateCanonicalURLRejects covers the values that must never reach a
// published page. The scripting schemes are the security case: the stored value
// is emitted as a <link rel="canonical"> href and as og:url meta content, and a
// theme can opt out of contextual escaping via the safeURL template function.
// The relative cases are the correctness case: og:url must be absolute.
//
// Bug state: drop any branch from ValidateCanonicalURL and the matching subtest
// names the value that got through.
func TestValidateCanonicalURLRejects(t *testing.T) {
	cases := map[string]string{
		"javascript scheme":   "javascript:alert(1)",
		"data scheme":         "data:text/html;base64,PHNjcmlwdD4=",
		"file scheme":         "file:///etc/passwd",
		"mailto scheme":       "mailto:someone@example.com",
		"ftp scheme":          "ftp://example.com/a",
		"relative path":       "/about",
		"scheme relative":     "//cdn.example.com/a",
		"bare relative":       "about",
		"missing host":        "https://",
		"credentials":         "https://user:pass@example.com/a",
		"username only":       "https://user@example.com/a",
		"port out of range":   "https://example.com:99999/a",
		"zero port":           "https://example.com:0/a",
		"control character":   "https://example.com/\x7f",
		"over maximum length": "https://example.com/" + strings.Repeat("a", util.MaxCanonicalURLLength),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := util.ValidateCanonicalURL(raw)
			if err == nil {
				t.Fatalf("ValidateCanonicalURL(%q) = %q with no error, want it rejected", raw, got)
			}
			if got != "" {
				t.Errorf("ValidateCanonicalURL(%q) returned %q alongside an error, want an empty value", raw, got)
			}
		})
	}
}
