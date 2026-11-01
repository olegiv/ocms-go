// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import "database/sql"

// migrateDraftSettings is atomic and tolerates an already-applied schema when
// module registration metadata is rebuilt independently from the settings table.
func migrateDraftSettings(db *sql.DB, up bool) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query("PRAGMA table_info(mcp_settings)")
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, required, primary int
		var name, kind string
		var fallback sql.NullString
		if err = rows.Scan(&cid, &name, &kind, &required, &fallback, &primary); err != nil {
			_ = rows.Close()
			return err
		}
		columns[name] = true
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, column := range []struct{ name, definition string }{{"draft_tools_enabled", "INTEGER NOT NULL DEFAULT 0 CHECK(draft_tools_enabled IN (0,1))"}, {"external_links", "TEXT NOT NULL DEFAULT 'keep' CHECK(external_links IN ('keep','nofollow','strip'))"}} {
		if up && !columns[column.name] {
			_, err = tx.Exec("ALTER TABLE mcp_settings ADD COLUMN " + column.name + " " + column.definition)
		} else if !up && columns[column.name] {
			_, err = tx.Exec("ALTER TABLE mcp_settings DROP COLUMN " + column.name)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
