// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Fix invariant: every path that inserts a new AI draft page runs the draft
// limit in the write transaction. The only insert is Pages.CreateWith inside
// (*Service).create, which Create and Translate share, and its prepare call
// must opt in to the limit; Update must not (updates never add a draft).
// A new creation path (or a create that forgets the flag) fails here.
func TestEveryNewDraftPathRunsTheDraftLimit(t *testing.T) {
	type call struct{ function, last string }
	var creators, prepares []call
	limitUsers := map[string][]string{}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				invoked, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch name := calledName(invoked); name {
				case "CreateWith":
					creators = append(creators, call{function: fn.Name.Name})
				case "prepare":
					last := ""
					if len(invoked.Args) > 0 {
						if ident, ok := invoked.Args[len(invoked.Args)-1].(*ast.Ident); ok {
							last = ident.Name
						}
					}
					prepares = append(prepares, call{function: fn.Name.Name, last: last})
				case "checkDraftLimit", "CountOwnAIDrafts":
					limitUsers[name] = append(limitUsers[name], fn.Name.Name)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(creators, []call{{function: "create"}}) {
		t.Errorf("page creation paths changed: %v; each needs the draft limit and this test updated", creators)
	}
	slices.SortFunc(prepares, func(a, b call) int { return strings.Compare(a.function, b.function) })
	if want := []call{{"Update", "false"}, {"create", "true"}}; !reflect.DeepEqual(prepares, want) {
		t.Errorf("prepare is called as %v, want %v: new drafts must pass newDraft=true, updates false", prepares, want)
	}
	// The cap and list_my_drafts count with the same query: one definition.
	slices.Sort(limitUsers["CountOwnAIDrafts"])
	if !reflect.DeepEqual(limitUsers["checkDraftLimit"], []string{"prepare"}) || !reflect.DeepEqual(limitUsers["CountOwnAIDrafts"], []string{"List", "checkDraftLimit"}) {
		t.Errorf("the limit is not wired through prepare and the shared own-draft count: %v", limitUsers)
	}
}

func calledName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}
