// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"modernc.org/sqlite"
)

var (
	sqlcQueryHeader = regexp.MustCompile(`(?m)^[ \t]*--[ \t]+name:[ \t]+(\w+)[ \t]+:\w+[ \t]*$`)
	sqlcNamedArg    = regexp.MustCompile(`(?i)\bsqlc\.(?:n?arg)\(\s*(?:\w+|'\w+'|"\w+")\s*\)`)
)

// SQLCMutation locates a named SQL statement that writes to a tracked table.
type SQLCMutation struct {
	Path, Name, SQL string
}

// SQLCMutations inventories writes in both the SQL source and generated Go.
// It prepares queries against a migrated SQLite database without running them.
func SQLCMutations(t *testing.T, storeDir string, tables ...string) []SQLCMutation {
	t.Helper()
	mutates := sqlcMutationDetector(t, tables)
	var mutations []SQLCMutation
	check := func(path, name, query string) {
		t.Helper()
		if mutates(path, name, query) {
			mutations = append(mutations, SQLCMutation{Path: path, Name: name, SQL: query})
		}
	}

	for _, path := range sqlcCatalogFiles(t, filepath.Join(storeDir, "queries", "*.sql")) {
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

	for _, path := range sqlcCatalogFiles(t, filepath.Join(storeDir, "*.sql.go")) {
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

	return mutations
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

// sqlcMutationDetector observes operations during PrepareContext, including
// foreign-key cascades. The scanner never executes the prepared queries.
func sqlcMutationDetector(t *testing.T, tables []string) func(path, name, query string) bool {
	t.Helper()
	db, cleanup := TestDB(t)
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
				if slices.Contains(tables, strings.ToLower(table)) {
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
