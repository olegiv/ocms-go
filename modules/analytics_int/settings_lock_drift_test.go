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

// TestSettingsAccessHoldsTheLock fails when module code touches m.settings in
// a function (or closure) that does not itself take m.settingsMu.
//
// The tracker reads the settings on every frontend request while the settings
// form and salt rotation write them, so an unguarded access is a data race
// (security audit 2026-10-17, L-3). No test drives the two concurrently and CI
// does not run -race, so this is the check that holds the line. Readers go
// through settingsSnapshot or IsEnabled; writers through storeSettings or
// updateSettings.
//
// Bug state: make IsEnabled read m.settings.Enabled directly again and this
// names the line.
func TestSettingsAccessHoldsTheLock(t *testing.T) {
	names, err := filepath.Glob("*.go")
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
				checkSettingsLock(fn.Body, func(pos token.Pos) {
					unguarded = append(unguarded, fset.Position(pos).String())
				}, &accesses)
			}
		}
	}
	if accesses == 0 {
		t.Fatal("no m.settings access found; the field moved and this test is vacuous")
	}
	if len(unguarded) > 0 {
		t.Errorf("these m.settings accesses are not under m.settingsMu; use "+
			"settingsSnapshot, IsEnabled, storeSettings or updateSettings:\n  %s",
			strings.Join(unguarded, "\n  "))
	}
}

// checkSettingsLock reports every .settings access in body when body does not
// call settingsMu.Lock or RLock itself. Closures are checked on their own,
// because a lock taken by the enclosing function is not held when they run.
func checkSettingsLock(body *ast.BlockStmt, report func(token.Pos), accesses *int) {
	var found []token.Pos
	locked := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			checkSettingsLock(node.Body, report, accesses)
			return false
		case *ast.SelectorExpr:
			switch node.Sel.Name {
			case "settings":
				found = append(found, node.Pos())
			case "Lock", "RLock":
				if mu, ok := node.X.(*ast.SelectorExpr); ok && mu.Sel.Name == "settingsMu" {
					locked = true
				}
			}
		}
		return true
	})
	*accesses += len(found)
	if !locked {
		for _, pos := range found {
			report(pos)
		}
	}
}
