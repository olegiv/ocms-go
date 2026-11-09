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

// TestOAuthAllowedHostsComeFromSplitAllowedHosts fails when the server builds
// oauth.Options without reading the host allowlist through
// oauth.SplitAllowedHosts.
//
// oauth.Options.AllowedHosts treats nil as "the documented defaults" and an
// empty slice as "no hosts". A hand-rolled split can return nil for an empty
// setting, silently turning an operator's "pre-registered clients only" back
// into claude.ai and chatgpt.com (security audit 2026-11-03, A-4).
//
// Bug state: replace the AllowedHosts value in main.go with
// strings.Split(cfg.OAuthCIMDAllowedHosts, ",") and this names the literal.
func TestOAuthAllowedHostsComeFromSplitAllowedHosts(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	literals := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isOAuthQualifiedName(lit.Type, "oauth", "Options") {
				return true
			}
			literals++
			var hosts ast.Expr
			for _, element := range lit.Elts {
				if kv, ok := element.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "AllowedHosts" {
						hosts = kv.Value
					}
				}
			}
			position := fset.Position(lit.Pos())
			if hosts == nil {
				t.Errorf("%s: oauth.Options without AllowedHosts; the default allowlist would apply whatever OCMS_OAUTH_CIMD_ALLOWED_HOSTS says", position)
				return true
			}
			if call, ok := hosts.(*ast.CallExpr); !ok || !isOAuthQualifiedName(call.Fun, "oauth", "SplitAllowedHosts") {
				t.Errorf("%s: AllowedHosts must come from oauth.SplitAllowedHosts so an empty setting stays an empty allowlist", position)
			}
			return true
		})
	}
	if literals == 0 {
		t.Fatal("no oauth.Options literal found in cmd/ocms; the scan is broken and this test is vacuous")
	}
}

// isOAuthQualifiedName reports whether expr is the qualified identifier pkg.name.
func isOAuthQualifiedName(expr ast.Expr, pkg, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg
}
