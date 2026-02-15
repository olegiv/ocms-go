// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import (
	"net"
	"strings"
	"testing"
)

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		private bool
	}{
		// Private IPv4 ranges
		{"loopback", "127.0.0.1", true},
		{"loopback high", "127.255.255.255", true},
		{"10.x.x.x", "10.0.0.1", true},
		{"10.x.x.x high", "10.255.255.255", true},
		{"172.16.x.x", "172.16.0.1", true},
		{"172.31.x.x", "172.31.255.255", true},
		{"192.168.x.x", "192.168.1.1", true},
		{"link-local", "169.254.1.1", true},
		{"this network", "0.0.0.0", true},
		{"CGNAT", "100.64.0.1", true},
		{"documentation 192", "192.0.2.1", true},
		{"documentation 198", "198.51.100.1", true},
		{"documentation 203", "203.0.113.1", true},
		{"benchmarking", "198.18.0.1", true},
		{"multicast", "224.0.0.1", true},
		{"reserved", "240.0.0.1", true},

		// Public IPv4
		{"public cloudflare", "1.1.1.1", false},
		{"public google", "8.8.8.8", false},
		{"public random", "203.0.114.1", false},
		{"172.15.x.x public", "172.15.255.255", false},
		{"172.32.x.x public", "172.32.0.1", false},

		// IPv6
		{"ipv6 loopback", "::1", true},
		{"ipv6 link-local", "fe80::1", true},
		{"ipv6 unique-local", "fd00::1", true},
		{"ipv6 public", "2001:db8::1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			got := IsPrivateIP(ip)
			if got != tt.private {
				t.Errorf("IsPrivateIP(%s) = %v, want %v", tt.ip, got, tt.private)
			}
		})
	}
}

func TestIsPrivateIP_Nil(t *testing.T) {
	if !IsPrivateIP(nil) {
		t.Error("IsPrivateIP(nil) should return true (deny by default)")
	}
}

func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string // substring of expected error message
	}{
		// Valid URLs
		{
			name:    "valid https",
			url:     "https://example.com/webhook",
			wantErr: false,
		},
		{
			name:    "valid http",
			url:     "http://example.com/webhook",
			wantErr: false,
		},
		{
			name:    "valid with port",
			url:     "https://example.com:8443/webhook",
			wantErr: false,
		},
		{
			name:    "valid with path and query",
			url:     "https://example.com/api/v1/webhook?token=abc",
			wantErr: false,
		},

		// Invalid schemes
		{
			name:    "ftp scheme",
			url:     "ftp://example.com/file",
			wantErr: true,
			errMsg:  "http or https",
		},
		{
			name:    "javascript scheme",
			url:     "javascript:alert(1)",
			wantErr: true,
			errMsg:  "http or https",
		},
		{
			name:    "file scheme",
			url:     "file:///etc/passwd",
			wantErr: true,
			errMsg:  "http or https",
		},

		// Private IPs
		{
			name:    "localhost IP",
			url:     "http://127.0.0.1/webhook",
			wantErr: true,
			errMsg:  "private",
		},
		{
			name:    "10.x.x.x",
			url:     "http://10.0.0.1/webhook",
			wantErr: true,
			errMsg:  "private",
		},
		{
			name:    "172.16.x.x",
			url:     "http://172.16.0.1/webhook",
			wantErr: true,
			errMsg:  "private",
		},
		{
			name:    "192.168.x.x",
			url:     "https://192.168.1.100:8080/webhook",
			wantErr: true,
			errMsg:  "private",
		},
		{
			name:    "ipv6 loopback",
			url:     "http://[::1]/webhook",
			wantErr: true,
			errMsg:  "private",
		},
		{
			name:    "link-local",
			url:     "http://169.254.169.254/latest/meta-data/",
			wantErr: true,
			errMsg:  "private",
		},
		{
			name:    "zero IP",
			url:     "http://0.0.0.0/webhook",
			wantErr: true,
			errMsg:  "private",
		},

		// Localhost hostname
		{
			name:    "localhost hostname",
			url:     "http://localhost/webhook",
			wantErr: true,
			errMsg:  "localhost",
		},
		{
			name:    "localhost with port",
			url:     "http://localhost:8080/webhook",
			wantErr: true,
			errMsg:  "localhost",
		},
		{
			name:    "subdomain localhost",
			url:     "http://evil.localhost/webhook",
			wantErr: true,
			errMsg:  "localhost",
		},

		// Too long
		{
			name:    "URL too long",
			url:     "https://example.com/" + strings.Repeat("a", MaxWebhookURLLength),
			wantErr: true,
			errMsg:  "maximum length",
		},

		// Missing hostname
		{
			name:    "missing hostname",
			url:     "http:///path",
			wantErr: true,
			errMsg:  "hostname",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWebhookURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateWebhookURL(%q) = nil, want error containing %q", tt.url, tt.errMsg)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateWebhookURL(%q) error = %q, want error containing %q", tt.url, err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateWebhookURL(%q) = %v, want nil", tt.url, err)
				}
			}
		})
	}
}

func TestSSRFSafeDialContext(t *testing.T) {
	dialer := &net.Dialer{}
	dialFn := SSRFSafeDialContext(dialer)

	// Attempting to connect to a private IP should be blocked
	t.Run("blocks loopback", func(t *testing.T) {
		_, err := dialFn(t.Context(), "tcp", "127.0.0.1:80")
		if err == nil {
			t.Error("expected error connecting to 127.0.0.1, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "private IP") {
			t.Errorf("expected 'private IP' error, got: %v", err)
		}
	})

	t.Run("blocks private range", func(t *testing.T) {
		_, err := dialFn(t.Context(), "tcp", "10.0.0.1:80")
		if err == nil {
			t.Error("expected error connecting to 10.0.0.1, got nil")
		}
	})
}
