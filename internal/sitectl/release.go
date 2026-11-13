// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxBinaryBytes = 256 << 20
	keepReleases   = 3
	releaseStamp   = "20060102T150405Z"
)

var sha256Re = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// Releases lists the installed releases, oldest first. A release is a
// directory <Root>/releases/<UTC time>-<sha256 prefix>/ holding the binary;
// the name sorts by install time.
func (a *App) Releases() ([]string, error) {
	entries, err := os.ReadDir(a.releasesDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Current returns the release <Root>/bin/<Name> points to, or "" when it is
// not a release symlink.
func (a *App) Current() string {
	target, err := os.Readlink(a.binary())
	if err != nil {
		return ""
	}
	return filepath.Base(filepath.Dir(target))
}

// switchTo points <Root>/bin/<Name> at a release. The symlink is created
// beside the old one and renamed over it, so the path never goes missing and
// running processes keep the binary they started from.
func (a *App) switchTo(release string) error {
	if _, err := os.Stat(filepath.Join(a.releasesDir(), release, a.Name)); err != nil {
		return fmt.Errorf("release %s has no binary: %w", release, err)
	}
	if err := os.MkdirAll(a.binDir(), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(a.binDir(), "."+a.Name+".new")
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Join("..", "releases", release, a.Name), tmp); err != nil {
		return err
	}
	return os.Rename(tmp, a.binary())
}

// adoptLegacy turns a plain binary at <Root>/bin/<Name>, as the shell scripts
// installed it, into the oldest release, so the first install keeps it for
// rollback.
func (a *App) adoptLegacy() error {
	info, err := os.Lstat(a.binary())
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	dir := filepath.Join(a.releasesDir(), info.ModTime().UTC().Format(releaseStamp)+"-legacy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.Rename(a.binary(), filepath.Join(dir, a.Name))
}

// Install reads a new binary from r, verifies it against the SHA-256 computed
// where it was built, makes it the current release and restarts the sites on
// it. Nothing is stopped until the binary is verified and in place.
func (a *App) Install(ctx context.Context, r io.Reader, wantSHA string, sites []Site) error {
	if !sha256Re.MatchString(wantSHA) {
		return errors.New("--sha256 must be 64 hex digits")
	}
	if err := os.MkdirAll(a.releasesDir(), 0o755); err != nil {
		return err
	}
	// Staged inside releases/: the rename below must not cross filesystems.
	tmp, err := os.CreateTemp(a.releasesDir(), ".upload-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(r, maxBinaryBytes+1))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("receiving the binary: %w", err)
	}
	if size > maxBinaryBytes {
		return fmt.Errorf("binary is larger than %d MiB", maxBinaryBytes>>20)
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	if sum != strings.ToLower(wantSHA) {
		return fmt.Errorf("received %d bytes with SHA-256 %s, expected %s: nothing installed", size, sum, wantSHA)
	}
	if head, err := readHead(tmp.Name(), 4); err != nil || !bytes.Equal(head, []byte("\x7fELF")) {
		return errors.New("upload is not a Linux executable: nothing installed")
	}

	release := a.Now().UTC().Format(releaseStamp) + "-" + sum[:8]
	dir := filepath.Join(a.releasesDir(), release)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, a.Name)); err != nil {
		return err
	}
	if err := a.adoptLegacy(); err != nil {
		return err
	}
	previous := a.Current()
	if err := a.switchTo(release); err != nil {
		return err
	}
	a.logf("Installed release %s (%d bytes)", release, size)

	err = a.Restart(ctx, sites)
	if pruneErr := a.prune(); pruneErr != nil {
		a.logf("warning: could not remove old releases: %v", pruneErr)
	}
	if err != nil && previous != "" {
		// No automatic rollback: the new binary may already have migrated a
		// database, and the old one is not guaranteed to run on it.
		a.logf("Release %s is still installed. To go back: %s rollback %s <site>...", release, filepath.Base(a.ctl()), previous)
	}
	return err
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- the upload this process just wrote
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, n)
	_, err = io.ReadFull(f, head)
	return head, err
}

// Rollback makes an earlier release current and restarts the sites on it.
// With no release named, it is the one installed before the current one.
func (a *App) Rollback(ctx context.Context, release string, sites []Site) error {
	releases, err := a.Releases()
	if err != nil {
		return err
	}
	current := a.Current()
	if release == "" {
		for _, name := range releases {
			if name < current {
				release = name
			}
		}
		if release == "" {
			return fmt.Errorf("no release older than the current one (%s)", current)
		}
	}
	if err := a.switchTo(release); err != nil {
		return err
	}
	a.logf("Current release is now %s (was %s)", release, current)
	return a.Restart(ctx, sites)
}

// prune removes all but the newest releases; the current one always stays.
func (a *App) prune() error {
	releases, err := a.Releases()
	if err != nil {
		return err
	}
	current := a.Current()
	var errs []error
	for i, name := range releases {
		if i < len(releases)-keepReleases && name != current {
			errs = append(errs, os.RemoveAll(filepath.Join(a.releasesDir(), name)))
		}
	}
	return errors.Join(errs...)
}

// PrintReleases lists the releases and marks the current one.
func (a *App) PrintReleases() error {
	releases, err := a.Releases()
	if err != nil {
		return err
	}
	current := a.Current()
	for _, name := range releases {
		mark := " "
		if name == current {
			mark = "*"
		}
		a.logf("%s %s", mark, name)
	}
	return nil
}
