// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"net"
	"testing"
)

func setRequireHTTPSOutboundForTest(t *testing.T, required bool) {
	t.Helper()
	SetRequireHTTPSOutbound(required)
	t.Cleanup(func() {
		SetRequireHTTPSOutbound(false)
	})
}

func TestValidateTaskURL(t *testing.T) {
	SetRequireHTTPSOutbound(false)

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

func TestValidateTaskURL_RequireHTTPSOutbound(t *testing.T) {
	setRequireHTTPSOutboundForTest(t, true)

	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		{"https allowed", "https://example.com/health", false, ""},
		{"http rejected", "http://example.com/health", true, "only https URLs are allowed"},
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
				return
			}
			if err != nil {
				t.Errorf("ValidateTaskURL(%q) = %v, want nil", tt.url, err)
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

func TestIsPrivateIP_CIDRBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		private bool
	}{
		// 172.16.0.0/12: 172.16.0.0 – 172.31.255.255
		{"172.16.0.0 start", "172.16.0.0", true},
		{"172.31.255.255 end", "172.31.255.255", true},
		{"172.15.255.255 just before", "172.15.255.255", false},
		{"172.32.0.0 just after", "172.32.0.0", false},

		// 10.0.0.0/8
		{"10.0.0.0 start", "10.0.0.0", true},
		{"10.255.255.255 end", "10.255.255.255", true},
		{"9.255.255.255 just before", "9.255.255.255", false},
		{"11.0.0.0 just after", "11.0.0.0", false},

		// 192.168.0.0/16
		{"192.168.0.0 start", "192.168.0.0", true},
		{"192.168.255.255 end", "192.168.255.255", true},
		{"192.167.255.255 just before", "192.167.255.255", false},
		{"192.169.0.0 just after", "192.169.0.0", false},

		// 100.64.0.0/10 shared address space
		{"100.64.0.0 start", "100.64.0.0", true},
		{"100.127.255.255 end", "100.127.255.255", true},
		{"100.63.255.255 just before", "100.63.255.255", false},
		{"100.128.0.0 just after", "100.128.0.0", false},

		// 169.254.0.0/16 link-local (cloud metadata)
		{"169.254.0.0 start", "169.254.0.0", true},
		{"169.254.255.255 end", "169.254.255.255", true},
		{"169.253.255.255 just before", "169.253.255.255", false},
		{"169.255.0.0 just after", "169.255.0.0", false},

		// Cloud metadata IPs
		{"AWS metadata", "169.254.169.254", true},
		{"GCP metadata (alternative)", "169.254.169.253", true},

		// IPv6 loopback
		{"::1 loopback", "::1", true},
		// IPv6 link-local fe80::/10: covers fe80:: through febf::
		{"fe80:: start", "fe80::", true},
		{"febf:ffff:ffff:ffff:ffff:ffff:ffff:ffff end", "febf:ffff:ffff:ffff:ffff:ffff:ffff:ffff", true},
		{"fec0:: just after link-local not in fe80::/10 or fc00::/7", "fec0::", false},
		// IPv6 unique local fc00::/7 covers fc00:: through fdff::
		{"fc00:: start", "fc00::", true},
		{"fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff end", "fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", true},

		// Public IPv6
		{"2001:db8::1 documentation", "2001:db8::1", false},
		{"2606:4700::1111 cloudflare", "2606:4700::1111", false},

		// Documentation ranges (reserved)
		{"192.0.2.1 TEST-NET-1", "192.0.2.1", true},
		{"198.51.100.1 TEST-NET-2", "198.51.100.1", true},
		{"203.0.113.1 TEST-NET-3", "203.0.113.1", true},
		{"198.18.0.1 benchmarking", "198.18.0.1", true},
		{"198.19.255.255 benchmarking end", "198.19.255.255", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

func TestValidateTaskURL_EdgeCases(t *testing.T) {
	SetRequireHTTPSOutbound(false)

	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		// Various rejected schemes
		{"ssh scheme", "ssh://example.com/path", true, "only http and https"},
		{"data scheme", "data:text/plain,hello", true, "only http and https"},
		{"ws scheme", "ws://example.com/socket", true, "only http and https"},
		{"wss scheme", "wss://example.com/socket", true, "only http and https"},
		{"mailto scheme", "mailto:user@example.com", true, "only http and https"},

		// Hostname with explicit port
		{"http with port 80", "http://example.com:80/path", false, ""},
		{"https with port 443", "https://example.com:443/path", false, ""},
		{"https with non-standard port", "https://example.com:9443/api", false, ""},

		// URL with query string and fragment
		{"url with query", "https://example.com/path?key=value&other=123", false, ""},
		{"url with fragment", "https://example.com/page#section", false, ""},

		// Empty hostname (scheme only)
		{"scheme only no host", "https:///path", true, "hostname"},

		// Blocked hostnames (case-insensitive)
		{"google metadata uppercase", "http://METADATA.GOOGLE.INTERNAL/", true, "cloud metadata"},
		{"google metadata mixed case", "http://Metadata.Google.Internal/", true, "cloud metadata"},
		{"metadata.goog uppercase", "http://METADATA.GOOG/", true, "cloud metadata"},

		// localhost case-insensitive
		{"LOCALHOST uppercase", "http://LOCALHOST/path", true, "localhost"},
		{"Localhost mixed case", "http://Localhost:8080/", true, "localhost"},

		// IPv6 direct addresses in URL
		{"ipv6 fc00 unique local", "http://[fc00::1]/path", true, "private/reserved IP"},
		{"ipv6 fd00 unique local 2", "http://[fd00::1]/path", true, "private/reserved IP"},
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

func TestSetRequireHTTPSOutbound(t *testing.T) {
	// Ensure clean state
	SetRequireHTTPSOutbound(false)
	if isHTTPSOutboundRequired() {
		t.Error("expected HTTPS requirement to be false initially")
	}

	SetRequireHTTPSOutbound(true)
	if !isHTTPSOutboundRequired() {
		t.Error("expected HTTPS requirement to be true after setting")
	}

	SetRequireHTTPSOutbound(false)
	if isHTTPSOutboundRequired() {
		t.Error("expected HTTPS requirement to be false after reset")
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
