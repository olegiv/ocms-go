// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestModuleShutdownsWaitWithinTheLimit fails when a module's Shutdown waits
// for its goroutines with X.Wait() instead of module.WaitWithin. The modules'
// Shutdowns share module.ShutdownTimeout, so one waiting for a stuck
// goroutine takes the others' time (review of the 2026-10-17 audit fixes).
// It flags every X.Wait() with no arguments written in a Shutdown body, the
// goroutine literals in it included, so even a hand-rolled timed wait must
// use module.WaitWithin. It reads the Shutdown methods in modules/ and the
// tracked ones in custom/modules/: site modules synced there are gitignored
// and not read. Waits in methods Shutdown calls, and other unbounded waits
// such as channel receives, are not detected.
//
// Bug state: call m.bgWG.Wait() in the Internal Analytics Shutdown and this
// names it.
func TestModuleShutdownsWaitWithinTheLimit(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	shutdowns := 0
	err := walkRepoFiles(root, func(rel string) bool {
		return (strings.HasPrefix(rel, "modules/") || strings.HasPrefix(rel, "custom/modules/")) &&
			strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") && !strings.Contains(rel, "/testdata/")
	}, func(path, rel string) error {
		src, err := os.ReadFile(path) // #nosec G304 -- repository files
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			return err
		}
		found, waits := shutdownWaits(file)
		shutdowns += found
		for _, call := range waits {
			t.Errorf("%s: Shutdown calls %s; wait with module.WaitWithin", fset.Position(call.Pos()), exprString(call))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the repository: %v", err)
	}
	if shutdowns < 10 {
		t.Fatalf("found only %d module Shutdown methods; the scan is broken and this test is vacuous", shutdowns)
	}
}

// shutdownWaits returns how many Shutdown methods file declares and the
// X.Wait() calls with no arguments in their bodies.
func shutdownWaits(file *ast.File) (shutdowns int, waits []*ast.CallExpr) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != "Shutdown" || fn.Body == nil {
			continue
		}
		shutdowns++
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && len(call.Args) == 0 {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Wait" {
					waits = append(waits, call)
				}
			}
			return true
		})
	}
	return shutdowns, waits
}

// TestShutdownWaits checks the detector TestModuleShutdownsWaitWithinTheLimit
// relies on, so a broken one cannot pass the repository scan unnoticed.
func TestShutdownWaits(t *testing.T) {
	for _, tc := range []struct {
		name      string
		src       string
		wantWaits int
	}{
		{"direct wait", `func (m *M) Shutdown() error { m.wg.Wait(); return nil }`, 1},
		{"wait in a goroutine", `func (m *M) Shutdown() error { go func() { m.wg.Wait() }(); return nil }`, 1},
		{"bounded wait", `func (m *M) Shutdown() error { module.WaitWithin(&m.wg, time.Second); return nil }`, 0},
		{"wait with an argument", `func (m *M) Shutdown() error { m.cond.Wait(ctx); return nil }`, 0},
		{"wait outside Shutdown", `func (m *M) Stop() { m.wg.Wait() }`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "probe.go", "package probe\n"+tc.src, 0)
			if err != nil {
				t.Fatal(err)
			}
			shutdowns, waits := shutdownWaits(file)
			if wantShutdowns := strings.Count(tc.src, ") Shutdown("); shutdowns != wantShutdowns || len(waits) != tc.wantWaits {
				t.Errorf("shutdownWaits found %d Shutdown methods and %d waits, want %d and %d", shutdowns, len(waits), wantShutdowns, tc.wantWaits)
			}
		})
	}
}
