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

// unsetEnv unsets the environment variables keys until the test ends, so no
// OCMS_DB_PATH or OCMS_ENV_FILE from the developer's environment reaches
// runMigrate.
func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		t.Setenv(key, "") // restores the variable when the test ends
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRunMigrate checks `ocms migrate`: up applies the migrations, down rolls
// back the latest, a Go migration the goose CLI cannot roll back, status and
// version run, and a missing or unknown command is refused.
func TestRunMigrate(t *testing.T) {
	unsetEnv(t, "OCMS_DB_PATH", "OCMS_ENV_FILE")
	t.Chdir(t.TempDir()) // no ./.env
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

// TestRunMigrateReadsTheEnvFile fails when ocms migrate ignores the env file
// serve reads, .env in the working directory or the file OCMS_ENV_FILE
// names: run in a site directory, it migrated a new, empty ./data/ocms.db
// instead of the database OCMS_DB_PATH names there, and reported success
// (review of the 2026-10-17 audit fixes). -db and an OCMS_DB_PATH already in
// the environment win over the file, and OCMS_ENV_FILE replaces ./.env.
//
// Bug state: drop the loadEnvFile call in runMigrate and this fails.
func TestRunMigrateReadsTheEnvFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		// setup writes the env files and sets the environment in site, the
		// working directory, and returns the arguments, the database they
		// must migrate and one they must not.
		setup func(t *testing.T, site string) (args []string, want, decoy string)
	}{
		{".env", func(t *testing.T, site string) ([]string, string, string) {
			want := filepath.Join(t.TempDir(), "site.db")
			writeDBPathEnv(t, filepath.Join(site, ".env"), want)
			return []string{"up"}, want, ""
		}},
		{"OCMS_ENV_FILE over .env", func(t *testing.T, site string) ([]string, string, string) {
			want, decoy := filepath.Join(t.TempDir(), "site.db"), filepath.Join(t.TempDir(), "decoy.db")
			envFile := filepath.Join(t.TempDir(), "instance.env")
			writeDBPathEnv(t, envFile, want)
			writeDBPathEnv(t, filepath.Join(site, ".env"), decoy)
			t.Setenv("OCMS_ENV_FILE", envFile)
			return []string{"up"}, want, decoy
		}},
		{"-db over the env file", func(t *testing.T, site string) ([]string, string, string) {
			want, decoy := filepath.Join(t.TempDir(), "flag.db"), filepath.Join(t.TempDir(), "decoy.db")
			writeDBPathEnv(t, filepath.Join(site, ".env"), decoy)
			return []string{"-db", want, "up"}, want, decoy
		}},
		{"environment over .env", func(t *testing.T, site string) ([]string, string, string) {
			want, decoy := filepath.Join(t.TempDir(), "env.db"), filepath.Join(t.TempDir(), "decoy.db")
			writeDBPathEnv(t, filepath.Join(site, ".env"), decoy)
			t.Setenv("OCMS_DB_PATH", want)
			return []string{"up"}, want, decoy
		}},
		{"environment over OCMS_ENV_FILE", func(t *testing.T, _ string) ([]string, string, string) {
			want, decoy := filepath.Join(t.TempDir(), "env.db"), filepath.Join(t.TempDir(), "decoy.db")
			envFile := filepath.Join(t.TempDir(), "instance.env")
			writeDBPathEnv(t, envFile, decoy)
			t.Setenv("OCMS_ENV_FILE", envFile)
			t.Setenv("OCMS_DB_PATH", want)
			return []string{"up"}, want, decoy
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnv(t, "OCMS_DB_PATH", "OCMS_ENV_FILE")
			site := newSiteDir(t)
			args, want, decoy := tc.setup(t, site)
			if err := runMigrate(args); err != nil {
				t.Fatalf("ocms migrate %v: %v", args, err)
			}
			if _, err := os.Stat(want); err != nil {
				t.Errorf("the database %s was not migrated: %v", want, err)
			}
			for _, unwanted := range []string{decoy, filepath.Join(site, "data", "ocms.db")} {
				if _, err := os.Stat(unwanted); unwanted != "" && err == nil {
					t.Errorf("ocms migrate %v created %s", args, unwanted)
				}
			}
		})
	}
}

// TestRunMigrateRefusesWhatWouldMigrateTheWrongDatabase checks that ocms
// migrate fails, without creating a database, when an env file it is to read
// is missing or does not parse, and when a command other than up names a
// database that is not there. Each of these used to end in ./data/ocms.db or
// the mistyped path being created, and up reporting success or status
// listing every migration as pending (review of the 2026-10-17 audit fixes).
//
// Bug state: ignore the error of godotenv.Load in loadEnvFile, or drop the
// os.Stat check in runMigrate, and this fails.
func TestRunMigrateRefusesWhatWouldMigrateTheWrongDatabase(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T, site string) (args []string, created string)
		wantErr string
	}{
		{"missing OCMS_ENV_FILE", func(t *testing.T, site string) ([]string, string) {
			t.Setenv("OCMS_ENV_FILE", filepath.Join(site, "missing.env"))
			return []string{"up"}, filepath.Join(site, "data", "ocms.db")
		}, "reading OCMS_ENV_FILE"},
		{"malformed .env", func(t *testing.T, site string) ([]string, string) {
			body := "OCMS_DB_PATH=" + filepath.Join(site, "site.db") + "\nthis is not a setting\n"
			if err := os.WriteFile(filepath.Join(site, ".env"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			return []string{"up"}, filepath.Join(site, "data", "ocms.db")
		}, "reading .env"},
		{"status of a missing database", func(t *testing.T, site string) ([]string, string) {
			path := filepath.Join(site, "typo.db")
			return []string{"-db", path, "status"}, path
		}, "no database to status"},
		{"down on a missing database", func(t *testing.T, site string) ([]string, string) {
			path := filepath.Join(site, "typo.db")
			return []string{"-db", path, "down"}, path
		}, "no database to down"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnv(t, "OCMS_DB_PATH", "OCMS_ENV_FILE")
			site := newSiteDir(t)
			args, created := tc.setup(t, site)
			err := runMigrate(args)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ocms migrate %v returned %v, want an error mentioning %q", args, err, tc.wantErr)
			}
			if _, err := os.Stat(created); err == nil {
				t.Errorf("ocms migrate %v created %s", args, created)
			}
		})
	}
}

// newSiteDir makes a site directory with a data directory, as ocms init does,
// and changes into it until the test ends.
func newSiteDir(t *testing.T) string {
	t.Helper()
	site := t.TempDir()
	if err := os.Mkdir(filepath.Join(site, "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(site)
	return site
}

// writeDBPathEnv writes an env file at path naming dbPath as OCMS_DB_PATH.
func writeDBPathEnv(t *testing.T, path, dbPath string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("OCMS_DB_PATH="+dbPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestMakefileMigratesThroughTheApp fails when a migrate target of the
// Makefile or site.mk runs the goose CLI, which does not know the Go
// migrations: on a database that has applied one, `goose down` fails, and
// `goose up` can apply newer SQL migrations first, after which the server
// refuses to start over the "missing" Go migration (review of the 2026-10-17
// audit fixes). It also fails when the Makefile passes -db without a DB_PATH
// given, which migrated ./data/ocms.db whatever .env named.
//
// Bug state: run `goose -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) down` in
// the Makefile's migrate-down, or set DB_PATH := ./data/ocms.db, and this
// fails.
func TestMakefileMigratesThroughTheApp(t *testing.T) {
	migrateCommand := regexp.MustCompile(`\brun \S+ migrate\b`) // go run ./cmd/ocms migrate, $(GO) run $(MAIN_DIR) migrate
	for _, name := range []string{"Makefile", "site.mk"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name)) // #nosec G304 -- fixed repository paths
		if err != nil {
			t.Fatal(err)
		}
		if name == "Makefile" && !regexp.MustCompile(`(?m)^DB_PATH\s*:=\s*$`).Match(data) {
			t.Error("the Makefile gives DB_PATH a value; empty, the migrate targets use the database .env names, as the server does")
		}
		for _, target := range []string{"migrate-up", "migrate-down", "migrate-status"} {
			m := regexp.MustCompile(`(?m)^` + target + `:.*\n((?:\t.*\n)+)`).FindStringSubmatch(string(data))
			if m == nil {
				t.Errorf("%s has no %s target with a recipe; the layout changed and this test is vacuous", name, target)
				continue
			}
			recipe := strings.TrimSpace(m[1])
			if !migrateCommand.MatchString(recipe) || strings.Contains(recipe, "goose") {
				t.Errorf("%s's %s runs %q, want ocms migrate: the goose CLI does not know the Go migrations", name, target, recipe)
			}
			if name == "Makefile" && !strings.Contains(recipe, `$(if $(DB_PATH),-db "$(DB_PATH)")`) {
				t.Errorf("the Makefile's %s runs %q, want -db only when DB_PATH is given", target, recipe)
			}
		}
	}
}
