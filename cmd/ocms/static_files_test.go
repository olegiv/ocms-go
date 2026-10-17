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
	}
	for path, want := range cases {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != want {
			t.Errorf("GET %s = %d, want %d; body %q", path, rr.Code, want, rr.Body.String())
		}
		if want == http.StatusInternalServerError && rr.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("GET %s: Cache-Control = %q, want no-store", path, rr.Header().Get("Cache-Control"))
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

// TestFileServersRefuseDirectories fails when production code builds an
// http.FileServer over a file system that is not wrapped in
// middleware.NoDirectoryFS, or calls http.ServeFile in a function that
// neither checks for a directory nor calls a function of its package that
// does. Both render a listing of every file below a directory URL; /uploads
// and theme static directories were kept from doing so only by an accidental
// redirect loop (security audit 2026-10-17, I-3).
//
// Bug state: unwrap the uploads file system in cmd/ocms/main.go and this names
// the line.
func TestFileServersRefuseDirectories(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	servers := 0
	var offenders []string
	// fileServerFunc is a function that calls http.ServeFile.
	type fileServerFunc struct {
		dir        string
		calls      map[string]bool
		serveFiles []ast.Node
	}
	var fileServerFuncs []fileServerFunc
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
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				dir := filepath.Dir(path)
				calls := map[string]bool{}
				var serveFiles []ast.Node
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					switch node := n.(type) {
					case *ast.SelectorExpr:
						if node.Sel.Name == "IsDir" {
							checksDirectory[dir+"."+fn.Name.Name] = true
						}
					case *ast.CallExpr:
						if callee, ok := node.Fun.(*ast.Ident); ok {
							calls[callee.Name] = true
						}
						switch httpCall(node) {
						case "FileServer", "FileServerFS":
							servers++
							if len(node.Args) != 1 || calleeName(node.Args[0]) != "NoDirectoryFS" {
								offenders = append(offenders, fset.Position(node.Pos()).String()+
									"  http.FileServer without middleware.NoDirectoryFS")
							}
						case "ServeFile", "ServeFileFS":
							servers++
							serveFiles = append(serveFiles, node)
						}
					}
					return true
				})
				if len(serveFiles) > 0 {
					calls[fn.Name.Name] = true
					fileServerFuncs = append(fileServerFuncs, fileServerFunc{dir, calls, serveFiles})
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}
	for _, fn := range fileServerFuncs {
		checked := false
		for callee := range fn.calls {
			checked = checked || checksDirectory[fn.dir+"."+callee]
		}
		if !checked {
			for _, call := range fn.serveFiles {
				offenders = append(offenders, fset.Position(call.Pos()).String()+
					"  http.ServeFile without a directory check")
			}
		}
	}
	if servers == 0 {
		t.Fatal("no file server found; the scan is broken and this test is vacuous")
	}
	if len(offenders) > 0 {
		t.Errorf("these file servers can list a directory:\n  %s", strings.Join(offenders, "\n  "))
	}
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
