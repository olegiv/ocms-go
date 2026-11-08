// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/repotree"
)

// TestDCRWritersUseRegistrationBudget requires client mutations to have
// guarded callers and checks how client creation reaches the budget.
func TestDCRWritersUseRegistrationBudget(t *testing.T) {
	t.Run("client query catalog", checkOAuthClientWriters)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	dcrWriters := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				selector, isSelector := node.(*ast.SelectorExpr)
				if !isSelector {
					return true
				}
				switch selector.Sel.Name {
				case "RecordOAuthDCRRegistration":
					if fn.Name.Name != "registerDCR" {
						t.Errorf("%s: DCR admission write outside registerDCR", fset.Position(node.Pos()))
					}
				case "saveClient", "saveClientTx":
					// Inspect the enclosing call separately; method aliases cannot
					// establish their client source at this boundary.
					var call *ast.CallExpr
					ast.Inspect(fn.Body, func(candidate ast.Node) bool {
						if c, isCall := candidate.(*ast.CallExpr); isCall && c.Fun == selector {
							call = c
						}
						return true
					})
					source := ""
					sourceArg := 2
					if selector.Sel.Name == "saveClientTx" {
						sourceArg++
					}
					if call != nil && len(call.Args) == sourceArg+2 {
						if ident, isIdent := call.Args[sourceArg].(*ast.Ident); isIdent && ident.Name == "source" &&
							selector.Sel.Name == "saveClientTx" && fn.Name.Name == "saveClient" {
							return true
						}
						if literal, isLiteral := call.Args[sourceArg].(*ast.BasicLit); isLiteral && literal.Kind == token.STRING {
							var decodeErr error
							source, decodeErr = strconv.Unquote(literal.Value)
							if decodeErr != nil {
								t.Fatal(decodeErr)
							}
						}
					}
					if source == "dcr" {
						dcrWriters++
					}
					if !(source == "cimd" && selector.Sel.Name == "saveClient") &&
						!(source == "dcr" && selector.Sel.Name == "saveClientTx" && fn.Name.Name == "registerDCR") {
						t.Errorf("%s: client source %q bypasses the DCR admission boundary", fset.Position(node.Pos()), source)
					}
				}
				return true
			})
		}
	}
	if dcrWriters != 1 {
		t.Fatalf("client writer inventory: DCR=%d, want 1", dcrWriters)
	}
}

func checkOAuthClientWriters(t *testing.T) {
	root := filepath.Join("..", "..")
	owners := map[string]struct{ path, function string }{
		"UpsertOAuthClient":               {"internal/oauth/clients.go", "saveClientTx"},
		"DeleteExpiredUnusedOAuthClients": {"internal/oauth/clients.go", "saveClientTx"},
		"ExtendDCROAuthClient":            {"internal/oauth/protocol.go", "Token"},
		"DeleteRetainedOAuthClients":      {"internal/oauth/retention.go", "cleanupAt"},
		"EvictOAuthDCRClient":             {"internal/oauth/registration.go", "registerDCR"},
	}
	found := make(map[string]bool)
	for _, mutation := range testutil.SQLCMutations(t, filepath.Join(root, "internal", "store"), "oauth_clients") {
		if _, ok := owners[mutation.Name]; !ok {
			t.Errorf("%s: %s writes oauth_clients without a guarded caller", mutation.Path, mutation.Name)
		}
		found[mutation.Name] = true
	}
	for name := range owners {
		if !found[name] {
			t.Errorf("%s has a caller guard but no OAuth client mutation in the sqlc catalog", name)
		}
	}

	fset := token.NewFileSet()
	calls := make(map[string]int)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if repotree.Skip(root, path, entry) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			strings.HasSuffix(path, ".sql.go") || strings.HasSuffix(path, "_templ.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			function := ""
			if fn, ok := declaration.(*ast.FuncDecl); ok {
				function = fn.Name.Name
			}
			for node := range ast.Preorder(declaration) {
				selected, ok := node.(*ast.SelectorExpr)
				if !ok || !found[selected.Sel.Name] {
					continue
				}
				owner, guarded := owners[selected.Sel.Name]
				if !guarded || owner.path != filepath.ToSlash(relative) || owner.function != function {
					t.Errorf("%s: %s writes oauth_clients outside its guarded caller", fset.Position(selected.Pos()), selected.Sel.Name)
				} else {
					calls[selected.Sel.Name]++
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls["UpsertOAuthClient"] != 1 {
		t.Errorf("found %d guarded UpsertOAuthClient references, want 1", calls["UpsertOAuthClient"])
	}
}
