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

// TestUnguardedPublishPageStaysInImporters fails when code outside the
// Developer and Migrator modules calls the store's PublishPage, which
// publishes a page whatever its status. Those modules publish pages they
// have just created. Everywhere else a page is published on the strength of
// a status read earlier, and the unguarded update published it again after
// the scheduler had, sending page.published twice (2026-10 audit backlog).
// Use PublishUnpublishedPage or PublishScheduledPage, which change only a
// page that still has the status read.
//
// Bug state: call h.queries.PublishPage in PagesHandler.TogglePublish and
// this names it.
func TestUnguardedPublishPageStaysInImporters(t *testing.T) {
	root := filepath.Join("..", "..")
	allowed := []string{"modules/developer/", "modules/migrator/"}
	fset := token.NewFileSet()
	calls := 0
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
			if !ok || calledName(call) != "PublishPage" {
				return true
			}
			calls++
			for _, prefix := range allowed {
				if strings.HasPrefix(rel, prefix) {
					return true
				}
			}
			t.Errorf("%s calls PublishPage, which publishes a page whatever its status; use PublishUnpublishedPage or PublishScheduledPage", fset.Position(call.Pos()))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("found no PublishPage call at all, not even in the importers; the scan is broken")
	}
}
