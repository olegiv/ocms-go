// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"strings"
	"testing"
	"time"
)

func TestAnonymizeIP_IPv4(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard IPv4",
			input:    "192.168.1.100",
			expected: "192.168.1.0",
		},
		{
			name:     "another IPv4",
			input:    "10.0.0.255",
			expected: "10.0.0.0",
		},
		{
			name:     "public IP",
			input:    "8.8.8.8",
			expected: "8.8.8.0",
		},
		{
			name:     "localhost",
			input:    "127.0.0.1",
			expected: "127.0.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := anonymizeIP(tt.input)
			if result != tt.expected {
				t.Errorf("anonymizeIP(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestAnonymizeIP_IPv6(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "full IPv6",
			input: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
		},
		{
			name:  "compressed IPv6",
			input: "2001:db8::1",
		},
		{
			name:  "localhost IPv6",
			input: "::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := anonymizeIP(tt.input)
			// IPv6 should have last 80 bits zeroed
			if result == "" {
				t.Errorf("anonymizeIP(%q) returned empty string", tt.input)
			}
			// The result should be different from input (anonymized)
			if result == tt.input {
				t.Errorf("anonymizeIP(%q) should anonymize the IP", tt.input)
			}
		})
	}
}

func TestAnonymizeIP_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty string", input: ""},
		{name: "invalid format", input: "not-an-ip"},
		{name: "partial IP", input: "192.168"},
		{name: "out of range", input: "999.999.999.999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := anonymizeIP(tt.input)
			if result != "" {
				t.Errorf("anonymizeIP(%q) = %q, want empty string", tt.input, result)
			}
		})
	}
}

func TestGenerateRandomSalt(t *testing.T) {
	salt1 := generateRandomSalt()
	salt2 := generateRandomSalt()

	// Salts should be non-empty
	if salt1 == "" {
		t.Error("generateRandomSalt() returned empty string")
	}

	// Salts should be different
	if salt1 == salt2 {
		t.Error("generateRandomSalt() should return unique values")
	}

	// Salt should be hex encoded (64 chars for 32 bytes)
	if len(salt1) != 64 {
		t.Errorf("generateRandomSalt() returned salt of length %d, want 64", len(salt1))
	}
}

func TestCreateVisitorHash(t *testing.T) {
	// Create a minimal module for testing
	m := &Module{
		settings: &Settings{
			CurrentSalt:       "test-salt-12345",
			SaltCreatedAt:     time.Now(),
			SaltRotationHours: 24,
		},
	}

	ip := "192.168.1.100"
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"

	hash1 := m.CreateVisitorHash(ip, ua)
	hash2 := m.CreateVisitorHash(ip, ua)

	// Same inputs should produce same hash
	if hash1 != hash2 {
		t.Errorf("CreateVisitorHash with same inputs produced different hashes: %q vs %q", hash1, hash2)
	}

	// Hash should be 16 chars (truncated)
	if len(hash1) != 16 {
		t.Errorf("CreateVisitorHash returned hash of length %d, want 16", len(hash1))
	}

	// Different UA should produce different hash
	hash3 := m.CreateVisitorHash(ip, "Different User Agent")
	if hash1 == hash3 {
		t.Error("CreateVisitorHash should produce different hashes for different user agents")
	}

	// Different IP should produce different hash
	hash4 := m.CreateVisitorHash("10.0.0.1", ua)
	if hash1 == hash4 {
		t.Error("CreateVisitorHash should produce different hashes for different IPs")
	}
}

func TestCreateSessionHash(t *testing.T) {
	m := &Module{
		settings: &Settings{
			CurrentSalt:       "test-salt-12345",
			SaltCreatedAt:     time.Now(),
			SaltRotationHours: 24,
		},
	}

	ip := "192.168.1.100"
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"

	sessionHash := m.CreateSessionHash(ip, ua)
	visitorHash := m.CreateVisitorHash(ip, ua)

	// Session hash and visitor hash should be different
	if sessionHash == visitorHash {
		t.Error("CreateSessionHash and CreateVisitorHash should produce different hashes")
	}

	// Hash should be 16 chars
	if len(sessionHash) != 16 {
		t.Errorf("CreateSessionHash returned hash of length %d, want 16", len(sessionHash))
	}
}

func TestHashConsistency(t *testing.T) {
	m := &Module{
		settings: &Settings{
			CurrentSalt:       "consistent-salt",
			SaltCreatedAt:     time.Now(),
			SaltRotationHours: 24,
		},
	}

	// Same IP after anonymization should produce same hash
	ip1 := "192.168.1.100"
	ip2 := "192.168.1.200" // Different last octet, but same after anonymization
	ua := "Test Agent"

	// Both IPs anonymize to 192.168.1.0
	hash1 := m.CreateVisitorHash(ip1, ua)
	hash2 := m.CreateVisitorHash(ip2, ua)

	// Hashes should be the same because IPs anonymize to the same value
	if hash1 != hash2 {
		t.Errorf("IPs in same /24 subnet should produce same visitor hash: %q vs %q", hash1, hash2)
	}
}

func TestHashHexFormat(t *testing.T) {
	m := &Module{
		settings: &Settings{
			CurrentSalt:       "test-salt",
			SaltCreatedAt:     time.Now(),
			SaltRotationHours: 24,
		},
	}

	hash := m.CreateVisitorHash("1.2.3.4", "test")

	// Hash should only contain hex characters
	for _, c := range hash {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("CreateVisitorHash returned non-hex character: %c in %q", c, hash)
		}
	}
}
