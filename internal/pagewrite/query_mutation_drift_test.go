// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
	"modernc.org/sqlite"
)

var pageMutationBoundaries = map[string]struct{ path, function string }{
	// Finish advances the revision after detecting a tracked change.
	"AdvancePageRevision": {"internal/pagewrite/transaction.go", "Finish"},
	// The deletion transaction rejects all references to this language,
	// including the translations that would otherwise cascade.
	"DeleteLanguage": {"internal/handler/languages.go", "deleteLanguageIfUnused"},
}

var (
	sqlcQueryHeader = regexp.MustCompile(`(?m)^[ \t]*--[ \t]+name:[ \t]+(\w+)[ \t]+:\w+[ \t]*$`)
	sqlcNamedArg    = regexp.MustCompile(`(?i)\bsqlc\.(?:n?arg)\(\s*(?:\w+|'\w+'|"\w+")\s*\)`)
)

// checkPageMutationQueryCatalog requires a caller rule for every writer in
// either catalog. A transaction alone cannot prove which rows were tracked.
func checkPageMutationQueryCatalog(t *testing.T) {
	root := filepath.Join("..", "store")
	mutates := pageMutationDetector(t)
	found := make(map[string]bool)
	check := func(path, name, query string) {
		t.Helper()
		if !mutates(path, name, query) {
			return
		}
		found[name] = true
		if _, guarded := pageMutationBoundaries[name]; !guarded && !pageMutations[name] {
			t.Errorf("%s: %s writes page state without a tracking rule", path, name)
		}
	}

	for _, path := range sqlcCatalogFiles(t, filepath.Join(root, "queries", "*.sql")) {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(source)
		headers := sqlcQueryHeader.FindAllStringSubmatchIndex(sql, -1)
		if len(headers) == 0 {
			t.Fatalf("%s: found no sqlc queries", path)
		}
		for i, header := range headers {
			end := len(sql)
			if i+1 < len(headers) {
				end = headers[i+1][0]
			}
			check(path, sql[header[2]:header[3]], sqlcNamedArg.ReplaceAllString(sql[header[1]:end], "?"))
		}
	}

	for _, path := range sqlcCatalogFiles(t, filepath.Join(root, "*.sql.go")) {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			query, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			if header := sqlcQueryHeader.FindStringSubmatch(query); header != nil {
				count++
				check(path, header[1], query)
			}
			return true
		})
		if count == 0 {
			t.Fatalf("%s: found no generated sqlc queries", path)
		}
	}

	for name := range pageMutations {
		if !found[name] {
			t.Errorf("%s has a tracking rule but no page mutation in the sqlc catalog", name)
		}
	}
	for name := range pageMutationBoundaries {
		if !found[name] {
			t.Errorf("%s has a dedicated guard but no page mutation in the sqlc catalog", name)
		}
	}
}

func sqlcCatalogFiles(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no sqlc catalog files match %s", pattern)
	}
	return paths
}

// SQLite reports operations during PrepareContext, including foreign-key
// cascades. The scanner never executes the prepared queries.
func pageMutationDetector(t *testing.T) func(path, name, query string) bool {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	mutates := false
	if err = conn.Raw(func(driverConn any) error {
		registerer, ok := driverConn.(sqlite.AuthorizerRegisterer)
		if !ok {
			return fmt.Errorf("SQLite connection %T lacks an authorizer", driverConn)
		}
		return registerer.RegisterAuthorizer(func(action sqlite.AuthorizerActionCode, table, _, _, _ string) sqlite.AuthorizerReturnCode {
			switch action {
			case sqlite.AuthInsert, sqlite.AuthUpdate, sqlite.AuthDelete:
				switch strings.ToLower(table) {
				case "pages", "page_tags", "page_categories", "page_aliases", "translations", "page_ai_access":
					mutates = true
				}
			}
			return sqlite.AuthorizerOK
		})
	}); err != nil {
		t.Fatal(err)
	}
	return func(path, name, query string) bool {
		t.Helper()
		mutates = false
		stmt, prepareErr := conn.PrepareContext(t.Context(), query)
		if prepareErr != nil {
			t.Fatalf("%s: prepare %s for page mutation discovery: %v", path, name, prepareErr)
		}
		if closeErr := stmt.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		return mutates
	}
}
