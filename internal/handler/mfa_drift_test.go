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
// would hand out sessions that skip two-factor authentication. The key is
// matched by value, so a module's own constant for "user_id" (sentinel has
// one) or the bare literal counts too, and package-level func literals are
// scanned as well as functions.
//
// Bug state: put SessionKeyUserID in Login before the two-factor check, or in
// any other function, and this names the call.
func TestOnlyGrantSessionSignsIn(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
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
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = file
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	keys := userIDKeyNames(files)
	if !keys["SessionKeyUserID"] {
		t.Fatal(`no constant named SessionKeyUserID holds "user_id"; the scan no longer knows the session key`)
	}
	var allowed int
	for rel, file := range files {
		for _, put := range userIDPuts(file, keys) {
			if rel == "internal/handler/mfa.go" && put.fn != nil && isAuthHandlerMethod(put.fn, "grantSession") {
				allowed++
				continue
			}
			t.Errorf("%s: %s puts the user ID key into a session; sign in through AuthHandler.grantSession so the second factor cannot be skipped",
				fset.Position(put.pos), put.where)
		}
	}
	if allowed != 1 {
		t.Errorf("found %d user ID writes in AuthHandler.grantSession, want 1; the scan no longer sees the sign-in", allowed)
	}
}

// TestUserIDPutScanMatchesByValue checks the scan behind
// TestOnlyGrantSessionSignsIn on a fixture: a local constant with the key's
// value, the bare literal and a package-level func literal are all found,
// and other keys are not.
func TestUserIDPutScanMatchesByValue(t *testing.T) {
	const src = `package p
const sessionKeyUserID = "user_id"
var signIn = func() { sm.Put(ctx, sessionKeyUserID, 1) }
func f() {
	sm.Put(ctx, "user_id", 1)
	sm.Put(ctx, other.sessionKeyUserID, 1)
	sm.Put(ctx, "admin_lang", "en")
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*ast.File{"p.go": file}
	puts := userIDPuts(file, userIDKeyNames(files))
	if len(puts) != 3 {
		t.Fatalf("found %d user ID writes, want 3: %+v", len(puts), puts)
	}
}

// userIDKeyNames returns the names of the constants whose value is the
// session's user ID key, "user_id".
func userIDKeyNames(files map[string]*ast.File) map[string]bool {
	names := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i < len(vs.Values) && isUserIDLiteral(vs.Values[i]) {
						names[name.Name] = true
					}
				}
			}
		}
	}
	return names
}

// userIDPut is a call that puts the user ID key into a session.
type userIDPut struct {
	pos   token.Pos
	fn    *ast.FuncDecl // nil for a package-level declaration
	where string
}

// userIDPuts returns every x.Put(ctx, key, v) in file whose key is the user
// ID literal or one of keys.
func userIDPuts(file *ast.File, keys map[string]bool) []userIDPut {
	var puts []userIDPut
	for _, decl := range file.Decls {
		fn, _ := decl.(*ast.FuncDecl)
		where := "a package-level declaration"
		if fn != nil {
			where = funcName(fn)
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && isPutOfUserID(call, keys) {
				puts = append(puts, userIDPut{pos: call.Pos(), fn: fn, where: where})
			}
			return true
		})
	}
	return puts
}

// isPutOfUserID reports whether call is x.Put(ctx, key, v) with the user ID
// key: the literal, or a constant named in keys.
func isPutOfUserID(call *ast.CallExpr, keys map[string]bool) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Put" || len(call.Args) < 2 {
		return false
	}
	switch key := call.Args[1].(type) {
	case *ast.Ident:
		return keys[key.Name]
	case *ast.SelectorExpr:
		return keys[key.Sel.Name]
	}
	return isUserIDLiteral(call.Args[1])
}

func isUserIDLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING && lit.Value == `"user_id"`
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
