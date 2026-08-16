// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// canonicalURLValidator is the single rule every write path must reach.
// Matching on the identifier covers both the direct util call and a
// package-local wrapper named after it.
const canonicalURLValidator = "ValidateCanonicalURL"

// canonicalURLWriters pins every place that assigns a non-constant value to
// pages.canonical_url, keyed by "file:function". A site that writes a literal
// empty string is inherently safe and is not listed.
//
// Adding, renaming or moving a write path fails this test until its author
// records it here, which is the point: the admin form, the v2 API and the
// content import each grew their own rule at a different time, and the form
// spent months accepting values the API rejected.
var canonicalURLWriters = map[string]string{
	"internal/handler/pages.go:Create":                 "admin form create, validated by validateCanonicalURL",
	"internal/handler/pages.go:Update":                 "admin form update, validated by validateCanonicalURL",
	"internal/handler/pages.go:RestoreVersion":         "carries the current row forward, cleared when it no longer validates",
	"internal/transfer/importer.go:createNewPage":      "archive import, validated in importWithPreCommit preflight",
	"internal/transfer/importer.go:updateExistingPage": "archive import, validated in importWithPreCommit preflight",
	"internal/api/v2/pages/service.go:Create":          "v2 create, validated by validateCanonicalURLField",
	"internal/api/v2/pages/service.go:Update":          "v2 update baseline, re-reads the stored row before applyUpdate",
}

// TestEveryCanonicalURLWriterIsValidated asserts that no code path can put an
// unchecked value into pages.canonical_url.
//
// The stored value is emitted as a <link rel="canonical"> href and as og:url
// meta content. The href is scheme-filtered by both template engines, but the
// meta content attribute is not a URL context in either, and a theme can opt
// out of filtering entirely through the safeURL template function. Write-time
// validation is therefore the only real gate, and it has to hold on all three
// write paths at once — fixing one and leaving the others is exactly how this
// gap survived an audit that reported it.
func TestEveryCanonicalURLWriterIsValidated(t *testing.T) {
	root := repositoryRoot(t)
	fileSet := token.NewFileSet()
	found := map[string]string{}
	constantSites := 0

	for _, tree := range []string{"internal", "modules", "custom"} {
		walkErr := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "node_modules" || entry.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			// Generated SQLC code declares the field; it never assigns it.
			if !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") ||
				strings.HasSuffix(path, ".sql.go") {
				return nil
			}
			source, readErr := os.ReadFile(path) // #nosec G304 -- paths come from walking the repository tree
			if readErr != nil {
				return readErr
			}
			file, parseErr := parser.ParseFile(fileSet, path, source, 0)
			if parseErr != nil {
				return parseErr
			}

			relative := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
			validatorInFile := strings.Contains(string(source), canonicalURLValidator)

			for _, site := range canonicalURLWriteSites(file) {
				if site.constantEmpty {
					constantSites++
					continue
				}
				key := relative + ":" + site.function
				found[key] = site.function
				if _, pinned := canonicalURLWriters[key]; !pinned {
					t.Errorf("%s writes pages.canonical_url from a value this test does not know about; "+
						"validate it with %s and add it to canonicalURLWriters with the reason",
						key, canonicalURLValidator)
					continue
				}
				if !validatorInFile {
					t.Errorf("%s writes pages.canonical_url but %s never mentions %s; "+
						"an unchecked value reaches the canonical link and og:url",
						key, relative, canonicalURLValidator)
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", tree, walkErr)
		}
	}

	for key, reason := range canonicalURLWriters {
		if reason == "" {
			t.Errorf("%s: every pinned writer must record why its value is safe", key)
		}
		if _, seen := found[key]; !seen {
			t.Errorf("%s is pinned in canonicalURLWriters but no longer writes pages.canonical_url; "+
				"remove the entry so the list keeps describing the code", key)
		}
	}

	if len(found) == 0 {
		t.Fatal("found no canonical_url writers; this test is no longer checking anything")
	}
	if constantSites == 0 {
		t.Fatal("found no constant canonical_url writers; the constant-value branch is untested")
	}

	keys := make([]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	t.Logf("checked %d non-constant and %d constant canonical_url writers: %s",
		len(found), constantSites, strings.Join(keys, ", "))
}

// canonicalURLWriteSite is one CanonicalUrl assignment inside page insert or
// update parameters, with the function that contains it.
type canonicalURLWriteSite struct {
	function      string
	constantEmpty bool
}

// canonicalURLWriteSites finds every CanonicalUrl field assignment in a
// CreatePageParams or UpdatePageParams literal, which is the only way a value
// reaches the column.
func canonicalURLWriteSites(file *ast.File) []canonicalURLWriteSite {
	var sites []canonicalURLWriteSite

	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(function, func(node ast.Node) bool {
			composite, ok := node.(*ast.CompositeLit)
			if !ok || !isPageParamsType(composite.Type) {
				return true
			}
			for _, element := range composite.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := pair.Key.(*ast.Ident)
				if !ok || key.Name != "CanonicalUrl" {
					continue
				}
				sites = append(sites, canonicalURLWriteSite{
					function:      function.Name.Name,
					constantEmpty: isEmptyStringLiteral(pair.Value),
				})
			}
			return true
		})
	}
	return sites
}

// isPageParamsType reports whether a composite literal builds page insert or
// update parameters.
func isPageParamsType(expr ast.Expr) bool {
	var name string
	switch typeExpr := expr.(type) {
	case *ast.SelectorExpr:
		name = typeExpr.Sel.Name
	case *ast.Ident:
		name = typeExpr.Name
	}
	return name == "CreatePageParams" || name == "UpdatePageParams"
}

// isEmptyStringLiteral reports whether an expression is the literal "".
func isEmptyStringLiteral(expr ast.Expr) bool {
	literal, ok := expr.(*ast.BasicLit)
	return ok && literal.Kind == token.STRING && (literal.Value == `""` || literal.Value == "``")
}
