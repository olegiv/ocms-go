// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestRevocationDoesNotResolveClientMetadata pins audit A-3: an anonymous
// revocation request must not validate or fetch client metadata. This direct
// selector guard also catches taking a resolver or method alias; the behavioral
// TestRevokeUsesStoredGrantWithoutClientMetadata guards observable lookups.
func TestRevocationDoesNotResolveClientMetadata(t *testing.T) {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, "protocol.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, declaration := range file.Decls {
		method, ok := declaration.(*ast.FuncDecl)
		if !ok || method.Name.Name != "Revoke" || method.Recv == nil || method.Body == nil {
			continue
		}
		checked++
		for node := range ast.Preorder(method.Body) {
			selector, isSelector := node.(*ast.SelectorExpr)
			if isSelector && (selector.Sel.Name == "clients" || selector.Sel.Name == "resolve") {
				t.Errorf("%s: Revoke must use the stored token and grant, not the client resolver", positions.Position(node.Pos()))
			}
		}
	}
	if checked != 1 {
		t.Fatalf("found %d Revoke methods; update metadata-independence drift coverage", checked)
	}
}
