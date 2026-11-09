// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// Pin the request attributes of the core pre-auth error paths. Login
// protection also passes attributes to slog via a shared slice.
func TestPreAuthErrorLogsBoundRequestFields(t *testing.T) {
	fields := []string{"path", "method", "request_id", "origin", "sec_fetch_site", "email", "ip", "panic", "slug", "query", "category", "tag"}
	for _, name := range []string{"middleware/csrf.go", "middleware/timeout.go", "middleware/login_protection.go",
		"handler/frontend.go", "handler/frontend_preview.go", "handler/forms.go", "handler/auth.go", "handler/mfa.go"} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join("..", "..", "internal", name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		check := func(key, value ast.Expr) {
			literal, ok := key.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return
			}
			field, _ := strconv.Unquote(literal.Value)
			if !slices.Contains(fields, field) {
				return
			}
			if _, ok := value.(*ast.BasicLit); ok {
				return
			}
			if call, ok := value.(*ast.CallExpr); ok {
				if fn, ok := call.Fun.(*ast.SelectorExpr); ok && fn.Sel.Name == "BoundedLogField" {
					if pkg, ok := fn.X.(*ast.Ident); ok && pkg.Name == "util" {
						return
					}
				}
			}
			t.Errorf("%s logs request field %q without util.BoundedLogField", fset.Position(value.Pos()), field)
		}
		checkPairs := func(args []ast.Expr) {
			for i := 0; i+1 < len(args); i++ {
				check(args[i], args[i+1])
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				fn, ok := n.Fun.(*ast.SelectorExpr)
				if !ok || (fn.Sel.Name != "Error" && fn.Sel.Name != "ErrorContext") {
					return true
				}
				start := 1
				if fn.Sel.Name == "ErrorContext" {
					start++
				}
				if len(n.Args) > start {
					checkPairs(n.Args[start:])
				}
				for _, arg := range n.Args {
					attr, ok := arg.(*ast.CallExpr)
					if !ok || len(attr.Args) != 2 {
						continue
					}
					sel, ok := attr.Fun.(*ast.SelectorExpr)
					if ok && (sel.Sel.Name == "String" || sel.Sel.Name == "Any") {
						check(attr.Args[0], attr.Args[1])
					}
				}
			case *ast.CompositeLit:
				if arr, ok := n.Type.(*ast.ArrayType); ok && arr.Len == nil {
					if elt, ok := arr.Elt.(*ast.Ident); ok && elt.Name == "any" {
						checkPairs(n.Elts)
					}
				}
			}
			return true
		})
	}
}
