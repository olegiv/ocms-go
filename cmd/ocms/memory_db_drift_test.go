// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestInMemoryDatabasesKeepOneConnection fails when code opens an in-memory
// SQLite database without limiting its pool to one connection. Each
// connection to ":memory:" (or a mode=memory URI) opens its own empty
// database, so a query that lands on a second pooled connection sees no
// schema, and a test can pass for the wrong reason or fail at random
// (review of the 2026-10-17 audit fixes). Shared-cache DSNs (cache=shared)
// are one database and are not checked.
//
// The DSN is read, best effort, from its literal parts: a literal, a
// concatenation, the arguments of a call (fmt.Sprintf, filepath.Join), or a
// variable or constant defined in the function, an enclosing function or
// the file, merged by name without regard to scope. A part it cannot read
// is left out, so a parameter with a literal suffix, or os.Getenv("X")
// (whose argument names the variable, not the DSN), counts as a file
// database and is not checked. An open in a _test.go file whose DSN has no
// literal part at all fails, since it cannot be checked; elsewhere it is
// skipped. An in-memory open must be assigned to a local variable, and the
// limit set in the same function; an open that is returned or stored in a
// field cannot be checked and fails. store.NewDB keeps in-memory databases
// on one connection itself.
//
// Bug state: delete db.SetMaxOpenConns(1) from testutil.TestMemoryDB and
// this names the open it belongs to.
func TestInMemoryDatabasesKeepOneConnection(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	opens := 0
	err := walkRepoFiles(root, func(rel string) bool {
		return strings.HasSuffix(rel, ".go")
	}, func(path, rel string) error {
		src, err := os.ReadFile(path) // #nosec G304 -- repository files
		if err != nil || !bytes.Contains(src, []byte("sql.Open(")) {
			return err
		}
		file, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			return err
		}
		isTest := strings.HasSuffix(rel, "_test.go")
		fileDefs := definitions(file)
		var check func(body *ast.BlockStmt, outer map[string][]ast.Expr)
		check = func(body *ast.BlockStmt, outer map[string][]ast.Expr) {
			defs := mergeDefinitions(outer, definitions(body))
			for _, open := range sqlOpens(body) {
				parts := dsnParts(open.dsn, defs, 0)
				switch {
				case len(parts) == 0:
					if isTest {
						t.Errorf("%s opens a database with a DSN this test cannot read; build it from literals so an in-memory one can be checked",
							fset.Position(open.pos))
					}
					continue
				case !isMemoryDSN(parts):
					continue
				}
				opens++
				switch {
				case open.name == "":
					t.Errorf("%s opens an in-memory database without assigning it to a local variable, so its pool limit cannot be checked",
						fset.Position(open.pos))
				case !limitsPoolToOne(body, open.name):
					t.Errorf("%s opens an in-memory database as %s without %s.SetMaxOpenConns(1) in the same function",
						fset.Position(open.pos), open.name, open.name)
				}
			}
			ast.Inspect(body, func(n ast.Node) bool {
				if lit, ok := n.(*ast.FuncLit); ok {
					check(lit.Body, defs)
					return false
				}
				return true
			})
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				check(fn.Body, fileDefs)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the repository: %v", err)
	}
	if opens < 10 {
		t.Fatalf("found only %d in-memory database opens; the scan is broken and this test is vacuous", opens)
	}
}

// sqlOpen is a sql.Open call: where it is, its DSN, and the local variable
// it is assigned to, or "" when it is not assigned to one.
type sqlOpen struct {
	pos  token.Pos
	dsn  ast.Expr
	name string
}

// sqlOpens returns the sql.Open calls in body, not counting nested function
// literals.
func sqlOpens(body *ast.BlockStmt) []sqlOpen {
	assigned := map[*ast.CallExpr]string{}
	var calls []*ast.CallExpr
	inspectOwnStatements(body, func(n ast.Node) {
		var lhs ast.Expr
		var rhs []ast.Expr
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			if len(stmt.Lhs) > 0 {
				lhs, rhs = stmt.Lhs[0], stmt.Rhs
			}
		case *ast.ValueSpec:
			if len(stmt.Names) > 0 {
				lhs, rhs = stmt.Names[0], stmt.Values
			}
		case *ast.CallExpr:
			if isSelectorCall(stmt, "sql", "Open") && len(stmt.Args) == 2 {
				calls = append(calls, stmt)
			}
		}
		if ident, ok := lhs.(*ast.Ident); ok && len(rhs) == 1 {
			if call, ok := rhs[0].(*ast.CallExpr); ok {
				assigned[call] = ident.Name
			}
		}
	})
	opens := make([]sqlOpen, 0, len(calls))
	for _, call := range calls {
		opens = append(opens, sqlOpen{pos: call.Pos(), dsn: call.Args[1], name: assigned[call]})
	}
	return opens
}

// definitions maps each name node assigns or declares, not counting nested
// function literals, to the expressions it is given.
func definitions(node ast.Node) map[string][]ast.Expr {
	defs := map[string][]ast.Expr{}
	add := func(names []ast.Expr, values []ast.Expr) {
		for i, name := range names {
			ident, ok := name.(*ast.Ident)
			if !ok {
				continue
			}
			switch {
			case len(values) == len(names):
				defs[ident.Name] = append(defs[ident.Name], values[i])
			case len(values) == 1 && i == 0: // dsn, err := build()
				defs[ident.Name] = append(defs[ident.Name], values[0])
			}
		}
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.FuncDecl:
			return node == n
		case *ast.AssignStmt:
			add(stmt.Lhs, stmt.Rhs)
		case *ast.ValueSpec:
			names := make([]ast.Expr, len(stmt.Names))
			for i, name := range stmt.Names {
				names[i] = name
			}
			add(names, stmt.Values)
		}
		return true
	})
	return defs
}

// mergeDefinitions returns the definitions of outer and inner together.
func mergeDefinitions(outer, inner map[string][]ast.Expr) map[string][]ast.Expr {
	merged := make(map[string][]ast.Expr, len(outer)+len(inner))
	for name, exprs := range outer {
		merged[name] = append(merged[name], exprs...)
	}
	for name, exprs := range inner {
		merged[name] = append(merged[name], exprs...)
	}
	return merged
}

// dsnParts returns the string literals a DSN expression is built from,
// following variables and constants through defs.
func dsnParts(expr ast.Expr, defs map[string][]ast.Expr, depth int) []string {
	if depth > 8 {
		return nil
	}
	switch e := expr.(type) {
	case *ast.BasicLit:
		if value, err := strconv.Unquote(e.Value); err == nil && e.Kind == token.STRING {
			return []string{value}
		}
	case *ast.BinaryExpr:
		return append(dsnParts(e.X, defs, depth+1), dsnParts(e.Y, defs, depth+1)...)
	case *ast.ParenExpr:
		return dsnParts(e.X, defs, depth+1)
	case *ast.CallExpr:
		var parts []string
		for _, arg := range e.Args {
			parts = append(parts, dsnParts(arg, defs, depth+1)...)
		}
		return parts
	case *ast.Ident:
		var parts []string
		for _, def := range defs[e.Name] {
			parts = append(parts, dsnParts(def, defs, depth+1)...)
		}
		return parts
	}
	return nil
}

// isMemoryDSN reports whether a DSN built from parts names an in-memory
// database that does not share its cache.
func isMemoryDSN(parts []string) bool {
	memory, shared := false, false
	for _, part := range parts {
		memory = memory || strings.Contains(part, ":memory:") || strings.Contains(part, "mode=memory")
		shared = shared || strings.Contains(part, "cache=shared")
	}
	return memory && !shared
}

// limitsPoolToOne reports whether body, not counting nested function
// literals, calls name.SetMaxOpenConns(1).
func limitsPoolToOne(body *ast.BlockStmt, name string) bool {
	found := false
	inspectOwnStatements(body, func(n ast.Node) {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isSelectorCall(call, name, "SetMaxOpenConns") || len(call.Args) != 1 {
			return
		}
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Value == "1" {
			found = true
		}
	})
	return found
}

// inspectOwnStatements calls visit for every node in body except those
// inside nested function literals, which are functions of their own.
func inspectOwnStatements(body *ast.BlockStmt, visit func(ast.Node)) {
	ast.Inspect(body, func(n ast.Node) bool {
		if _, nested := n.(*ast.FuncLit); nested {
			return false
		}
		if n != nil {
			visit(n)
		}
		return true
	})
}

// isSelectorCall reports whether call calls x.sel for an identifier x.
func isSelectorCall(call *ast.CallExpr, x, sel string) bool {
	fun, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || fun.Sel.Name != sel {
		return false
	}
	ident, ok := fun.X.(*ast.Ident)
	return ok && ident.Name == x
}
