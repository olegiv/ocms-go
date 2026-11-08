// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil/repotree"
)

func requireChromeCLI(t *testing.T) string {
	t.Helper()
	cli := os.Getenv("OCMS_PLAYWRIGHT_CLI")
	if cli == "" {
		if os.Getenv("OCMS_TEST_BROWSER") == "1" {
			t.Fatal("OCMS_TEST_BROWSER=1 requires OCMS_PLAYWRIGHT_CLI")
		}
		t.Skip("set OCMS_PLAYWRIGHT_CLI to run the Chrome regression")
	}
	return cli
}

func TestChromeCLIReadsGoThroughHelper(t *testing.T) {
	root := filepath.Join("..", "..")
	helper := filepath.Join(root, "internal", "handler", "chrome_test.go")
	positions := token.NewFileSet()
	reads := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if repotree.Skip(root, path, entry) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(positions, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		for _, declaration := range file.Decls {
			for node := range ast.Preorder(declaration) {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					continue
				}
				key, ok := call.Args[0].(*ast.BasicLit)
				if !ok || key.Kind != token.STRING {
					continue
				}
				value, _ := strconv.Unquote(key.Value)
				if value != "OCMS_PLAYWRIGHT_CLI" {
					continue
				}
				selected, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (selected.Sel.Name != "Getenv" && selected.Sel.Name != "LookupEnv") {
					continue
				}
				reads++
				if fn, ok := declaration.(*ast.FuncDecl); !ok || path != helper || fn.Name.Name != "requireChromeCLI" {
					t.Errorf("%s: use requireChromeCLI for browser tests", positions.Position(call.Pos()))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reads == 0 {
		t.Fatal("no guarded read of OCMS_PLAYWRIGHT_CLI found")
	}
}
