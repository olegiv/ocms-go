// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package hcaptcha

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"strings"
)

// hCaptcha test/debug keys for development.
// These always pass verification without showing a challenge.
// See: https://docs.hcaptcha.com/#integration-testing-test-keys
const (
	TestSiteKey   = "10000000-ffff-ffff-ffff-000000000001"
	TestSecretKey = "0x0000000000000000000000000000000000000000"
)

// Settings holds the hCaptcha configuration.
type Settings struct {
	Enabled   bool
	SiteKey   string // Public site key
	SecretKey string // Secret key for verification
	Theme     string // "light" or "dark"
	Size      string // "normal" or "compact"
}

// loadSettings loads hCaptcha settings from the database.
func loadSettings(db *sql.DB) (*Settings, error) {
	row := db.QueryRow(`
		SELECT enabled, site_key, secret_key, theme, size
		FROM hcaptcha_settings WHERE id = 1
	`)

	s := &Settings{}
	var enabled int
	err := row.Scan(&enabled, &s.SiteKey, &s.SecretKey, &s.Theme, &s.Size)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Settings{
				SiteKey:   TestSiteKey,
				SecretKey: TestSecretKey,
				Theme:     "light",
				Size:      "normal",
			}, nil
		}
		return nil, fmt.Errorf("scanning hCaptcha settings: %w", err)
	}

	// Use test keys as defaults if no keys are configured
	if s.SiteKey == "" {
		s.SiteKey = TestSiteKey
	}
	if s.SecretKey == "" {
		s.SecretKey = TestSecretKey
	}

	s.Enabled = enabled == 1
	return s, nil
}

// saveSettings saves hCaptcha settings to the database.
func saveSettings(db *sql.DB, s *Settings) error {
	enabled := 0
	if s.Enabled {
		enabled = 1
	}

	_, err := db.Exec(`
		UPDATE hcaptcha_settings SET
			enabled = ?,
			site_key = ?,
			secret_key = ?,
			theme = ?,
			size = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, enabled, s.SiteKey, s.SecretKey, s.Theme, s.Size)
	return err
}

// RenderWidget returns the hCaptcha widget HTML.
func (m *Module) RenderWidget() template.HTML {
	if !m.IsEnabled() {
		return ""
	}

	var html strings.Builder

	// Add hCaptcha script
	html.WriteString(`<script src="https://js.hcaptcha.com/1/api.js" async defer></script>`)

	// Add hCaptcha widget div
	html.WriteString(fmt.Sprintf(
		`<div class="h-captcha" data-sitekey="%s" data-theme="%s" data-size="%s"></div>`,
		template.HTMLEscapeString(m.settings.SiteKey),
		template.HTMLEscapeString(m.settings.Theme),
		template.HTMLEscapeString(m.settings.Size),
	))

	return template.HTML(html.String())
}
