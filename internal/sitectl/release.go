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
	"slices"
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
// installed it, into the oldest release and makes it current, so the first
// install keeps it for rollback and names it as the release to go back to.
// The binary is linked into the release first: <Root>/bin/<Name> never goes
// missing.
func (a *App) adoptLegacy() error {
	info, err := os.Lstat(a.binary())
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	release := info.ModTime().UTC().Format(releaseStamp) + "-legacy"
	dir := filepath.Join(a.releasesDir(), release)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Link(a.binary(), filepath.Join(dir, a.Name)); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return a.switchTo(release)
}

// release checks that name is an installed release.
func (a *App) release(name string) error {
	releases, err := a.Releases()
	if err != nil {
		return err
	}
	if !slices.Contains(releases, name) {
		return fmt.Errorf("no release %q (see %s releases)", name, filepath.Base(a.ctl()))
	}
	return nil
}

// Install reads a new binary from r, verifies it against the SHA-256 computed
// where it was built, makes it the current release and restarts the sites on
// it. Nothing is stopped until the binary is verified and in place.
func (a *App) Install(ctx context.Context, r io.Reader, wantSHA string, sites []Site) error {
	tmp, sum, err := a.receive(r, wantSHA)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	release, err := a.newRelease(tmp, sum)
	if err != nil {
		return err
	}
	return a.Activate(ctx, release, sites)
}

// Stage reads and verifies a binary as Install does, and keeps it as
// <Root>/releases/.staged-<sha256> instead of making it a release. deploy
// --themes stages first, so a binary that does not arrive intact leaves the
// themes alone too. A staged binary is no release: rollback and pruning
// never see one that a failed deploy left behind, and the next Stage
// replaces it.
func (a *App) Stage(r io.Reader, wantSHA string) error {
	tmp, sum, err := a.receive(r, wantSHA)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	old, err := filepath.Glob(filepath.Join(a.releasesDir(), ".staged-*"))
	if err != nil {
		return err
	}
	for _, path := range old {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, a.stagedPath(sum)); err != nil {
		return err
	}
	if err := syncDir(a.releasesDir()); err != nil {
		return err
	}
	a.logf("Received binary %s, kept until it is installed", sum[:8])
	return nil
}

// PlaceStaged makes the binary Stage kept with the SHA-256 wantSHA a release
// and returns its name.
func (a *App) PlaceStaged(wantSHA string) (string, error) {
	if !sha256Re.MatchString(wantSHA) {
		return "", errors.New("--sha256 must be 64 hex digits")
	}
	want := strings.ToLower(wantSHA)
	path := a.stagedPath(want)
	if sum, err := fileSHA256(path); err != nil || sum != want {
		return "", fmt.Errorf("no binary with SHA-256 %s was staged", want)
	}
	return a.newRelease(path, want)
}

func (a *App) stagedPath(sum string) string {
	return filepath.Join(a.releasesDir(), ".staged-"+sum)
}

// receive reads a binary from r into a private file in releases/ and checks
// it against wantSHA and that it is a Linux executable. The file is on disk
// (synced, mode 0755) when it returns; the caller removes or renames it.
func (a *App) receive(r io.Reader, wantSHA string) (path, sum string, err error) {
	if !sha256Re.MatchString(wantSHA) {
		return "", "", errors.New("--sha256 must be 64 hex digits")
	}
	if err := os.MkdirAll(a.releasesDir(), 0o755); err != nil {
		return "", "", err
	}
	// Inside releases/: the rename into place must not cross filesystems.
	tmp, err := os.CreateTemp(a.releasesDir(), ".upload-*")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(r, maxBinaryBytes+1))
	if err = errors.Join(err, tmp.Sync(), tmp.Close()); err != nil {
		return "", "", fmt.Errorf("receiving the binary: %w", err)
	}
	if size > maxBinaryBytes {
		return "", "", fmt.Errorf("binary is larger than %d MiB", maxBinaryBytes>>20)
	}
	sum = hex.EncodeToString(hash.Sum(nil))
	if sum != strings.ToLower(wantSHA) {
		return "", "", fmt.Errorf("received %d bytes with SHA-256 %s, expected %s: nothing installed", size, sum, wantSHA)
	}
	if head, headErr := readHead(tmp.Name(), 4); headErr != nil || !bytes.Equal(head, []byte("\x7fELF")) {
		return "", "", errors.New("upload is not a Linux executable: nothing installed")
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return "", "", err
	}
	return tmp.Name(), sum, nil
}

// newRelease moves the verified binary at path into a new release directory
// and returns the release's name.
func (a *App) newRelease(path, sum string) (string, error) {
	release := a.Now().UTC().Format(releaseStamp) + "-" + sum[:8]
	dir := filepath.Join(a.releasesDir(), release)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(path, filepath.Join(dir, a.Name)); err != nil {
		return "", err
	}
	if err := errors.Join(syncDir(dir), syncDir(a.releasesDir())); err != nil {
		return "", err
	}
	return release, nil
}

// Activate makes an installed release current and restarts the sites on it.
func (a *App) Activate(ctx context.Context, release string, sites []Site) error {
	if err := a.release(release); err != nil {
		return err
	}
	if err := a.adoptLegacy(); err != nil {
		return err
	}
	previous := a.Current()
	if err := a.switchTo(release); err != nil {
		return err
	}
	a.logf("Installed release %s", release)

	err := a.Restart(ctx, sites)
	// Only a health-checked deployment may expire recovery releases. An
	// install without sites has not demonstrated that the binary works.
	if err == nil && len(sites) > 0 {
		if pruneErr := a.prune(); pruneErr != nil {
			a.logf("warning: could not remove old releases: %v", pruneErr)
		}
	}
	if err != nil && previous != "" {
		// No automatic rollback: the new binary may already have migrated a
		// database, and the old one is not guaranteed to run on it.
		a.logf("Release %s is still installed. To go back: %s rollback <site>... --to %s", release, filepath.Base(a.ctl()), previous)
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
	if err := a.release(release); err != nil {
		return err
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
			if err := os.RemoveAll(filepath.Join(a.releasesDir(), name)); err != nil {
				errs = append(errs, err)
				continue
			}
			a.logf("Removed release %s (%d are kept)", name, keepReleases)
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
