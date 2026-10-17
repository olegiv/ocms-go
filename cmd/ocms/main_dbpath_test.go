// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteFilePathFromDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dsn  string
		want string
		ok   bool
	}{
		{name: "plain path", dsn: "./data/ocms.db", want: "./data/ocms.db", ok: true},
		{name: "memory dsn", dsn: ":memory:", want: "", ok: false},
		{name: "file memory uri", dsn: "file::memory:?cache=shared", want: "", ok: false},
		{name: "file memory mode uri", dsn: "file:ignored.db?mode=memory&cache=shared", want: "", ok: false},
		{name: "file absolute uri", dsn: "file:/var/db/ocms.db", want: "/var/db/ocms.db", ok: true},
		{name: "file relative uri", dsn: "file:./data/ocms.db", want: "./data/ocms.db", ok: true},
		{name: "file escaped uri", dsn: "file:/tmp/ocms%20prod.db", want: "/tmp/ocms prod.db", ok: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := sqliteFilePathFromDSN(tc.dsn)
			if ok != tc.ok {
				t.Fatalf("ok mismatch: got %v want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Fatalf("path mismatch: got %q want %q", got, tc.want)
			}
		})
	}
}

// TestOpenErrorLog checks the optional error log: no file without a path,
// and owner-only permissions even on a file that already existed with a
// wider mode (OpenFile applies the mode only when it creates the file).
func TestOpenErrorLog(t *testing.T) {
	t.Parallel()

	if file, err := openErrorLog(""); file != nil || err != nil {
		t.Fatalf("openErrorLog(\"\") = %v, %v; want no file", file, err)
	}

	logPath := filepath.Join(t.TempDir(), "error.log")
	if err := os.WriteFile(logPath, []byte("earlier\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// WriteFile's mode is filtered by the umask; set it outright.
	if err := os.Chmod(logPath, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := openErrorLog(logPath)
	if err != nil {
		t.Fatalf("openErrorLog: %v", err)
	}
	defer closeErrorLog(file)
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("error log mode = %v, want 0600", mode)
	}
	if _, err := openErrorLog(filepath.Join(t.TempDir(), "missing", "error.log")); err == nil {
		t.Error("openErrorLog succeeded in a directory that does not exist")
	}
}

// TestWithErrorLog checks that the error log receives ERROR records only,
// while every record still reaches the base handler.
func TestWithErrorLog(t *testing.T) {
	t.Parallel()

	file, err := openErrorLog(filepath.Join(t.TempDir(), "error.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeErrorLog(file)
	var base strings.Builder
	logger := slog.New(withErrorLog(slog.NewTextHandler(&base, nil), file))
	logger.Info("routine startup")
	logger.Error("disk full")

	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, "disk full") || strings.Contains(got, "routine startup") {
		t.Errorf("error log = %q, want only the ERROR record", got)
	}
	if !strings.Contains(base.String(), "routine startup") || !strings.Contains(base.String(), "disk full") {
		t.Errorf("base handler = %q, want both records", base.String())
	}
	if withErrorLog(slog.NewTextHandler(&base, nil), nil) == nil {
		t.Error("withErrorLog without a file returned no handler")
	}
}

// TestOpenDatabase checks that the database file and its WAL and
// shared-memory files end up owner-only even when the database already
// existed with a wider mode (SQLite gives the sidecars that mode on open),
// and that migrations ran.
func TestOpenDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ocms.db")
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		t.Fatalf("openDatabase: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v (the database runs in WAL mode, so the file should exist)", filepath.Base(path), err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %v, want 0600", filepath.Base(path), mode)
		}
	}
	var pages int
	if err := db.QueryRow("SELECT COUNT(*) FROM pages").Scan(&pages); err != nil {
		t.Errorf("migrations did not run: %v", err)
	}
}

// TestLogRunFailure checks that a run failure is logged once, by the first
// deferred call to see it, and marked so main does not log it again after
// run's deferred closes have shut the error log file and the event log.
func TestLogRunFailure(t *testing.T) {
	var buf strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	runErr := errors.New("running migrations: disk I/O error")
	logRunFailure(&runErr)
	logRunFailure(&runErr)
	if n := strings.Count(buf.String(), "application exited with error"); n != 1 {
		t.Errorf("failure logged %d times, want once:\n%s", n, buf.String())
	}
	var logged loggedRunError
	if !errors.As(runErr, &logged) || !strings.Contains(runErr.Error(), "disk I/O error") {
		t.Errorf("runErr = %v, want the original error marked as logged", runErr)
	}

	var none error
	logRunFailure(&none)
	if none != nil || strings.Count(buf.String(), "application exited with error") != 1 {
		t.Error("a nil error was logged or wrapped")
	}
}
