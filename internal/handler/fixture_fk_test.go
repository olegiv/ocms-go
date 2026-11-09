// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestHandlerFixtureForeignKeysMatchMigrations(t *testing.T) {
	fixture := testDB(t)
	migrated, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)

	type foreignKey struct{ table, from, to, onUpdate, onDelete, match string }
	read := func(db *sql.DB, table string) []foreignKey {
		t.Helper()
		rows, err := db.QueryContext(t.Context(), `
			SELECT "table", "from", "to", on_update, on_delete, "match"
			FROM pragma_foreign_key_list(?)
			ORDER BY "table", "from", "to", on_update, on_delete, "match"`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := rows.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}()
		var keys []foreignKey
		for rows.Next() {
			var key foreignKey
			if scanErr := rows.Scan(&key.table, &key.from, &key.to, &key.onUpdate, &key.onDelete, &key.match); scanErr != nil {
				t.Fatal(scanErr)
			}
			keys = append(keys, key)
		}
		if readErr := rows.Err(); readErr != nil {
			t.Fatal(readErr)
		}
		return keys
	}
	for _, table := range []string{"pages", "menu_items"} {
		got, want := read(fixture, table), read(migrated, table)
		if !slices.Equal(got, want) {
			t.Errorf("%s fixture foreign keys = %+v, want %+v", table, got, want)
		}
	}
}
