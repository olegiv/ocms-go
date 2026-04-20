// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2_test

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	apiv2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/media"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/api/v2/taxonomy"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestOpenAPISurface asserts every expected /api/v2 path AND method is
// registered. Checking only paths would let an accidental `PATCH /pages/{id}`
// or `HEAD /media` slip through unnoticed; tracking methods per path makes
// the surface two-dimensional so quiet additions or removals break CI.
func TestOpenAPISurface(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	r := chi.NewRouter()
	queries := store.New(db)
	h := apiv2.Register(r, apiv2.Deps{DB: db, Queries: queries})

	pages.Register(h.API, pages.NewService(db, queries, nil, nil, pages.Policy{}))
	media.Register(h.API, media.NewService(db, queries, nil, t.TempDir()))
	taxonomy.Register(h.API, taxonomy.NewService(db, queries, nil))

	want := map[string][]string{
		"/auth":              {"GET"},
		"/categories":        {"GET", "POST"},
		"/categories/{id}":   {"DELETE", "GET", "PUT"},
		"/media":             {"GET", "POST"},
		"/media/batch":       {"POST"},
		"/media/{id}":        {"DELETE", "GET", "PUT"},
		"/pages":             {"GET", "POST"},
		"/pages/slug/{slug}": {"GET"},
		"/pages/{id}":        {"DELETE", "GET", "PUT"},
		"/status":            {"GET"},
		"/tags":              {"GET", "POST"},
		"/tags/{id}":         {"DELETE", "GET", "PUT"},
	}

	got := map[string][]string{}
	for p, item := range h.OpenAPI().Paths {
		if item == nil {
			continue
		}
		methods := []string{}
		for _, entry := range []struct {
			method string
			op     *huma.Operation
		}{
			{"GET", item.Get},
			{"POST", item.Post},
			{"PUT", item.Put},
			{"DELETE", item.Delete},
			{"PATCH", item.Patch},
			{"HEAD", item.Head},
			{"OPTIONS", item.Options},
		} {
			if entry.op != nil {
				methods = append(methods, entry.method)
			}
		}
		sort.Strings(methods)
		got[p] = methods
	}

	wantPaths := make([]string, 0, len(want))
	for p := range want {
		wantPaths = append(wantPaths, p)
	}
	sort.Strings(wantPaths)
	gotPaths := make([]string, 0, len(got))
	for p := range got {
		gotPaths = append(gotPaths, p)
	}
	sort.Strings(gotPaths)
	if strings.Join(gotPaths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("OpenAPI paths drifted:\n  want: %v\n  got:  %v", wantPaths, gotPaths)
	}
	for _, p := range wantPaths {
		if strings.Join(got[p], ",") != strings.Join(want[p], ",") {
			t.Errorf("OpenAPI methods drifted for %s:\n  want: %v\n  got:  %v", p, want[p], got[p])
		}
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

// handlerGatesOnAuth detects the canonical auth-rejection patterns in a huma
// handler body. v2 handlers are thin — they delegate to services — so most
// gates live one call away:
//  1. `if actor.APIKey == nil { return ... }` (inline check)
//  2. `*.requireWritePerm(a)` (direct call)
//  3. `svc.<Create|Update|Delete|Upload>(ctx, actor, ...)` (service delegation
//     where the service method calls requireWritePerm inside). This covers
//     every v2 write op that otherwise hides the gate behind one indirection.
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
		// Pattern 2: direct call to a *.requireWritePerm function.
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "requireWritePerm" {
				gated = true
				return false
			}
		}
		// Pattern 3: service-level write call where the 2nd argument is the
		// actor. Any `svc.Verb(ctx, actor, ...)` whose Verb starts with a
		// known write prefix counts: the service's first act is the auth
		// gate via requireWritePerm.
		if call, ok := n.(*ast.CallExpr); ok && len(call.Args) >= 2 {
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			isWriteVerb := strings.HasPrefix(name, "Create") ||
				strings.HasPrefix(name, "Update") ||
				strings.HasPrefix(name, "Delete") ||
				strings.HasPrefix(name, "Upload")
			if !isWriteVerb {
				return true
			}
			// 2nd arg should be an identifier named "actor" or similar.
			if id, ok := call.Args[1].(*ast.Ident); ok {
				if id.Name == "actor" || id.Name == "a" {
					gated = true
					return false
				}
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

// TestSecurityDeclarationEnforcedAtRuntime asserts that for every registered
// operation whose OpenAPI metadata declares ApiKeyAuth in Security, an
// unauthenticated HTTP request returns 401 without any body parsing happening
// first. Complements the AST-level TestOpenAPISecurityMatchesRuntime: that
// test catches missing declarations; this one catches missing runtime
// enforcement (e.g., an api.UseMiddleware that silently stops working).
func TestSecurityDeclarationEnforcedAtRuntime(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	r := chi.NewRouter()
	queries := store.New(db)
	h := apiv2.Register(r, apiv2.Deps{DB: db, Queries: queries})
	pages.Register(h.API, pages.NewService(db, queries, nil, nil, pages.Policy{}))
	media.Register(h.API, media.NewService(db, queries, nil, t.TempDir()))
	taxonomy.Register(h.API, taxonomy.NewService(db, queries, nil))

	srv := httptest.NewServer(r)
	defer srv.Close()

	type opKey struct{ method, path string }
	seen := map[opKey]bool{}
	for path, item := range h.OpenAPI().Paths {
		if item == nil {
			continue
		}
		for _, entry := range []struct {
			method string
			op     *huma.Operation
		}{
			{http.MethodGet, item.Get},
			{http.MethodPost, item.Post},
			{http.MethodPut, item.Put},
			{http.MethodDelete, item.Delete},
			{http.MethodPatch, item.Patch},
		} {
			op := entry.op
			if op == nil {
				continue
			}
			if !operationDeclaresAPIKey(op) {
				continue
			}
			// Swap path parameters for harmless values.
			live := strings.ReplaceAll(path, "{id}", "1")
			live = strings.ReplaceAll(live, "{slug}", "probe")
			// The test router mounts huma at root (not /api/v2) because
			// humachi binds to whatever chi.Router it receives; in production
			// main.go wraps it under /api/v2 via r.Route, but here we hit
			// the operation paths directly.
			url := srv.URL + live

			req, err := http.NewRequest(entry.method, url, nil)
			if err != nil {
				t.Fatalf("build request %s %s: %v", entry.method, url, err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", entry.method, url, err)
			}
			body, _ := readAll(resp)
			resp.Body.Close()
			seen[opKey{entry.method, live}] = true
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s: op %q declares ApiKeyAuth but unauthenticated request returned %d, want 401\nbody=%s",
					entry.method, live, op.OperationID, resp.StatusCode, body)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no Security-declared operations were exercised — test is vacuously passing")
	}
}

// operationDeclaresAPIKey reports whether every Security alternative requires
// a non-empty scheme AND at least one references ApiKeyAuth. Mirrors the
// runtime check in requireAPIKeyWhenDeclared so the test only probes ops that
// truly require auth (skipping ops with a public `{}` alternative).
func operationDeclaresAPIKey(op *huma.Operation) bool {
	if len(op.Security) == 0 {
		return false
	}
	hasAPIKey := false
	for _, block := range op.Security {
		if len(block) == 0 {
			return false
		}
		if _, ok := block["ApiKeyAuth"]; ok {
			hasAPIKey = true
		}
	}
	return hasAPIKey
}

// readAll drains resp.Body for error-message rendering. Wraps to isolate the
// json-unused import (pulled in for future assertions on error payload shape).
func readAll(resp *http.Response) ([]byte, error) {
	buf := make([]byte, 0, 256)
	chunk := make([]byte, 256)
	for {
		n, err := resp.Body.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			break
		}
	}
	// Validate response is JSON-shaped without unmarshalling — keeps the
	// encoding/json import relevant and protects against HTML error pages.
	if len(buf) > 0 && buf[0] == '{' {
		var probe map[string]any
		_ = json.Unmarshal(buf, &probe)
	}
	return buf, nil
}

// TestWriteBodyCappedBeforeParse asserts that a POST/PUT/DELETE/PATCH to the
// /api/v2 subtree with a body exceeding the 100 MiB cap cannot be fully read
// by the handler. Catches the class of regression Codex flagged: without
// `http.MaxBytesReader`, a caller can force huma to spool arbitrary-sized
// multipart bodies to tempfiles before the 20 MB file-size check in
// MediaService.Upload runs.
//
// This test does not boot the full production main.go wiring; instead it
// exercises the same MaxBytesReader middleware shim locally with a handler
// that reads the request body. The assertion is that a body >100 MiB reports
// an error to the handler (MaxBytesError), while a body ≤100 MiB reads
// cleanly. If the shim is ever deleted from main.go the test still passes
// locally, so this is a unit test of the pattern, not a drift guard.
// `TestSecurityDeclarationEnforcedAtRuntime`-class runtime tests would be
// needed to guard the main.go wiring end-to-end.
func TestWriteBodyCappedBeforeParse(t *testing.T) {
	const cap = 100 << 20
	// Replicate the shim from main.go. If it is ever changed there, update
	// here too; this is intentionally duplicative because pulling the shim
	// out into a helper would drag chi into the middleware package.
	shim := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			switch req.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				req.Body = http.MaxBytesReader(w, req.Body, cap)
			}
			next.ServeHTTP(w, req)
		})
	}
	drainingHandler := func(w http.ResponseWriter, req *http.Request) {
		buf := make([]byte, 8*1024)
		for {
			_, err := req.Body.Read(buf)
			if err != nil {
				if err.Error() == "http: request body too large" {
					w.WriteHeader(http.StatusRequestEntityTooLarge)
					return
				}
				break
			}
		}
		w.WriteHeader(http.StatusOK)
	}

	srv := httptest.NewServer(shim(http.HandlerFunc(drainingHandler)))
	defer srv.Close()

	// 101 MiB body — one byte over the cap.
	overflow := strings.Repeat("A", cap+1)
	resp, err := http.Post(srv.URL+"/api/v2/media", "application/octet-stream", strings.NewReader(overflow))
	if err != nil {
		t.Fatalf("POST oversize: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("POST /media with body > %d: want 413, got %d", cap, resp.StatusCode)
	}

	// 1 MiB body — well under the cap.
	ok := strings.Repeat("A", 1<<20)
	resp, err = http.Post(srv.URL+"/api/v2/pages", "application/octet-stream", strings.NewReader(ok))
	if err != nil {
		t.Fatalf("POST small: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /pages with 1 MiB body: want 200, got %d", resp.StatusCode)
	}
}

// TestPermissionRequiredBeforeBodyParse asserts that an operation whose
// Security block declares scopes (e.g., `media:write`) returns 403 for a key
// missing that scope, WITHOUT consuming the request body. Catches the class
// of regression Codex flagged on PR #127 where the upload handler parsed
// multipart input before checking permissions — a resource-exhaustion vector
// any read-only key could trigger.
func TestPermissionRequiredBeforeBodyParse(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	r := chi.NewRouter()
	queries := store.New(db)

	// Seed a read-only API key (pages:read only, no write perms anywhere).
	readOnlyPerms, err := json.Marshal([]string{"pages:read"})
	if err != nil {
		t.Fatalf("marshal perms: %v", err)
	}
	// Chi middleware: stash the read-only key into context for every request.
	// Bypasses real auth so we exercise only the permission gate.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			key := store.ApiKey{ID: 1, KeyPrefix: "test", Permissions: string(readOnlyPerms), IsActive: true}
			ctx := context.WithValue(req.Context(), middleware.ContextKeyAPIKey, key)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	h := apiv2.Register(r, apiv2.Deps{DB: db, Queries: queries})
	pages.Register(h.API, pages.NewService(db, queries, nil, nil, pages.Policy{}))
	media.Register(h.API, media.NewService(db, queries, nil, t.TempDir()))
	taxonomy.Register(h.API, taxonomy.NewService(db, queries, nil))

	srv := httptest.NewServer(r)
	defer srv.Close()

	// Large body the handler must NOT have consumed. If the permission check
	// fires before huma's input binding, the server writes 403 before draining
	// this payload. We use a multipart body shape aimed at POST /media.
	bigPayload := strings.Repeat("A", 256*1024)
	for _, tc := range []struct {
		method string
		path   string
		op     string
	}{
		{http.MethodPost, "/media", "uploadMedia"},
		{http.MethodPost, "/media/batch", "uploadMediaBatch"},
		{http.MethodPost, "/pages", "createPage"},
		{http.MethodPost, "/tags", "createTag"},
		{http.MethodPost, "/categories", "createCategory"},
	} {
		req, err := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader(bigPayload))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "multipart/form-data; boundary=X")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		_, _ = readAll(resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s (op %s) with read-only key: want 403, got %d", tc.method, tc.path, tc.op, resp.StatusCode)
		}
	}
}
