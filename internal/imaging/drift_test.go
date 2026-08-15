// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package imaging

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// This test enforces an invariant mechanically rather than by review. Fixing
// only the site that first broke it leaves the same class of bug reachable
// through another code path.

// TestOnlyMigratorSourcesDownscaleImages pins the trust boundary the downscale
// option creates.
//
// ProcessImageWithOptions{DownscaleOversized: true} raises the decode-bomb
// ceiling from 50 MP to 120 MP. That is defensible only for files an admin
// pointed the migrator at on local disk, processed one at a time. The upload
// path in internal/service is reachable concurrently by any editor with a
// browser, so it must keep the strict reject.
//
// Bug state: wire the option into internal/service/media.go — or any new
// handler — and this names the offending file and line.
func TestOnlyMigratorSourcesDownscaleImages(t *testing.T) {
	const allowedPrefix = "modules/migrator/sources/"

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	var callSites []string
	fset := token.NewFileSet()

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "bin", "data", "uploads":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			// A file that does not parse cannot contain a verified call site;
			// the build catches it far earlier than this test would.
			return nil //nolint:nilerr // parse failures are the compiler's job
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "ProcessImageWithOptions" {
				return true
			}
			if !callEnablesDownscale(call) {
				return true
			}
			if strings.HasPrefix(filepath.ToSlash(rel), allowedPrefix) {
				return true
			}
			callSites = append(callSites, rel+":"+
				fset.Position(call.Pos()).String()[len(fset.Position(call.Pos()).Filename)+1:])
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(callSites) > 0 {
		t.Errorf("DownscaleOversized is set outside %s, which raises the decode-bomb "+
			"ceiling on a path that may face untrusted uploads:\n  %s",
			allowedPrefix, strings.Join(callSites, "\n  "))
	}
}

// callEnablesDownscale reports whether a ProcessImageWithOptions call passes a
// ProcessOptions literal with DownscaleOversized set to true.
func callEnablesDownscale(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "DownscaleOversized" {
				continue
			}
			if val, ok := kv.Value.(*ast.Ident); ok && val.Name == "true" {
				return true
			}
		}
	}
	return false
}
