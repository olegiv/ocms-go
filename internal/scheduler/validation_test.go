// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"net"
	"testing"
)

func TestValidateTaskURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		// Valid URLs (public)
		{"valid https", "https://example.com/health", false, ""},
		{"valid http", "http://example.com/api/check", false, ""},
		{"valid with port", "https://example.com:8443/status", false, ""},
		{"valid with path", "https://httpbin.org/get", false, ""},

		// Empty / missing
		{"empty string", "", true, "URL is required"},

		// Bad schemes
		{"file scheme", "file:///etc/passwd", true, "only http and https"},
		{"ftp scheme", "ftp://example.com/file", true, "only http and https"},
		{"javascript scheme", "javascript:alert(1)", true, "only http and https"},
		{"no scheme", "example.com/path", true, "only http and https"},

		// Localhost
		{"localhost", "http://localhost", true, "localhost"},
		{"localhost with port", "http://localhost:8080/secret", true, "localhost"},
		{"localhost https", "https://localhost/admin", true, "localhost"},

		// Loopback IPv4
		{"127.0.0.1", "http://127.0.0.1", true, "private/reserved IP"},
		{"127.0.0.1 with port", "http://127.0.0.1:3000/api", true, "private/reserved IP"},

		// Private IPv4 ranges
		{"10.x", "http://10.0.0.1/internal", true, "private/reserved IP"},
		{"10.x high", "http://10.255.255.255", true, "private/reserved IP"},
		{"172.16.x", "http://172.16.0.1/service", true, "private/reserved IP"},
		{"172.31.x", "http://172.31.255.255", true, "private/reserved IP"},
		{"192.168.x", "http://192.168.1.1/admin", true, "private/reserved IP"},
		{"192.168.0.x", "http://192.168.0.100:9090", true, "private/reserved IP"},

		// Cloud metadata
		{"aws metadata", "http://169.254.169.254/latest/meta-data/", true, "private/reserved IP"},
		{"aws metadata with path", "http://169.254.169.254/latest/api/token", true, "private/reserved IP"},
		{"google metadata hostname", "http://metadata.google.internal/computeMetadata/v1/", true, "cloud metadata"},
		{"google metadata alt", "http://metadata.goog/computeMetadata/v1/", true, "cloud metadata"},

		// Zero address
		{"0.0.0.0", "http://0.0.0.0", true, "private/reserved IP"},

		// IPv6 loopback
		{"ipv6 loopback", "http://[::1]/path", true, "private/reserved IP"},

		// IPv6 link-local
		{"ipv6 link-local", "http://[fe80::1]/path", true, "private/reserved IP"},

		// IPv6 unique local
		{"ipv6 unique local", "http://[fc00::1]/path", true, "private/reserved IP"},

		// Unresolvable hostname
		{"unresolvable", "http://this-domain-does-not-exist-xyz123.invalid/path", true, "cannot resolve hostname"},

		// Shared address space
		{"100.64.x", "http://100.64.0.1/path", true, "private/reserved IP"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTaskURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateTaskURL(%q) = nil, want error containing %q", tt.url, tt.errMsg)
				} else if tt.errMsg != "" && !containsSubstring(err.Error(), tt.errMsg) {
					t.Errorf("ValidateTaskURL(%q) error = %q, want error containing %q", tt.url, err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateTaskURL(%q) = %v, want nil", tt.url, err)
				}
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip      string
		private bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"192.168.255.255", true},
		{"169.254.169.254", true},
		{"0.0.0.0", true},
		{"100.64.0.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.215.14", false}, // example.com
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"2607:f8b0:4004:800::200e", false}, // google.com
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			got := isPrivateIP(ip)
			if got != tt.private {
				t.Errorf("isPrivateIP(%q) = %v, want %v", tt.ip, got, tt.private)
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
