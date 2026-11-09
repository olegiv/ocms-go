// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestEveryRestrictingUserForeignKeyIsCountedAsOwnedContent derives the
// columns that stop a user from being deleted from the migrated schema and
// requires CountUserOwnedContent to count each (2026-11-03 review R2 P3-5:
// page_ai_access.set_by was restricting and uncounted, so deleting that user
// failed with a foreign-key error instead of the content refusal).
//
// Bug state: add a table with a restricting reference to users(id), or drop
// one of the counts from CountUserOwnedContent, and this fails.
func TestEveryRestrictingUserForeignKeyIsCountedAsOwnedContent(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	rows, err := db.Query(`SELECT m.name, f."from", f."on_delete" FROM sqlite_master m, pragma_foreign_key_list(m.name) f
		WHERE m.type = 'table' AND f."table" = 'users' AND f."on_delete" IN ('RESTRICT', 'NO ACTION') ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	type reference struct{ table, column string }
	var references []reference
	for rows.Next() {
		var table, column, action string
		if err = rows.Scan(&table, &column, &action); err != nil {
			t.Fatal(err)
		}
		references = append(references, reference{table, column})
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(references) < 6 {
		t.Fatalf("found %d restricting references to users, want at least the six known ones: %v", len(references), references)
	}

	source, err := os.ReadFile(filepath.Join("..", "store", "queries", "users.sql"))
	if err != nil {
		t.Fatal(err)
	}
	query := string(source)
	start := strings.Index(query, "-- name: CountUserOwnedContent")
	if start < 0 {
		t.Fatal("CountUserOwnedContent is missing")
	}
	query = query[start+len("-- name: CountUserOwnedContent"):]
	if end := strings.Index(query, "-- name:"); end >= 0 {
		query = query[:end]
	}
	for _, ref := range references {
		counted := regexp.MustCompile(`(?s)FROM\s+` + regexp.QuoteMeta(ref.table) + `\b[^()]*?\b(?:\w+\.)?` + regexp.QuoteMeta(ref.column) + `\s*=\s*sqlc\.arg\(user_id\)`)
		if !counted.MatchString(query) {
			t.Errorf("%s.%s restricts deleting a user but CountUserOwnedContent does not count it", ref.table, ref.column)
		}
	}
}
