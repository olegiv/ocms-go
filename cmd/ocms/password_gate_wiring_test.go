// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// lockoutSetters are the handler methods that give a password gate the
// login lockout after construction.
var lockoutSetters = map[string]bool{
	"SetLoginProtection":      true,
	"SetPasswordConfirmation": true,
}

// TestPasswordGatesGetTheLockout fails when main.go builds a handler whose
// password gate takes the login lockout without giving it one: a nil
// *middleware.LoginProtection passed to its constructor, or a lockout setter
// (SetLoginProtection, SetPasswordConfirmation) never called with a non-nil
// first argument. The gate would still check the password, but wrong ones
// would stop counting toward the lockout, so a stolen session could guess
// it there without limit.
//
// It reads every non-test file of cmd/ocms. Handlers are matched to
// variables through their constructors (usersHandler :=
// handler.NewUsersHandler(...)), and every constructor that takes the
// lockout must be called somewhere.
//
// Bug state: drop modulesHandler.SetPasswordConfirmation from main.go, or
// pass nil for NewThemesHandler's lockout (in main.go or a helper file),
// and this names the handler.
func TestPasswordGatesGetTheLockout(t *testing.T) {
	setters, lockoutParams := handlerLockoutAPI(t)
	if len(setters) == 0 || len(lockoutParams) == 0 {
		t.Fatalf("found %d lockout setters and %d constructors taking the lockout in internal/handler; the scan no longer sees them", len(setters), len(lockoutParams))
	}
	files := parseNonTestGoFiles(t, ".")

	varType := map[string]string{} // usersHandler -> UsersHandler
	called := map[string]bool{}    // NewUsersHandler
	wired := map[string]bool{}     // "UsersHandler.SetLoginProtection"
	for name, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.AssignStmt:
				for i, rhs := range node.Rhs {
					constructor, ok := handlerConstructor(rhs)
					if !ok || i >= len(node.Lhs) {
						continue
					}
					if ident, ok := node.Lhs[i].(*ast.Ident); ok {
						varType[ident.Name] = strings.TrimPrefix(constructor, "New")
					}
				}
			case *ast.CallExpr:
				if constructor, ok := handlerConstructor(node); ok {
					called[constructor] = true
					if idx, gated := lockoutParams[constructor]; gated && (idx >= len(node.Args) || isNil(node.Args[idx])) {
						t.Errorf("%s calls handler.%s without the login lockout, so wrong passwords at its password gate are not counted", name, constructor)
					}
				}
			}
			return true
		})
	}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 || isNil(call.Args[0]) {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !lockoutSetters[sel.Sel.Name] {
				return true
			}
			if recv, ok := sel.X.(*ast.Ident); ok {
				wired[varType[recv.Name]+"."+sel.Sel.Name] = true
			}
			return true
		})
	}
	for constructor := range lockoutParams {
		if !called[constructor] {
			t.Errorf("cmd/ocms never calls handler.%s, which takes the login lockout; the scan no longer sees how that handler is built", constructor)
		}
	}
	for setter := range setters {
		if !wired[setter] {
			t.Errorf("cmd/ocms never calls %s with the login lockout, so wrong passwords at that handler's password gate are not counted", setter)
		}
	}
}

// parseNonTestGoFiles parses the non-test Go files in dir, keyed by name.
func parseNonTestGoFiles(t *testing.T, dir string) map[string]*ast.File {
	t.Helper()
	goFiles, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*ast.File{}
	for _, goFile := range goFiles {
		if strings.HasSuffix(goFile, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), goFile, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files[goFile] = file
	}
	return files
}

// handlerLockoutAPI reads internal/handler and returns its lockout setters
// ("UsersHandler.SetLoginProtection") and, for each constructor taking a
// *middleware.LoginProtection, that parameter's index.
func handlerLockoutAPI(t *testing.T) (setters map[string]bool, lockoutParams map[string]int) {
	t.Helper()
	setters = map[string]bool{}
	lockoutParams = map[string]int{}
	for _, file := range parseNonTestGoFiles(t, filepath.Join("..", "..", "internal", "handler")) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fn.Recv != nil {
				if typeName, ok := receiverTypeName(fn); ok && lockoutSetters[fn.Name.Name] {
					setters[typeName+"."+fn.Name.Name] = true
				}
				continue
			}
			if !strings.HasPrefix(fn.Name.Name, "New") {
				continue
			}
			idx := 0
			for _, field := range fn.Type.Params.List {
				names := max(len(field.Names), 1)
				if isLoginProtectionPointer(field.Type) {
					lockoutParams[fn.Name.Name] = idx
				}
				idx += names
			}
		}
	}
	return setters, lockoutParams
}

// receiverTypeName returns the type name of fn's receiver, for a value or
// pointer receiver and with any type parameters dropped.
func receiverTypeName(fn *ast.FuncDecl) (string, bool) {
	if len(fn.Recv.List) == 0 {
		return "", false
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch e := expr.(type) {
	case *ast.IndexExpr:
		expr = e.X
	case *ast.IndexListExpr:
		expr = e.X
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

// handlerConstructor reports the name of expr when it is a call to a
// handler package constructor, handler.NewX(...).
func handlerConstructor(expr ast.Expr) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(sel.Sel.Name, "New") {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "handler" {
		return "", false
	}
	return sel.Sel.Name, true
}

func isLoginProtectionPointer(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "LoginProtection" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "middleware"
}

func isNil(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}
