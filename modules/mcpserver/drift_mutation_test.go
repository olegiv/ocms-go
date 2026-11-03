// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

// packageFiles parses the non-test Go files of this package.
func packageFiles(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range entries {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = file
	}
	if len(files) == 0 {
		t.Fatal("no source files found; the scan is broken")
	}
	return fset, files
}

// catalogHandlers maps every tool name in toolCatalog's source to the *Module
// method its register field binds, read from the composite literals
// ({Name: "x", register: registerTool((*Module).method)}), so the mapping needs
// no naming convention.
func catalogHandlers(t *testing.T, file *ast.File) map[string]string {
	t.Helper()
	handlers := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		var name, method string
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "Name":
				if basic, ok := kv.Value.(*ast.BasicLit); ok && basic.Kind == token.STRING {
					name, _ = strconv.Unquote(basic.Value)
				}
			case "register":
				call, ok := kv.Value.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					continue
				}
				if sel, ok := call.Args[0].(*ast.SelectorExpr); ok {
					method = sel.Sel.Name
				}
			}
		}
		if name != "" && method != "" {
			handlers[name] = method
		}
		return true
	})
	return handlers
}

// writeAdapters are the calls through which a tool handler writes: the shared
// authoring writer for page content (draftActor) and the preview-link issuer.
var writeAdapters = []string{"draftActor", "CreatePreviewLink"}

// TestMutatingToolsAreExactlyTheClassifiedOnes keeps the catalog's mutation
// classes honest. The annotations a client sees (read-only, destructive,
// idempotent) are derived from toolSpec.mutation, so a write tool declared
// without a class would be advertised as a read-only, idempotent query. The
// class is therefore checked against what the handler does, found in its
// source: a handler that calls a write adapter must have a class, and a
// class must belong to a handler that does, and to the mcp:drafts scope that
// gates every write.
//
// Bug state: register a handler that calls draftActor without mutation: set
// in its catalog entry, or give a read tool a mutation class.
func TestMutatingToolsAreExactlyTheClassifiedOnes(t *testing.T) {
	_, files := packageFiles(t)
	handlers := catalogHandlers(t, files["catalog.go"])
	if len(handlers) != len(toolCatalog()) {
		t.Fatalf("read %d handlers from catalog.go, the catalog has %d tools; the scan is broken", len(handlers), len(toolCatalog()))
	}
	writes := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && slices.Contains(writeAdapters, sel.Sel.Name) {
						writes[fn.Name.Name] = true
					}
				}
				return true
			})
		}
	}

	mutating := 0
	for _, spec := range toolCatalog() {
		method, ok := handlers[spec.Name]
		if !ok {
			t.Errorf("%s: catalog.go binds no handler method", spec.Name)
			continue
		}
		mutates := writes[method]
		if mutates {
			mutating++
		}
		classified := spec.mutation != ""
		if mutates != classified {
			t.Errorf("%s: handler %s writes = %v but the catalog's mutation class is %q; a write tool needs a class (and a read tool none), or its annotations lie",
				spec.Name, method, mutates, spec.mutation)
		}
		if classified && !slices.Contains([]string{mutationCreate, mutationReplace, mutationShare}, spec.mutation) {
			t.Errorf("%s: unknown mutation class %q", spec.Name, spec.mutation)
		}
		if mutates && spec.scope != model.PermissionMCPDrafts {
			t.Errorf("%s: a writing tool must require %s, has scope %q", spec.Name, model.PermissionMCPDrafts, spec.scope)
		}
	}
	if mutating < 4 {
		t.Errorf("found %d writing tools, want at least 4 (three content writes and the preview link); the scan no longer sees the adapters", mutating)
	}
}

// rowsClosedPackages are the packages whose queries must defer rows.Close in
// the caller. Other packages (the media cleanup and phpnuke readers still close
// rows by hand) belong to separate fixes; add them here as they are converted.
var rowsClosedPackages = []string{"modules/mcpserver", "internal/oauth", "internal/middleware", "internal/handler"}

// TestRowsAreClosedByDeferInTheCaller fails when code that runs a query, such
// as rows, err := db.QueryContext(...), does not defer rows.Close in the same
// function. A helper that closes rows, or a loop that closes them by hand,
// leaves them open when a Scan or a callback panics, or when a later edit adds
// an early return.
//
// Bug state: delete the defer in migrateSettingsColumns and this names it.
func TestRowsAreClosedByDeferInTheCaller(t *testing.T) {
	fset := token.NewFileSet()
	queries := 0
	for _, pkg := range rowsClosedPackages {
		dir := filepath.Join("..", "..", filepath.FromSlash(pkg))
		entries, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			t.Fatalf("%s has no Go files; the package list is stale", pkg)
		}
		for _, path := range entries {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				for _, result := range queryResults(fn.Body) {
					queries++
					if !closesByDefer(fn.Body, result.name) {
						t.Errorf("%s: %s does not defer %s.Close() in the function that ran the query", fset.Position(result.pos), fn.Name.Name, result.name)
					}
				}
			}
		}
	}
	if queries == 0 {
		t.Fatal("found no query in the listed packages; the scan is broken")
	}
}

type queryResult struct {
	name string
	pos  token.Pos
}

// queryResults returns the variables that hold the rows of a Query or
// QueryContext call in body.
func queryResults(body *ast.BlockStmt) []queryResult {
	var results []queryResult
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) < 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Query" && sel.Sel.Name != "QueryContext") {
			return true
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name != "_" && len(assign.Lhs) == 2 {
			results = append(results, queryResult{ident.Name, assign.Pos()})
		}
		return true
	})
	return results
}

// closesByDefer reports whether body defers name.Close(), directly or inside a
// deferred function literal.
func closesByDefer(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		deferred, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		ast.Inspect(deferred.Call, func(m ast.Node) bool {
			if call, ok := m.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Close" {
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == name {
						found = true
					}
				}
			}
			return true
		})
		return false
	})
	return found
}
