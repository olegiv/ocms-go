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
	"unicode"
	"unicode/utf8"
)

// lockoutSetters are the handler methods that give a password gate the
// login lockout. A handler whose setter main never calls still checks the
// password, but wrong ones stop counting toward the lockout, so a stolen
// session could guess it there without limit.
var lockoutSetters = map[string]bool{
	"SetLoginProtection":      true,
	"SetPasswordConfirmation": true,
}

// TestPasswordGatesGetTheLockout fails when main.go does not call a
// handler's lockout setter (SetLoginProtection, SetPasswordConfirmation)
// with a non-nil first argument. Handlers are matched to main's variables
// by name: UsersHandler to usersHandler.
//
// Bug state: drop modulesHandler.SetPasswordConfirmation from main.go, and
// this names ModulesHandler.
func TestPasswordGatesGetTheLockout(t *testing.T) {
	fset := token.NewFileSet()
	paths, err := filepath.Glob(filepath.Join("..", "..", "internal", "handler", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{} // "usersHandler.SetLoginProtection" -> type
	for _, goFile := range paths {
		if strings.HasSuffix(goFile, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, goFile, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !lockoutSetters[fn.Name.Name] {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			typeName := star.X.(*ast.Ident).Name
			first, size := utf8.DecodeRuneInString(typeName)
			want[string(unicode.ToLower(first))+typeName[size:]+"."+fn.Name.Name] = typeName
		}
	}
	if len(want) == 0 {
		t.Fatal("found no lockout setters in internal/handler; the scan no longer sees them")
	}

	mainFile, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(mainFile, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if arg, isIdent := call.Args[0].(*ast.Ident); isIdent && arg.Name == "nil" {
			return true
		}
		delete(want, recv.Name+"."+sel.Sel.Name)
		return true
	})
	for call, typeName := range want {
		t.Errorf("main.go never calls %s with the login lockout, so wrong passwords at %s's password gate are not counted", call, typeName)
	}
}
