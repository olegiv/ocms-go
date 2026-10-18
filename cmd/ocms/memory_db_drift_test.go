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
// SQLite database from a literal DSN without limiting its pool to one
// connection. Each connection to ":memory:" (or a mode=memory URI) opens its
// own empty database, so a query that lands on a second pooled connection
// sees no schema, and a test can pass for the wrong reason or fail at random
// (review of the 2026-10-17 audit fixes). Shared-cache DSNs (cache=shared)
// are one database and are not checked. The open must be assigned to a
// local variable, and the limit set in the same function; an open that is
// returned or stored in a field cannot be checked and fails. DSNs held in
// variables or constants are not seen.
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
		if err != nil || !bytes.Contains(src, []byte(":memory:")) {
			return err
		}
		file, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			var body *ast.BlockStmt
			switch fn := n.(type) {
			case *ast.FuncDecl:
				body = fn.Body
			case *ast.FuncLit:
				body = fn.Body
			}
			if body == nil {
				return true
			}
			for _, open := range memoryDBOpens(body) {
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
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("reading the repository: %v", err)
	}
	if opens < 10 {
		t.Fatalf("found only %d in-memory database opens; the scan is broken and this test is vacuous", opens)
	}
}

// memoryDBOpen is an in-memory database open: where it is, and the local
// variable it is assigned to, or "" when it is not assigned to one.
type memoryDBOpen struct {
	pos  token.Pos
	name string
}

// memoryDBOpens returns the sql.Open calls with a literal in-memory DSN that
// does not share its cache in body, not counting nested function literals.
func memoryDBOpens(body *ast.BlockStmt) []memoryDBOpen {
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
			if isMemoryDBOpen(stmt) {
				calls = append(calls, stmt)
			}
		}
		if ident, ok := lhs.(*ast.Ident); ok && len(rhs) == 1 {
			if call, ok := rhs[0].(*ast.CallExpr); ok {
				assigned[call] = ident.Name
			}
		}
	})
	opens := make([]memoryDBOpen, 0, len(calls))
	for _, call := range calls {
		opens = append(opens, memoryDBOpen{pos: call.Pos(), name: assigned[call]})
	}
	return opens
}

// isMemoryDBOpen reports whether call is sql.Open with a literal in-memory
// DSN that does not share its cache.
func isMemoryDBOpen(call *ast.CallExpr) bool {
	if !isSelectorCall(call, "sql", "Open") || len(call.Args) != 2 {
		return false
	}
	lit, ok := call.Args[1].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	dsn, err := strconv.Unquote(lit.Value)
	return err == nil && (strings.Contains(dsn, ":memory:") || strings.Contains(dsn, "mode=memory")) &&
		!strings.Contains(dsn, "cache=shared")
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
