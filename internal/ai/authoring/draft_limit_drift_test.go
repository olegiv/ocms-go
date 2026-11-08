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

// TestEveryNewDraftPathRunsTheDraftLimit inventories CreateWith references and
// explicit ForceDraft options across internal, modules, custom and cmd.
// AI creates share authoring.Service.create and must opt in to the draft limit
// through prepare; Update must not (updates never add a draft). The ordinary
// REST Create wrapper is the only other CreateWith caller and has no ForceDraft
// option. A new reference or ForceDraft write needs review and an inventory update.
func TestEveryNewDraftPathRunsTheDraftLimit(t *testing.T) {
	type call struct{ function, last string }
	var creators, forceDrafts []string
	var prepares []call
	limitUsers := map[string][]string{}
	root := filepath.Join("..", "..", "..")
	for _, directory := range []string{"internal", "modules", "custom", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
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
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			for _, declaration := range file.Decls {
				function := "<package>"
				if fn, ok := declaration.(*ast.FuncDecl); ok {
					function = fn.Name.Name
				}
				ast.Inspect(declaration, func(node ast.Node) bool {
					if selected, ok := node.(*ast.SelectorExpr); ok && selected.Sel.Name == "CreateWith" {
						creators = append(creators, relative+":"+function)
					}
					if field, ok := node.(*ast.KeyValueExpr); ok {
						if key, ok := field.Key.(*ast.Ident); ok && key.Name == "ForceDraft" {
							forceDrafts = append(forceDrafts, relative+":"+function)
						}
					}
					if assign, ok := node.(*ast.AssignStmt); ok {
						for _, target := range assign.Lhs {
							if selected, ok := target.(*ast.SelectorExpr); ok && selected.Sel.Name == "ForceDraft" {
								forceDrafts = append(forceDrafts, relative+":"+function)
							}
						}
					}
					invoked, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					name := calledName(invoked)
					if !strings.HasPrefix(relative, "internal/ai/authoring/") {
						return true
					}
					switch name {
					case "prepare":
						last := ""
						if len(invoked.Args) > 0 {
							if ident, ok := invoked.Args[len(invoked.Args)-1].(*ast.Ident); ok {
								last = ident.Name
							}
						}
						prepares = append(prepares, call{function: function, last: last})
					case "checkDraftLimit", "CountOwnAIDrafts":
						limitUsers[name] = append(limitUsers[name], function)
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(forceDrafts)
	slices.Sort(creators)
	if want := []string{"internal/ai/authoring/service.go:create", "internal/api/v2/pages/write.go:Create"}; !reflect.DeepEqual(creators, want) {
		t.Errorf("page creation paths changed: %v; each needs the draft limit and this test updated", creators)
	}
	if !reflect.DeepEqual(forceDrafts, []string{"internal/ai/authoring/service.go:writeOptions"}) {
		t.Errorf("ForceDraft options changed: %v; AI draft writes must use the shared authoring service", forceDrafts)
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
