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
)

// TestRefererRedirectsUseSafeHelper fails when runtime code redirects in a
// function that reads the Referer header, instead of calling
// SafeRefererRedirect.
//
// A same-host Referer is not a safe destination on its own: a path starting
// with "//" becomes a scheme-relative Location that sends the browser to
// another host. The language switch re-implemented the same-host check
// without that case after demo mode already had it (security audit
// 2026-10-17, L-1). The check is per function declaration, closures
// included; a Referer passed to another function first is not traced.
//
// Bug state: restore the inline url.Parse(ref) / http.Redirect block in
// setLanguagePreference (internal/handler/response.go) and this names the
// function.
func TestRefererRedirectsUseSafeHelper(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var offenders []string
	helperFound := false

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Dot directories hold tooling and gitignored audit snapshots;
			// output/ holds gitignored scratch programs.
			if path != repoRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			switch d.Name() {
			case "node_modules", "vendor", "wiki", "bin", "data", "uploads", "output":
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
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			readsReferer, redirect := scanRefererRedirect(fn.Body)
			if fn.Name.Name == "SafeRefererRedirect" {
				helperFound = readsReferer
				continue
			}
			if readsReferer && redirect.IsValid() {
				pos := fset.Position(redirect)
				rel, rerr := filepath.Rel(repoRoot, pos.Filename)
				if rerr != nil {
					rel = pos.Filename
				}
				offenders = append(offenders, rel+":"+strconv.Itoa(pos.Line)+" in "+fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking repository: %v", err)
	}

	if !helperFound {
		t.Fatal("SafeRefererRedirect no longer reads the Referer header; this test is vacuous")
	}
	if len(offenders) > 0 {
		t.Errorf("these functions read the Referer header and redirect without "+
			"middleware.SafeRefererRedirect, which rejects cross-origin and "+
			"scheme-relative (\"//host\") destinations:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// scanRefererRedirect reports whether body reads the Referer header and the
// position of its first redirect call other than SafeRefererRedirect.
func scanRefererRedirect(body *ast.BlockStmt) (readsReferer bool, redirect token.Pos) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			name = fun.Name
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		}
		switch {
		case name == "Referer" && len(call.Args) == 0:
			readsReferer = true
		case name == "Get" && len(call.Args) == 1 && isRefererLiteral(call.Args[0]):
			readsReferer = true
		case strings.Contains(name, "Redirect") && name != "SafeRefererRedirect" && !redirect.IsValid():
			redirect = call.Pos()
		}
		return true
	})
	return readsReferer, redirect
}

// isRefererLiteral reports whether expr is the string literal "Referer" in any case.
func isRefererLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(lit.Value)
	return err == nil && strings.EqualFold(value, "Referer")
}
