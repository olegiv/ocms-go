// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package util provides general-purpose utility functions including
// URL slug generation and validation with Unicode normalization support.
package util

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var (
	// slugRegex matches non-alphanumeric characters (except hyphens)
	slugRegex = regexp.MustCompile(`[^a-z0-9-]+`)
	// multipleHyphens matches multiple consecutive hyphens
	multipleHyphens = regexp.MustCompile(`-{2,}`)
)

// Slugify converts a string to a URL-friendly slug.
// It converts to lowercase, removes accents, replaces spaces with hyphens,
// and removes all non-alphanumeric characters except hyphens.
func Slugify(s string) string {
	// Normalize unicode characters (decompose accents)
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, s)

	// Convert to lowercase
	result = strings.ToLower(result)

	// Replace spaces with hyphens
	result = strings.ReplaceAll(result, " ", "-")

	// Remove all non-alphanumeric characters except hyphens
	result = slugRegex.ReplaceAllString(result, "")

	// Replace multiple hyphens with single hyphen
	result = multipleHyphens.ReplaceAllString(result, "-")

	// Trim hyphens from start and end
	result = strings.Trim(result, "-")

	return result
}

// IsValidSlug checks if a string is a valid slug format.
func IsValidSlug(s string) bool {
	if s == "" {
		return false
	}

	// Check if it only contains lowercase letters, numbers, and hyphens
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}

	// Check that it doesn't start or end with a hyphen
	if s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}

	// Check for consecutive hyphens
	if strings.Contains(s, "--") {
		return false
	}

	return true
}
