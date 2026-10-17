// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoFilesAreGofmtFormatted fails when a tracked Go file is not
// gofmt-formatted. CI runs no formatter, so fifteen files had drifted
// (review of the 2026-10-17 audit fixes); this check runs in the same go test
// as every other gate. In a git checkout it reads the tracked files, so local
// scratch files do not fail the gate; in an exported tree (no .git) every
// file is tracked, so it walks the tree.
//
// Bug state: re-align a struct literal's values by hand in any tracked file
// and this names the file.
func TestGoFilesAreGofmtFormatted(t *testing.T) {
	root := filepath.Join("..", "..")
	checked := 0
	var unformatted []string
	check := func(path string) error {
		src, err := os.ReadFile(path) // #nosec G304 -- tracked repository files
		if errors.Is(err, fs.ErrNotExist) {
			return nil // tracked but deleted in the working tree
		}
		if err != nil {
			return err
		}
		formatted, err := format.Source(src)
		if err != nil {
			return fmt.Errorf("formatting %s: %w", path, err)
		}
		checked++
		if !bytes.Equal(src, formatted) {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			unformatted = append(unformatted, rel)
		}
		return nil
	}
	if tracked, err := trackedGoFiles(root); err == nil && len(tracked) > 0 {
		for _, path := range tracked {
			if err := check(path); err != nil {
				t.Fatal(err)
			}
		}
	} else if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Dot directories hold tooling and gitignored audit snapshots;
			// the others hold dependencies, the wiki, scratch and data.
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			switch d.Name() {
			case "node_modules", "vendor", "wiki", "output", "bin", "data", "uploads":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		return check(path)
	}); err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	if checked < 100 {
		t.Fatalf("checked only %d Go files; the walk is broken and this test is vacuous", checked)
	}
	if len(unformatted) > 0 {
		t.Errorf("these files are not gofmt-formatted; run gofmt -w on them:\n  %s", strings.Join(unformatted, "\n  "))
	}
}

// trackedGoFiles lists the Go files git tracks under root, or fails when root
// is not a git checkout. An exported tree inside another repository yields an
// empty list, which the caller treats like no checkout.
func trackedGoFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--", "*.go")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, name := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if name != "" {
			files = append(files, filepath.Join(root, name))
		}
	}
	return files, nil
}
