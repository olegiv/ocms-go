// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDCRWritersUseRegistrationBudget inventories client persistence in the
// owning package so a new DCR writer cannot bypass the tested admission path.
func TestDCRWritersUseRegistrationBudget(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	dcrWriters, upserts := 0, 0
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
				case "UpsertOAuthClient":
					upserts++
					if fn.Name.Name != "saveClientTx" {
						t.Errorf("%s: client write outside the shared persistence boundary", fset.Position(node.Pos()))
					}
				case "RecordOAuthDCRRegistration", "EvictOAuthDCRClient":
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
	if dcrWriters != 1 || upserts != 1 {
		t.Fatalf("client writer inventory: DCR=%d upserts=%d", dcrWriters, upserts)
	}
}
