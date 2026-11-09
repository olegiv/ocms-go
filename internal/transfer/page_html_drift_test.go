// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Inventory body-writing queries from SQL, then inspect their transfer callers.
// This follows direct local assignments, not interprocedural/control-flow aliases;
// the runtime matrix verifies flag handling, ordering and persisted outcomes.
func TestImportBodyWritersApplyHTMLPolicy(t *testing.T) {
	names := importBodyQueries(t)
	positions := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	writers := 0
	preflight := false
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(positions, path, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range file.Decls {
			fn, functionOK := declaration.(*ast.FuncDecl)
			if !functionOK || fn.Body == nil {
				continue
			}
			normalized := make(map[string]bool)
			var policy, dryRun, transaction token.Pos
			for node := range ast.Preorder(fn.Body) {
				switch current := node.(type) {
				case *ast.AssignStmt:
					if len(current.Rhs) == 1 {
						if call, callOK := current.Rhs[0].(*ast.CallExpr); callOK && importHTMLCallName(call) == "NormalizePageHTMLForStorage" && len(call.Args) == 2 {
							flag, flagOK := call.Args[1].(*ast.SelectorExpr)
							normalized[importHTMLBinding(current.Lhs[0])] = flagOK && flag.Sel.Name == "sanitizePageHTML"
							continue
						}
					}
					for index, lhs := range current.Lhs {
						key := importHTMLBinding(lhs)
						if index < len(current.Rhs) {
							normalized[key] = normalized[importHTMLBinding(current.Rhs[index])]
						} else {
							normalized[key] = false
						}
					}
				case *ast.IfStmt:
					if flag, flagOK := current.Cond.(*ast.SelectorExpr); flagOK && flag.Sel.Name == "DryRun" && dryRun == token.NoPos {
						dryRun = current.Pos()
					}
				case *ast.CallExpr:
					name := importHTMLCallName(current)
					switch name {
					case "validatePageHTMLPolicy":
						policy = current.Pos()
					case "BeginTx":
						transaction = current.Pos()
					}
					if !names[name] {
						continue
					}
					writers++
					valid := false
					if len(current.Args) == 2 {
						params := current.Args[1]
						if literal, literalOK := params.(*ast.CompositeLit); literalOK {
							for _, element := range literal.Elts {
								field, fieldOK := element.(*ast.KeyValueExpr)
								if fieldOK && importHTMLBinding(field.Key) == "Body" {
									valid = normalized[importHTMLBinding(field.Value)]
								}
							}
						} else {
							valid = normalized[importHTMLBinding(params)+".Body"]
						}
					}
					if !valid {
						t.Errorf("%s: %s must store a body normalized with the configured HTML policy", positions.Position(current.Pos()), name)
					}
				}
			}
			if fn.Name.Name == "importWithPreCommit" {
				preflight = policy != token.NoPos && dryRun != token.NoPos && transaction != token.NoPos && policy < dryRun && policy < transaction
			}
		}
	}
	if writers == 0 || !preflight {
		t.Fatalf("body writers=%d; policy must run before dry-run counting and writes (preflight=%v)", writers, preflight)
	}
}

func importHTMLBinding(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return importHTMLBinding(value.X) + "." + value.Sel.Name
	default:
		return ""
	}
}

func importHTMLCallName(call *ast.CallExpr) string {
	if selected, ok := call.Fun.(*ast.SelectorExpr); ok {
		return selected.Sel.Name
	}
	return ""
}

func importBodyQueries(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "store", "queries", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	declaration := regexp.MustCompile(`(?m)^-- name: (\w+) :\w+`)
	pageWrite := regexp.MustCompile(`(?i)\b(?:INSERT\s+INTO|UPDATE)\s+pages\b`)
	bodyColumn := regexp.MustCompile(`(?i)\bbody\b`)
	names := make(map[string]bool)
	for _, path := range files {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		queries := declaration.FindAllSubmatchIndex(data, -1)
		for index, offsets := range queries {
			end := len(data)
			if index+1 < len(queries) {
				end = queries[index+1][0]
			}
			query := data[offsets[1]:end]
			if pageWrite.Match(query) && bodyColumn.Match(query) {
				names[string(data[offsets[2]:offsets[3]])] = true
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("no body-writing queries discovered; update drift coverage")
	}
	return names
}
