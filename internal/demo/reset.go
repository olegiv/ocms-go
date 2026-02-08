// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package demo

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// timestampFile is the name of the file storing the last reset time.
	timestampFile = ".last_reset"

	// resetInterval is how often the demo data should be refreshed.
	resetInterval = 24 * time.Hour
)

// ResetIfNeeded checks the last reset timestamp and performs a full reset
// if more than 24 hours have passed. This is the startup check for when
// the Fly.io machine was stopped overnight and starts on first request.
func ResetIfNeeded(dbPath, uploadsDir, dataDir string) error {
	tsPath := filepath.Join(dataDir, timestampFile)

	data, err := os.ReadFile(tsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading reset timestamp: %w", err)
	}

	if err == nil {
		unixSec, parseErr := strconv.ParseInt(string(data), 10, 64)
		if parseErr == nil {
			lastReset := time.Unix(unixSec, 0)
			if time.Since(lastReset) < resetInterval {
				slog.Info("demo reset not needed",
					"last_reset", lastReset.UTC().Format(time.RFC3339),
					"next_reset", lastReset.Add(resetInterval).UTC().Format(time.RFC3339),
				)
				return nil
			}
		}
	}

	slog.Info("demo reset overdue, resetting database and uploads")
	return Reset(dbPath, uploadsDir, dataDir)
}

// Reset performs a full demo data reset: deletes the database files,
// clears all uploaded files, and writes a fresh reset timestamp.
func Reset(dbPath, uploadsDir, dataDir string) error {
	// Delete database files (main, WAL, SHM)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", dbPath+suffix, err)
		}
	}
	slog.Info("demo database deleted", "path", dbPath)

	// Clear uploads directory contents but keep the directory itself
	if err := clearDir(uploadsDir); err != nil {
		return fmt.Errorf("clearing uploads: %w", err)
	}
	slog.Info("demo uploads cleared", "path", uploadsDir)

	// Write fresh reset timestamp
	if err := writeTimestamp(dataDir); err != nil {
		return fmt.Errorf("writing reset timestamp: %w", err)
	}

	slog.Info("demo reset complete")
	return nil
}

// clearDir removes all files and subdirectories inside dir,
// but keeps the directory itself.
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("removing %s: %w", path, err)
		}
	}
	return nil
}

// writeTimestamp writes the current UTC unix timestamp to the data directory.
func writeTimestamp(dataDir string) error {
	tsPath := filepath.Join(dataDir, timestampFile)
	data := []byte(strconv.FormatInt(time.Now().UTC().Unix(), 10))
	return os.WriteFile(tsPath, data, 0644)
}
