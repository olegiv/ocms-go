// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestMediaDeleteCallersKeepTheChangedPages requires every runtime caller of
// MediaService.DeleteWithPrincipal to keep the pages it returns. Deleting media
// gives each page that used it as an image a new revision, and the caller owes
// those pages the after-commit side effects (cache invalidation, page.updated);
// discarding the result, as the admin handler and the REST service once did,
// silently skips them (2026-11-03 review R2 P3-8).
//
// Bug state: call DeleteWithPrincipal as a bare statement or assign its first
// result to _ and this names the call.
func TestMediaDeleteCallersKeepTheChangedPages(t *testing.T) {
	root := filepath.Join("..", "..")
	set := token.NewFileSet()
	checked := 0
	for _, directory := range []string{"internal", "modules", "custom", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_templ.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(set, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch statement := node.(type) {
				case *ast.ExprStmt:
					if isMediaDeleteCall(statement.X) {
						t.Errorf("%s: the result of DeleteWithPrincipal is discarded", set.Position(statement.Pos()))
					}
				case *ast.AssignStmt:
					if len(statement.Rhs) != 1 || !isMediaDeleteCall(statement.Rhs[0]) {
						return true
					}
					checked++
					if first, ok := statement.Lhs[0].(*ast.Ident); !ok || first.Name == "_" {
						t.Errorf("%s: the pages DeleteWithPrincipal returns are discarded", set.Position(statement.Pos()))
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked < 3 {
		t.Fatalf("found %d callers, want the admin single and bulk delete and the REST service", checked)
	}
}

func isMediaDeleteCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	selected, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selected.Sel.Name == "DeleteWithPrincipal"
}
