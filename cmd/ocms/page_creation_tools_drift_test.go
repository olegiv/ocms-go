// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPageCreationToolsUseInventoriedBoundary keeps operator commands and
// scripts from adding literal inserts outside the revisioned write boundary.
// The pagewrite package's Go scan covers typed store calls in scripts.
func TestPageCreationToolsUseInventoriedBoundary(t *testing.T) {
	root := filepath.Join("..", "..")
	const command = ".claude/commands/create-page.md"
	const client = "scripts/codex/create-page"
	blockComment := regexp.MustCompile(`(?s)/\*.*?\*/`)
	insert := regexp.MustCompile("(?is)\\b(?:insert(?:\\s+or\\s+\\w+)?|replace)\\s+into\\s+(?:[\"`\\[]?main[\"`\\]]?\\s*\\.\\s*)?[\"`\\[]?pages\\b")
	keep := func(rel string) bool {
		return strings.HasPrefix(rel, ".claude/commands/") ||
			(strings.HasPrefix(rel, "scripts/") && !strings.HasSuffix(rel, "_test.go"))
	}
	if err := walkRepoFiles(root, keep, func(path, rel string) error {
		data, readErr := os.ReadFile(path)
		if errors.Is(readErr, fs.ErrNotExist) && strings.HasPrefix(rel, ".claude/commands/") &&
			!sharedToolingRequired() {
			info, statErr := os.Lstat(path)
			if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
				return nil // shared command targets are absent from some archives
			}
		}
		if readErr != nil {
			return readErr
		}
		if insert.Match(blockComment.ReplaceAll(data, []byte(" "))) {
			t.Errorf("%s inserts pages directly; use the revisioned page-write boundary", rel)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	instructions, err := os.ReadFile(filepath.Join(root, command))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(instructions), "go run ./"+client+" --site-url") {
		t.Error("/create-page must dispatch to the transport-checked REST client")
	}
	inventory, err := os.ReadFile(filepath.Join(root, "docs", "page-writes.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(inventory), "\n") {
		if strings.HasPrefix(line, "|") && strings.Contains(line, "/create-page") && strings.Contains(line, client) {
			return
		}
	}
	t.Error("writer inventory must list the /create-page command and its REST client")
}
