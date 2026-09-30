// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoUntrackedGoroutines fails when module code starts a goroutine with a
// bare go statement instead of m.bgWG.Go.
//
// Shutdown waits on bgWG before the database and GeoIP reader close, and a
// bare goroutine outlives it. A read-beacon insert still running when a test
// closes its database recreates the SQLite journal inside the test's TempDir,
// and the cleanup fails with "directory not empty" (Go workflow run
// 36757893076 on master).
//
// Bug state: turn m.bgWG.Go(func() { m.recordReadWithIdentity(id, &req) }) in
// handlers.go back into go m.recordReadWithIdentity(id, &req) and this names
// the line.
func TestNoUntrackedGoroutines(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing package files: %v", err)
	}

	fset := token.NewFileSet()
	parsed := 0
	var bare []string
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}
		parsed++
		ast.Inspect(file, func(n ast.Node) bool {
			if stmt, ok := n.(*ast.GoStmt); ok {
				bare = append(bare, fset.Position(stmt.Go).String())
			}
			return true
		})
	}
	if parsed == 0 {
		t.Fatal("no non-test Go files parsed; the package moved and this test is vacuous")
	}
	if len(bare) > 0 {
		t.Errorf("these goroutines are not tracked on m.bgWG, so Shutdown cannot "+
			"wait for them; start them with m.bgWG.Go instead:\n  %s",
			strings.Join(bare, "\n  "))
	}
}
