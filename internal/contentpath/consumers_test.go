// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package contentpath_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// TestFrontendAndGroundingKeepUsingSharedResolvers supplements executed route fixtures.
func TestFrontendAndGroundingKeepUsingSharedResolvers(t *testing.T) {
	for _, tc := range []struct{ file, function, callee string }{
		{"../middleware/language.go", "activeLanguageMap", "ActiveLanguages"},
		{"../middleware/language.go", "matchLanguagePrefix", "MatchLanguagePrefix"},
		{"../middleware/redirects.go", "Handler", "FirstRedirect"},
		{"../handler/frontend.go", "canonicalPageLanguagePrefix", "PageLanguagePrefix"},
		{"../handler/frontend.go", "canonicalAliasLanguagePrefix", "AliasLanguagePrefix"},
		{"../handler/frontend.go", "pageAliasLookupPath", "AliasPath"},
		{"../handler/frontend.go", "explicitPageLanguage", "ExplicitLanguage"},
		{"../handler/frontend.go", "publishedPageForRoute", "PublishedPage"},
		{"../handler/frontend.go", "PageByID", "PublishedPageByID"},
		{"../handler/frontend.go", "Category", "Category"},
		{"../handler/frontend.go", "Tag", "Tag"},
		{"../handler/forms.go", "getActiveFormBySlug", "PublicForm"},
		{"../../modules/migrator/sources/shared/redirects.go", "WildcardRedirectMatchesPath", "MatchRedirect"},
		{"../ai/grounding/resolver.go", "resolvePath", "FirstRedirect"},
		{"../ai/grounding/resolver.go", "resolvePath", "PublishedPage"},
		{"../ai/grounding/resolver.go", "resolvePath", "DynamicRoute"},
		{"../ai/draft/links.go", "origin", "Origin"},
	} {
		t.Run(tc.function+"/"+tc.callee, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tc.file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			alias := ""
			for _, spec := range file.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if path == "github.com/olegiv/ocms-go/internal/contentpath" {
					alias = "contentpath"
					if spec.Name != nil {
						alias = spec.Name.Name
					}
				}
			}
			found := false
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != tc.function {
					continue
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					owner, ok := selector.X.(*ast.Ident)
					if ok && alias != "" && owner.Name == alias && selector.Sel.Name == tc.callee {
						found = true
					}
					return true
				})
			}
			if !found {
				t.Fatalf("%s %s no longer delegates to shared %s", tc.file, tc.function, tc.callee)
			}
		})
	}
}
