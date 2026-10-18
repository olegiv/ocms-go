// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
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
// the "missing" Go migration. The Makefile's migrate targets use it.
func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	defaultPath := os.Getenv("OCMS_DB_PATH")
	if defaultPath == "" {
		defaultPath = "./data/ocms.db"
	}
	dbPath := fs.String("db", defaultPath, "SQLite database path (default: OCMS_DB_PATH or ./data/ocms.db)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: ocms migrate [options] %s\n\n", strings.Join(migrateCommands, "|"))
		_, _ = fmt.Fprintf(os.Stderr, "Run the database migrations without starting the server.\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || !slices.Contains(migrateCommands, fs.Arg(0)) {
		fs.Usage()
		return fmt.Errorf("migrate takes one command: %s", strings.Join(migrateCommands, ", "))
	}
	db, err := store.NewDB(*dbPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer func() { _ = db.Close() }()
	return store.RunMigrationCommand(context.Background(), db, fs.Arg(0))
}
