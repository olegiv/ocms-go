// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package imaging

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/olegiv/ocms-go/internal/model"
)

// IsCanonicalMediaUUID reports whether value is a canonical hyphenated UUID.
// It is deliberately stricter than uuid.Parse, which also accepts unhyphenated
// and URN forms. Media UUIDs become directory names, so deletion accepts one
// unambiguous representation only.
func IsCanonicalMediaUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		c := value[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

// DeleteMediaFiles removes every directory that can contain files for one
// media UUID. The UUID and each joined path are validated before removal.
//
// Every directory is attempted even after a failure. Returning errors.Join of
// all failures prevents one unwritable variant from hiding additional orphaned
// directories and gives callers enough information to retain a durable retry.
func DeleteMediaFiles(uploadDir, mediaUUID string) error {
	return deleteMediaFilesWithPolicy(uploadDir, mediaUUID, false, (*os.Root).RemoveAll)
}

// DeleteMediaFilesFromCanonicalRoot removes media files only when uploadDir is
// still the canonical, non-symlink root recorded by a durable cleanup queue.
// The equality check happens inside the same capability-opening operation as
// deletion, so replacing the queued directory with a symlink between an outer
// validation and this call cannot retarget cleanup outside the persisted root.
func DeleteMediaFilesFromCanonicalRoot(uploadDir, mediaUUID string) error {
	return deleteMediaFilesWithPolicy(uploadDir, mediaUUID, true, (*os.Root).RemoveAll)
}

// DeleteMediaFilesFromRoot removes one media UUID through an already verified
// caller-owned root capability. Keeping the capability open binds cleanup to
// the directory that received the writes even if its canonical pathname is
// renamed and replaced before compensation runs.
func DeleteMediaFilesFromRoot(uploadRoot *os.Root, mediaUUID string) error {
	if uploadRoot == nil {
		return errors.New("uploads root is nil")
	}
	if !IsCanonicalMediaUUID(mediaUUID) {
		return fmt.Errorf("invalid media UUID %q", mediaUUID)
	}
	return deleteMediaFilesFromOpenRoot(uploadRoot, mediaUUID, (*os.Root).RemoveAll)
}

// ValidateOpenUploadRootIdentity proves that an open capability still names
// the directory reachable at canonicalRoot. Call this immediately before any
// operation that must use ordinary canonical paths rather than root-relative
// access, such as image variant generation.
func ValidateOpenUploadRootIdentity(uploadRoot *os.Root, canonicalRoot string) error {
	if uploadRoot == nil {
		return errors.New("uploads root is nil")
	}
	if canonicalRoot == "" || !filepath.IsAbs(canonicalRoot) || filepath.Clean(canonicalRoot) != canonicalRoot {
		return fmt.Errorf("invalid canonical uploads root %q", canonicalRoot)
	}
	pathInfo, err := os.Lstat(canonicalRoot)
	if err != nil {
		return fmt.Errorf("inspect canonical uploads root: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() {
		return fmt.Errorf("canonical uploads root %q is no longer a directory", canonicalRoot)
	}
	rootInfo, err := uploadRoot.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect open uploads root: %w", err)
	}
	if !os.SameFile(pathInfo, rootInfo) {
		return fmt.Errorf("canonical uploads root %q was replaced", canonicalRoot)
	}
	return nil
}

// ValidateMediaCleanupTarget proves that a media UUID and configured uploads
// root are safe to use before a caller performs an irreversible database
// delete. It does not remove files. DeleteMediaFiles repeats the validation
// when cleanup actually runs, closing any replacement race between the two
// operations.
func ValidateMediaCleanupTarget(uploadDir, mediaUUID string) (resultErr error) {
	uploadRoot, err := openMediaCleanupRoot(uploadDir, mediaUUID, false)
	if err != nil || uploadRoot == nil {
		return err
	}
	return uploadRoot.Close()
}

// CanonicalUploadRoot resolves and opens the configured uploads directory,
// returning the canonical absolute root named by that verified capability.
// Long-lived workflows capture this once before their first write and use the
// returned path for every write, compensating cleanup, and durable retry. A
// later retarget of a configured symlink therefore cannot redirect cleanup.
func CanonicalUploadRoot(uploadDir string) (canonicalRoot string, resultErr error) {
	uploadRoot, err := openOrCreateUploadRoot(uploadDir)
	if err != nil {
		return "", err
	}
	canonicalRoot = uploadRoot.Name()
	if err := uploadRoot.Close(); err != nil {
		return "", fmt.Errorf("close uploads directory: %w", err)
	}
	return canonicalRoot, nil
}

// OpenUploadRoot resolves the configured uploads directory and returns a
// verified root capability. Callers that perform a multi-file operation keep
// this handle open, use Name as the canonical cleanup identity, and never need
// to re-resolve a mutable configured symlink between individual writes.
func OpenUploadRoot(uploadDir string) (*os.Root, error) {
	return openOrCreateUploadRoot(uploadDir)
}

// OpenExistingUploadRoot resolves an already existing uploads directory and
// returns the same verified root capability as OpenUploadRoot without creating
// anything. A missing directory is reported as a nil root so read-only
// preflights can prove storage freshness without mutating the filesystem.
func OpenExistingUploadRoot(uploadDir string) (*os.Root, error) {
	absRoot, err := validatedUploadRootPath(uploadDir)
	if err != nil {
		return nil, err
	}
	root, err := openVerifiedUploadRoot(absRoot, nil)
	if err != nil || root != nil {
		return root, err
	}
	// A dangling symlink is not an absent upload root: the real write would fail
	// rather than create through it, so dry-run must fail in the same way.
	if _, err := os.Lstat(absRoot); err == nil {
		return nil, fmt.Errorf("uploads path %q exists but cannot be opened", uploadDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect uploads path: %w", err)
	}
	return nil, nil
}

// openOrCreateUploadRoot returns a capability rooted at the configured uploads
// directory. Only the operator-controlled root path is ever passed to ordinary
// path APIs; every media-controlled descendant is subsequently accessed
// through os.Root.
func openOrCreateUploadRoot(uploadDir string) (*os.Root, error) {
	absRoot, err := validatedUploadRootPath(uploadDir)
	if err != nil {
		return nil, err
	}
	root, err := openVerifiedUploadRoot(absRoot, nil)
	if err != nil {
		return nil, err
	}
	if root != nil {
		return root, nil
	}
	if err := os.MkdirAll(absRoot, 0o750); err != nil {
		return nil, fmt.Errorf("create uploads directory: %w", err)
	}
	root, err = openVerifiedUploadRoot(absRoot, nil)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, fmt.Errorf("uploads directory %q disappeared while it was being created", uploadDir)
	}
	return root, nil
}

func validatedUploadRootPath(uploadDir string) (string, error) {
	if uploadDir == "" {
		return "", errors.New("uploads directory is empty")
	}
	absRoot, err := filepath.Abs(filepath.Clean(uploadDir))
	if err != nil {
		return "", fmt.Errorf("resolve uploads directory: %w", err)
	}
	if filepath.Dir(absRoot) == absRoot {
		return "", fmt.Errorf("uploads directory %q is too broad", uploadDir)
	}
	return absRoot, nil
}

func deleteMediaFilesWith(
	uploadDir, mediaUUID string,
	removeAll func(*os.Root, string) error,
) (resultErr error) {
	return deleteMediaFilesWithPolicy(uploadDir, mediaUUID, false, removeAll)
}

func deleteMediaFilesWithPolicy(
	uploadDir, mediaUUID string,
	requireCanonicalRoot bool,
	removeAll func(*os.Root, string) error,
) (resultErr error) {
	uploadRoot, err := openMediaCleanupRoot(uploadDir, mediaUUID, requireCanonicalRoot)
	if err != nil || uploadRoot == nil {
		return err
	}
	defer func() {
		if err := uploadRoot.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close uploads directory: %w", err))
		}
	}()

	return deleteMediaFilesFromOpenRoot(uploadRoot, mediaUUID, removeAll)
}

func deleteMediaFilesFromOpenRoot(
	uploadRoot *os.Root,
	mediaUUID string,
	removeAll func(*os.Root, string) error,
) error {
	var cleanupErrors []error
	for _, storageDir := range model.MediaStorageDirs() {
		target := filepath.ToSlash(filepath.Join(storageDir, mediaUUID))
		if !fs.ValidPath(target) || target == "." {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("validate %s media directory: invalid root-relative path", storageDir))
			continue
		}
		if err := removeAll(uploadRoot, target); err != nil && !os.IsNotExist(err) {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("remove %s media directory: %w", storageDir, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func openMediaCleanupRoot(uploadDir, mediaUUID string, requireCanonicalRoot bool) (*os.Root, error) {
	if !IsCanonicalMediaUUID(mediaUUID) {
		return nil, fmt.Errorf("invalid media UUID %q", mediaUUID)
	}
	if uploadDir == "" {
		return nil, errors.New("uploads directory is empty")
	}

	absRoot, err := filepath.Abs(filepath.Clean(uploadDir))
	if err != nil {
		return nil, fmt.Errorf("resolve uploads directory: %w", err)
	}
	if filepath.Dir(absRoot) == absRoot {
		return nil, fmt.Errorf("uploads directory %q is too broad", uploadDir)
	}

	uploadRoot, err := openVerifiedUploadRootWithPolicy(absRoot, nil, requireCanonicalRoot)
	if err != nil {
		return nil, err
	}
	if uploadRoot == nil {
		// Cleanup is idempotent. If the entire configured uploads directory is
		// already absent, none of the UUID's storage directories can remain and
		// durable retry work is complete rather than permanently pending.
		return nil, nil
	}
	return uploadRoot, nil
}

// openVerifiedUploadRoot resolves a configured uploads path, opens it through
// a parent os.Root capability, and proves the handle still names the directory
// inspected before resolution. The identity check closes the rename/symlink
// race between EvalSymlinks and OpenRoot; opening through the parent also
// rejects a replacement symlink that escapes the canonical parent.
//
// beforeOpen is a test seam for deterministic path-replacement regressions.
// Production passes nil.
func openVerifiedUploadRoot(absRoot string, beforeOpen func()) (*os.Root, error) {
	return openVerifiedUploadRootWithPolicy(absRoot, beforeOpen, false)
}

func openVerifiedUploadRootWithPolicy(absRoot string, beforeOpen func(), requireCanonicalRoot bool) (*os.Root, error) {
	expectedInfo, err := os.Stat(absRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect uploads directory: %w", err)
	}
	if !expectedInfo.IsDir() {
		return nil, fmt.Errorf("uploads path %q is not a directory", absRoot)
	}

	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve uploads directory symlinks: %w", err)
	}
	resolvedRoot, err = filepath.Abs(resolvedRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve uploads directory absolute path: %w", err)
	}
	resolvedRoot = filepath.Clean(resolvedRoot)
	if filepath.Dir(resolvedRoot) == resolvedRoot {
		return nil, fmt.Errorf("uploads directory %q resolves to filesystem root", absRoot)
	}
	if requireCanonicalRoot && resolvedRoot != absRoot {
		return nil, fmt.Errorf("uploads directory %q no longer names its canonical root %q", absRoot, resolvedRoot)
	}
	if beforeOpen != nil {
		beforeOpen()
	}

	parentPath := filepath.Dir(resolvedRoot)
	parentRoot, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, fmt.Errorf("open uploads parent directory: %w", err)
	}
	base := filepath.ToSlash(filepath.Base(resolvedRoot))
	uploadRoot, openErr := parentRoot.OpenRoot(base)
	closeParentErr := parentRoot.Close()
	if openErr != nil {
		return nil, errors.Join(fmt.Errorf("open uploads directory: %w", openErr), closeParentErr)
	}
	if closeParentErr != nil {
		_ = uploadRoot.Close()
		return nil, fmt.Errorf("close uploads parent directory: %w", closeParentErr)
	}

	openedInfo, err := uploadRoot.Stat(".")
	if err != nil {
		_ = uploadRoot.Close()
		return nil, fmt.Errorf("inspect opened uploads directory: %w", err)
	}
	if !os.SameFile(expectedInfo, openedInfo) {
		_ = uploadRoot.Close()
		return nil, fmt.Errorf("uploads directory %q changed while it was being opened", absRoot)
	}
	return uploadRoot, nil
}
