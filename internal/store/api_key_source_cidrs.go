// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
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
		if err := q.WithTx(tx).replaceAPIKeySourceCIDRs(ctx, apiKeyID, cidrs); err != nil {
			return err
		}
		return tx.Commit()
	case *sql.Tx:
		// Participate in the caller's transaction; never commit it here.
		if _, err := db.ExecContext(ctx, "SAVEPOINT api_key_source_replace"); err != nil {
			return err
		}
		replaceErr := q.replaceAPIKeySourceCIDRs(ctx, apiKeyID, cidrs)
		if replaceErr == nil {
			replaceErr = ctx.Err()
		}
		// Closing the savepoint must survive cancellation of a child request:
		// the caller may still commit its longer-lived transaction.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if replaceErr == nil {
			_, replaceErr = db.ExecContext(cleanupCtx, "RELEASE api_key_source_replace")
			if replaceErr == nil {
				return nil
			}
		}
		_, rollbackErr := db.ExecContext(cleanupCtx, "ROLLBACK TO api_key_source_replace")
		_, releaseErr := db.ExecContext(cleanupCtx, "RELEASE api_key_source_replace")
		return errors.Join(replaceErr, rollbackErr, releaseErr)
	default:
		return errors.New("API key source replacement requires a database or transaction")
	}
}

func (q *Queries) replaceAPIKeySourceCIDRs(ctx context.Context, apiKeyID int64, cidrs []string) error {
	if _, err := q.db.ExecContext(ctx, `
		DELETE FROM api_key_source_cidrs
		WHERE api_key_id = ?
	`, apiKeyID); err != nil {
		return err
	}

	for _, cidr := range cidrs {
		if _, err := q.db.ExecContext(ctx, `
			INSERT INTO api_key_source_cidrs (api_key_id, cidr)
			VALUES (?, ?)
		`, apiKeyID, cidr); err != nil {
			return err
		}
	}

	return nil
}
