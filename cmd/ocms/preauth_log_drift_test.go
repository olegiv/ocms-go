// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Walk the core HTTP and OAuth packages and all first-party modules. Check
// obvious request expressions and simple aliases, including values passed in
// structured attribute slices and Event Log metadata maps.
func TestPreAuthErrorLogsBoundRequestFields(t *testing.T) {
	root := filepath.Join("..", "..")
	err := walkRepoFiles(root, func(rel string) bool {
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || strings.HasSuffix(rel, "_templ.go") {
			return false
		}
		for _, prefix := range []string{"internal/handler/", "internal/middleware/", "internal/oauth/", "modules/"} {
			if strings.HasPrefix(rel, prefix) {
				return true
			}
		}
		return false
	}, func(path, rel string) error {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		check := func(key, value ast.Expr, parseError bool) {
			literal, ok := key.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return
			}
			field, _ := strconv.Unquote(literal.Value)
			if _, constant := value.(*ast.BasicLit); constant || boundedLogValue(value, 0) {
				return
			}
			var required bool
			switch field {
			case "path", "request_url", "method", "request_id", "origin", "referer", "sec_fetch_site",
				"email", "slug", "query", "category", "tag", "target_host":
				required = requestLogValue(value, 0)
			case "source", "target":
				required = rel == "internal/middleware/redirects.go"
			case "form_slug":
				required = rel == "modules/sentinel/module.go" || requestLogValue(value, 0)
			case "ip":
				required = rel == "internal/middleware/login_protection.go"
			case "client_id":
				required = strings.HasPrefix(rel, "internal/oauth/") && rel != "internal/oauth/audit.go"
			case "panic":
				required = rel == "internal/middleware/timeout.go" || rel == "modules/mcpserver/module.go"
			case "error":
				required = parseError
			}
			if required || field == "path" && rel == "modules/analytics_int/tracker.go" ||
				field == "method" && rel == "modules/mcpserver/server.go" {
				t.Errorf("%s logs request field %q without util.BoundedLogField", fset.Position(value.Pos()), field)
			}
		}
		checkPairs := func(args []ast.Expr, parseError bool) {
			for i := 0; i+1 < len(args); i++ {
				check(args[i], args[i+1], parseError)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.CallExpr:
				name := logCallName(n.Fun)
				switch name {
				case "Debug", "Info", "Warn", "Error":
					if len(n.Args) > 0 {
						msg, _ := n.Args[0].(*ast.BasicLit)
						checkPairs(n.Args[1:], rel == "internal/handler/forms.go" && msg != nil && msg.Value == `"failed to parse form"`)
					}
				case "DebugContext", "InfoContext", "WarnContext", "ErrorContext", "Log", "LogAttrs":
					if len(n.Args) > 1 {
						msg, _ := n.Args[1].(*ast.BasicLit)
						checkPairs(n.Args[2:], rel == "internal/handler/forms.go" && msg != nil && msg.Value == `"failed to parse form"`)
					}
				case "String", "Any":
					if len(n.Args) == 2 {
						check(n.Args[0], n.Args[1], false)
					}
				case "failure", "storageProblem", "logFault":
					if strings.HasPrefix(rel, "internal/oauth/") {
						checkPairs(n.Args, false)
					}
				default:
					if (strings.HasPrefix(name, "Log") && strings.HasSuffix(name, "Event")) || name == "logEmbedSecurityEvent" {
						if len(n.Args) > 0 {
							meta, ok := logValueInitializer(n.Args[len(n.Args)-1], 0).(*ast.CompositeLit)
							if ok {
								for _, elt := range meta.Elts {
									if pair, ok := elt.(*ast.KeyValueExpr); ok {
										check(pair.Key, pair.Value, false)
									}
								}
							}
						}
					}
				}
			case *ast.CompositeLit:
				if logCallName(n.Type) == "CreateEventParams" && rel == "internal/middleware/api.go" {
					for _, elt := range n.Elts {
						if field, ok := elt.(*ast.KeyValueExpr); ok && logCallName(field.Key) == "RequestUrl" && !boundedLogValue(field.Value, 0) {
							t.Errorf("%s stores the request path without util.BoundedLogField", fset.Position(field.Value.Pos()))
						}
					}
				}
				if arr, ok := n.Type.(*ast.ArrayType); ok && arr.Len == nil {
					if elt, ok := arr.Elt.(*ast.Ident); ok && elt.Name == "any" {
						checkPairs(n.Elts, false)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func logCallName(expr ast.Expr) string {
	switch fn := expr.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

func logValueInitializer(expr ast.Expr, depth int) ast.Expr {
	id, ok := expr.(*ast.Ident)
	if !ok || id.Obj == nil || depth == 8 {
		return expr
	}
	switch decl := id.Obj.Decl.(type) {
	case *ast.AssignStmt:
		for i, lhs := range decl.Lhs {
			if logCallName(lhs) == id.Name && i < len(decl.Rhs) {
				return logValueInitializer(decl.Rhs[i], depth+1)
			}
		}
	case *ast.ValueSpec:
		for i, name := range decl.Names {
			if name.Name == id.Name && i < len(decl.Values) {
				return logValueInitializer(decl.Values[i], depth+1)
			}
		}
	}
	return expr
}

func boundedLogValue(expr ast.Expr, depth int) bool {
	call, ok := logValueInitializer(expr, depth).(*ast.CallExpr)
	if !ok {
		return false
	}
	fn, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || fn.Sel.Name != "BoundedLogField" {
		return false
	}
	pkg, ok := fn.X.(*ast.Ident)
	return ok && pkg.Name == "util"
}

func requestLogValue(expr ast.Expr, depth int) bool {
	if depth == 8 || boundedLogValue(expr, depth) {
		return false
	}
	expr = logValueInitializer(expr, depth)
	switch v := expr.(type) {
	case *ast.SelectorExpr:
		if id, ok := v.X.(*ast.Ident); ok {
			return (id.Name == "r" || id.Name == "req" || id.Name == "request") &&
				(v.Sel.Name == "URL" || v.Sel.Name == "Method" || v.Sel.Name == "Header" || v.Sel.Name == "Host")
		}
		return requestLogValue(v.X, depth+1)
	case *ast.CallExpr:
		switch logCallName(v.Fun) {
		case "URLParam", "FormValue", "GetReqID", "GetRequestURL", "GetRequestPath", "requestOrigin":
			return true
		}
		if selector, ok := v.Fun.(*ast.SelectorExpr); ok && requestLogValue(selector.X, depth+1) {
			return true
		}
		for _, arg := range v.Args {
			if requestLogValue(arg, depth+1) {
				return true
			}
		}
	}
	return false
}
