// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// walkRepoFiles calls visit for each repository file whose slash-separated
// path relative to root keep accepts. In a git checkout it visits the files
// git tracks or would track (untracked files that are not ignored), so a new
// file is checked before it is staged while gitignored scratch is not, and
// it skips tracked files deleted in the working tree; a git failure there is
// returned. In an exported tree (no .git) every file is tracked, so it walks
// the tree, skipping dot directories and, at the top level, the dependency,
// wiki, scratch and data directories.
func walkRepoFiles(root string, keep func(rel string) bool, visit func(path, rel string) error) error {
	if _, err := os.Lstat(filepath.Join(root, ".git")); err == nil {
		tracked, err := trackedFiles(root)
		if err != nil {
			return fmt.Errorf("listing the files of the git checkout: %w", err)
		}
		if len(tracked) == 0 {
			return errors.New("git lists no files in the checkout")
		}
		for _, rel := range tracked {
			if !keep(rel) {
				continue
			}
			path := filepath.Join(root, filepath.FromSlash(rel))
			if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
				continue // tracked but deleted in the working tree
			}
			if err := visit(path, rel); err != nil {
				return err
			}
		}
		return nil
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Dot directories hold tooling and gitignored audit snapshots;
			// the others hold dependencies, the wiki, scratch and data.
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if filepath.Dir(path) == root {
				switch d.Name() {
				case "node_modules", "vendor", "wiki", "output", "bin", "data", "uploads":
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !keep(rel) {
			return nil
		}
		return visit(path, rel)
	})
}

// trackedFiles lists the files git tracks under root, and the untracked
// files it does not ignore, as slash-separated relative paths.
func trackedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, name := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	return files, nil
}
