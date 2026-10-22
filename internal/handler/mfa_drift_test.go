// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil/repotree"
)

// TestOnlyGrantSessionSignsIn fails when code outside AuthHandler.grantSession
// puts a user ID into a session.
//
// A session carrying middleware.SessionKeyUserID is signed in: every route
// behind middleware.Auth serves it. grantSession is where the login steps
// meet, after the password and, when the account has one or must have one,
// the second factor, and where the lockout is cleared and the sign-in logged.
// A second writer, such as a new login shortcut or a module's own sign-in,
// would hand out sessions that skip two-factor authentication.
//
// Bug state: put SessionKeyUserID in Login before the two-factor check, or in
// any other function, and this names the call.
func TestOnlyGrantSessionSignsIn(t *testing.T) {
	root := filepath.Join("..", "..")
	var allowed int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if repotree.Skip(root, path, d) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isPutOfUserID(call) {
					return true
				}
				rel, _ := filepath.Rel(root, path)
				if filepath.ToSlash(rel) == "internal/handler/mfa.go" && isAuthHandlerMethod(fn, "grantSession") {
					allowed++
					return true
				}
				t.Errorf("%s: %s puts SessionKeyUserID into a session; sign in through AuthHandler.grantSession so the second factor cannot be skipped",
					fset.Position(call.Pos()), funcName(fn))
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if allowed != 1 {
		t.Errorf("found %d SessionKeyUserID writes in AuthHandler.grantSession, want 1; the scan no longer sees the sign-in", allowed)
	}
}

// isPutOfUserID reports whether call is x.Put(ctx, ...SessionKeyUserID, v).
func isPutOfUserID(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Put" || len(call.Args) < 2 {
		return false
	}
	switch key := call.Args[1].(type) {
	case *ast.Ident:
		return key.Name == "SessionKeyUserID"
	case *ast.SelectorExpr:
		return key.Sel.Name == "SessionKeyUserID"
	case *ast.BasicLit:
		return key.Value == `"user_id"`
	}
	return false
}

func isAuthHandlerMethod(fn *ast.FuncDecl, name string) bool {
	if fn.Name.Name != name || fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == "AuthHandler"
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	switch recv := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if ident, ok := recv.X.(*ast.Ident); ok {
			return "(*" + ident.Name + ")." + fn.Name.Name
		}
	case *ast.Ident:
		return recv.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}
