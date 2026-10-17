// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/handler"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/seo"
	"github.com/olegiv/ocms-go/internal/util"
)

// TestTopLevelRoutesAreReserved fails when the router gains a top-level path
// segment that util.IsReservedRouteSegment does not know.
//
// chi matches an application route's static segment before the
// language-prefix mount, so a language code equal to one of these first
// segments would collide with the route; page slugs are checked against exact
// routes by TestExactRoutesReservePageSlugs. The reserved list was kept by
// hand beside the redirect, analytics and robots.txt lists, and /account
// reached only two of them (security audit 2026-10-17, L-2).
//
// The scan reads route registrations in cmd/ocms and in the RegisterRoutes
// methods of built-in modules. It resolves string literals, constants and
// their concatenations; a path built at run time (a loop variable, a helper
// parameter) is skipped, and routes nested in r.Route(prefix, ...) count under
// the prefix. Custom modules under custom/ choose their own segments and are
// not covered.
//
// Bug state: drop "account" from util.IsReservedRouteSegment and this names
// the /account registration in cmd/ocms/main.go.
func TestTopLevelRoutesAreReserved(t *testing.T) {
	routes := scanTopLevelRoutes(t)
	var missing []string
	for _, route := range routes {
		if !util.IsReservedRouteSegment(route.segment) {
			missing = append(missing, route.position+"  /"+route.segment)
		}
	}
	if len(missing) > 0 {
		t.Errorf("these top-level routes are not in util.IsReservedRouteSegment, "+
			"so a language code can take the same URL prefix and be shadowed:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

// TestAuthenticatedRoutesArePrivate fails when a top-level route behind
// middleware.Auth is missing from util.IsPrivateRoutePath or from the default
// robots.txt disallow list.
//
// Content redirects and first-party analytics skip private paths, and
// robots.txt keeps crawlers off them. /account was added to the redirect skip
// list and to neither of the others (security audit 2026-10-17, L-2).
//
// Bug state: remove "/account" from the robots.txt defaults in
// internal/seo/robots.go and this names the route.
func TestAuthenticatedRoutesArePrivate(t *testing.T) {
	robots := seo.NewRobotsBuilder(seo.RobotsConfig{}).Build()
	authenticated := 0
	var problems []string
	for _, route := range scanTopLevelRoutes(t) {
		if !route.authenticated {
			continue
		}
		authenticated++
		path := "/" + route.segment
		if !util.IsPrivateRoutePath(path) {
			problems = append(problems, route.position+"  "+path+" is not in util.IsPrivateRoutePath")
		}
		if !strings.Contains(robots, "Disallow: "+path+"\n") {
			problems = append(problems, route.position+"  "+path+" is not disallowed in robots.txt")
		}
	}
	if authenticated == 0 {
		t.Fatal("no route behind middleware.Auth found; the scanner is broken and this test is vacuous")
	}
	if len(problems) > 0 {
		t.Errorf("session-authenticated routes must be private:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestExactRoutesReservePageSlugs fails when util.ReservedPageSlugs differs
// from the single-segment URLs that a route answers on GET.
//
// chi matches such a route before the page catch-all, so a page saved at its
// slug is never served; the list must name every one of them. The other
// direction matters as much: reserving a name whose bare URL no route answers
// (forms, uploads, POST-only language) refused pages that worked and failed
// imports of sites containing them (review of the 2026-10-17 audit fixes).
//
// Bug state: drop "search" from util.reservedPageSlugs and this names the
// frontend /search route; add "forms" and this reports that no exact route
// answers /forms.
func TestExactRoutesReservePageSlugs(t *testing.T) {
	exact := map[string]string{}
	for _, route := range scanTopLevelRoutes(t) {
		switch route.method {
		case "Get", "Handle", "HandleFunc", "Mount", "Route":
			if route.exact {
				exact[route.segment] = route.position
			}
		}
	}
	for segment, position := range exact {
		if !util.IsReservedPageSlug(segment) {
			t.Errorf("%s  /%s answers GET but is not in util.ReservedPageSlugs, so a page saved there is never served", position, segment)
		}
	}
	for _, segment := range util.ReservedPageSlugs() {
		if _, ok := exact[segment]; !ok {
			t.Errorf("util.ReservedPageSlugs has %q but no route answers GET /%s; remove it so pages can use the slug", segment, segment)
		}
	}
}

// TestRoutePathsReserveAliases fails when util.ReservedRoutePaths differs
// from the multi-segment paths and subtrees that routes answer on GET.
//
// A page alias such as "uploads/2016/report" or "blog/tag/news" is answered
// by the route that owns it, so the alias lookup never runs and the alias is
// dead; the admin form rejects aliases that match the list. Reserving only
// first segments refused live aliases such as "blog/post/55", reserving
// nothing stored dead ones, and reserving /uploads itself or parameter routes
// that fall back to the alias lookup refused slugs and aliases that work
// (reviews of the 2026-10-17 audit fixes).
//
// Bug state: drop "uploads/*/**" from util.reservedRoutePaths and this names
// the /uploads/* registration; add "blog/post/*" and this reports that no
// route answers it.
func TestRoutePathsReserveAliases(t *testing.T) {
	answered := map[string]string{}
	fallbacks := map[string]bool{}
	for _, route := range scanTopLevelRoutes(t) {
		if aliasFallbackRoutes[route.path] {
			fallbacks[route.path] = true
		}
		if pattern, ok := aliasShadowPattern(route); ok {
			answered[pattern] = route.position
		}
	}
	for path := range aliasFallbackRoutes {
		if !fallbacks[path] {
			t.Errorf("aliasFallbackRoutes names %s, which no route registers", path)
		}
	}
	for pattern, position := range answered {
		if !slices.Contains(util.ReservedRoutePaths(), pattern) {
			t.Errorf("%s  %s answers GET but is not in util.ReservedRoutePaths, so a page alias there is never served", position, pattern)
		}
	}
	for _, pattern := range util.ReservedRoutePaths() {
		if _, ok := answered[pattern]; !ok {
			t.Errorf("util.ReservedRoutePaths has %q but no route answers it; remove it so aliases can use the path", pattern)
		}
	}
}

// aliasFallbackRoutes are parameter routes whose handlers send a miss to the
// frontend not-found handler, which resolves page aliases, so an alias below
// them works while no such page, term or form exists.
var aliasFallbackRoutes = map[string]bool{
	handler.RoutePageByID:     true,
	handler.RouteCategorySlug: true,
	handler.RouteTagSlug:      true,
	handler.RouteFormsSlug:    true,
}

// aliasShadowPattern returns the util.ReservedRoutePaths pattern for a route
// that answers GET on more than its first segment without falling back to the
// alias lookup: "*" for each parameter, a trailing "/**" for a mounted router
// (which answers its prefix too) and "/*/**" for a /x/* route (which answers
// only paths below its prefix).
func aliasShadowPattern(route topLevelRoute) (string, bool) {
	switch route.method {
	case "Get", "Handle", "HandleFunc", "Mount", "Route":
	default:
		return "", false
	}
	if aliasFallbackRoutes[route.path] {
		return "", false
	}
	path := strings.TrimPrefix(route.path, "/")
	mounted := route.method == "Mount" || route.method == "Route"
	belowOnly := false
	if trimmed, ok := strings.CutSuffix(path, "/*"); ok {
		path, belowOnly = trimmed, true
	}
	if !mounted && !belowOnly && !strings.Contains(path, "/") {
		return "", false // a single segment: TestExactRoutesReservePageSlugs
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "{") {
			parts[i] = "*"
		}
	}
	switch {
	case belowOnly:
		parts = append(parts, "*", "**")
	case mounted:
		parts = append(parts, "**")
	}
	return strings.Join(parts, "/"), true
}

// topLevelRoute is one route registration on the root router.
type topLevelRoute struct {
	segment       string
	path          string // the full registered path, e.g. "/forms/{slug}"
	method        string // registrar name: Get, Post, Handle, Route, ...
	exact         bool   // the path is exactly "/" + segment
	authenticated bool
	position      string
}

// routeRegistrars are the chi.Router methods whose first argument is a path.
var routeRegistrars = map[string]bool{
	"Connect": true, "Delete": true, "Get": true, "Head": true, "Options": true,
	"Patch": true, "Post": true, "Put": true, "Trace": true,
	"Handle": true, "HandleFunc": true, "Mount": true, "Route": true,
}

// scanTopLevelRoutes returns every resolvable route registration in cmd/ocms
// and in built-in modules' RegisterRoutes whose first segment could also be a
// page slug. It fails the test when the scan finds too little to be trusted.
func scanTopLevelRoutes(t *testing.T) []topLevelRoute {
	t.Helper()
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()

	files := map[string][]*ast.File{} // directory → parsed non-test files
	parseDir := func(dir string) {
		matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("listing %s: %v", dir, err)
		}
		for _, name := range matches {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, name, nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", name, err)
			}
			files[dir] = append(files[dir], file)
		}
	}
	cmdDir := "."
	handlerDir := filepath.Join(root, "internal", "handler")
	parseDir(cmdDir)
	parseDir(handlerDir)
	modulesRoot := filepath.Join(root, "modules")
	err := filepath.WalkDir(modulesRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			parseDir(path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking modules: %v", err)
	}

	handlerConsts := stringConstants(files[handlerDir])
	var routes []topLevelRoute
	scan := func(dir string, body *ast.BlockStmt) {
		resolve := pathResolver{local: stringConstants(files[dir]), handler: handlerConsts}
		walkRoutes(body, false, func(pathExpr ast.Expr, method string, authenticated bool) {
			path, ok := resolve.value(pathExpr)
			if !ok || !strings.HasPrefix(path, "/") {
				return
			}
			segment, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
			// Only a segment with slug grammar can collide with a page slug
			// or language code; "sitemap.xml" and "{slug}" cannot.
			if !util.IsValidSlug(segment) {
				return
			}
			routes = append(routes, topLevelRoute{
				segment:       segment,
				path:          path,
				method:        method,
				exact:         path == "/"+segment,
				authenticated: authenticated,
				position:      fset.Position(pathExpr.Pos()).String(),
			})
		})
	}
	for dir, parsed := range files {
		inCmd := dir == cmdDir
		inModules := strings.HasPrefix(dir, modulesRoot)
		if !inCmd && !inModules {
			continue
		}
		for _, file := range parsed {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				if inCmd || (fn.Recv != nil && fn.Name.Name == "RegisterRoutes") {
					scan(dir, fn.Body)
				}
			}
		}
	}

	// The scan is only meaningful while it still sees routes it must see.
	seen := map[string]bool{}
	for _, route := range routes {
		seen[route.segment] = true
	}
	for _, want := range []string{"admin", "account", "login", "api", "analytics"} {
		if !seen[want] {
			t.Fatalf("route scan did not find /%s; the registration style changed and the scan needs updating", want)
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].position < routes[j].position })
	return routes
}

// walkRoutes calls visit with the path argument of every route registration in
// body. Group bodies are walked in place; a Route body is not, because its
// paths are relative to the prefix that Route itself registers.
func walkRoutes(body *ast.BlockStmt, authenticated bool, visit func(ast.Expr, string, bool)) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		routeAuth := authenticated || chainUsesAuth(selector.X)
		switch name := selector.Sel.Name; {
		case name == "Group" && len(call.Args) == 1:
			if fn, ok := call.Args[0].(*ast.FuncLit); ok {
				walkRoutes(fn.Body, routeAuth || bodyUsesAuth(fn.Body), visit)
				return false
			}
		case name == "Route" && len(call.Args) == 2:
			if fn, ok := call.Args[1].(*ast.FuncLit); ok {
				routeAuth = routeAuth || bodyUsesAuth(fn.Body)
			}
			visit(call.Args[0], name, routeAuth)
			return false
		case routeRegistrars[name] && len(call.Args) >= 1:
			visit(call.Args[0], name, routeAuth)
		}
		return true
	})
}

// chainUsesAuth reports whether a router expression such as
// r.With(csrf, middleware.Auth(sm)) applies middleware.Auth.
func chainUsesAuth(expr ast.Expr) bool {
	for {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if selector.Sel.Name == "With" && slicesContainAuth(call.Args) {
			return true
		}
		expr = selector.X
	}
}

// bodyUsesAuth reports whether a Group or Route body calls r.Use with
// middleware.Auth among its arguments.
func bodyUsesAuth(body *ast.BlockStmt) bool {
	for _, stmt := range body.List {
		exprStmt, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := exprStmt.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Use" && slicesContainAuth(call.Args) {
			return true
		}
	}
	return false
}

// slicesContainAuth reports whether args include a middleware.Auth(...) call.
func slicesContainAuth(args []ast.Expr) bool {
	for _, arg := range args {
		call, ok := arg.(*ast.CallExpr)
		if !ok {
			continue
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "middleware" && selector.Sel.Name == "Auth" {
			return true
		}
	}
	return false
}

// pathResolver evaluates route path expressions built from string literals,
// package constants and handler.* constants.
type pathResolver struct {
	local   map[string]string
	handler map[string]string
}

func (p pathResolver) value(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(e.Value)
		return value, err == nil
	case *ast.Ident:
		value, ok := p.local[e.Name]
		return value, ok
	case *ast.SelectorExpr:
		if pkg, ok := e.X.(*ast.Ident); ok && pkg.Name == "handler" {
			value, ok := p.handler[e.Sel.Name]
			return value, ok
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			left, leftOK := p.value(e.X)
			right, rightOK := p.value(e.Y)
			return left + right, leftOK && rightOK
		}
	}
	return "", false
}

// stringConstants returns the string constants declared in files, resolving
// constants defined in terms of each other.
func stringConstants(files []*ast.File) map[string]string {
	values := map[string]string{}
	pending := map[string]ast.Expr{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok || len(valueSpec.Names) != len(valueSpec.Values) {
					continue
				}
				for i, name := range valueSpec.Names {
					pending[name.Name] = valueSpec.Values[i]
				}
			}
		}
	}
	for progress := true; progress; {
		progress = false
		resolver := pathResolver{local: values}
		for name, expr := range pending {
			if value, ok := resolver.value(expr); ok {
				values[name] = value
				delete(pending, name)
				progress = true
			}
		}
	}
	return values
}

// TestCSPReportEndpointIsRouted fails when the CSP sends violation reports to
// a path the router does not serve, which would turn every report into a 404
// and leave violations invisible again.
func TestCSPReportEndpointIsRouted(t *testing.T) {
	cfg := middleware.DefaultSecurityHeadersConfig(false)
	if !strings.Contains(cfg.ContentSecurityPolicy, "report-uri "+handler.RouteCSPReport) {
		t.Errorf("CSP report-uri does not point at %s: %s", handler.RouteCSPReport, cfg.ContentSecurityPolicy)
	}
	segment := strings.TrimPrefix(handler.RouteCSPReport, "/")
	for _, route := range scanTopLevelRoutes(t) {
		if route.segment == segment {
			return
		}
	}
	t.Errorf("no route registers %s", handler.RouteCSPReport)
}
