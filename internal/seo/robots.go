// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package seo

import (
	"strings"
)

// RobotsConfig holds configuration for robots.txt generation.
type RobotsConfig struct {
	SiteURL       string   // Base URL for sitemap reference
	DisallowAll   bool     // Block all crawlers (for staging sites)
	ExtraRules    string   // Additional custom rules
	DisallowPaths []string // Paths to disallow (e.g., /admin)
}

// RobotsBuilder builds robots.txt content.
type RobotsBuilder struct {
	config RobotsConfig
}

// NewRobotsBuilder creates a new robots.txt builder.
func NewRobotsBuilder(config RobotsConfig) *RobotsBuilder {
	return &RobotsBuilder{config: config}
}

// Build generates the robots.txt content.
func (b *RobotsBuilder) Build() string {
	var sb strings.Builder

	// User-agent directive (applies to all crawlers)
	sb.WriteString("User-agent: *\n")

	if b.config.DisallowAll {
		// Block all crawlers (for staging/development)
		sb.WriteString("Disallow: /\n")
	} else {
		// Default disallow paths
		defaultDisallow := []string{
			"/admin",
			"/login",
			"/logout",
			"/session",
		}

		// Combine default and custom disallow paths
		allPaths := defaultDisallow
		allPaths = append(allPaths, b.config.DisallowPaths...)

		for _, path := range allPaths {
			sb.WriteString("Disallow: ")
			sb.WriteString(path)
			sb.WriteString("\n")
		}

		// Allow everything else
		sb.WriteString("Allow: /\n")
	}

	// Add extra rules if provided
	if b.config.ExtraRules != "" {
		sb.WriteString("\n")
		sb.WriteString(b.config.ExtraRules)
		if !strings.HasSuffix(b.config.ExtraRules, "\n") {
			sb.WriteString("\n")
		}
	}

	// Add sitemap reference if site URL is provided
	if b.config.SiteURL != "" && !b.config.DisallowAll {
		sb.WriteString("\n")
		sb.WriteString("Sitemap: ")
		sb.WriteString(strings.TrimSuffix(b.config.SiteURL, "/"))
		sb.WriteString("/sitemap.xml\n")
	}

	return sb.String()
}

// GenerateRobots is a convenience function to generate robots.txt content.
func GenerateRobots(siteURL string, disallowAll bool, extraRules string) string {
	builder := NewRobotsBuilder(RobotsConfig{
		SiteURL:     siteURL,
		DisallowAll: disallowAll,
		ExtraRules:  extraRules,
	})
	return builder.Build()
}
