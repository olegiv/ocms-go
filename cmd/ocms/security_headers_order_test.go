// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// middlewareBeforeSecurityHeaders lists the only root middleware allowed to
// run before middleware.SecurityHeaders. None of them writes a response: they
// tag the request (ID, trusted-proxy flag) or rewrite RemoteAddr.
var middlewareBeforeSecurityHeaders = map[string]bool{
	"chimw.RequestID":                 true,
	"middleware.AnnotateTrustedProxy": true,
	"func literal":                    true, // trusted-proxy-aware RemoteAddr rewrite
}

// TestSecurityHeadersPrecedeAnsweringMiddleware fails when a root middleware
// outside middlewareBeforeSecurityHeaders is registered before
// middleware.SecurityHeaders in run().
//
// A middleware that answers on its own (sentinel ban, timeout, trailing-slash
// or content redirect) only sends the security headers if SecurityHeaders ran
// first. The trailing-slash 301 used to go out with no CSP, HSTS or COOP
// (security audit 2026-10-17, follow-up to I-4).
//
// Bug state: move r.Use(middleware.SecurityHeaders(...)) back below
// r.Use(redirectsMiddleware.Handler) and this names the middleware in front.
func TestSecurityHeadersPrecedeAnsweringMiddleware(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}
	var run *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "run" {
			run = fn
		}
	}
	if run == nil {
		t.Fatal("run() not found in main.go")
	}

	var before []string
	found := false
	for _, stmt := range run.Body.List {
		name, ok := rootUseArg(stmt)
		if !ok {
			continue
		}
		if name == "middleware.SecurityHeaders" {
			found = true
			break
		}
		before = append(before, name)
	}
	if !found {
		t.Fatal("r.Use(middleware.SecurityHeaders(...)) not found at the top level of run()")
	}
	if len(before) == 0 {
		t.Fatal("no middleware found before SecurityHeaders; the scan is broken and this test is vacuous")
	}
	for _, name := range before {
		if !middlewareBeforeSecurityHeaders[name] {
			t.Errorf("%s runs before middleware.SecurityHeaders; its responses would miss the security headers", name)
		}
	}
}

// rootUseArg returns the middleware named in a top-level r.Use(...) statement:
// "pkg.Func" for pkg.Func or pkg.Func(...), "func literal" for an inline one.
func rootUseArg(stmt ast.Stmt) (string, bool) {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return "", false
	}
	call, ok := exprStmt.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Use" {
		return "", false
	}
	if recv, ok := selector.X.(*ast.Ident); !ok || recv.Name != "r" {
		return "", false
	}
	arg := call.Args[0]
	if inner, ok := arg.(*ast.CallExpr); ok {
		arg = inner.Fun
	}
	switch fn := arg.(type) {
	case *ast.FuncLit:
		return "func literal", true
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); ok {
			return pkg.Name + "." + fn.Sel.Name, true
		}
	}
	return "unrecognised middleware", true
}
