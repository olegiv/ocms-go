// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package demo

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestResetIfNeeded_MissingTimestamp(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "test.db")
	uploadsDir := filepath.Join(dataDir, "uploads")

	// Create a DB file and uploads
	if err := os.WriteFile(dbPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(uploadsDir, "originals"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploadsDir, "originals", "img.png"), []byte("img"), 0644); err != nil {
		t.Fatal(err)
	}

	// No .last_reset file — should trigger reset
	if err := ResetIfNeeded(dbPath, uploadsDir, dataDir); err != nil {
		t.Fatalf("ResetIfNeeded() error = %v", err)
	}

	// DB should be deleted
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Error("expected DB file to be deleted")
	}

	// Uploads should be cleared
	entries, _ := os.ReadDir(uploadsDir)
	if len(entries) != 0 {
		t.Errorf("expected uploads dir to be empty, got %d entries", len(entries))
	}

	// Timestamp file should exist
	tsPath := filepath.Join(dataDir, timestampFile)
	if _, err := os.Stat(tsPath); err != nil {
		t.Errorf("expected timestamp file to exist: %v", err)
	}
}

func TestResetIfNeeded_StaleTimestamp(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "test.db")
	uploadsDir := filepath.Join(dataDir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a DB file
	if err := os.WriteFile(dbPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a stale timestamp (25 hours ago)
	stale := time.Now().Add(-25 * time.Hour).Unix()
	tsPath := filepath.Join(dataDir, timestampFile)
	if err := os.WriteFile(tsPath, []byte(strconv.FormatInt(stale, 10)), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ResetIfNeeded(dbPath, uploadsDir, dataDir); err != nil {
		t.Fatalf("ResetIfNeeded() error = %v", err)
	}

	// DB should be deleted
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Error("expected DB file to be deleted after stale timestamp")
	}
}

func TestResetIfNeeded_FreshTimestamp(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "test.db")
	uploadsDir := filepath.Join(dataDir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a DB file
	if err := os.WriteFile(dbPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a fresh timestamp (1 hour ago)
	fresh := time.Now().Add(-1 * time.Hour).Unix()
	tsPath := filepath.Join(dataDir, timestampFile)
	if err := os.WriteFile(tsPath, []byte(strconv.FormatInt(fresh, 10)), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ResetIfNeeded(dbPath, uploadsDir, dataDir); err != nil {
		t.Fatalf("ResetIfNeeded() error = %v", err)
	}

	// DB should NOT be deleted
	if _, err := os.Stat(dbPath); err != nil {
		t.Error("expected DB file to still exist with fresh timestamp")
	}
}

func TestReset_DeletesDBFiles(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "test.db")
	uploadsDir := filepath.Join(dataDir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create DB files (main, WAL, SHM)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.WriteFile(dbPath+suffix, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := Reset(dbPath, uploadsDir, dataDir); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}

	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); !os.IsNotExist(err) {
			t.Errorf("expected %s to be deleted", dbPath+suffix)
		}
	}
}

func TestReset_ClearsUploads(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "test.db")
	uploadsDir := filepath.Join(dataDir, "uploads")

	// Create nested upload structure
	dirs := []string{
		filepath.Join(uploadsDir, "originals", "uuid1"),
		filepath.Join(uploadsDir, "thumbnail", "uuid1"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "img.png"), []byte("img"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := Reset(dbPath, uploadsDir, dataDir); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}

	// Uploads dir should exist but be empty
	info, err := os.Stat(uploadsDir)
	if err != nil {
		t.Fatalf("uploads dir should still exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("uploads should be a directory")
	}

	entries, _ := os.ReadDir(uploadsDir)
	if len(entries) != 0 {
		t.Errorf("expected uploads dir to be empty, got %d entries", len(entries))
	}
}

func TestReset_WritesTimestamp(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "test.db")
	uploadsDir := filepath.Join(dataDir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		t.Fatal(err)
	}

	before := time.Now().Unix()
	if err := Reset(dbPath, uploadsDir, dataDir); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	after := time.Now().Unix()

	data, err := os.ReadFile(filepath.Join(dataDir, timestampFile))
	if err != nil {
		t.Fatalf("failed to read timestamp file: %v", err)
	}

	ts, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		t.Fatalf("failed to parse timestamp: %v", err)
	}

	if ts < before || ts > after {
		t.Errorf("timestamp %d not in expected range [%d, %d]", ts, before, after)
	}
}

func TestReset_NonexistentUploadsDir(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "test.db")
	uploadsDir := filepath.Join(dataDir, "nonexistent")

	// Should not error on missing uploads dir
	if err := Reset(dbPath, uploadsDir, dataDir); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
}
