// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package seo

import (
	"strings"
	"time"
)

// SecurityTxtConfig holds configuration for security.txt generation (RFC 9116).
type SecurityTxtConfig struct {
	// Contact is required. Email, URL, or phone for reporting vulnerabilities.
	// Multiple contacts can be provided (e.g., "mailto:security@example.com").
	Contact []string

	// Expires is required. The date after which this file should be considered stale.
	// If not set, defaults to 1 year from now.
	Expires time.Time

	// Encryption is optional. Link to a PGP key for encrypted communication.
	Encryption string

	// Acknowledgments is optional. Link to a page acknowledging security researchers.
	Acknowledgments string

	// PreferredLanguages is optional. Languages spoken by the security team (e.g., "en, es").
	PreferredLanguages string

	// Canonical is optional. The canonical URL for this security.txt file.
	Canonical string

	// Policy is optional. Link to the security policy.
	Policy string

	// Hiring is optional. Link to security-related job positions.
	Hiring string
}

// SecurityTxtBuilder builds security.txt content according to RFC 9116.
type SecurityTxtBuilder struct {
	config SecurityTxtConfig
}

// NewSecurityTxtBuilder creates a new security.txt builder.
func NewSecurityTxtBuilder(config SecurityTxtConfig) *SecurityTxtBuilder {
	return &SecurityTxtBuilder{config: config}
}

// Build generates the security.txt content.
func (b *SecurityTxtBuilder) Build() string {
	var sb strings.Builder

	// Contact (required, can have multiple)
	for _, contact := range b.config.Contact {
		if contact != "" {
			sb.WriteString("Contact: ")
			sb.WriteString(contact)
			sb.WriteString("\n")
		}
	}

	// Expires (required)
	expires := b.config.Expires
	if expires.IsZero() {
		// Default to 1 year from now
		expires = time.Now().AddDate(1, 0, 0)
	}
	sb.WriteString("Expires: ")
	sb.WriteString(expires.Format(time.RFC3339))
	sb.WriteString("\n")

	// Optional fields
	if b.config.Encryption != "" {
		sb.WriteString("Encryption: ")
		sb.WriteString(b.config.Encryption)
		sb.WriteString("\n")
	}

	if b.config.Acknowledgments != "" {
		sb.WriteString("Acknowledgments: ")
		sb.WriteString(b.config.Acknowledgments)
		sb.WriteString("\n")
	}

	if b.config.PreferredLanguages != "" {
		sb.WriteString("Preferred-Languages: ")
		sb.WriteString(b.config.PreferredLanguages)
		sb.WriteString("\n")
	}

	if b.config.Canonical != "" {
		sb.WriteString("Canonical: ")
		sb.WriteString(b.config.Canonical)
		sb.WriteString("\n")
	}

	if b.config.Policy != "" {
		sb.WriteString("Policy: ")
		sb.WriteString(b.config.Policy)
		sb.WriteString("\n")
	}

	if b.config.Hiring != "" {
		sb.WriteString("Hiring: ")
		sb.WriteString(b.config.Hiring)
		sb.WriteString("\n")
	}

	return sb.String()
}

// GenerateSecurityTxt is a convenience function to generate security.txt content.
func GenerateSecurityTxt(contact string, expires time.Time) string {
	builder := NewSecurityTxtBuilder(SecurityTxtConfig{
		Contact: []string{contact},
		Expires: expires,
	})
	return builder.Build()
}
