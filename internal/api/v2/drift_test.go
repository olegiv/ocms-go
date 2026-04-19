// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2_test

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	apiv2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/media"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/api/v2/taxonomy"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestOpenAPISurface asserts every expected /api/v2 path is registered. If a
// domain operation is dropped or its path renamed, this test breaks — clients
// consuming the OpenAPI doc get the same visibility we do.
func TestOpenAPISurface(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	r := chi.NewRouter()
	queries := store.New(db)
	h := apiv2.Register(r, apiv2.Deps{DB: db, Queries: queries})

	pages.Register(h.API, pages.NewService(db, queries, nil, nil, pages.Policy{}))
	media.Register(h.API, media.NewService(db, queries, t.TempDir()))
	taxonomy.Register(h.API, taxonomy.NewService(db, queries))

	want := []string{
		"/auth",
		"/categories",
		"/categories/{id}",
		"/media",
		"/media/batch",
		"/media/{id}",
		"/pages",
		"/pages/slug/{slug}",
		"/pages/{id}",
		"/status",
		"/tags",
		"/tags/{id}",
	}

	got := make([]string, 0, len(h.OpenAPI().Paths))
	for p := range h.OpenAPI().Paths {
		got = append(got, p)
	}
	sort.Strings(got)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("OpenAPI paths drifted:\n  want: %v\n  got:  %v", want, got)
	}
}

// TestV2DoesNotImportV1 enforces the architectural rule that /api/v2 is a fresh
// build. Re-introducing the deleted internal/handler/api package or importing
// any of its types anywhere under internal/api/v2 would be a silent regression
// that this test catches early. Parses real import statements so comments and
// test strings that mention the v1 path are ignored.
func TestV2DoesNotImportV1(t *testing.T) {
	pkg, err := build.Default.Import("github.com/olegiv/ocms-go/internal/api/v2", "", build.FindOnly)
	if err != nil {
		t.Fatalf("locate v2 package: %v", err)
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(pkg.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if strings.Contains(imp.Path.Value, "internal/handler/api") {
				rel, _ := filepath.Rel(pkg.Dir, path)
				t.Errorf("%s imports forbidden v1 package %s", rel, imp.Path.Value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk v2 tree: %v", err)
	}
}

// walkV2Files applies fn to every parsed non-test .go file under internal/api/v2.
// The callback receives the shared fileset so positions can be resolved for
// error messages during the walk.
func walkV2Files(t *testing.T, fn func(fset *token.FileSet, path string, f *ast.File)) {
	t.Helper()
	pkg, err := build.Default.Import("github.com/olegiv/ocms-go/internal/api/v2", "", build.FindOnly)
	if err != nil {
		t.Fatalf("locate v2 package: %v", err)
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(pkg.Dir, func(path string, d fs.DirEntry, _ error) error {
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(pkg.Dir, path)
		fn(fset, rel, f)
		return nil
	})
	if err != nil {
		t.Fatalf("walk v2 tree: %v", err)
	}
}

// TestOpenAPISecurityMatchesRuntime asserts that every huma.Register call whose
// handler rejects unauthenticated callers at runtime also declares Security in
// its huma.Operation metadata. Prevents the class of bug Codex caught on
// /auth: runtime handler returns 401 when APIKey is nil but the OpenAPI spec
// advertises the endpoint as public.
func TestOpenAPISecurityMatchesRuntime(t *testing.T) {
	walkV2Files(t, func(_ *token.FileSet, path string, f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !isHumaRegisterCall(call) {
				return true
			}
			if len(call.Args) < 3 {
				return true
			}
			opLit, ok := extractOperationLiteral(call.Args[1])
			if !ok {
				return true
			}
			handler, ok := call.Args[2].(*ast.FuncLit)
			if !ok {
				return true
			}
			if !handlerGatesOnAuth(handler) {
				return true
			}
			if operationHasSecurity(opLit) {
				return true
			}
			id := operationIDFromLiteral(opLit)
			t.Errorf("%s: operation %q gates on auth at runtime but has no Security declaration", path, id)
			return true
		})
	})
}

// isHumaRegisterCall matches `huma.Register(...)` regardless of the receiver
// expression (h.API, api).
func isHumaRegisterCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Register" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == "huma"
}

// extractOperationLiteral digs through a composite literal whose type is
// `huma.Operation`, returning the inner literal and true on match.
func extractOperationLiteral(arg ast.Expr) (*ast.CompositeLit, bool) {
	lit, ok := arg.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Operation" {
		return nil, false
	}
	return lit, true
}

// handlerGatesOnAuth detects the two canonical auth-rejection patterns in a
// huma handler body:
//  1. `if actor.APIKey == nil { return ... }` (read ops)
//  2. `s.requireWritePerm(a)` or `svc.requireWritePerm(actor)` (write ops)
func handlerGatesOnAuth(h *ast.FuncLit) bool {
	gated := false
	ast.Inspect(h.Body, func(n ast.Node) bool {
		// Pattern 1: explicit nil-check on any identifier's APIKey field.
		if bin, ok := n.(*ast.BinaryExpr); ok && bin.Op == token.EQL {
			if sel, ok := bin.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "APIKey" {
				if id, ok := bin.Y.(*ast.Ident); ok && id.Name == "nil" {
					gated = true
					return false
				}
			}
		}
		// Pattern 2: call to a *.requireWritePerm function.
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "requireWritePerm" {
				gated = true
				return false
			}
		}
		return true
	})
	return gated
}

// operationHasSecurity reports whether the Operation literal declares Security.
func operationHasSecurity(op *ast.CompositeLit) bool {
	for _, el := range op.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Security" {
			return true
		}
	}
	return false
}

// operationIDFromLiteral extracts the OperationID field for error messages.
// Falls back to "<unknown>" when the field is missing or not a simple string.
func operationIDFromLiteral(op *ast.CompositeLit) string {
	for _, el := range op.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		id, ok := kv.Key.(*ast.Ident)
		if !ok || id.Name != "OperationID" {
			continue
		}
		if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			return strings.Trim(lit.Value, `"`)
		}
	}
	return "<unknown>"
}

// TestResolveLanguageCodeCallersPropagate asserts that every caller of
// `s.resolveLanguageCode(ctx, ...)` propagates its error verbatim. No caller
// may flatten validation errors to ErrInternal; that collapses the helper's
// 422-class return into a misleading 500. Prevents the class of bug Codex
// caught in pages.Create and four taxonomy writes.
func TestResolveLanguageCodeCallersPropagate(t *testing.T) {
	walkV2Files(t, func(fset *token.FileSet, path string, f *ast.File) {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			checkResolveLanguageCodeCalls(t, fset, path, fn)
		}
	})
}

// checkResolveLanguageCodeCalls walks a function body and flags if-blocks that
// handle a resolveLanguageCode error by wrapping it as ErrInternal.
func checkResolveLanguageCodeCalls(t *testing.T, fset *token.FileSet, path string, fn *ast.FuncDecl) {
	t.Helper()
	body := fn.Body
	for i, stmt := range body.List {
		if !statementCallsResolveLanguageCode(stmt) {
			continue
		}
		// The immediately following statement should be the error-handling if.
		if i+1 >= len(body.List) {
			continue
		}
		ifStmt, ok := body.List[i+1].(*ast.IfStmt)
		if !ok {
			continue
		}
		if returnsErrInternal(ifStmt.Body) {
			pos := fset.Position(stmt.Pos())
			t.Errorf("%s:%d (%s): resolveLanguageCode error is wrapped as ErrInternal; return it verbatim so validation kind survives to the caller",
				path, pos.Line, fn.Name.Name)
		}
	}
	// Also scan inside nested blocks (if/for/switch bodies).
	ast.Inspect(body, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok || block == body {
			return true
		}
		for i, stmt := range block.List {
			if !statementCallsResolveLanguageCode(stmt) {
				continue
			}
			if i+1 >= len(block.List) {
				continue
			}
			ifStmt, ok := block.List[i+1].(*ast.IfStmt)
			if !ok {
				continue
			}
			if returnsErrInternal(ifStmt.Body) {
				pos := fset.Position(stmt.Pos())
				t.Errorf("%s:%d (%s): resolveLanguageCode error is wrapped as ErrInternal; return it verbatim so validation kind survives to the caller",
					path, pos.Line, fn.Name.Name)
			}
		}
		return true
	})
}

// statementCallsResolveLanguageCode matches `_, err := s.resolveLanguageCode(...)`
// and its sibling forms (including `if in.X != nil { lang, err := s.resolveLanguageCode(...)
// ...` where the assign is at the top of the block).
func statementCallsResolveLanguageCode(stmt ast.Stmt) bool {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok {
		return false
	}
	for _, rhs := range assign.Rhs {
		call, ok := rhs.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		if sel.Sel.Name == "resolveLanguageCode" {
			return true
		}
	}
	return false
}

// returnsErrInternal reports whether the given block returns a
// `v2.NewError(v2.ErrInternal, ...)` expression (in any return position).
func returnsErrInternal(block *ast.BlockStmt) bool {
	found := false
	ast.Inspect(block, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, res := range ret.Results {
			call, ok := res.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			if sel.Sel.Name != "NewError" {
				continue
			}
			// Check first arg is *.ErrInternal
			if len(call.Args) == 0 {
				continue
			}
			if argSel, ok := call.Args[0].(*ast.SelectorExpr); ok && argSel.Sel.Name == "ErrInternal" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
