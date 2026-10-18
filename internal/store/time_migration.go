// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
)

// timeMigrationName is the goose name of the Go migration that moves stored
// times to UTC. Its version orders it after the SQL migrations that existed
// when it was added; goose's CLI does not know it, so migrations run through
// `ocms migrate`. It has no down step: rolling it back only forgets it, and
// the times stay UTC.
const timeMigrationName = "20261018050000_store_times_in_utc.go"

// storedTimeLayout is the text NewDB stores a time.Time as (the driver's
// _time_format=sqlite).
const storedTimeLayout = "2006-01-02 15:04:05.999999999-07:00"

func init() {
	goose.AddNamedMigrationContext(timeMigrationName, migrateTimesToUTC, nil)
}

// migrateTimesToUTC rewrites, in every DATE, DATETIME and TIMESTAMP column of
// every rowid table, the times the driver stored as time.Time.String()
// ("2026-10-18 05:41:31.17 +0200 CEST m=+0.08") and those stored with an
// offset other than UTC ("2026-10-18 05:41:31.17+02:00") as UTC in the text
// NewDB writes now ("2026-10-18 03:41:31.17+00:00"). SQLite's date functions
// can then read them, and they sort with the times SQLite writes itself
// (CURRENT_TIMESTAMP, UTC without an offset). Times stored without a zone,
// such as those and Internal Analytics' local wall-clock times, are left as
// they are; so are values that look like a zoned time but do not parse,
// which are logged. Module tables are included: they exist from earlier runs
// when this migration runs.
func migrateTimesToUTC(ctx context.Context, tx *sql.Tx) error {
	tables, err := queryStrings(ctx, tx, `SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
			AND sql NOT LIKE '%WITHOUT ROWID%' AND sql NOT LIKE 'CREATE VIRTUAL TABLE%'`)
	if err != nil {
		return fmt.Errorf("listing tables: %w", err)
	}
	for _, table := range tables {
		columns, err := timeColumns(ctx, tx, table)
		if err != nil {
			return err
		}
		for _, column := range columns {
			if err := migrateTimeColumn(ctx, tx, table, column); err != nil {
				return fmt.Errorf("moving %s.%s to UTC: %w", table, column, err)
			}
		}
	}
	return nil
}

// timeColumns returns the columns of table declared DATE, DATETIME or
// TIMESTAMP.
func timeColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name, type FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("reading the columns of %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	var columns []string
	for rows.Next() {
		var name, declared string
		if err := rows.Scan(&name, &declared); err != nil {
			return nil, err
		}
		switch strings.ToUpper(declared) {
		case "DATE", "DATETIME", "TIMESTAMP":
			columns = append(columns, name)
		}
	}
	return columns, rows.Err()
}

// migrateTimeColumn rewrites the times of one column that are not yet UTC
// text in storedTimeLayout.
func migrateTimeColumn(ctx context.Context, tx *sql.Tx, table, column string) error {
	t, c := quoteIdentifier(table), quoteIdentifier(column)
	// CAST: the driver would otherwise parse a DATETIME value into a
	// time.Time, and the stored text is what needs rewriting.
	rows, err := tx.QueryContext(ctx, `SELECT rowid, CAST(`+c+` AS TEXT) FROM `+t+`
		WHERE typeof(`+c+`) = 'text' AND (`+c+` LIKE '% m=%'
			OR `+c+` GLOB '* [+-][0-9][0-9][0-9][0-9] *'
			OR (`+c+` GLOB '*[+-][0-9][0-9]:[0-9][0-9]' AND `+c+` NOT LIKE '%+00:00'))`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	type update struct {
		rowid int64
		value string
	}
	var updates []update
	var unparsed []string
	for rows.Next() {
		var rowid int64
		var text string
		if err := rows.Scan(&rowid, &text); err != nil {
			return err
		}
		if when, ok := parseStoredTime(text); ok {
			updates = append(updates, update{rowid, when.UTC().Format(storedTimeLayout)})
		} else {
			unparsed = append(unparsed, text)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// Close before updating the table the rows read.
	if err := rows.Close(); err != nil {
		return err
	}
	if len(unparsed) > 0 {
		slog.Warn("left times that do not parse as they are while moving times to UTC",
			"table", table, "column", column, "count", len(unparsed), "example", unparsed[0])
	}
	for _, u := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE `+t+` SET `+c+` = ? WHERE rowid = ?`, u.value, u.rowid); err != nil {
			return err
		}
	}
	return nil
}

// parseStoredTime parses a time stored as time.Time.String(), with or
// without its monotonic-clock suffix, or in storedTimeLayout.
func parseStoredTime(text string) (time.Time, bool) {
	if i := strings.Index(text, " m="); i >= 0 {
		text = text[:i]
	}
	text = strings.TrimSpace(text)
	for _, layout := range []string{"2006-01-02 15:04:05.999999999 -0700 MST", storedTimeLayout, "2006-01-02T15:04:05.999999999-07:00"} {
		if when, err := time.Parse(layout, text); err == nil {
			return when, true
		}
	}
	// String() names a zone without an abbreviation by its offset ("+0545
	// +0545" in Kathmandu), which the MST layout rejects; the offset before
	// it is all that is needed.
	if i := strings.LastIndexByte(text, ' '); i > 0 {
		if when, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", text[:i]); err == nil {
			return when, true
		}
	}
	return time.Time{}, false
}

// queryStrings returns the single text column query selects.
func queryStrings(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// quoteIdentifier quotes name as an SQL identifier.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
