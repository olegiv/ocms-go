// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Every callback in handler source must be a source-checked pre-consent error
// or one of the explicit consent outcomes. Discover new files/callers as well as
// existing branches; this is a direct-call invariant, not alias analysis.
func TestOAuthClientCallbacksRequireConsentOrSourcePolicy(t *testing.T) {
	positions := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatal("handler source inventory failed", err)
	}
	printed := func(node ast.Node) string {
		var output bytes.Buffer
		if printErr := format.Node(&output, positions, node); printErr != nil {
			t.Fatal(printErr)
		}
		return output.String()
	}
	var callbacks int
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(positions, path, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			allowed := make(map[token.Pos]bool)
			allowCallback := func(statement ast.Stmt, target string) {
				expression, isExpression := statement.(*ast.ExprStmt)
				if !isExpression {
					return
				}
				call, isCall := expression.X.(*ast.CallExpr)
				if !isCall || printed(call.Fun) != "oauthClientRedirect" || len(call.Args) != 3 || printed(call.Args[2]) != target {
					return
				}
				allowed[call.Pos()] = true
				if redirect, isRedirect := call.Args[2].(*ast.CallExpr); isRedirect {
					allowed[redirect.Pos()] = true
				}
			}
			cleared, authorized, handledError := false, false, false
			for _, statement := range function.Body.List {
				if function.Name.Name == "oauthPreConsentError" {
					branch, isBranch := statement.(*ast.IfStmt)
					if isBranch && branch.Init != nil && printed(branch.Init) == "redirect := h.oauth.PreConsentErrorRedirect(req, protocolErrorCode(err))" && printed(branch.Cond) == `redirect != ""` {
						for _, child := range branch.Body.List {
							allowCallback(child, "redirect")
						}
					}
					continue
				}
				if function.Name.Name != "oauthConsent" {
					continue
				}
				if printed(statement) == "h.clearOAuthConsent(r)" {
					cleared = true
					continue
				}
				if !cleared {
					continue
				}
				if printed(statement) == "code, err := h.oauth.Authorize(oauth.WithRequest(ctx, r), req, user.ID)" {
					authorized = true
					continue
				}
				if branch, isBranch := statement.(*ast.IfStmt); isBranch {
					var target string
					switch printed(branch.Cond) {
					case `r.PostForm.Get("decision") != "allow"`:
						target = `h.oauth.Redirect(req, url.Values{"error": {"access_denied"}})`
					case "err != nil":
						if authorized {
							target = `h.oauth.Redirect(req, url.Values{"error": {protocolErrorCode(err)}})`
							handledError = true
						}
					}
					for _, child := range branch.Body.List {
						allowCallback(child, target)
					}
				} else if authorized && handledError {
					allowCallback(statement, `h.oauth.Redirect(req, url.Values{"code": {code}})`)
				}
			}
			for node := range ast.Preorder(function.Body) {
				call, isCall := node.(*ast.CallExpr)
				if !isCall {
					continue
				}
				navigation := printed(call.Fun) == "oauthClientRedirect"
				selected, isSelected := call.Fun.(*ast.SelectorExpr)
				var rawRedirect bool
				if isSelected && selected.Sel.Name == "Redirect" {
					receiver, isReceiver := selected.X.(*ast.SelectorExpr)
					rawRedirect = isReceiver && receiver.Sel.Name == "oauth"
				}
				if navigation {
					callbacks++
				}
				if (navigation || rawRedirect) && !allowed[call.Pos()] {
					t.Errorf("%s: callback bypasses the pre-consent source policy or a verified consent outcome", positions.Position(call.Pos()))
				}
			}
		}
	}
	if callbacks < 4 {
		t.Fatal("callback inventory missed a source-policy or consent outcome")
	}
}
