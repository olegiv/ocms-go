// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package ai_content

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// Environment variable names for API keys (override database settings).
const (
	EnvOpenAIKey = "OCMS_OPENAI_API_KEY"
	EnvClaudeKey = "OCMS_CLAUDE_API_KEY"
	EnvGroqKey   = "OCMS_GROQ_API_KEY"
	EnvOllamaURL = "OCMS_OLLAMA_URL"
)

// envKeyForProvider returns the environment variable name for a provider's API key.
func envKeyForProvider(providerID string) string {
	switch providerID {
	case ProviderOpenAI:
		return EnvOpenAIKey
	case ProviderClaude:
		return EnvClaudeKey
	case ProviderGroq:
		return EnvGroqKey
	default:
		return ""
	}
}

// applyEnvOverrides applies environment variable overrides to provider settings.
func applyEnvOverrides(ps *ProviderSettings) {
	if ps == nil {
		return
	}
	// Check env var for API key
	envKey := envKeyForProvider(ps.Provider)
	if envKey != "" {
		if envVal := os.Getenv(envKey); envVal != "" {
			ps.APIKey = envVal
		}
	}
	// Check env var for Ollama URL
	if ps.Provider == ProviderOllama {
		if envVal := os.Getenv(EnvOllamaURL); envVal != "" {
			ps.BaseURL = envVal
		}
	}
}

// ProviderSettings holds the configuration for a single AI provider.
type ProviderSettings struct {
	ID           int64
	Provider     string
	APIKey       string
	Model        string
	BaseURL      string
	IsEnabled    bool
	ImageEnabled bool
	ImageModel   string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// loadProviderSettings loads settings for a specific provider.
func loadProviderSettings(db *sql.DB, providerID string) (*ProviderSettings, error) {
	ps := &ProviderSettings{Provider: providerID}
	var isEnabled, imageEnabled int64
	err := db.QueryRow(
		`SELECT id, provider, api_key, model, base_url, is_enabled, image_enabled, image_model, created_at, updated_at
		 FROM ai_content_settings WHERE provider = ?`, providerID,
	).Scan(&ps.ID, &ps.Provider, &ps.APIKey, &ps.Model, &ps.BaseURL, &isEnabled, &imageEnabled, &ps.ImageModel, &ps.CreatedAt, &ps.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ps, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading provider settings: %w", err)
	}
	ps.IsEnabled = isEnabled == 1
	ps.ImageEnabled = imageEnabled == 1
	applyEnvOverrides(ps)
	return ps, nil
}

// loadAllSettings loads settings for all providers.
func loadAllSettings(db *sql.DB) ([]*ProviderSettings, error) {
	rows, err := db.Query(
		`SELECT id, provider, api_key, model, base_url, is_enabled, image_enabled, image_model, created_at, updated_at
		 FROM ai_content_settings ORDER BY provider`)
	if err != nil {
		return nil, fmt.Errorf("loading all settings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []*ProviderSettings
	for rows.Next() {
		ps := &ProviderSettings{}
		var isEnabled, imageEnabled int64
		if err := rows.Scan(&ps.ID, &ps.Provider, &ps.APIKey, &ps.Model, &ps.BaseURL, &isEnabled, &imageEnabled, &ps.ImageModel, &ps.CreatedAt, &ps.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning settings row: %w", err)
		}
		ps.IsEnabled = isEnabled == 1
		ps.ImageEnabled = imageEnabled == 1
		applyEnvOverrides(ps)
		result = append(result, ps)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating settings rows: %w", err)
	}
	return result, nil
}

// loadEnabledProvider returns the first enabled provider settings.
func loadEnabledProvider(db *sql.DB) (*ProviderSettings, error) {
	ps := &ProviderSettings{}
	var isEnabled, imageEnabled int64
	err := db.QueryRow(
		`SELECT id, provider, api_key, model, base_url, is_enabled, image_enabled, image_model, created_at, updated_at
		 FROM ai_content_settings WHERE is_enabled = 1 LIMIT 1`,
	).Scan(&ps.ID, &ps.Provider, &ps.APIKey, &ps.Model, &ps.BaseURL, &isEnabled, &imageEnabled, &ps.ImageModel, &ps.CreatedAt, &ps.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading enabled provider: %w", err)
	}
	ps.IsEnabled = isEnabled == 1
	ps.ImageEnabled = imageEnabled == 1
	applyEnvOverrides(ps)
	return ps, nil
}

// saveProviderSettings saves or updates provider settings.
func saveProviderSettings(db *sql.DB, ps *ProviderSettings) error {
	enabledInt := int64(0)
	if ps.IsEnabled {
		enabledInt = 1
	}
	imageEnabledInt := int64(0)
	if ps.ImageEnabled {
		imageEnabledInt = 1
	}

	_, err := db.Exec(`
		INSERT INTO ai_content_settings (provider, api_key, model, base_url, is_enabled, image_enabled, image_model, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(provider) DO UPDATE SET
			api_key = excluded.api_key,
			model = excluded.model,
			base_url = excluded.base_url,
			is_enabled = excluded.is_enabled,
			image_enabled = excluded.image_enabled,
			image_model = excluded.image_model,
			updated_at = CURRENT_TIMESTAMP
	`, ps.Provider, ps.APIKey, ps.Model, ps.BaseURL, enabledInt, imageEnabledInt, ps.ImageModel)
	if err != nil {
		return fmt.Errorf("saving provider settings: %w", err)
	}
	return nil
}
