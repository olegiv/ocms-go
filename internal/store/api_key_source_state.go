// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// APIKeySourceState stores persisted API key source observation state.
type APIKeySourceState struct {
	APIKeyID   int64
	LastIP     string
	LastSeenAt time.Time
	UpdatedAt  time.Time
}

// GetAPIKeySourceState returns persisted source observation state for a key.
func (q *Queries) GetAPIKeySourceState(ctx context.Context, apiKeyID int64) (APIKeySourceState, error) {
	row := q.db.QueryRowContext(ctx, `
		SELECT api_key_id, last_ip, last_seen_at, updated_at
		FROM api_key_source_state
		WHERE api_key_id = ?
	`, apiKeyID)

	var state APIKeySourceState
	err := row.Scan(&state.APIKeyID, &state.LastIP, &state.LastSeenAt, &state.UpdatedAt)
	return state, err
}

// UpsertAPIKeySourceState persists source observation state for a key.
func (q *Queries) UpsertAPIKeySourceState(ctx context.Context, apiKeyID int64, ip string, seenAt time.Time) error {
	_, err := q.db.ExecContext(ctx, `
		INSERT INTO api_key_source_state (api_key_id, last_ip, last_seen_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(api_key_id) DO UPDATE SET
			last_ip = excluded.last_ip,
			last_seen_at = excluded.last_seen_at,
			updated_at = excluded.updated_at
	`, apiKeyID, ip, seenAt, seenAt)
	return err
}

// DeleteStaleAPIKeySourceState removes stale source observation rows.
func (q *Queries) DeleteStaleAPIKeySourceState(ctx context.Context, before time.Time) error {
	_, err := q.db.ExecContext(ctx, `
		DELETE FROM api_key_source_state
		WHERE last_seen_at < ?
	`, before)
	return err
}

// IsMissingAPIKeySourceStateTableError reports whether err indicates the
// api_key_source_state table is missing in the database.
func IsMissingAPIKeySourceStateTableError(err error) bool {
	if err == nil {
		return false
	}
	if err == sql.ErrNoRows {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") &&
		strings.Contains(msg, "api_key_source_state")
}
