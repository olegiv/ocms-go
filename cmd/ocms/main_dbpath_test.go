// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
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
