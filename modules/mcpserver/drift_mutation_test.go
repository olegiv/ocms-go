// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
	"golang.org/x/tools/go/packages"
)

// packageFiles parses the non-test Go files of this package.
func packageFiles(t *testing.T) map[string]*ast.File {
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
	return files
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

// draftWriteMethods lists the authoring.Service operations that save draft
// content or issue a preview link. Both service factories return that service.
var draftWriteMethods = []string{"Create", "Update", "Translate", "CreatePreviewLink"}

func loadDraftWriterPackage(t *testing.T, path string) *packages.Package {
	t.Helper()
	pkgs, err := packages.Load(&packages.Config{Mode: packages.LoadSyntax, Context: t.Context()}, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("loaded %d packages for %s, want 1", len(pkgs), path)
	}
	if len(pkgs[0].Errors) != 0 {
		t.Fatalf("cannot check draft writer references in %s: %v", path, pkgs[0].Errors)
	}
	return pkgs[0]
}

func draftMethodReference(info *types.Info, node ast.Node) string {
	if selected, ok := node.(*ast.SelectorExpr); ok {
		if selection := info.Selections[selected]; selection != nil && selection.Kind() != types.FieldVal {
			return selected.Sel.Name
		}
	}
	return ""
}

func callsDraftWrite(body *ast.BlockStmt, info *types.Info) bool {
	for node := range ast.Preorder(body) {
		if slices.Contains(draftWriteMethods, draftMethodReference(info, node)) {
			return true
		}
	}
	return false
}

func TestDraftWriteDetectionUsesTheOperation(t *testing.T) {
	for _, tc := range []struct {
		name, method, operation string
		writes                  bool
	}{
		{"create", "Create", "return svc.Create()", true},
		{"update", "Update", "return svc.Update()", true},
		{"translate", "Translate", "return svc.Translate()", true},
		{"preview link", "CreatePreviewLink", "return svc.CreatePreviewLink()", true},
		{"get", "Get", "return svc.Get()", false},
		{"list", "List", "return svc.List()", false},
		{"translation status", "TranslationStatus", "return svc.TranslationStatus()", false},
		{"write method value", "Create", "save := svc.Create; return save()", true},
		{"read method value", "Get", "load := svc.Get; return load()", false},
		{"schema field", "Create", "return schema.Create", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "package fixture\ntype writer struct{}\n" +
				"func (writer) " + tc.method + "() string { return \"\" }\n" +
				"func handler(svc writer, schema struct{ Create string }) string {\n" +
				tc.operation + "\n}"
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "handler.go", source, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Selections: make(map[*ast.SelectorExpr]*types.Selection)}
			var config types.Config
			if _, err = config.Check("fixture", fset, []*ast.File{file}, info); err != nil {
				t.Fatal(err)
			}
			body := file.Decls[len(file.Decls)-1].(*ast.FuncDecl).Body
			if got := callsDraftWrite(body, info); got != tc.writes {
				t.Errorf("%s writes = %v, want %v", tc.operation, got, tc.writes)
			}
		})
	}
}

// TestMutatingToolsAreExactlyTheClassifiedOnes keeps the catalog's mutation
// classes honest. The annotations a client sees (read-only, destructive,
// idempotent) are derived from toolSpec.mutation, so a write tool declared
// without a class would be advertised as a read-only, idempotent query. The
// class is therefore checked against what the handler does, found in its
// source. A handler that references one of the draft write methods must
// have a class and require mcp:drafts. A read tool must have no class.
//
// Bug state: register a handler that gets its service from authoringService,
// calls Create, and omits its mutation class. This test names that handler.
func TestMutatingToolsAreExactlyTheClassifiedOnes(t *testing.T) {
	pkg := loadDraftWriterPackage(t, ".")
	var handlers map[string]string
	for _, file := range pkg.Syntax {
		if filepath.Base(pkg.Fset.Position(file.Pos()).Filename) == "catalog.go" {
			handlers = catalogHandlers(t, file)
			break
		}
	}
	if len(handlers) != len(toolCatalog()) {
		t.Fatalf("read %d handlers from catalog.go, the catalog has %d tools; the scan is broken", len(handlers), len(toolCatalog()))
	}
	writes := map[string]bool{}
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv != nil && fn.Body != nil && callsDraftWrite(fn.Body, pkg.TypesInfo) {
				writes[fn.Name.Name] = true
			}
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
		t.Errorf("found %d writing tools, want at least 4 (three content writes and the preview link)", mutating)
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
