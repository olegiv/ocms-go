// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxInstructionsRunes caps the administrator's agent instructions. They are
// sent to every MCP client and land in the model's context, so they stay
// short enough not to crowd out the content agents actually asked for.
const maxInstructionsRunes = 4000

// i18n message keys returned by normalizeInstructions.
const (
	errInstructionsEncoding = "mcp.error_instructions_encoding"
	errInstructionsTooLong  = "mcp.error_instructions_too_long"
)

// Settings are the administrator-controlled MCP options (Admin > MCP Server).
type Settings struct {
	// AllowDrafts exposes unpublished pages to API keys that also hold
	// pages:read. Off by default: drafts are the sensitive part of a CMS, so
	// AI agents see published content unless an administrator opts in.
	AllowDrafts bool
	// Instructions is site-specific guidance appended to the instructions
	// every MCP client receives.
	Instructions string
}

// loadSettings reads the settings row. A missing row yields the defaults.
func loadSettings(ctx context.Context, db *sql.DB) (Settings, error) {
	var (
		allowDrafts int
		s           Settings
	)
	err := db.QueryRowContext(ctx,
		`SELECT allow_drafts, instructions FROM mcp_settings WHERE id = 1`,
	).Scan(&allowDrafts, &s.Instructions)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("loading MCP settings: %w", err)
	}
	s.AllowDrafts = allowDrafts == 1
	return s, nil
}

// saveSettings upserts the settings row.
func saveSettings(ctx context.Context, db *sql.DB, s Settings) error {
	allowDrafts := 0
	if s.AllowDrafts {
		allowDrafts = 1
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO mcp_settings (id, allow_drafts, instructions, updated_at)
		VALUES (1, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			allow_drafts = excluded.allow_drafts,
			instructions = excluded.instructions,
			updated_at = CURRENT_TIMESTAMP
	`, allowDrafts, s.Instructions)
	if err != nil {
		return fmt.Errorf("saving MCP settings: %w", err)
	}
	return nil
}

// normalizeInstructions cleans administrator input for the instructions
// field: line endings are unified, control characters other than newline and
// tab are dropped, and surrounding whitespace is trimmed. It returns the
// cleaned text, or an i18n message key describing why the input is invalid.
func normalizeInstructions(raw string) (string, string) {
	if !utf8.ValidString(raw) {
		return "", errInstructionsEncoding
	}
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxInstructionsRunes {
		return "", errInstructionsTooLong
	}
	return s, ""
}
