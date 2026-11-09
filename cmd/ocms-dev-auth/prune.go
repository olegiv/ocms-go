// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// codeDirPrefix is the os.MkdirTemp prefix of every private code
	// directory; the sweep below matches exactly the names MkdirTemp produces.
	codeDirPrefix = "ocms-dev-auth-"
	codeFileName  = "code"
	// staleCodeAge is far beyond the 55-second command deadline and the 30-second
	// life of a code, so only abandoned directories qualify.
	staleCodeAge = 2 * time.Minute
)

// isCodeDirName reports whether name is the prefix plus the decimal suffix that
// os.MkdirTemp appends.
func isCodeDirName(name string) bool {
	suffix, found := strings.CutPrefix(name, codeDirPrefix)
	if !found || suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// pruneStaleCodeDirectories removes code directories this tool created in
// tempDir and left behind, for example when the consuming agent crashed before
// cleaning up. It is best effort and silent: errors are ignored and nothing
// about the contents is ever read or reported. It only touches a directory that
// is an ordinary (not symlinked) 0700 directory owned by the current user, has
// the exact generated name, is older than staleCodeAge and holds nothing but
// the code file; it never recurses.
func pruneStaleCodeDirectories(tempDir string, now time.Time) {
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !isCodeDirName(entry.Name()) {
			continue
		}
		info, infoErr := entry.Info() // lstat: a symlink is never a directory here
		if infoErr != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || !ownedByCurrentUser(info) || now.Sub(info.ModTime()) < staleCodeAge {
			continue
		}
		removeCodeDirectory(filepath.Join(tempDir, entry.Name()))
	}
}

// removeCodeDirectory deletes the code file and then the directory itself,
// without recursion, so an unexpected entry keeps the directory in place.
func removeCodeDirectory(directory string) {
	codePath := filepath.Join(directory, codeFileName)
	codeInfo, err := os.Lstat(codePath)
	switch {
	case err == nil && codeInfo.Mode().IsRegular():
		if os.Remove(codePath) != nil {
			return
		}
	case !errors.Is(err, os.ErrNotExist):
		return
	}
	_ = os.Remove(directory)
}
