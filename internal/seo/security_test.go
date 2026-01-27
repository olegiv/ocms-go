// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package seo

import (
	"strings"
	"testing"
	"time"
)

func TestSecurityTxtBuilder_Build(t *testing.T) {
	fixedTime := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		config   SecurityTxtConfig
		contains []string
		excludes []string
	}{
		{
			name: "minimal config with contact",
			config: SecurityTxtConfig{
				Contact: []string{"mailto:security@example.com"},
				Expires: fixedTime,
			},
			contains: []string{
				"Contact: mailto:security@example.com",
				"Expires: 2027-01-01T00:00:00Z",
			},
			excludes: []string{
				"Encryption:",
				"Acknowledgments:",
				"Policy:",
			},
		},
		{
			name: "multiple contacts",
			config: SecurityTxtConfig{
				Contact: []string{
					"mailto:security@example.com",
					"https://example.com/security",
				},
				Expires: fixedTime,
			},
			contains: []string{
				"Contact: mailto:security@example.com",
				"Contact: https://example.com/security",
			},
		},
		{
			name: "full config",
			config: SecurityTxtConfig{
				Contact:            []string{"mailto:security@example.com"},
				Expires:            fixedTime,
				Encryption:         "https://example.com/pgp-key.txt",
				Acknowledgments:    "https://example.com/hall-of-fame",
				PreferredLanguages: "en, es",
				Canonical:          "https://example.com/.well-known/security.txt",
				Policy:             "https://example.com/security-policy",
				Hiring:             "https://example.com/jobs/security",
			},
			contains: []string{
				"Contact: mailto:security@example.com",
				"Expires: 2027-01-01T00:00:00Z",
				"Encryption: https://example.com/pgp-key.txt",
				"Acknowledgments: https://example.com/hall-of-fame",
				"Preferred-Languages: en, es",
				"Canonical: https://example.com/.well-known/security.txt",
				"Policy: https://example.com/security-policy",
				"Hiring: https://example.com/jobs/security",
			},
		},
		{
			name: "empty contact is skipped",
			config: SecurityTxtConfig{
				Contact: []string{"", "mailto:security@example.com", ""},
				Expires: fixedTime,
			},
			contains: []string{
				"Contact: mailto:security@example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := NewSecurityTxtBuilder(tt.config)
			result := builder.Build()

			for _, want := range tt.contains {
				if !strings.Contains(result, want) {
					t.Errorf("Build() missing %q in:\n%s", want, result)
				}
			}

			for _, exclude := range tt.excludes {
				if strings.Contains(result, exclude) {
					t.Errorf("Build() should not contain %q in:\n%s", exclude, result)
				}
			}
		})
	}
}

func TestSecurityTxtBuilder_DefaultExpires(t *testing.T) {
	builder := NewSecurityTxtBuilder(SecurityTxtConfig{
		Contact: []string{"mailto:security@example.com"},
		// Expires not set - should default to 1 year from now
	})
	result := builder.Build()

	if !strings.Contains(result, "Expires:") {
		t.Error("Build() should include Expires even when not set")
	}

	// Verify it's approximately 1 year from now
	if !strings.Contains(result, "Contact: mailto:security@example.com") {
		t.Error("Build() should include Contact")
	}
}

func TestGenerateSecurityTxt(t *testing.T) {
	expires := time.Date(2027, 6, 15, 12, 0, 0, 0, time.UTC)
	result := GenerateSecurityTxt("mailto:security@example.com", expires)

	expected := []string{
		"Contact: mailto:security@example.com",
		"Expires: 2027-06-15T12:00:00Z",
	}

	for _, want := range expected {
		if !strings.Contains(result, want) {
			t.Errorf("GenerateSecurityTxt() missing %q in:\n%s", want, result)
		}
	}
}
