// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import "context"

// ListAPIKeySourceCIDRs returns all CIDR/IP allowlist entries for an API key.
func (q *Queries) ListAPIKeySourceCIDRs(ctx context.Context, apiKeyID int64) ([]string, error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT cidr
		FROM api_key_source_cidrs
		WHERE api_key_id = ?
		ORDER BY id ASC
	`, apiKeyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var cidrs []string
	for rows.Next() {
		var cidr string
		if err := rows.Scan(&cidr); err != nil {
			return nil, err
		}
		cidrs = append(cidrs, cidr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cidrs, nil
}
