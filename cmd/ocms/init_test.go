// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
)

// TestParseCommand locks the back-compat dispatch invariant: bare invocations,
// flag-only invocations (systemd/Docker style), and unknown tokens must all
// route to serve; only "init"/"serve" are real subcommands.
func TestParseCommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCmd  string
		wantRest []string
	}{
		{"bare", nil, "serve", nil},
		{"version flag", []string{"-version"}, "serve", []string{"-version"}},
		{"help flag", []string{"-h"}, "serve", []string{"-h"}},
		{"serve", []string{"serve"}, "serve", nil},
		{"serve with flag", []string{"serve", "-version"}, "serve", []string{"-version"}},
		{"init", []string{"init", "my-site"}, "init", []string{"my-site"}},
		{"init with flag", []string{"init", "-force", "my-site"}, "init", []string{"-force", "my-site"}},
		{"unknown token", []string{"bogus"}, "serve", []string{"bogus"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotRest := parseCommand(tt.args)
			if gotCmd != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", gotCmd, tt.wantCmd)
			}
			if !slices.Equal(gotRest, tt.wantRest) {
				t.Errorf("rest = %v, want %v", gotRest, tt.wantRest)
			}
		})
	}
}

// TestRunInitCreatesScaffold verifies init creates the directory tree and a
// .env containing a usable session secret.
func TestRunInitCreatesScaffold(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "site")
	if err := runInit([]string{dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	for _, sub := range []string{"data", "uploads", "custom"} {
		info, err := os.Stat(filepath.Join(dir, sub))
		if err != nil || !info.IsDir() {
			t.Errorf("expected directory %s (err=%v)", sub, err)
		}
	}

	// The site root and .env hold credentials, so they must be owner-only.
	rootInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat site root: %v", err)
	}
	if perm := rootInfo.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("site root accessible by group/other: %#o (want owner-only)", perm)
	}

	envPath := filepath.Join(dir, ".env")
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat .env: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf(".env accessible by group/other: %#o (want owner-only)", perm)
	}

	secret := readEnvValue(t, envPath, "OCMS_SESSION_SECRET")
	if len(secret) < config.MinSessionSecretLength {
		t.Errorf("secret length %d < %d", len(secret), config.MinSessionSecretLength)
	}
}

// TestRunInitRefusesNonEmptyDir verifies init will not scaffold over an
// existing non-empty directory unless -force is given.
func TestRunInitRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir() // t.TempDir is empty; make it non-empty
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInit([]string{dir}); err == nil {
		t.Fatal("expected error for non-empty dir without -force")
	}
	if err := runInit([]string{"-force", dir}); err != nil {
		t.Fatalf("runInit -force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); err != nil {
		t.Errorf("expected .env after -force: %v", err)
	}
}

// TestRunInitTightensExistingRoot verifies init enforces owner-only perms on a
// pre-existing site root. os.MkdirAll leaves an existing directory's mode
// unchanged, so a user-created 0755 dir would otherwise expose the .env and DB
// under it.
func TestRunInitTightensExistingRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "site")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { // force 0755 regardless of umask
		t.Fatal(err)
	}

	if err := runInit([]string{dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("pre-existing root not tightened: %#o (want owner-only)", perm)
	}
}

// TestInitSecretPassesConfig is a drift test: a secret produced by init must
// always satisfy config.Load. It fails if MinSessionSecretLength or the
// secret-validation rules change without init keeping pace.
func TestInitSecretPassesConfig(t *testing.T) {
	secret, err := generateSessionSecret()
	if err != nil {
		t.Fatalf("generateSessionSecret: %v", err)
	}
	t.Setenv("OCMS_ENV", "development")
	t.Setenv("OCMS_SESSION_SECRET", secret)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load rejected init-generated secret: %v", err)
	}
	if cfg.SessionSecret != secret {
		t.Fatalf("config secret = %q, want %q", cfg.SessionSecret, secret)
	}
}

// readEnvValue returns the value for key from a .env-style file.
func readEnvValue(t *testing.T, path, key string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	prefix := key + "="
	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, prefix); ok {
			return v
		}
	}
	t.Fatalf("%s not found in %s", key, path)
	return ""
}
