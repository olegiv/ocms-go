// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sentinel

import (
	"testing"
)

func TestMatchIPPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		ip      string
		want    bool
	}{
		// Exact matches
		{"exact match IPv4", "192.168.1.1", "192.168.1.1", true},
		{"exact match different", "192.168.1.1", "192.168.1.2", false},
		{"exact match different subnet", "192.168.1.1", "192.168.2.1", false},

		// Wildcard patterns
		{"wildcard last octet", "192.168.1.*", "192.168.1.100", true},
		{"wildcard last octet different subnet", "192.168.1.*", "192.168.2.100", false},
		{"wildcard two octets", "192.168.*", "192.168.5.10", true},
		{"wildcard three octets", "10.*", "10.0.0.1", true},
		{"wildcard partial octet", "192.168.1.10*", "192.168.1.100", true},
		{"wildcard partial octet 2", "192.168.1.10*", "192.168.1.101", true},
		{"wildcard partial octet no match", "192.168.1.10*", "192.168.1.200", false},

		// Multi-wildcard patterns (full 4-octet)
		{"two wildcards", "192.168.*.*", "192.168.1.100", true},
		{"two wildcards different subnet", "192.168.*.*", "10.0.0.1", false},
		{"middle wildcard", "192.*.1.1", "192.168.1.1", true},
		{"middle wildcard no match", "192.*.1.1", "192.168.2.1", false},
		{"wildcard at specific position", "10.*.*.1", "10.0.0.1", true},
		{"wildcard at specific position no match", "10.*.*.1", "10.0.0.2", false},
		{"all wildcards except first", "192.*.*.*", "192.0.0.1", true},
		{"all wildcards except first no match", "192.*.*.*", "10.0.0.1", false},

		// Edge cases
		{"empty pattern", "", "192.168.1.1", false},
		{"empty ip", "192.168.1.1", "", false},
		{"wildcard only", "*", "192.168.1.1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchIPPattern(tt.pattern, tt.ip)
			if got != tt.want {
				t.Errorf("matchIPPattern(%q, %q) = %v, want %v", tt.pattern, tt.ip, got, tt.want)
			}
		})
	}
}

func TestIsValidIPPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    bool
	}{
		// Valid patterns
		{"valid IPv4", "192.168.1.1", true},
		{"valid wildcard", "192.168.1.*", true},
		{"valid partial wildcard", "192.168.1.10*", true},
		{"valid IPv6", "2001:db8::1", true},
		{"valid two octet wildcard", "192.168.*", true},

		// Invalid patterns
		{"empty", "", false},
		{"single char", "1", false},
		{"global wildcard", "*", false},
		{"all wildcards", "*.*.*.*", false},
		{"valid hex chars in IPv4 context", "192.168.1.abc", true}, // a-f are valid hex for IPv6
		{"invalid chars spaces", "192.168 .1.1", false},
		{"invalid chars special", "192.168.1.1;DROP TABLE", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidIPPattern(tt.pattern)
			if got != tt.want {
				t.Errorf("isValidIPPattern(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestIsValidIPChar(t *testing.T) {
	tests := []struct {
		name string
		char rune
		want bool
	}{
		{"digit 0", '0', true},
		{"digit 9", '9', true},
		{"dot", '.', true},
		{"colon", ':', true},
		{"asterisk", '*', true},
		{"lowercase a", 'a', true},
		{"lowercase f", 'f', true},
		{"uppercase A", 'A', true},
		{"uppercase F", 'F', true},
		{"letter g", 'g', false},
		{"space", ' ', false},
		{"semicolon", ';', false},
		{"slash", '/', false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidIPChar(tt.char)
			if got != tt.want {
				t.Errorf("isValidIPChar(%q) = %v, want %v", tt.char, got, tt.want)
			}
		})
	}
}

func TestMatchPathPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		// Exact match
		{"exact match", "/wp-admin", "/wp-admin", true},
		{"exact match no match", "/wp-admin", "/wp-login", false},
		{"exact match with trailing", "/wp-admin", "/wp-admin/", false},

		// Starts with (pattern ends with *)
		{"starts with match", "/wp-admin*", "/wp-admin/index.php", true},
		{"starts with match 2", "/api/*", "/api/v1/users", true},
		{"starts with no match", "/wp-admin*", "/admin/wp-admin", false},

		// Ends with (pattern starts with *)
		{"ends with match", "*/.env", "/path/to/.env", true},
		{"ends with match 2", "*/config.php", "/var/www/config.php", true},
		{"ends with no match", "*/.env", "/.env.local", false},

		// Contains (pattern starts and ends with *)
		{"contains match", "*/phpMyAdmin*", "/tools/phpMyAdmin/index.php", true},
		{"contains match 2", "*/admin*", "/path/admin/users", true},
		{"contains match exact", "*/wp-login*", "/wp-login.php", true},
		{"contains no match", "*/phpMyAdmin*", "/phpmyadmin/", false},

		// Edge cases
		{"empty pattern", "", "/some/path", false},
		{"empty path", "/admin", "", false},
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchPathPattern(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("matchPathPattern(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestIsValidPathPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    bool
	}{
		// Valid patterns
		{"exact path", "/wp-admin", true},
		{"path with wildcard suffix", "/wp-admin*", true},
		{"path with wildcard prefix", "*/wp-admin", true},
		{"path with both wildcards", "*/wp-admin*", true},
		{"path with subdirectory", "/api/v1/users", true},
		{"path with extension", "/.env", true},
		{"path with hyphen", "/wp-login.php", true},

		// Invalid patterns
		{"empty", "", false},
		{"just wildcard", "*", false},
		{"no leading slash", "wp-admin", false},
		{"invalid chars", "/wp-admin<script>", false},
		{"spaces", "/wp admin", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidPathPattern(tt.pattern)
			if got != tt.want {
				t.Errorf("isValidPathPattern(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}
}
