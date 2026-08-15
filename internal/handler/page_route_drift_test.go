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
	"strings"
	"testing"
)

// pageRouteGuards are the identifiers that decide whether a slug can serve as
// a page route. Each write path reaches one of them: the admin form and the v2
// API refuse the collision outright, the migrator suffixes around it, and the
// transfer import rejects the archive in preflight.
var pageRouteGuards = []string{
	"LanguagePrefixConflict",               // internal/handler, shared with the v2 API
	"languagePrefixConflict",               // its PagesHandler wrapper
	"ensureSlugAvailable",                  // internal/api/v2/pages
	"MakeUniqueSlug",                       // modules/migrator/sources/*, covers …WithGuard
	"validateSlugsAgainstLanguagePrefixes", // internal/transfer preflight
	// The Drupal source resolves slugs through its own route-ownership check,
	// which already treats an active language prefix as taken (see
	// reservedPublicPath's availableLangs branch) and suffixes around it.
	"reservedPublicPath",
}

// pageWritersWithoutRouteGuard lists the files that write page slugs without
// consulting a guard, with the reason each is exempt. Anything not named here
// must reach a guard, so a new write path fails this test until its author
// decides which it is.
var pageWritersWithoutRouteGuard = map[string]string{
	// Fixed demo fixtures with hand-picked slugs, seeded only when
	// OCMS_DEMO_MODE asks for them.
	"internal/store/seed_demo.go": "seeded demo content, not caller input",
	// Slugs are slugified multi-word lorem titles from a developer-only test
	// data generator; they cannot land on a 2-10 character language code.
	"modules/developer/generator.go": "generated test data, developer module only",
}

// TestEveryPageWriterConsultsARouteGuard keeps one class of bug from returning
// through a path nobody remembered.
//
// The language middleware strips a first path segment matching an active
// language code before the frontend router matches anything, so a page stored
// at that segment is answered by the language homepage and never by itself.
// Slug validation looks at pages and aliases only, so each write path has to
// compare the two namespaces itself. The admin form was fixed first and the v2
// API still had the hole; this test is what makes the next one visible.
func TestEveryPageWriterConsultsARouteGuard(t *testing.T) {
	root := repositoryRoot(t)
	fileSet := token.NewFileSet()
	writers := 0

	for _, tree := range []string{"internal", "modules"} {
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
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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
			if !writesPageParams(file) {
				return nil
			}
			writers++
			relative := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
			if reason, exempt := pageWritersWithoutRouteGuard[relative]; exempt {
				if reason == "" {
					t.Errorf("%s: exemptions must record why the path needs no route guard", relative)
				}
				return nil
			}
			for _, guard := range pageRouteGuards {
				if strings.Contains(string(source), guard) {
					return nil
				}
			}
			t.Errorf("%s writes a page slug without reaching a language-prefix guard (%s); "+
				"a slug matching an active language code is served by the language homepage, "+
				"so the page it stores is unreachable",
				relative, strings.Join(pageRouteGuards, ", "))
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", tree, walkErr)
		}
	}
	if writers == 0 {
		t.Fatal("found no page-writing files; this test is no longer checking anything")
	}
}

// writesPageParams reports whether a file builds page insert or update
// parameters, which is what every page-writing path has in common.
func writesPageParams(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		composite, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		var name string
		switch typeExpr := composite.Type.(type) {
		case *ast.SelectorExpr:
			name = typeExpr.Sel.Name
		case *ast.Ident:
			name = typeExpr.Name
		}
		if name == "CreatePageParams" || name == "UpdatePageParams" {
			found = true
			return false
		}
		return true
	})
	return found
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("no go.mod found above the test's working directory")
		}
		directory = parent
	}
}
