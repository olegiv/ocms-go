// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// AssertFieldAccessHoldsLock fails when a function or closure in the non-test
// Go files of dir touches the struct field named field without first calling
// Lock or RLock on a selector named mutex (m.settingsMu.RLock(), for example).
// The lock call must come before the first access in source order. Closures
// are checked on their own, because a lock taken by the enclosing function is
// not held when they run. The check is syntactic: it does not follow Unlock
// calls or match receivers, so it guards against a forgotten lock rather than
// proving the locking correct.
//
// It is meant for drift tests that keep a field which request handlers read
// concurrently behind its mutex.
func AssertFieldAccessHoldsLock(t *testing.T, dir, field, mutex string) {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("listing package files: %v", err)
	}

	fset := token.NewFileSet()
	accesses := 0
	var unguarded []string
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				checkFieldLock(fn.Body, field, mutex, &accesses, func(pos token.Pos) {
					unguarded = append(unguarded, fset.Position(pos).String())
				})
			}
		}
	}
	if accesses == 0 {
		t.Fatalf("no .%s access found in %s; the field moved and this test is vacuous", field, dir)
	}
	if len(unguarded) > 0 {
		t.Errorf("these .%s accesses are not under .%s; go through the package's "+
			"locked accessors instead:\n  %s", field, mutex, strings.Join(unguarded, "\n  "))
	}
}

// checkFieldLock reports every .field access in body that comes before the
// first mutex.Lock or mutex.RLock call in body (all of them when there is no
// such call), and recurses into closures separately.
func checkFieldLock(body *ast.BlockStmt, field, mutex string, accesses *int, report func(token.Pos)) {
	var found []token.Pos
	firstLock := token.NoPos
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			checkFieldLock(node.Body, field, mutex, accesses, report)
			return false
		case *ast.SelectorExpr:
			switch node.Sel.Name {
			case field:
				found = append(found, node.Pos())
			case "Lock", "RLock":
				if mu, ok := node.X.(*ast.SelectorExpr); ok && mu.Sel.Name == mutex && !firstLock.IsValid() {
					firstLock = node.Pos()
				}
			}
		}
		return true
	})
	*accesses += len(found)
	for _, pos := range found {
		if !firstLock.IsValid() || pos < firstLock {
			report(pos)
		}
	}
}
