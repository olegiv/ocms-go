// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/olegiv/ocms-go/internal/store"
)

// migrateCommands are the goose commands `ocms migrate` runs.
var migrateCommands = []string{"up", "down", "status", "version"}

// runMigrate runs the database migrations without starting the server. It
// knows the Go migrations, which the goose CLI does not: run against a
// database that has applied one, `goose down` fails, and `goose up` can apply
// newer SQL migrations first, after which the server refuses to start over
// the "missing" Go migration. The Makefile's migrate targets use it. Like
// serve, it reads .env or the file OCMS_ENV_FILE names, so it migrates the
// database the server opens unless -db names another. Only up creates a
// database: the other commands refuse a path where there is none, which a
// mistyped path would otherwise become.
func runMigrate(args []string) error {
	if err := loadEnvFile(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	defaultPath := os.Getenv("OCMS_DB_PATH")
	if defaultPath == "" {
		defaultPath = "./data/ocms.db"
	}
	dbPath := fs.String("db", defaultPath, "SQLite database path; OCMS_DB_PATH from the environment or env file, else ./data/ocms.db")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: ocms migrate [options] %s\n\n", strings.Join(migrateCommands, "|"))
		_, _ = fmt.Fprintf(os.Stderr, "Run the database migrations without starting the server.\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 || !slices.Contains(migrateCommands, fs.Arg(0)) {
		fs.Usage()
		return fmt.Errorf("migrate takes one command: %s", strings.Join(migrateCommands, ", "))
	}
	command := fs.Arg(0)
	if command != "up" {
		target := *dbPath
		if path, ok := sqliteFilePathFromDSN(target); ok {
			target = path // a file: URI names its file without the scheme and query
		}
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("no database to %s at %s: %w", command, *dbPath, err)
		}
	}
	slog.Info("running migrations", "command", command, "db", *dbPath)
	db, err := store.NewDB(*dbPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer func() { _ = db.Close() }()
	return store.RunMigrationCommand(context.Background(), db, command)
}
