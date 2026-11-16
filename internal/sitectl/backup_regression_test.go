// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// backupEnv is a registered site with a database, an executable alert hook
// and old archives of every kind the backup writes.
func backupEnv(t *testing.T) (*testEnv, Site, string) {
	t.Helper()
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	createDB(t, filepath.Join(site.Dir, env.DBPath))
	if err := os.WriteFile(filepath.Join(env.ConfDir, "alert"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(site.Dir, "backups")
	old := env.now.Add(-backupRetention - time.Hour)
	for _, name := range []string{"app_20261001_030000.db.gz", "uploads_20261001_030000.tar.gz", "custom_20261001_030000.tar.gz"} {
		writeFiles(t, backups, map[string]string{name: "old"})
		if err := os.Chtimes(filepath.Join(backups, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	return env, site, backups
}

func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// requireFailedBackup checks that the run failed, kept every old archive and
// told the alert hook.
func requireFailedBackup(t *testing.T, env *testEnv, site Site, backups string, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("backup error = %v, want one about %q", err, want)
	}
	for _, name := range []string{"app_20261001_030000.db.gz", "uploads_20261001_030000.tar.gz", "custom_20261001_030000.tar.gz"} {
		if _, err := os.Stat(filepath.Join(backups, name)); err != nil {
			t.Errorf("a failed backup removed %s: %v", name, err)
		}
	}
	hook := filepath.Join(env.ConfDir, "alert") + " " + site.ID + " BACKUP FAILED "
	if !slices.ContainsFunc(env.host.calls, func(call string) bool { return strings.HasPrefix(call, hook) }) {
		t.Errorf("the alert hook was not told; calls: %v", env.host.calls)
	}
}

func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads files regardless of their mode")
	}
}

func TestBackupFailsWhenTheDatabaseIsMissing(t *testing.T) {
	env, site, backups := backupEnv(t)
	env.DBPathEnv = "OCMS_DB_PATH"
	writeFiles(t, site.Dir, map[string]string{".env": "OCMS_DB_PATH=./data/typo.db\n"})
	err := env.Backup(context.Background(), []Site{site})
	requireFailedBackup(t, env, site, backups, err, "typo.db")
}

func TestBackupFailsWhenContentCannotBeRead(t *testing.T) {
	skipAsRoot(t)
	env, site, backups := backupEnv(t)
	writeFiles(t, site.Dir, map[string]string{"uploads/2026/photo.jpg": "jpeg"})
	locked := filepath.Join(site.Dir, "uploads", "2026")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	err := env.Backup(context.Background(), []Site{site})
	requireFailedBackup(t, env, site, backups, err, "uploads")
}

func TestBackupLeavesNoPartialArchive(t *testing.T) {
	skipAsRoot(t)
	env, site, backups := backupEnv(t)
	// a.jpg makes the directory count as content; b.jpg fails mid-archive.
	writeFiles(t, site.Dir, map[string]string{"uploads/a.jpg": "jpeg", "uploads/b.jpg": "jpeg"})
	unreadable := filepath.Join(site.Dir, "uploads", "b.jpg")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	err := env.Backup(context.Background(), []Site{site})
	requireFailedBackup(t, env, site, backups, err, "uploads")
	for _, name := range entryNames(t, backups) {
		if strings.HasPrefix(name, ".partial-") || name == "uploads_20261113_100000.tar.gz" {
			t.Errorf("a failed archive left %s behind", name)
		}
	}
}

func TestBackupRefusesAMissingSiteDirectory(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	if err := os.RemoveAll(site.Dir); err != nil {
		t.Fatal(err)
	}
	if err := env.Backup(context.Background(), []Site{site}); err == nil {
		t.Error("backup of a missing site directory succeeded")
	}
	if _, err := os.Lstat(site.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backup recreated the site directory: %v", err)
	}
}

func TestBackupArchivesASymlinkedContentDirectory(t *testing.T) {
	env, site, backups := backupEnv(t)
	store := filepath.Join(env.SitesBase, "store")
	writeFiles(t, store, map[string]string{"2026/photo.jpg": "jpeg"})
	if err := os.Symlink(store, filepath.Join(site.Dir, "uploads")); err != nil {
		t.Fatal(err)
	}
	if err := env.Backup(context.Background(), []Site{site}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(backups, "uploads_20261113_100000.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var names []string
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	if !slices.Contains(names, "uploads/2026/photo.jpg") {
		t.Errorf("the uploads archive holds %v, want the linked directory's files", names)
	}
}

func TestBackupExpiresOnlyKindsItWrote(t *testing.T) {
	env, site, backups := backupEnv(t)
	// No uploads and no custom content: their old archives are all there is.
	if err := env.Backup(context.Background(), []Site{site}); err != nil {
		t.Fatal(err)
	}
	want := []string{"app_20261113_100000.db.gz", "custom_20261001_030000.tar.gz", "uploads_20261001_030000.tar.gz"}
	if got := entryNames(t, backups); !slices.Equal(got, want) {
		t.Errorf("backups/ holds %v, want %v", got, want)
	}
}
