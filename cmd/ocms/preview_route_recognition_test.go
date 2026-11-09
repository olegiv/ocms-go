// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/preview"
)

var routeParam = regexp.MustCompile(`\{[^}/]*\}`)

// TestEveryPreviewRouteIsRecognizedByThePreviewMiddleware fails when the
// router registers a route with a "preview" path segment that preview.IsPath
// does not recognize.
//
// The middleware gives preview routes their privacy headers and request
// context and redacts legacy share paths before the router runs, so it
// matches paths by shape, anchored at /preview/ or /{lang}/preview/ (security
// audit 2026-11-03, C-3). A route registered elsewhere, say /{lang}/x/preview/
// or /preview below a prefix, would answer with no headers, and the matcher
// would need widening again, so this names the registration instead.
//
// A route parameter stands for "ru" in the first position (the language
// prefix) and "1" elsewhere; a mounted router is checked one segment below its
// prefix, since its own routes are relative to it.
//
// Bug state: register r.Get("/{lang}/x/preview/pages/{id}", ...) in cmd/ocms
// and this names it.
func TestEveryPreviewRouteIsRecognizedByThePreviewMiddleware(t *testing.T) {
	fileSet := token.NewFileSet()
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fileSet, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}
		files = append(files, file)
	}
	resolve := pathResolver{local: stringConstants(files)}
	checked := 0
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			walkRoutes(fn.Body, false, func(pathExpr ast.Expr, method string, _ bool) {
				route, ok := resolve.value(pathExpr)
				if !ok || !slices.Contains(strings.Split(route, "/"), "preview") {
					return
				}
				checked++
				concrete := routeParam.ReplaceAllStringFunc(route, func(string) string { return "1" })
				if strings.HasPrefix(route, "/{") {
					concrete = "/ru" + strings.TrimPrefix(concrete, "/1")
				}
				if method == "Route" || method == "Mount" {
					concrete = strings.TrimSuffix(concrete, "/") + "/x"
				}
				if !preview.IsPath(concrete) {
					t.Errorf("%s  %s is a preview route (checked as %s) but preview.IsPath does not recognize it, so it is served without the privacy headers and request context",
						fileSet.Position(pathExpr.Pos()), route, concrete)
				}
			})
		}
	}
	if checked < 2 {
		t.Fatalf("found %d preview route registrations, want at least the /preview mount and the /{lang} session route; the scan no longer sees them", checked)
	}
}
