// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Regression: unsuccessful uploads must not evict the only release whose
// restart and health gate have succeeded.
func TestFailedInstallsRetainLastHealthyRelease(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	if err := env.install(t, "healthy-v1", site); err != nil {
		t.Fatalf("install healthy baseline: %v", err)
	}
	healthyRelease := env.Current()
	health.set(false)
	for _, version := range []string{"failing-v2", "failing-v3", "failing-v4"} {
		env.now = env.now.Add(time.Minute)
		if err := env.install(t, version, site); err == nil {
			t.Fatalf("install %s unexpectedly passed the health gate", version)
		}
	}

	if _, err := os.Stat(filepath.Join(env.releasesDir(), healthyRelease, env.Name)); err != nil {
		releases, listErr := env.Releases()
		t.Fatalf("last healthy release %s was lost after failed installs: %v; remaining=%v (list error=%v)", healthyRelease, err, releases, listErr)
	}
	health.set(true)
	if err := env.Rollback(context.Background(), healthyRelease, []Site{site}); err != nil {
		t.Fatalf("cannot recover to last healthy release: %v", err)
	}
}

// Regression: an unsuccessful current backup must preserve the previous valid
// copy even when that copy has passed the normal retention cutoff.
func TestFailedSnapshotRetainsPreviousValidBackup(t *testing.T) {
	env := newTestEnv(t)
	dir := filepath.Join(env.SitesBase, "backup-regression")
	writeFiles(t, dir, map[string]string{
		env.DB.Default: "not a SQLite database",
	})
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(backups, env.Name+"_previous.db")
	createDB(t, previous)
	if err := checkIntegrity(context.Background(), previous); err != nil {
		t.Fatalf("previous backup is invalid: %v", err)
	}
	if err := gzipFile(previous, previous+".gz"); err != nil {
		t.Fatalf("compress previous valid backup: %v", err)
	}
	previous += ".gz"
	old := env.now.Add(-backupRetention - time.Hour)
	if err := os.Chtimes(previous, old, old); err != nil {
		t.Fatal(err)
	}

	if err := env.SiteOp(context.Background(), "backup", dir, nil, env.out); err == nil {
		t.Fatal("backup of corrupt current database unexpectedly succeeded")
	}
	if _, err := os.Stat(previous); err != nil {
		t.Fatalf("failed current snapshot removed previous valid backup: %v", err)
	}
}
