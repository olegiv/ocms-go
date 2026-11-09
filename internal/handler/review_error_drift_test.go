// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Transport consumers of review operations and MCP context builders must use
// domain errors. A raw missing row from these aggregate operations can come
// from later evidence, snapshot or site-default reads, not just request input.
func TestReviewAndContextHandlersUseDomainErrors(t *testing.T) {
	seen := make(map[string]bool)
	for _, directory := range []string{".", "../../modules/mcpserver"} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			positions := token.NewFileSet()
			parsed, parseErr := parser.ParseFile(positions, filepath.Join(directory, entry.Name()), nil, 0)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			aliases := make(map[string]string)
			for _, imported := range parsed.Imports {
				importPath, pathErr := strconv.Unquote(imported.Path.Value)
				if pathErr != nil {
					t.Fatal(pathErr)
				}
				name := filepath.Base(importPath)
				if imported.Name != nil {
					name = imported.Name.Name
				}
				aliases[importPath] = name
			}
			for _, declaration := range parsed.Decls {
				fn, ok := declaration.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				consumer := false
				for node := range ast.Preorder(fn.Body) {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						continue
					}
					selected, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						continue
					}
					name := selected.Sel.Name
					owner, named := selected.X.(*ast.Ident)
					reviewCall := named && owner.Name == aliases["github.com/olegiv/ocms-go/internal/review"] && (name == "SetClaim" || name == "MarkTranslationReviewed")
					contextCall := directory != "." && name == "Build"
					if reviewCall || contextCall {
						consumer, seen[name] = true, true
					}
				}
				if !consumer {
					continue
				}
				for node := range ast.Preorder(fn.Body) {
					selected, ok := node.(*ast.SelectorExpr)
					if !ok || selected.Sel.Name != "ErrNoRows" {
						continue
					}
					owner, ok := selected.X.(*ast.Ident)
					if ok && owner.Name == aliases["database/sql"] {
						t.Errorf("%s: %s must classify domain errors, not raw sql.ErrNoRows from an aggregate operation", positions.Position(selected.Pos()), fn.Name.Name)
					}
				}
			}
		}
	}
	for _, operation := range []string{"SetClaim", "MarkTranslationReviewed", "Build"} {
		if !seen[operation] {
			t.Errorf("no %s consumer found; update domain-error drift coverage", operation)
		}
	}
}
