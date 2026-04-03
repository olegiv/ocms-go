// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// loadSettings loads settings from the database.
func (m *Module) loadSettings() (*Settings, error) {
	row := m.ctx.DB.QueryRow(`
		SELECT enabled, retention_days, exclude_paths, exclude_ips, current_salt,
		       salt_created_at, salt_rotation_hours
		FROM page_analytics_settings
		WHERE id = 1
	`)

	var (
		enabled           int
		retentionDays     int
		excludePathsJSON  string
		excludeIPsJSON    string
		currentSalt       string
		saltCreatedAt     time.Time
		saltRotationHours int
	)

	err := row.Scan(&enabled, &retentionDays, &excludePathsJSON, &excludeIPsJSON,
		&currentSalt, &saltCreatedAt, &saltRotationHours)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Return defaults
			return &Settings{
				Enabled:           true,
				RetentionDays:     365,
				ExcludePaths:      []string{},
				ExcludeIPs:        []string{},
				SaltRotationHours: 24,
			}, nil
		}
		return nil, err
	}

	var excludePaths []string
	if excludePathsJSON != "" {
		if err := json.Unmarshal([]byte(excludePathsJSON), &excludePaths); err != nil {
			excludePaths = []string{}
		}
	}

	var excludeIPs []string
	if excludeIPsJSON != "" {
		if err := json.Unmarshal([]byte(excludeIPsJSON), &excludeIPs); err != nil {
			excludeIPs = []string{}
		}
	}

	return &Settings{
		Enabled:           enabled == 1,
		RetentionDays:     retentionDays,
		ExcludePaths:      excludePaths,
		ExcludeIPs:        excludeIPs,
		CurrentSalt:       currentSalt,
		SaltCreatedAt:     saltCreatedAt,
		SaltRotationHours: saltRotationHours,
	}, nil
}

// saveSettings saves settings to the database.
func (m *Module) saveSettings() error {
	excludePathsJSON, err := json.Marshal(m.settings.ExcludePaths)
	if err != nil {
		excludePathsJSON = []byte("[]")
	}

	excludeIPsJSON, err := json.Marshal(m.settings.ExcludeIPs)
	if err != nil {
		excludeIPsJSON = []byte("[]")
	}

	enabled := 0
	if m.settings.Enabled {
		enabled = 1
	}

	_, err = m.ctx.DB.Exec(`
		UPDATE page_analytics_settings
		SET enabled = ?, retention_days = ?, exclude_paths = ?, exclude_ips = ?,
		    current_salt = ?, salt_created_at = ?, salt_rotation_hours = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, enabled, m.settings.RetentionDays, string(excludePathsJSON), string(excludeIPsJSON),
		m.settings.CurrentSalt, m.settings.SaltCreatedAt, m.settings.SaltRotationHours)

	return err
}

// saveSalt saves only the salt (called during rotation).
func (m *Module) saveSalt(newSalt string) error {
	_, err := m.ctx.DB.Exec(`
		UPDATE page_analytics_settings
		SET current_salt = ?, salt_created_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, newSalt)
	return err
}

// ReloadSettings reloads settings from the database.
func (m *Module) ReloadSettings() error {
	settings, err := m.loadSettings()
	if err != nil {
		return err
	}
	m.settings = settings
	return nil
}
