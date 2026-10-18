// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoFilesAreGofmtFormatted fails when a tracked Go file is not
// gofmt-formatted. CI runs no formatter, so fifteen files had drifted
// (review of the 2026-10-17 audit fixes); this check runs in the same go test
// as every other gate. It reads the files walkRepoFiles lists, so gitignored
// scratch files do not fail the gate.
//
// Bug state: re-align a struct literal's values by hand in any tracked file
// and this names the file.
func TestGoFilesAreGofmtFormatted(t *testing.T) {
	root := filepath.Join("..", "..")
	checked := 0
	var unformatted []string
	err := walkRepoFiles(root, func(rel string) bool {
		return strings.HasSuffix(rel, ".go")
	}, func(path, rel string) error {
		src, err := os.ReadFile(path) // #nosec G304 -- repository files
		if err != nil {
			return err
		}
		formatted, err := format.Source(src)
		if err != nil {
			return fmt.Errorf("formatting %s: %w", rel, err)
		}
		checked++
		if !bytes.Equal(src, formatted) {
			unformatted = append(unformatted, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the repository: %v", err)
	}
	if checked < 100 {
		t.Fatalf("checked only %d Go files; the walk is broken and this test is vacuous", checked)
	}
	if len(unformatted) > 0 {
		t.Errorf("these files are not gofmt-formatted; run gofmt -w on them:\n  %s", strings.Join(unformatted, "\n  "))
	}
}
