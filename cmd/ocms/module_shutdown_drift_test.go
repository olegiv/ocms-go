// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestModuleShutdownsWaitWithinTheLimit fails when a module's Shutdown waits
// without a limit: for its goroutines with X.Wait() instead of
// module.WaitWithin, or by receiving from a channel. The modules' Shutdowns
// share module.ShutdownTimeout, so one waiting for a stuck goroutine takes
// the others' time (review of the 2026-10-17 audit fixes).
//
// It reads each Shutdown method and the methods it calls on its receiver,
// transitively, in modules/ and the tracked part of custom/modules/: site
// modules synced there are gitignored and not read. It flags every X.Wait()
// with no arguments, the goroutine literals included, so even a hand-rolled
// timed wait must use module.WaitWithin; and every channel receive outside a
// select and outside function literals, as those run on Shutdown's own
// goroutine. A select is trusted to have a way out; waits behind calls to
// other packages or through interfaces are not followed.
//
// Bug state: call m.bgWG.Wait() in the Internal Analytics Shutdown, or move
// it into a method Shutdown calls, and this names it.
func TestModuleShutdownsWaitWithinTheLimit(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	packages := map[string][]*ast.File{}
	err := walkRepoFiles(root, func(rel string) bool {
		return (strings.HasPrefix(rel, "modules/") || strings.HasPrefix(rel, "custom/modules/")) &&
			strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") && !strings.Contains(rel, "/testdata/")
	}, func(file, rel string) error {
		src, err := os.ReadFile(file) // #nosec G304 -- repository files
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			return err
		}
		packages[path.Dir(rel)] = append(packages[path.Dir(rel)], parsed)
		return nil
	})
	if err != nil {
		t.Fatalf("reading the repository: %v", err)
	}
	dirs := make([]string, 0, len(packages))
	for dir := range packages {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	shutdowns := 0
	for _, dir := range dirs {
		found, waits := unboundedShutdownWaits(packages[dir])
		shutdowns += found
		for _, w := range waits {
			t.Errorf("%s: %s; wait with module.WaitWithin", fset.Position(w.pos), w.what)
		}
	}
	if shutdowns < 10 {
		t.Fatalf("found only %d module Shutdown methods; the scan is broken and this test is vacuous", shutdowns)
	}
}

// shutdownWait is a wait without a limit found in a Shutdown.
type shutdownWait struct {
	pos  token.Pos
	what string
}

// unboundedShutdownWaits returns how many Shutdown methods the files of one
// package declare and the waits without a limit in them and in the methods
// they call on their receiver.
func unboundedShutdownWaits(files []*ast.File) (shutdowns int, waits []shutdownWait) {
	methods := map[string]map[string]*ast.FuncDecl{} // receiver type -> name -> method
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil && fn.Body != nil {
				recv := receiverType(fn)
				if methods[recv] == nil {
					methods[recv] = map[string]*ast.FuncDecl{}
				}
				methods[recv][fn.Name.Name] = fn
			}
		}
	}
	for recv, byName := range methods {
		shutdown, ok := byName["Shutdown"]
		if !ok {
			continue
		}
		shutdowns++
		seen := map[*ast.FuncDecl]bool{}
		var visit func(fn *ast.FuncDecl, via string)
		visit = func(fn *ast.FuncDecl, via string) {
			if seen[fn] {
				return
			}
			seen[fn] = true
			for _, w := range waitsIn(fn.Body) {
				w.what = "Shutdown " + via + w.what
				waits = append(waits, w)
			}
			name := receiverName(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok && name != "" && x.Name == name {
						if callee, ok := byName[sel.Sel.Name]; ok {
							visit(callee, via+"calls "+recv+"."+callee.Name.Name+", which ")
						}
					}
				}
				return true
			})
		}
		visit(shutdown, "")
	}
	sort.Slice(waits, func(i, j int) bool { return waits[i].pos < waits[j].pos })
	return shutdowns, waits
}

// waitsIn returns the X.Wait() calls with no arguments in body, and the
// channel receives outside a select and outside function literals.
func waitsIn(body *ast.BlockStmt) []shutdownWait {
	var waits []shutdownWait
	var inspect func(n ast.Node, ownGoroutine bool)
	inspect = func(n ast.Node, ownGoroutine bool) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				inspect(n.Body, false)
				return false
			case *ast.SelectStmt:
				for _, clause := range n.Body.List {
					for _, stmt := range clause.(*ast.CommClause).Body {
						inspect(stmt, ownGoroutine)
					}
				}
				return false
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Wait" && len(n.Args) == 0 {
					waits = append(waits, shutdownWait{n.Pos(), "calls " + exprString(n)})
				}
			case *ast.UnaryExpr:
				if n.Op == token.ARROW && ownGoroutine {
					waits = append(waits, shutdownWait{n.Pos(), "receives " + exprString(n)})
				}
			}
			return true
		})
	}
	inspect(body, true)
	return waits
}

// receiverType returns the name of fn's receiver type, without a pointer.
func receiverType(fn *ast.FuncDecl) string {
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if ident, ok := typ.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// receiverName returns the name fn gives its receiver, or "".
func receiverName(fn *ast.FuncDecl) string {
	if names := fn.Recv.List[0].Names; len(names) == 1 {
		return names[0].Name
	}
	return ""
}

// TestUnboundedShutdownWaits checks the detector
// TestModuleShutdownsWaitWithinTheLimit relies on, so a broken one cannot
// pass the repository scan unnoticed.
func TestUnboundedShutdownWaits(t *testing.T) {
	for _, tc := range []struct {
		name      string
		src       string
		wantWaits []string
	}{
		{"direct wait", `func (m *M) Shutdown() error { m.wg.Wait(); return nil }`, []string{"Shutdown calls m.wg.Wait()"}},
		{"wait in a goroutine", `func (m *M) Shutdown() error { go func() { m.wg.Wait() }(); return nil }`, []string{"Shutdown calls m.wg.Wait()"}},
		{"wait in a helper", `func (m *M) Shutdown() error { m.drain(); return nil }
			func (m *M) drain() { m.stop(); m.wg.Wait() }
			func (m *M) stop() { m.drain() }`, []string{"Shutdown calls M.drain, which calls m.wg.Wait()"}},
		{"receive", `func (m *M) Shutdown() error { <-m.done; return nil }`, []string{"Shutdown receives <-m.done"}},
		{"receive in a helper", `func (m *M) Shutdown() error { m.drain(); return nil }
			func (m *M) drain() { v := <-m.results; _ = v }`, []string{"Shutdown calls M.drain, which receives <-m.results"}},
		{"receive in a goroutine", `func (m *M) Shutdown() error { m.wg.Go(func() { <-m.done }); return nil }`, nil},
		{"receive in a select", `func (m *M) Shutdown() error { select { case <-m.done: case <-time.After(time.Second): }; return nil }`, nil},
		{"wait in a select clause", `func (m *M) Shutdown() error { select { case <-m.done: m.wg.Wait(); default: }; return nil }`, []string{"Shutdown calls m.wg.Wait()"}},
		{"bounded wait", `func (m *M) Shutdown() error { module.WaitWithin(&m.wg, time.Second); return nil }`, nil},
		{"wait with an argument", `func (m *M) Shutdown() error { m.cond.Wait(ctx); return nil }`, nil},
		{"call on a field", `func (m *M) Shutdown() error { m.cache.drain(); return nil }
			func (m *M) drain() { m.wg.Wait() }`, nil},
		{"wait outside Shutdown", `func (m *M) Stop() { m.wg.Wait() }`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "probe.go", "package probe\n"+tc.src, 0)
			if err != nil {
				t.Fatal(err)
			}
			shutdowns, waits := unboundedShutdownWaits([]*ast.File{file})
			var got []string
			for _, w := range waits {
				got = append(got, w.what)
			}
			if wantShutdowns := strings.Count(tc.src, ") Shutdown("); shutdowns != wantShutdowns || strings.Join(got, "; ") != strings.Join(tc.wantWaits, "; ") {
				t.Errorf("found %d Shutdown methods and waits %q, want %d and %q", shutdowns, got, wantShutdowns, tc.wantWaits)
			}
		})
	}
}
