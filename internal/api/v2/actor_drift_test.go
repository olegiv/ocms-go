// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestActorOwnershipUsesOneCredentialMapping(t *testing.T) {
	walkV2Sources(t, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if path == "api.go" && fn.Name.Name == "ActingUserID" {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				selected, ok := node.(*ast.SelectorExpr)
				if !ok || selected.Sel.Name != "CreatedBy" {
					return true
				}
				credential, ok := selected.X.(*ast.SelectorExpr)
				if ok && credential.Sel.Name == "APIKey" {
					t.Errorf("%s:%s reads APIKey.CreatedBy outside Actor.ActingUserID", path, fn.Name.Name)
				}
				return true
			})
		}
	})
}

func TestPagesDoNotImportHandlers(t *testing.T) {
	walkV2Sources(t, func(path string, file *ast.File) {
		if !strings.HasPrefix(path, "pages/") {
			return
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if importPath == "github.com/olegiv/ocms-go/internal/handler" {
				t.Errorf("%s: domain services must use internal/pagewrite, not handler invariants", path)
			}
		}
	})
}

// Actor adapters must share both provenance and preview revocation bindings.
// Inspect type references, not just literals, so aliases and zero-value
// declarations cannot introduce another hand-written conversion.
func TestActorProvenanceUsesOneCredentialMapping(t *testing.T) {
	for _, root := range []string{".", "../../ai/authoring", "../../../modules/mcpserver"} {
		walkGoSources(t, root, func(path string, file *ast.File) {
			var provenanceName string
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if importPath != "github.com/olegiv/ocms-go/internal/provenance" {
					continue
				}
				provenanceName = "provenance"
				if spec.Name != nil {
					provenanceName = spec.Name.Name
				}
				if provenanceName == "." {
					t.Errorf("%s: provenance must use a named import for the actor mapping guard", filepath.Join(root, path))
				}
			}
			for _, declaration := range file.Decls {
				if fn, ok := declaration.(*ast.FuncDecl); ok && root == "." && path == "api.go" && fn.Name.Name == "Principal" && fn.Recv != nil && len(fn.Recv.List) == 1 {
					if receiver, isActor := fn.Recv.List[0].Type.(*ast.Ident); isActor && receiver.Name == "Actor" {
						continue
					}
				}
				ast.Inspect(declaration, func(node ast.Node) bool {
					selected, ok := node.(*ast.SelectorExpr)
					if !ok || selected.Sel.Name != "Principal" {
						return true
					}
					if qualifier, qualified := selected.X.(*ast.Ident); qualified && qualifier.Name == provenanceName {
						t.Errorf("%s: use Actor.Principal for actor provenance and preview issuer bindings", filepath.Join(root, path))
					}
					return true
				})
			}
		})
	}
}

func walkV2Sources(t *testing.T, check func(string, *ast.File)) {
	t.Helper()
	walkGoSources(t, ".", check)
}

func walkGoSources(t *testing.T, root string, check func(string, *ast.File)) {
	t.Helper()
	set := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		check(filepath.ToSlash(relative), file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
