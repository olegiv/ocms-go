// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests in this file use only the public RFC seed, a memory credential store and
// private temporary directories (fixture redirects TMPDIR); none can reach the
// real Keychain or a real enrollment.

// newDirectory creates a directory with the given entries, mode and age. The
// modification time is set last because creating entries updates it.
func newDirectory(t *testing.T, path string, mode os.FileMode, age time.Duration, entries ...string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range entries {
		if err := os.WriteFile(filepath.Join(path, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	modified := time.Now().Add(-age)
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func issueFresh(t *testing.T, s *authenticator, target authTarget) codeResult {
	t.Helper()
	s.now = func() time.Time { return time.Unix(1234567890, 0) }
	result, err := s.issue(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(result.directory) })
	return result
}

func TestIssuePrunesOnlyStaleCodeDirectories(t *testing.T) {
	s, target, _ := fixture(t)
	if err := s.importSeed(t.Context(), target, fixtureSeed); err != nil {
		t.Fatal(err)
	}
	root := os.TempDir()
	const old = 10 * time.Minute
	removed := []string{
		filepath.Join(root, "ocms-dev-auth-1001"), // abandoned directory with its code file
		filepath.Join(root, "ocms-dev-auth-1002"), // abandoned empty directory
	}
	newDirectory(t, removed[0], 0o700, old, "code")
	newDirectory(t, removed[1], 0o700, old)

	// Everything below belongs to somebody else or is not yet abandoned.
	kept := map[string]string{ // path -> a file in it that must survive, if any
		filepath.Join(root, "ocms-dev-auth-1003"):   "code",  // fresh: a consumer may still read it
		filepath.Join(root, "ocms-dev-auth-1004"):   "code",  // not private (0755)
		filepath.Join(root, "ocms-dev-auth-1005"):   "extra", // holds something besides the code file
		filepath.Join(root, "ocms-dev-auth-1006"):   "code",  // modification time in the future
		filepath.Join(root, "ocms-dev-auth-abc"):    "code",  // name is not the generated form
		filepath.Join(root, "ocms-dev-auth-"):       "code",
		filepath.Join(root, "ocms-dev-auth-12x"):    "code",
		filepath.Join(root, "ocms-dev-auth-1-more"): "code",
		filepath.Join(root, "other-1007"):           "code",
	}
	newDirectory(t, filepath.Join(root, "ocms-dev-auth-1003"), 0o700, 30*time.Second, "code")
	newDirectory(t, filepath.Join(root, "ocms-dev-auth-1004"), 0o755, old, "code")
	newDirectory(t, filepath.Join(root, "ocms-dev-auth-1005"), 0o700, old, "code", "extra")
	newDirectory(t, filepath.Join(root, "ocms-dev-auth-1006"), 0o700, -time.Hour, "code")
	for _, name := range []string{"ocms-dev-auth-abc", "ocms-dev-auth-", "ocms-dev-auth-12x", "ocms-dev-auth-1-more", "other-1007"} {
		newDirectory(t, filepath.Join(root, name), 0o700, old, "code")
	}

	// A regular file with a generated name, and symlinks that must never be
	// followed: one to a stale private directory, one used as the code file.
	if err := os.WriteFile(filepath.Join(root, "ocms-dev-auth-1008"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(t.TempDir(), "decoy")
	newDirectory(t, decoy, 0o700, old, "code")
	if err := os.Symlink(decoy, filepath.Join(root, "ocms-dev-auth-1009")); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "ocms-dev-auth-1010")
	newDirectory(t, linked, 0o700, old)
	if err := os.Symlink(victim, filepath.Join(linked, "code")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(linked, time.Now().Add(-old), time.Now().Add(-old)); err != nil {
		t.Fatal(err)
	}

	result := issueFresh(t, s, target)

	for _, path := range removed {
		if exists(path) {
			t.Errorf("stale code directory %s survived the sweep", filepath.Base(path))
		}
	}
	for path, file := range kept {
		if !exists(filepath.Join(path, file)) {
			t.Errorf("%s was removed by the sweep", filepath.Base(path))
		}
	}
	for _, path := range []string{
		filepath.Join(root, "ocms-dev-auth-1008"), filepath.Join(root, "ocms-dev-auth-1009"),
		filepath.Join(decoy, "code"), linked, filepath.Join(linked, "code"), victim,
		result.directory, result.CodeFile,
	} {
		if !exists(path) {
			t.Errorf("%s was removed by the sweep", strings.TrimPrefix(path, root))
		}
	}
}

func TestPruneRemovesDirectoriesTheToolCreated(t *testing.T) {
	s, target, _ := fixture(t)
	if err := s.importSeed(t.Context(), target, fixtureSeed); err != nil {
		t.Fatal(err)
	}
	result := issueFresh(t, s, target)
	if !isCodeDirName(filepath.Base(result.directory)) {
		t.Fatal("the sweep does not match the directory names the tool generates")
	}
	pruneStaleCodeDirectories(os.TempDir(), time.Now())
	if !exists(result.CodeFile) {
		t.Fatal("a fresh code file was pruned")
	}
	pruneStaleCodeDirectories(os.TempDir(), time.Now().Add(staleCodeAge+time.Second))
	if exists(result.directory) {
		t.Fatal("an abandoned code directory created by the tool survived the sweep")
	}
}

func TestOwnershipCheckRejectsOtherUsers(t *testing.T) {
	info, err := os.Lstat(t.TempDir())
	if err != nil || !ownedByCurrentUser(info) {
		t.Fatal("the caller's own directory was not recognised")
	}
	if os.Geteuid() == 0 {
		t.Skip("the root directory is owned by the calling user")
	}
	root, err := os.Lstat(string(filepath.Separator))
	if err != nil || ownedByCurrentUser(root) {
		t.Fatal("a directory owned by another user was accepted")
	}
}

func TestStateReservationIsReplacedAtomically(t *testing.T) {
	s, target, _ := fixture(t)
	if err := s.importSeed(t.Context(), target, fixtureSeed); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1234567890, 0)
	s.now = func() time.Time { return now }
	s.wait = func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }
	first, err := s.issue(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(first.directory) })
	stateDir := filepath.Join(s.cache, "ocms-dev-auth", target.id())
	statePath := filepath.Join(stateDir, "last-step")
	// A second name for the file keeps whatever the first inode contains: an
	// in-place truncate-and-write would show through it, a rename would not.
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Link(statePath, alias); err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(alias)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.issue(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(second.directory) })

	current, err := os.ReadFile(alias)
	if err != nil || !bytes.Equal(current, previous) {
		t.Fatal("the reservation was rewritten in place instead of being replaced")
	}
	step, err := readLastStep(statePath)
	if err != nil || step != now.Unix()/30 {
		t.Fatal("the new reservation was not stored")
	}
	info, err := os.Stat(statePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("the reservation is not private")
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "lock" && entry.Name() != "last-step" {
			t.Errorf("unexpected leftover %s in the state directory", entry.Name())
		}
	}
}

func TestAtomicWriteFailureKeepsNothingBehind(t *testing.T) {
	directory := t.TempDir()
	// Renaming a file over a non-empty directory fails after the temporary file
	// was written, which must still not leave it behind.
	target := filepath.Join(directory, "state")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFileAtomic(target, []byte("1")); err == nil {
		t.Fatal("replacing a directory was reported as success")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary file left behind after a failed write: %v", err)
	}
	if err := writePrivateFileAtomic(filepath.Join(directory, "missing", "state"), []byte("1")); err == nil {
		t.Fatal("writing into a missing directory was reported as success")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func codeFixture(t *testing.T) codeResult {
	t.Helper()
	directory := t.TempDir()
	file := filepath.Join(directory, "code")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return codeResult{CodeFile: file, directory: directory}
}

func TestDeliverRemovesCodeNobodyWillCollect(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	// The run was cancelled after the code was written: report nothing, delete it.
	result := codeFixture(t)
	var output bytes.Buffer
	if err := deliver(cancelled, &output, result); err == nil || output.Len() != 0 || exists(result.directory) {
		t.Fatal("a cancelled run delivered or kept its code directory")
	}
	// The consumer cannot be told about the file.
	result = codeFixture(t)
	if err := deliver(t.Context(), failingWriter{}, result); err == nil || exists(result.directory) {
		t.Fatal("a failed delivery kept its code directory")
	}
	// Normal delivery keeps the file for the consumer.
	result = codeFixture(t)
	output.Reset()
	if err := deliver(t.Context(), &output, result); err != nil || !strings.Contains(output.String(), result.CodeFile) || !exists(result.CodeFile) {
		t.Fatal("a successful delivery did not hand over the code file")
	}
	// Only code results own a directory; other results are unaffected.
	output.Reset()
	if err := deliver(cancelled, &output, statusResult{Configured: true}); err != nil || output.Len() == 0 {
		t.Fatal("a status result was dropped")
	}
}
