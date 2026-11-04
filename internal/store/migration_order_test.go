// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

// TestMigrateAppliesAnOlderMigrationMergedLater checks that a database which
// already applied newer migrations still gets an older one that arrives with
// a later merge. Feature branches add migrations in parallel: the MCP
// staging database ran the editorial-review branch's 2026-10-30 to
// 2026-11-02 migrations before master's 20261023120000_create_page_search
// was merged in, and goose's default refuses such a database ("missing
// migrations") so the merged build could not start there.
//
// Bug state: run "up" without goose.WithAllowMissing and this fails.
func TestMigrateAppliesAnOlderMigrationMergedLater(t *testing.T) {
	const late = "20261023120000_create_page_search.sql"
	db, err := NewDB(filepath.Join(t.TempDir(), "order.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The branch database: every migration except the one merged later.
	without := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	newest := ""
	for _, entry := range entries {
		if entry.Name() == late {
			continue
		}
		data, readErr := fs.ReadFile(migrations, "migrations/"+entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		without["migrations/"+entry.Name()] = &fstest.MapFile{Data: data}
		if entry.Name() > newest {
			newest = entry.Name()
		}
	}
	if newest < late {
		t.Fatalf("no migration is newer than %s; the scenario no longer applies", late)
	}
	goose.SetBaseFS(without)
	t.Cleanup(func() { goose.SetBaseFS(migrations) })
	if err = goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err = goose.Up(db, "migrations"); err != nil {
		t.Fatalf("migrating the branch database: %v", err)
	}

	if err = Migrate(db); err != nil {
		t.Fatalf("Migrate refused a database missing an older merged migration: %v", err)
	}
	var applied int
	version := strings.SplitN(late, "_", 2)[0]
	if err = db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, version).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("%s applied %d times: %v", late, applied, err)
	}
	if _, err = db.Exec(`SELECT COUNT(*) FROM page_search`); err != nil {
		t.Fatalf("page_search missing after Migrate: %v", err)
	}
}
