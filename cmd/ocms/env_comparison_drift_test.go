// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNothingComparesEnvOutsideConfig fails when code outside
// internal/config compares an Env field with an OCMS_ENV value, with == or
// != or as a switch tag. Such a check silently skips a new environment:
// every guard compared Env to "production", so OCMS_ENV=staging, a public
// profile, got none of the production hardening (2026-11-03 audit C-1).
// Code asks config.Config.IsDevelopment or IsProductionLike instead, so the
// meaning of each environment lives in one place. Test files are not
// checked.
//
// Bug state: write `if cfg.Env != "production"` back into
// runPreModulePostureAudits and this names it.
func TestNothingComparesEnvOutsideConfig(t *testing.T) {
	envValues := map[string]bool{"development": true, "staging": true, "production": true}
	isEnvValue := func(expr ast.Expr) bool {
		lit, ok := expr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return false
		}
		value, err := strconv.Unquote(lit.Value)
		return err == nil && envValues[value]
	}
	isEnvField := func(expr ast.Expr) bool {
		sel, ok := expr.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "Env"
	}
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	checked := 0
	err := walkRepoFiles(root, func(rel string) bool {
		return strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") && !strings.HasPrefix(rel, "internal/config/")
	}, func(path, rel string) error {
		src, err := os.ReadFile(path) // #nosec G304 -- repository files
		if err != nil || !bytes.Contains(src, []byte("Env")) {
			return err
		}
		file, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			return err
		}
		checked++
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.BinaryExpr:
				if node.Op != token.EQL && node.Op != token.NEQ {
					return true
				}
				if isEnvField(node.X) && isEnvValue(node.Y) || isEnvField(node.Y) && isEnvValue(node.X) {
					t.Errorf("%s compares Env with an OCMS_ENV value; use config.Config.IsDevelopment or IsProductionLike", fset.Position(node.Pos()))
				}
			case *ast.SwitchStmt:
				if node.Tag != nil && isEnvField(node.Tag) {
					t.Errorf("%s switches on Env; use config.Config.IsDevelopment or IsProductionLike", fset.Position(node.Pos()))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 10 {
		t.Fatalf("checked only %d files that mention Env", checked)
	}
}
