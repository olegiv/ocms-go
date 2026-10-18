// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRunMigrate checks `ocms migrate`: up applies the migrations, down rolls
// back the latest, a Go migration the goose CLI cannot roll back, status and
// version run, and a missing or unknown command is refused.
func TestRunMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migrate.db")
	for _, command := range []string{"up", "status", "down", "up", "version"} {
		if err := runMigrate([]string{"-db", path, command}); err != nil {
			t.Fatalf("ocms migrate %s: %v", command, err)
		}
	}
	for _, args := range [][]string{{"-db", path}, {"-db", path, "create"}, {"-db", path, "up", "extra"}} {
		if err := runMigrate(args); err == nil {
			t.Errorf("ocms migrate %v succeeded, want it refused", args)
		}
	}
}

// TestMakefileMigratesThroughTheApp fails when a migrate target of the
// Makefile or site.mk runs the goose CLI, which does not know the Go
// migrations: on a database that has applied one, `goose down` fails, and
// `goose up` can apply newer SQL migrations first, after which the server
// refuses to start over the "missing" Go migration (review of the 2026-10-17
// audit fixes).
//
// Bug state: run `goose -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) down` in
// the Makefile's migrate-down and this fails.
func TestMakefileMigratesThroughTheApp(t *testing.T) {
	for _, name := range []string{"Makefile", "site.mk"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name)) // #nosec G304 -- fixed repository paths
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"migrate-up", "migrate-down", "migrate-status"} {
			m := regexp.MustCompile(`(?m)^` + target + `:.*\n((?:\t.*\n)+)`).FindStringSubmatch(string(data))
			if m == nil {
				t.Errorf("%s has no %s target with a recipe; the layout changed and this test is vacuous", name, target)
				continue
			}
			if recipe := strings.TrimSpace(m[1]); !strings.Contains(recipe, " migrate -db ") || strings.Contains(recipe, "goose") {
				t.Errorf("%s's %s runs %q, want ocms migrate: the goose CLI does not know the Go migrations", name, target, recipe)
			}
		}
	}
}
