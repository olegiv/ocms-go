// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPagePublicationUsesLifecycleGuards forbids the store's unconditional
// PublishPage query. Imports and generators now set initial publication fields
// in the atomic insert. Existing pages use PublishUnpublishedPage or
// PublishScheduledPage so racing editor/scheduler actions cannot publish twice.
//
// Bug state: call h.queries.PublishPage in PagesHandler.TogglePublish and
// this names it.
func TestPagePublicationUsesLifecycleGuards(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	guarded := 0
	err := walkRepoFiles(root, func(rel string) bool {
		return strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") && !strings.Contains(rel, "/testdata/")
	}, func(path, rel string) error {
		src, err := os.ReadFile(path) // #nosec G304 -- repository files
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch calledName(call) {
			case "PublishUnpublishedPage", "PublishScheduledPage":
				guarded++
			case "PublishPage":
				t.Errorf("%s calls PublishPage, which publishes a page whatever its status; use PublishUnpublishedPage or PublishScheduledPage", fset.Position(call.Pos()))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if guarded == 0 {
		t.Fatal("found no guarded publication call; the scan is broken")
	}
}
