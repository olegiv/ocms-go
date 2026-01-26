// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package testutil provides shared test helpers for the oCMS project.
package testutil

import (
	"database/sql"
	"log/slog"
	"os"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"

	_ "github.com/mattn/go-sqlite3"
)

// TestLogger creates a silent test logger that only outputs warnings and errors.
func TestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
}

// TestLoggerSilent creates a completely silent test logger (error level only).
func TestLoggerSilent() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))
}

// TestDB creates a temporary test database with core migrations applied.
// Returns the database and a cleanup function that should be deferred.
func TestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	f, err := os.CreateTemp("", "ocms-test-*.db")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	dbPath := f.Name()
	_ = f.Close()

	db, err := store.NewDB(dbPath)
	if err != nil {
		_ = os.Remove(dbPath)
		t.Fatalf("NewDB: %v", err)
	}

	if err := store.Migrate(db); err != nil {
		_ = db.Close()
		_ = os.Remove(dbPath)
		t.Fatalf("Migrate: %v", err)
	}

	return db, func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}
}

// TestMemoryDB creates an in-memory SQLite database for testing.
// Useful for tests that don't need persistent storage or migrations.
func TestMemoryDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	return db
}
