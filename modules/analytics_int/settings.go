// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// loadSettings loads settings from the database.
func (m *Module) loadSettings() (*Settings, error) {
	row := m.ctx.DB.QueryRow(`
		SELECT enabled, retention_days, exclude_paths, current_salt,
		       salt_created_at, salt_rotation_hours, show_post_stats
		FROM page_analytics_settings
		WHERE id = 1
	`)

	var (
		enabled           int
		retentionDays     int
		excludePathsJSON  string
		currentSalt       string
		saltCreatedAt     time.Time
		saltRotationHours int
		showPostStats     int
	)

	err := row.Scan(&enabled, &retentionDays, &excludePathsJSON,
		&currentSalt, &saltCreatedAt, &saltRotationHours, &showPostStats)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Return defaults
			return &Settings{
				Enabled:           true,
				RetentionDays:     365,
				ExcludePaths:      []string{},
				SaltRotationHours: 24,
				ShowPostStats:     true,
			}, nil
		}
		return nil, err
	}

	var excludePaths []string
	if excludePathsJSON != "" {
		if err := json.Unmarshal([]byte(excludePathsJSON), &excludePaths); err != nil {
			m.ctx.Logger.Warn("analytics_int exclude_paths is not valid JSON; no paths are excluded", "error", err)
			excludePaths = []string{}
		}
	}

	return &Settings{
		Enabled:           enabled == 1,
		RetentionDays:     retentionDays,
		ExcludePaths:      excludePaths,
		CurrentSalt:       currentSalt,
		SaltCreatedAt:     saltCreatedAt,
		SaltRotationHours: saltRotationHours,
		ShowPostStats:     showPostStats == 1,
	}, nil
}

// settingsSnapshot returns a copy of the settings, or the zero value (tracking
// disabled) before they are loaded. The copy shares ExcludePaths with the
// stored value; writers replace that slice and never modify it in place.
func (m *Module) settingsSnapshot() Settings {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	if m.settings == nil {
		return Settings{}
	}
	return *m.settings
}

// storeSettings replaces the settings with ones loaded from the database.
func (m *Module) storeSettings(settings *Settings) {
	m.settingsMu.Lock()
	m.settings = settings
	m.settingsMu.Unlock()
}

// updateSettings applies change to a copy of the settings, saves the copy and
// publishes it only when the save succeeds. The lock is held across the save
// so a concurrent salt rotation cannot be overwritten by a stale copy.
func (m *Module) updateSettings(change func(*Settings)) error {
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	if m.settings == nil {
		return errors.New("analytics_int settings are not loaded")
	}
	next := *m.settings
	change(&next)
	if err := m.saveSettings(&next); err != nil {
		return err
	}
	m.settings = &next
	return nil
}

// saveSettings saves settings to the database.
func (m *Module) saveSettings(settings *Settings) error {
	excludePathsJSON, err := json.Marshal(settings.ExcludePaths)
	if err != nil {
		excludePathsJSON = []byte("[]")
	}

	enabled := 0
	if settings.Enabled {
		enabled = 1
	}
	showPostStats := 0
	if settings.ShowPostStats {
		showPostStats = 1
	}

	_, err = m.ctx.DB.Exec(`
		UPDATE page_analytics_settings
		SET enabled = ?, retention_days = ?, exclude_paths = ?,
		    current_salt = ?, salt_created_at = ?, salt_rotation_hours = ?,
		    show_post_stats = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, enabled, settings.RetentionDays, string(excludePathsJSON),
		settings.CurrentSalt, settings.SaltCreatedAt, settings.SaltRotationHours,
		showPostStats)

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
	m.storeSettings(settings)
	m.reload.MarkLoaded()
	return nil
}

// errSettingsNotLoaded reports work skipped because the stored settings could
// not be read; a scheduled run logs it as a failed run (Event Log), and a
// manual trigger reports it.
var errSettingsNotLoaded = errors.New("analytics_int settings are not loaded")

// ensureSettingsLoaded returns nil when the stored settings are in effect,
// reloading them at once if startup could not read them, and otherwise the
// reload error wrapped in errSettingsNotLoaded. It does not wait for or
// reset the retry interval. The weekly expiry cleanup calls it, and so does
// a save that passes the fallback-form check, since both act on the result.
func (m *Module) ensureSettingsLoaded() error {
	if m.reload.Loaded() {
		return nil
	}
	return m.logSettingsReload(m.ReloadSettings())
}

// retrySettingsLoad returns nil when the stored settings are in effect,
// reloading them first if startup could not read them and the gate has not
// tried within its interval (module.SettingsRetryInterval unless
// RetryInterval is set); ensureSettingsLoaded's reloads do not count. It
// returns errSettingsNotLoaded otherwise, wrapping the reload error when a
// reload failed. The hourly aggregation and the dashboard call it, so
// settings that stay unreadable are queried and logged at most once per
// interval.
func (m *Module) retrySettingsLoad() error {
	if m.reload.Loaded() {
		return nil
	}
	loaded, err := m.reload.Retry(m.ReloadSettings)
	if !loaded && err == nil {
		// Tried within the interval, or another caller's retry is running.
		return errSettingsNotLoaded
	}
	return m.logSettingsReload(err)
}

// logSettingsReload logs the outcome of a reload of settings that were not
// loaded: it returns nil on success, logged at Info, and otherwise the
// error, logged at Warn and wrapped in errSettingsNotLoaded.
func (m *Module) logSettingsReload(err error) error {
	if err != nil {
		m.ctx.Logger.Warn("analytics_int settings still cannot be read", "error", err)
		return fmt.Errorf("%w: %w", errSettingsNotLoaded, err)
	}
	m.ctx.Logger.Info("analytics_int settings loaded; tracking follows them again")
	return nil
}
