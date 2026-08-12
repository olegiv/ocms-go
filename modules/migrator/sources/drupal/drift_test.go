// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// These tests enforce invariants mechanically rather than by review. Fixing
// only the site that first broke an invariant leaves the same class of bug
// reachable through another code path.

// TestModelFieldsAssignedByImporterAreRead catches write-only model fields.
//
// Node.ImageAlt was read out of the source database, assigned onto the struct,
// and then never consumed by anything. The compiler cannot see this — a struct
// field assignment is always "used" — so every image imported with its alt text
// silently discarded, for as long as the field existed.
//
// reader.go is excluded from the write set on purpose: its rows.Scan(&n.Field)
// calls are column carriers that prove nothing about whether a value is ever
// consumed. Including them would let a scan-only field satisfy the invariant,
// which is precisely the bug.
//
// Bug state: restore `n.ImageAlt = ...` in importer.go without a reader, and
// this names the field and the file it was written in.
func TestModelFieldsAssignedByImporterAreRead(t *testing.T) {
	fset := token.NewFileSet()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	fieldNames := modelFieldNames(t, fset)
	if len(fieldNames) == 0 {
		t.Fatal("found no model struct fields; the AST walk is broken")
	}

	writes := make(map[string]string) // field -> "file:line" of first write
	reads := make(map[string]bool)

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		// reader.go writes are scans, not consumption. Its reads still count.
		countWrites := name != "reader.go" && name != "models.go"
		collectFieldUses(file, fset, name, fieldNames, countWrites, writes, reads)
	}

	var writeOnly []string
	for field, where := range writes {
		if !reads[field] {
			writeOnly = append(writeOnly, field+" (written at "+where+")")
		}
	}
	sort.Strings(writeOnly)

	if len(writeOnly) > 0 {
		t.Errorf("model fields are assigned but never read — the value is computed and "+
			"then silently dropped:\n  %s", strings.Join(writeOnly, "\n  "))
	}
}

// modelFieldNames returns the exported field names declared in models.go.
func modelFieldNames(t *testing.T, fset *token.FileSet) map[string]bool {
	t.Helper()

	file, err := parser.ParseFile(fset, "models.go", nil, 0)
	if err != nil {
		t.Fatalf("parse models.go: %v", err)
	}

	names := make(map[string]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok || st.Fields == nil {
			return true
		}
		for _, f := range st.Fields.List {
			for _, ident := range f.Names {
				if ident.IsExported() {
					names[ident.Name] = true
				}
			}
		}
		return true
	})
	return names
}

// collectFieldUses records selector expressions naming a model field, split
// into assignment targets (writes) and every other use (reads).
func collectFieldUses(file *ast.File, fset *token.FileSet, filename string,
	fieldNames map[string]bool, countWrites bool, writes map[string]string, reads map[string]bool) {

	// Selectors that are assignment targets or the operand of & are writes;
	// everything else that names a field is a read.
	writeSites := make(map[ast.Node]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				writeSites[lhs] = true
			}
		case *ast.UnaryExpr:
			if node.Op == token.AND {
				writeSites[node.X] = true
			}
		}
		return true
	})

	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || !fieldNames[sel.Sel.Name] {
			return true
		}
		if writeSites[ast.Node(sel)] {
			if countWrites {
				if _, seen := writes[sel.Sel.Name]; !seen {
					pos := fset.Position(sel.Pos())
					writes[sel.Sel.Name] = filepath.Base(filename) + ":" + itoa(pos.Line)
				}
			}
			return true
		}
		reads[sel.Sel.Name] = true
		return true
	})
}

// itoa avoids pulling strconv in for a single conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
