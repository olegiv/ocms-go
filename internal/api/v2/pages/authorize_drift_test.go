// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestAccountLookupsInPageWritesUseTheEditorErrorMapping fails when a function
// in this package reads the acting user without the editor guard and a
// separate error branch that classifies the lookup. REST faults map through
// EditorError. The draft snapshot lookup uses the retryable credential
// mapping while retaining the missing-account refusal (reviews S12 and N-11).
//
// Bug state: answer a failed GetUserByID with a fixed 401, as authorizeWrite
// did, and this fails for that function.
func TestAccountLookupsInPageWritesUseTheEditorErrorMapping(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(set, entry.Name(), nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			roleGuard, lookups := false, 0
			for node := range ast.Preorder(fn.Body) {
				if selected, ok := node.(*ast.SelectorExpr); ok {
					roleGuard = roleGuard || selected.Sel.Name == "RequireEditor"
					if selected.Sel.Name == "GetUserByID" {
						lookups++
					}
				}
			}
			if lookups == 0 {
				continue
			}
			checked += lookups
			if !roleGuard || classifiedAccountLookups(fn) != lookups {
				t.Errorf("%s: %s reads the acting user without v2.RequireEditor and a classified lookup error", entry.Name(), fn.Name.Name)
			}
			if fn.Name.Name == "authorizeWrite" && !draftRoleFaultClassified(fn.Body) {
				t.Error("authorizeWrite must classify draft role lookup faults before returning the editor refusal")
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no account lookup to check; the scan is out of date")
	}
}

func draftRoleFaultClassified(body *ast.BlockStmt) bool {
	for _, statement := range body.List {
		branch, ok := statement.(*ast.IfStmt)
		if !ok {
			continue
		}
		init, ok := branch.Init.(*ast.AssignStmt)
		if !ok || len(init.Rhs) != 1 {
			continue
		}
		call, ok := init.Rhs[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		role, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || role.Sel.Name != "RequireEditor" {
			continue
		}
		for node := range ast.Preorder(branch.Body) {
			if name, ok := node.(*ast.Ident); ok && name.Name == "draftCredentialLookupError" {
				return true
			}
		}
	}
	return false
}

func classifiedAccountLookups(fn *ast.FuncDecl) int {
	classified := 0
	for node := range ast.Preorder(fn.Body) {
		block, ok := node.(*ast.BlockStmt)
		if !ok {
			continue
		}
		for index, statement := range block.List {
			assignment, ok := statement.(*ast.AssignStmt)
			if !ok || len(assignment.Rhs) != 1 || index+1 >= len(block.List) {
				continue
			}
			call, ok := assignment.Rhs[0].(*ast.CallExpr)
			if !ok {
				continue
			}
			lookup, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || lookup.Sel.Name != "GetUserByID" {
				continue
			}
			branch, ok := block.List[index+1].(*ast.IfStmt)
			if !ok || len(branch.Body.List) != 1 {
				continue
			}
			ret, ok := branch.Body.List[0].(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				continue
			}
			mapping, ok := ret.Results[0].(*ast.CallExpr)
			if !ok {
				continue
			}
			switch mapper := mapping.Fun.(type) {
			case *ast.Ident:
				if fn.Name.Name == "authorizeWrite" && mapper.Name == "draftCredentialLookupError" {
					classified++
				}
			case *ast.SelectorExpr:
				if fn.Name.Name != "authorizeWrite" && mapper.Sel.Name == "EditorError" {
					classified++
				}
			}
		}
	}
	return classified
}
