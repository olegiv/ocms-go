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

// canonicalURLValidator matches the rule every write path must reach. It is
// compared case-insensitively against identifiers so it covers the direct
// util.ValidateCanonicalURL call and both package-local wrappers
// (validateCanonicalURL, validateCanonicalURLField).
const canonicalURLValidator = "validatecanonicalurl"

// canonicalURLWriter records one pinned write path and the function that is
// responsible for validating what it stores. validatedIn is usually the writer
// itself; it differs only where validation happens in an earlier pass over the
// whole payload, as the importer does in its preflight.
type canonicalURLWriter struct {
	reason      string
	validatedIn string
}

// canonicalURLWriters pins every place that assigns a non-constant value to
// pages.canonical_url, keyed by "file:function". A site that writes a literal
// empty string is inherently safe and is not listed.
//
// Adding, renaming or moving a write path fails this test until its author
// records it here, which is the point: the admin form, the v2 API and the
// content import each grew their own rule at a different time, and the form
// spent months accepting values the API rejected.
var canonicalURLWriters = map[string]canonicalURLWriter{
	"internal/handler/pages.go:Create": {
		reason:      "admin form create",
		validatedIn: "Create",
	},
	"internal/handler/pages.go:Update": {
		reason:      "admin form update",
		validatedIn: "Update",
	},
	"internal/handler/pages.go:RestoreVersion": {
		reason:      "carries the current row forward, cleared when it no longer validates",
		validatedIn: "RestoreVersion",
	},
	"internal/transfer/importer.go:createNewPage": {
		reason:      "archive import; the whole payload is normalized in preflight",
		validatedIn: "normalizeImportedPageCanonicalURLs",
	},
	"internal/transfer/importer.go:updateExistingPage": {
		reason:      "archive import; the whole payload is normalized in preflight",
		validatedIn: "normalizeImportedPageCanonicalURLs",
	},
	"internal/api/v2/pages/service.go:Create": {
		reason:      "v2 create",
		validatedIn: "Create",
	},
	"internal/api/v2/pages/service.go:Update": {
		reason:      "v2 update baseline; clears a stored value that no longer validates",
		validatedIn: "Update",
	},
	"internal/api/v2/pages/service.go:applyUpdate": {
		reason:      "v2 update applies the client's value",
		validatedIn: "applyUpdate",
	},
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
//
// Scope: this walks Go source for writes through CreatePageParams and
// UpdatePageParams, which are the only queries that write the column. It does
// not and cannot cover the DB Manager module's arbitrary-SQL console
// (modules/dbmanager), which is an admin-only escape hatch by design.
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
			validators := functionsReachingValidator(file)

			for _, site := range canonicalURLWriteSites(file) {
				if site.constantEmpty {
					constantSites++
					continue
				}
				key := relative + ":" + site.function
				found[key] = site.function
				writer, pinned := canonicalURLWriters[key]
				if !pinned {
					t.Errorf("%s writes pages.canonical_url from a value this test does not know about; "+
						"validate it and add it to canonicalURLWriters with the reason",
						key)
					continue
				}
				// Checking the named function rather than the file is what
				// makes this bite: a file keeps mentioning the validator from
				// some other function long after a call is deleted.
				if !validators[writer.validatedIn] {
					t.Errorf("%s writes pages.canonical_url but %s in %s does not reach the validator; "+
						"an unchecked value reaches the canonical link and og:url",
						key, writer.validatedIn, relative)
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", tree, walkErr)
		}
	}

	for key, writer := range canonicalURLWriters {
		if writer.reason == "" || writer.validatedIn == "" {
			t.Errorf("%s: every pinned writer must record its reason and validating function", key)
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

// canonicalURLWriteSites finds every place a function sets the CanonicalUrl
// field, either as a key in a CreatePageParams/UpdatePageParams literal or as a
// later assignment to that field. Both forms are in use: the v2 patch path
// builds a baseline literal and then overwrites the field, so matching only
// literals would miss the code that applies the caller's value.
func canonicalURLWriteSites(file *ast.File) []canonicalURLWriteSite {
	var sites []canonicalURLWriteSite

	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(function, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CompositeLit:
				if !isPageParamsType(typed.Type) {
					return true
				}
				for _, element := range typed.Elts {
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
			case *ast.AssignStmt:
				for index, target := range typed.Lhs {
					selector, ok := target.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "CanonicalUrl" {
						continue
					}
					constantEmpty := index < len(typed.Rhs) && isEmptyStringLiteral(typed.Rhs[index])
					sites = append(sites, canonicalURLWriteSite{
						function:      function.Name.Name,
						constantEmpty: constantEmpty,
					})
				}
			}
			return true
		})
	}
	return sites
}

// functionsReachingValidator reports which top-level functions mention the
// canonical URL validator, by any of its names. Comparison is on identifiers
// rather than raw source so a mention inside a comment cannot satisfy the
// check.
func functionsReachingValidator(file *ast.File) map[string]bool {
	reaching := map[string]bool{}

	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(function, func(node ast.Node) bool {
			var name string
			switch typed := node.(type) {
			case *ast.SelectorExpr:
				name = typed.Sel.Name
			case *ast.Ident:
				name = typed.Name
			default:
				return true
			}
			if strings.Contains(strings.ToLower(name), canonicalURLValidator) {
				reaching[function.Name.Name] = true
				return false
			}
			return true
		})
	}
	return reaching
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
