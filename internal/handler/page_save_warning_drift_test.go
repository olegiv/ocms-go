// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestPageSavesReportRemovedMarkup is the drift test for the save warning:
// every handler that stores a page body through normalizePageBodyForStorage
// must report the save with pageSaved, which warns when the page HTML policy
// removed markup, and never with flashSuccess, which would hide the loss.
func TestPageSavesReportRemovedMarkup(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	saves := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == "normalizePageBodyForStorage" {
				continue
			}
			called := calledNames(fn.Body)
			if !called["normalizePageBodyForStorage"] {
				continue
			}
			saves++
			if !called["pageSaved"] {
				t.Errorf("%s: %s stores a sanitized page body but does not report the save with pageSaved", fset.Position(fn.Pos()), fn.Name.Name)
			}
			if called["flashSuccess"] {
				t.Errorf("%s: %s stores a sanitized page body and calls flashSuccess, which hides removed markup", fset.Position(fn.Pos()), fn.Name.Name)
			}
		}
	}
	if saves < 3 {
		t.Errorf("found %d page save handlers, want at least create, update and version restore", saves)
	}
}

// calledNames returns the names of the functions and methods called in body.
func calledNames(body *ast.BlockStmt) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			names[fun.Name] = true
		case *ast.SelectorExpr:
			names[fun.Sel.Name] = true
		}
		return true
	})
	return names
}
