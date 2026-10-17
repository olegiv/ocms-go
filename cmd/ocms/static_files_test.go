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
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/theme"
	"github.com/olegiv/ocms-go/internal/themes"
)

// TestThemeStaticRefusesDirectories checks that a custom theme's static route
// serves files and answers a directory with 404 instead of a listing
// (security audit 2026-10-17, I-3).
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

	for path, want := range map[string]int{
		"/themes/probe/static/css/site.css": http.StatusOK,
		"/themes/probe/static/css":          http.StatusNotFound,
		"/themes/probe/static/css/":         http.StatusNotFound,
	} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != want {
			t.Errorf("GET %s = %d, want %d; body %q", path, rr.Code, want, rr.Body.String())
		}
	}
}

// TestFileServersRefuseDirectories fails when production code builds an
// http.FileServer over a file system that is not wrapped in
// middleware.NoDirectoryFS, or calls http.ServeFile in a function that never
// checks for a directory. Both render a listing of every file below a
// directory URL; /uploads and theme static directories were kept from doing
// so only by an accidental redirect loop (security audit 2026-10-17, I-3).
//
// Bug state: unwrap the uploads file system in cmd/ocms/main.go and this names
// the line.
func TestFileServersRefuseDirectories(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	servers := 0
	var offenders []string
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
				checksDirectory := false
				var serveFiles []ast.Node
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					switch node := n.(type) {
					case *ast.SelectorExpr:
						if node.Sel.Name == "IsDir" {
							checksDirectory = true
						}
					case *ast.CallExpr:
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
				if !checksDirectory {
					for _, call := range serveFiles {
						offenders = append(offenders, fset.Position(call.Pos()).String()+
							"  http.ServeFile without a directory check")
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", tree, err)
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
