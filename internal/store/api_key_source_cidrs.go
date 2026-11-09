// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
)

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

// ReplaceAPIKeySourceCIDRs replaces source CIDR allowlist entries for an API key.
func (q *Queries) ReplaceAPIKeySourceCIDRs(ctx context.Context, apiKeyID int64, cidrs []string) error {
	switch db := q.db.(type) {
	case *sql.DB:
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := q.WithTx(tx).replaceAPIKeySourceCIDRs(ctx, ctx, apiKeyID, cidrs); err != nil {
			return err
		}
		return tx.Commit()
	case *sql.Tx:
		if err := ctx.Err(); err != nil {
			return err
		}
		// Interrupting a SQLite write can roll back the entire transaction.
		// Observe child cancellation between statements and undo only our savepoint.
		queryCtx := context.WithoutCancel(ctx)
		if _, err := db.ExecContext(queryCtx, "SAVEPOINT api_key_source_replace"); err != nil {
			return err
		}
		replaceErr := q.replaceAPIKeySourceCIDRs(ctx, queryCtx, apiKeyID, cidrs)
		if replaceErr == nil {
			_, replaceErr = db.ExecContext(queryCtx, "RELEASE api_key_source_replace")
			if replaceErr == nil {
				return nil
			}
		}
		_, rollbackErr := db.ExecContext(queryCtx, "ROLLBACK TO api_key_source_replace")
		_, releaseErr := db.ExecContext(queryCtx, "RELEASE api_key_source_replace")
		return errors.Join(replaceErr, rollbackErr, releaseErr)
	default:
		return errors.New("API key source replacement requires a database or transaction")
	}
}

func (q *Queries) replaceAPIKeySourceCIDRs(ctx, queryCtx context.Context, apiKeyID int64, cidrs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := q.db.ExecContext(queryCtx, `
		DELETE FROM api_key_source_cidrs
		WHERE api_key_id = ?
	`, apiKeyID); err != nil {
		return err
	}

	for _, cidr := range cidrs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := q.db.ExecContext(queryCtx, `
			INSERT INTO api_key_source_cidrs (api_key_id, cidr)
			VALUES (?, ?)
		`, apiKeyID, cidr); err != nil {
			return err
		}
	}

	return ctx.Err()
}
