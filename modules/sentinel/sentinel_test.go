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
		{"invalid chars", "192.168.1.abc", false},
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
