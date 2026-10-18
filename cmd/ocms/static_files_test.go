// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/theme"
	"github.com/olegiv/ocms-go/internal/themes"
)

// TestThemeStaticRefusesDirectories checks that a theme's static route serves
// files and answers a directory with 404 instead of a listing (security audit
// 2026-10-17, I-3), and that only a missing file is a 404: a file the server
// cannot read is a logged, uncached 500.
func TestThemeStaticRefusesDirectories(t *testing.T) {
	customDir := t.TempDir()
	static := filepath.Join(customDir, "themes", "probe", "static", "css")
	if err := os.MkdirAll(static, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(customDir, "themes", "probe", "theme.json"): `{"name": "probe", "version": "1.0.0"}`,
		filepath.Join(static, "site.css"):                         "body{}",
	}
	for name, content := range files {
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager := theme.NewManager(themes.FS, customDir, testutil.TestLoggerSilent())
	if err := manager.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}
	router := chi.NewRouter()
	router.Get("/themes/{themeName}/static/*", func(w http.ResponseWriter, r *http.Request) {
		serveThemeStaticFile(w, r, manager)
	})

	cases := map[string]int{
		"/themes/probe/static/css/site.css":                             http.StatusOK,
		"/themes/probe/static/css":                                      http.StatusNotFound,
		"/themes/probe/static/css/":                                     http.StatusNotFound,
		"/themes/probe/static/css/missing.css":                          http.StatusNotFound,
		"/themes/probe/static/css/site.css/more":                        http.StatusNotFound,
		"/themes/probe/static/css/%00.css":                              http.StatusNotFound,
		"/themes/probe/static/css/%ff.css":                              http.StatusNotFound,
		"/themes/probe/static/css/" + strings.Repeat("a", 300) + ".css": http.StatusNotFound,
	}
	// Root reads the directory regardless of its mode.
	if os.Geteuid() != 0 {
		locked := filepath.Join(static, "locked")
		if err := os.Mkdir(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(locked, "a.css"), []byte("a{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
		cases["/themes/probe/static/css/locked/a.css"] = http.StatusInternalServerError
		// A file the server may not read, in a readable directory.
		unreadable := filepath.Join(static, "secret.css")
		if err := os.WriteFile(unreadable, []byte("b{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(unreadable, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })
		cases["/themes/probe/static/css/secret.css"] = http.StatusInternalServerError
	}
	for path, want := range cases {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != want {
			t.Errorf("GET %s = %d, want %d; body %q", path, rr.Code, want, rr.Body.String())
		}
		switch cache := rr.Header().Get("Cache-Control"); {
		case want == http.StatusInternalServerError && cache != "no-store":
			t.Errorf("GET %s: Cache-Control = %q, want no-store", path, cache)
		case want == http.StatusNotFound && cache == themeStaticCacheControl:
			t.Errorf("GET %s: a 404 carries the month-long Cache-Control", path)
		case want == http.StatusOK && cache != themeStaticCacheControl:
			t.Errorf("GET %s: Cache-Control = %q, want %q", path, cache, themeStaticCacheControl)
		}
	}
}

// TestThemeStaticFileServable checks the lookup results the embedded and
// external theme paths share: a file is served, a missing file or a directory
// is a 404, and any other error is an uncached 500.
func TestThemeStaticFileServable(t *testing.T) {
	embedded := fstest.MapFS{"static/css/theme.css": {Data: []byte("body{}")}}
	lookup := func(name string) (fs.FileInfo, error) { return fs.Stat(embedded, name) }
	for name, tc := range map[string]struct {
		lookup    func() (fs.FileInfo, error)
		servable  bool
		wantCode  int
		wantCache string
	}{
		"file":      {func() (fs.FileInfo, error) { return lookup("static/css/theme.css") }, true, http.StatusOK, ""},
		"directory": {func() (fs.FileInfo, error) { return lookup("static/css") }, false, http.StatusNotFound, ""},
		"missing":   {func() (fs.FileInfo, error) { return lookup("static/css/none.css") }, false, http.StatusNotFound, ""},
		"not a directory": {func() (fs.FileInfo, error) {
			return nil, &fs.PathError{Op: "stat", Path: "theme.css/x", Err: syscall.ENOTDIR}
		}, false, http.StatusNotFound, ""},
		"invalid path": {func() (fs.FileInfo, error) { return lookup("static/\xff.css") }, false, http.StatusNotFound, ""},
		"name too long": {func() (fs.FileInfo, error) {
			return nil, &fs.PathError{Op: "stat", Path: "a.css", Err: syscall.ENAMETOOLONG}
		}, false, http.StatusNotFound, ""},
		"permission": {func() (fs.FileInfo, error) {
			return nil, &fs.PathError{Op: "stat", Path: "locked/a.css", Err: fs.ErrPermission}
		}, false, http.StatusInternalServerError, "no-store"},
	} {
		info, err := tc.lookup()
		rr := httptest.NewRecorder()
		got := themeStaticFileServable(rr, httptest.NewRequest(http.MethodGet, "/", nil), info, err, "probe", name)
		if got != tc.servable || rr.Code != tc.wantCode || rr.Header().Get("Cache-Control") != tc.wantCache {
			t.Errorf("%s: servable=%v code=%d Cache-Control=%q, want %v %d %q",
				name, got, rr.Code, rr.Header().Get("Cache-Control"), tc.servable, tc.wantCode, tc.wantCache)
		}
	}
}

// TestServeEmbeddedThemeFile checks the embedded-theme branch on the core
// default theme, as the theme manager hands it over (fs.Sub of the embedded
// file system): a file is served and cached, while a directory or a missing
// file is an uncached 404. Reading a directory from an embed.FS fails with
// "is a directory", which would otherwise be a 500 that anyone can trigger.
func TestServeEmbeddedThemeFile(t *testing.T) {
	fsys, err := fs.Sub(themes.FS, "default")
	if err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]int{
		"css/theme.css":   http.StatusOK,
		"css":             http.StatusNotFound,
		"css/missing.css": http.StatusNotFound,
	} {
		rr := httptest.NewRecorder()
		serveEmbeddedThemeFile(rr, httptest.NewRequest(http.MethodGet, "/", nil), fsys, "default", file)
		cached := rr.Header().Get("Cache-Control") == themeStaticCacheControl
		if rr.Code != want || cached != (want == http.StatusOK) {
			t.Errorf("%s: code %d, cached %v; want %d, cached %v", file, rr.Code, cached, want, want == http.StatusOK)
		}
		if want == http.StatusOK && (rr.Body.Len() == 0 || rr.Header().Get("Content-Type") == "") {
			t.Errorf("%s: %d-byte body, Content-Type %q", file, rr.Body.Len(), rr.Header().Get("Content-Type"))
		}
	}
}

// TestFileServersRefuseDirectories fails when production code builds an
// http.FileServer over a file system that is not wrapped in
// middleware.NoDirectoryFS, or calls http.ServeFile or http.ServeContent
// without first guarding it with a directory check: an earlier if statement
// in the same block whose body returns and whose condition calls IsDir, or a
// function of its package that does, such as
// `if !themeStaticFileServable(...) { return }`. FileServer and ServeFile
// render a listing of every file below a directory URL, and an unguarded
// ServeContent answers a directory with an error instead of a 404; /uploads
// and theme static directories were kept from listing only by an accidental
// redirect loop (security audit 2026-10-17, I-3). A check made on another
// branch does not count.
//
// Bug state: unwrap the uploads file system in cmd/ocms/main.go and this names
// the line; drop the `if !themeStaticFileServable(w, r, info, ...)` guard
// before ServeContent and this names the ServeContent call.
func TestFileServersRefuseDirectories(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	servers := 0
	var offenders []string
	// serveSite is an http.ServeFile or ServeContent call and the directory
	// checks of the if statements before it in its block.
	type serveSite struct {
		dir          string
		pos          token.Pos
		guardCallees []string
		guardIsDir   bool
	}
	var sites []serveSite
	checksDirectory := map[string]bool{} // "dir.func" for functions that call IsDir
	for _, tree := range []string{"cmd", "internal", "modules"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			dir := filepath.Dir(path)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				guarded := map[token.Pos]serveSite{}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					switch node := n.(type) {
					case *ast.SelectorExpr:
						if node.Sel.Name == "IsDir" {
							checksDirectory[dir+"."+fn.Name.Name] = true
						}
					case *ast.BlockStmt:
						for i, stmt := range node.List {
							expr, ok := stmt.(*ast.ExprStmt)
							if !ok {
								continue
							}
							call, ok := expr.X.(*ast.CallExpr)
							if !ok || !isServeCall(call) {
								continue
							}
							site := serveSite{dir: dir, pos: call.Pos()}
							for _, earlier := range node.List[:i] {
								if ifStmt, ok := earlier.(*ast.IfStmt); ok && endsInReturn(ifStmt.Body) {
									callees, isDir := directoryGuard(ifStmt.Cond)
									site.guardCallees = append(site.guardCallees, callees...)
									site.guardIsDir = site.guardIsDir || isDir
								}
							}
							guarded[call.Pos()] = site
						}
					case *ast.CallExpr:
						switch httpCall(node) {
						case "FileServer", "FileServerFS":
							servers++
							if len(node.Args) != 1 || calleeName(node.Args[0]) != "NoDirectoryFS" {
								offenders = append(offenders, fset.Position(node.Pos()).String()+
									"  http.FileServer without middleware.NoDirectoryFS")
							}
						case "ServeFile", "ServeFileFS", "ServeContent":
							servers++
							if _, ok := guarded[node.Pos()]; !ok {
								// Not a statement of its own (deferred, nested in an
								// expression): there is no block to guard it.
								guarded[node.Pos()] = serveSite{dir: dir, pos: node.Pos()}
							}
						}
					}
					return true
				})
				for _, site := range guarded {
					sites = append(sites, site)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}
	for _, site := range sites {
		ok := site.guardIsDir
		for _, callee := range site.guardCallees {
			ok = ok || checksDirectory[site.dir+"."+callee]
		}
		if !ok {
			offenders = append(offenders, fset.Position(site.pos).String()+
				"  http.ServeFile/ServeContent without a directory check guarding it")
		}
	}
	if servers == 0 {
		t.Fatal("no file server found; the scan is broken and this test is vacuous")
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("these file servers can list a directory:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// isServeCall reports whether call is http.ServeFile, ServeFileFS or ServeContent.
func isServeCall(call *ast.CallExpr) bool {
	switch httpCall(call) {
	case "ServeFile", "ServeFileFS", "ServeContent":
		return true
	}
	return false
}

// endsInReturn reports whether block's last statement is a return, so the
// statements after an if with that body run only when its condition is false.
func endsInReturn(block *ast.BlockStmt) bool {
	if len(block.List) == 0 {
		return false
	}
	_, ok := block.List[len(block.List)-1].(*ast.ReturnStmt)
	return ok
}

// directoryGuard returns the package-local functions an if condition calls
// and whether it calls IsDir itself.
func directoryGuard(cond ast.Expr) (callees []string, isDir bool) {
	ast.Inspect(cond, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			isDir = isDir || node.Sel.Name == "IsDir"
		case *ast.CallExpr:
			if ident, ok := node.Fun.(*ast.Ident); ok {
				callees = append(callees, ident.Name)
			}
		}
		return true
	})
	return callees, isDir
}

// httpCall returns the function name of a call to the net/http package, or "".
func httpCall(call *ast.CallExpr) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "http" {
		return selector.Sel.Name
	}
	return ""
}

// calleeName returns the name of the function an expression calls, or "".
func calleeName(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return ""
	}
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	}
	return ""
}
