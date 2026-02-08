// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package informer

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// Settings holds the informer bar configuration.
type Settings struct {
	Enabled   bool
	Text      string
	BgColor   string
	TextColor string
	Version   string
}

// loadSettings loads informer settings from the database.
func loadSettings(db *sql.DB) (*Settings, error) {
	row := db.QueryRow(`
		SELECT enabled, text, bg_color, text_color, version
		FROM informer_settings WHERE id = 1
	`)

	s := &Settings{}
	var enabled, version int
	err := row.Scan(&enabled, &s.Text, &s.BgColor, &s.TextColor, &version)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return defaultSettings(), nil
		}
		return nil, fmt.Errorf("scanning informer settings: %w", err)
	}

	s.Enabled = enabled == 1
	s.Version = strconv.Itoa(version)
	return s, nil
}

// saveSettings saves informer settings to the database.
func saveSettings(db *sql.DB, s *Settings) error {
	enabled := 0
	if s.Enabled {
		enabled = 1
	}

	_, err := db.Exec(`
		UPDATE informer_settings SET
			enabled = ?,
			text = ?,
			bg_color = ?,
			text_color = ?,
			version = version + 1,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, enabled, s.Text, s.BgColor, s.TextColor)
	return err
}

// defaultSettings returns default informer settings.
func defaultSettings() *Settings {
	return &Settings{
		BgColor:   "#1e40af",
		TextColor: "#ffffff",
		Version:   "0",
	}
}
