// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ProviderSettings represents the stored settings for a provider.
type ProviderSettings struct {
	ID         int64
	ProviderID string
	Settings   map[string]string
	IsEnabled  bool
	Position   int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// loadProviderSettings loads settings for a specific provider from the database.
func loadProviderSettings(db *sql.DB, providerID string) (*ProviderSettings, error) {
	row := db.QueryRow(`
		SELECT id, provider, settings, is_enabled, position, created_at, updated_at
		FROM embed_settings
		WHERE provider = ?
	`, providerID)

	ps := &ProviderSettings{
		ProviderID: providerID,
		Settings:   make(map[string]string),
	}

	var settingsJSON string
	var isEnabled int
	err := row.Scan(&ps.ID, &ps.ProviderID, &settingsJSON, &isEnabled, &ps.Position, &ps.CreatedAt, &ps.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Return empty settings for new provider
			return ps, nil
		}
		return nil, fmt.Errorf("scanning provider settings: %w", err)
	}

	ps.IsEnabled = isEnabled == 1

	if settingsJSON != "" {
		if err := json.Unmarshal([]byte(settingsJSON), &ps.Settings); err != nil {
			return nil, fmt.Errorf("unmarshaling settings JSON: %w", err)
		}
	}

	return ps, nil
}

// loadSettings loads provider settings with an optional enabled-only filter.
func loadSettings(db *sql.DB, enabledOnly bool) ([]*ProviderSettings, error) {
	query := `SELECT id, provider, settings, is_enabled, position, created_at, updated_at
		FROM embed_settings`
	if enabledOnly {
		query += ` WHERE is_enabled = 1`
	}
	query += ` ORDER BY position ASC`

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("querying settings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []*ProviderSettings
	for rows.Next() {
		ps := &ProviderSettings{
			Settings: make(map[string]string),
		}

		var settingsJSON string
		var isEnabled int
		if err := rows.Scan(&ps.ID, &ps.ProviderID, &settingsJSON, &isEnabled, &ps.Position, &ps.CreatedAt, &ps.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		ps.IsEnabled = isEnabled == 1

		if settingsJSON != "" {
			if err := json.Unmarshal([]byte(settingsJSON), &ps.Settings); err != nil {
				return nil, fmt.Errorf("unmarshaling settings JSON: %w", err)
			}
		}

		result = append(result, ps)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}

	return result, nil
}

// loadAllEnabledSettings loads all enabled provider settings.
func loadAllEnabledSettings(db *sql.DB) ([]*ProviderSettings, error) {
	return loadSettings(db, true)
}

// loadAllSettings loads settings for all providers.
func loadAllSettings(db *sql.DB) ([]*ProviderSettings, error) {
	return loadSettings(db, false)
}

// saveProviderSettings saves provider settings to the database.
func saveProviderSettings(db *sql.DB, ps *ProviderSettings) error {
	settingsJSON, err := json.Marshal(ps.Settings)
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}

	isEnabled := 0
	if ps.IsEnabled {
		isEnabled = 1
	}

	// Upsert: insert or update
	_, err = db.Exec(`
		INSERT INTO embed_settings (provider, settings, is_enabled, position, created_at, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(provider) DO UPDATE SET
			settings = excluded.settings,
			is_enabled = excluded.is_enabled,
			position = excluded.position,
			updated_at = CURRENT_TIMESTAMP
	`, ps.ProviderID, string(settingsJSON), isEnabled, ps.Position)
	if err != nil {
		return fmt.Errorf("saving provider settings: %w", err)
	}

	return nil
}

// toggleProvider enables or disables a provider.
func toggleProvider(db *sql.DB, providerID string, enabled bool) error {
	isEnabled := 0
	if enabled {
		isEnabled = 1
	}

	result, err := db.Exec(`
		UPDATE embed_settings
		SET is_enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE provider = ?
	`, isEnabled, providerID)
	if err != nil {
		return fmt.Errorf("toggling provider: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		// Create new entry if it doesn't exist
		_, err = db.Exec(`
			INSERT INTO embed_settings (provider, settings, is_enabled, position, created_at, updated_at)
			VALUES (?, '{}', ?, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, providerID, isEnabled)
		if err != nil {
			return fmt.Errorf("creating provider entry: %w", err)
		}
	}

	return nil
}
