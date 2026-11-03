// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestAccountLookupsInPageWritesUseTheEditorErrorMapping fails when a function
// in this package reads the acting user and does not map the error through
// v2.RequireEditor and v2.EditorError, which is how a missing account (401), a
// lost role (403) and a storage fault (500) stay apart (2026-11-03 review S12).
//
// Bug state: answer a failed GetUserByID with a fixed 401, as authorizeWrite
// did, and this fails for that function.
func TestAccountLookupsInPageWritesUseTheEditorErrorMapping(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(set, entry.Name(), nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			names := make(map[string]bool)
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if selected, ok := node.(*ast.SelectorExpr); ok {
					names[selected.Sel.Name] = true
				}
				return true
			})
			if !names["GetUserByID"] {
				continue
			}
			checked++
			if !names["RequireEditor"] || !names["EditorError"] {
				t.Errorf("%s: %s reads the acting user without v2.RequireEditor and v2.EditorError", entry.Name(), fn.Name.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no account lookup to check; the scan is out of date")
	}
}
