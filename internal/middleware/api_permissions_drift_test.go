// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil/repotree"
)

// strictPermissionDecoders are the files allowed to decode a stored API key
// permissions string. Both fail on malformed input instead of keeping what
// decoded: api.go answers "no permissions", the Connected Apps overview
// returns the error (its SQL already excludes keys whose array is not all
// text).
var strictPermissionDecoders = map[string]bool{
	"internal/middleware/api.go":        true,
	"internal/connectedapps/service.go": true,
}

// TestAPIKeyPermissionsHaveOneStrictDecoder fails when runtime code decodes
// an API key's stored permissions leniently.
//
// model.APIKey.GetPermissions dropped json.Unmarshal's error, so a stored
// `["mcp:access", 1]` decoded to a partial list. Nothing on an authority path
// called it, but the method and its HasPermission/HasAnyPermission wrappers
// stayed one call away from becoming a grant (security audit 2026-11-03, A-5).
// ParseAPIKeyPermissions is the decoder: it answers "no permissions" for
// anything that is not an array of strings.
//
// The scan finds every json.Unmarshal whose input reads a Permissions field,
// requires it to sit in strictPermissionDecoders and its error to be used,
// and rejects any method named GetPermissions.
//
// Bug state: restore `func (k *APIKey) GetPermissions()` in
// internal/model/api_key.go, or write `_ = json.Unmarshal([]byte(k.Permissions), &p)`
// anywhere in runtime code, and this names it.
func TestAPIKeyPermissionsHaveOneStrictDecoder(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var problems []string
	decoders := map[string]int{}
	report := func(pos token.Pos, format string) {
		p := fset.Position(pos)
		rel, err := filepath.Rel(repoRoot, p.Filename)
		if err != nil {
			rel = p.Filename
		}
		problems = append(problems, filepath.ToSlash(rel)+":"+strconv.Itoa(p.Line)+"  "+format)
	}

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if repotree.Skip(repoRoot, path, d) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil //nolint:nilerr // unparseable files are the compiler's problem
		}
		rel, rerr := filepath.Rel(repoRoot, path)
		if rerr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "GetPermissions" {
				report(fn.Pos(), "GetPermissions is a lenient permissions accessor; use middleware.ParseAPIKeyPermissions")
			}
		}

		var stack []ast.Node
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)
			call, ok := n.(*ast.CallExpr)
			if !ok || !isJSONUnmarshal(call) || len(call.Args) == 0 || !readsPermissionsField(call.Args[0]) {
				return true
			}
			decoders[rel]++
			if !strictPermissionDecoders[rel] {
				report(call.Pos(), "decodes API key permissions outside strictPermissionDecoders; use middleware.ParseAPIKeyPermissions")
			}
			if len(stack) >= 2 && discardsResult(stack[len(stack)-2], call) {
				report(call.Pos(), "drops the json.Unmarshal error, so malformed permissions decode partially")
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking repository: %v", err)
	}
	for path := range strictPermissionDecoders {
		if decoders[path] == 0 {
			problems = append(problems, path+"  is in strictPermissionDecoders but no longer decodes permissions; remove it (or the scan is broken)")
		}
	}
	if len(problems) > 0 {
		t.Errorf("API key permissions must be decoded strictly:\n  %s", strings.Join(problems, "\n  "))
	}
}

// isJSONUnmarshal reports whether call is json.Unmarshal(...).
func isJSONUnmarshal(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Unmarshal" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "json"
}

// readsPermissionsField reports whether expr mentions a field named
// Permissions, directly or inside a conversion such as []byte(k.Permissions).
func readsPermissionsField(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if selector, ok := n.(*ast.SelectorExpr); ok && selector.Sel.Name == "Permissions" {
			found = true
		}
		return !found
	})
	return found
}

// discardsResult reports whether the error returned by call is thrown away:
// the call is a bare statement or is assigned to the blank identifier.
func discardsResult(parent ast.Node, call *ast.CallExpr) bool {
	switch p := parent.(type) {
	case *ast.ExprStmt:
		return p.X == call
	case *ast.AssignStmt:
		for i, rhs := range p.Rhs {
			if rhs != call {
				continue
			}
			ident, ok := p.Lhs[min(i, len(p.Lhs)-1)].(*ast.Ident)
			return ok && ident.Name == "_"
		}
	}
	return false
}
