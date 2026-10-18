// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// eventWriteMethod matches the calls that write to the Event Log: the
// EventService Log* methods and the store's CreateEvent.
var eventWriteMethod = regexp.MustCompile(`^(Log\w*Event|LogInfo|LogWarning|LogError|CreateEvent)$`)

// logMethod matches slog-style logging calls.
var logMethod = regexp.MustCompile(`^(Debug|Info|Warn|Error)(Context)?$`)

// TestEventWriteFailuresStayOutOfTheEventLog fails when code logs the
// failure of an Event Log write without service.WithoutEventLog, or uses
// WithoutEventLog other than as the context of a single log call. Such a
// log record goes through the Event Log handler to the database that just
// failed, and on a locked database waits out its busy timeout a second time
// (review of the 2026-10-17 audit fixes). It checks the error branch of an
// Event Log write: if err := w(...); err != nil { ... }, or err := w(...)
// followed by if err != nil { ... }.
//
// Bug state: log a failed CreateEvent in internal/scheduler/scheduler.go
// with s.logger.Warn(...) and this names it.
func TestEventWriteFailuresStayOutOfTheEventLog(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	branches := 0
	err := walkRepoFiles(root, func(rel string) bool {
		return strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") && !strings.Contains(rel, "/testdata/")
	}, func(path, rel string) error {
		src, err := os.ReadFile(path) // #nosec G304 -- repository files
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			return err
		}
		// Every WithoutEventLog call is the context of one log call.
		marked := map[*ast.CallExpr]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && isLogCall(call, true) && len(call.Args) > 0 {
				if inner, ok := call.Args[0].(*ast.CallExpr); ok && calledName(inner) == "WithoutEventLog" {
					marked[inner] = true
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && calledName(call) == "WithoutEventLog" && !marked[call] {
				if fn, ok := call.Fun.(*ast.Ident); !ok || fn.Name != "WithoutEventLog" || !strings.HasSuffix(rel, "internal/service/events.go") {
					t.Errorf("%s uses WithoutEventLog outside the context argument of a log call; a marked context would keep later records out of the Event Log", fset.Position(call.Pos()))
				}
			}
			return true
		})
		// The error branch of each Event Log write logs with the marker.
		ast.Inspect(file, func(n ast.Node) bool {
			block, ok := n.(*ast.BlockStmt)
			if !ok {
				return true
			}
			for i, stmt := range block.List {
				var branch *ast.BlockStmt
				switch s := stmt.(type) {
				case *ast.IfStmt:
					if assign, ok := s.Init.(*ast.AssignStmt); ok && writesEventLog(assign) {
						branch = s.Body
					}
				case *ast.AssignStmt:
					if name := errorVar(s); name != "" && writesEventLog(s) && i+1 < len(block.List) {
						if next, ok := block.List[i+1].(*ast.IfStmt); ok && next.Init == nil && mentions(next.Cond, name) {
							branch = next.Body
						}
					}
				}
				if branch == nil {
					continue
				}
				branches++
				ast.Inspect(branch, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || !isLogCall(call, false) {
						return true
					}
					first, _ := firstArg(call).(*ast.CallExpr)
					if !strings.HasSuffix(calledName(call), "Context") || first == nil || calledName(first) != "WithoutEventLog" {
						t.Errorf("%s logs a failed Event Log write without service.WithoutEventLog, so the record goes back to the database that failed", fset.Position(call.Pos()))
					}
					return true
				})
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("reading the repository: %v", err)
	}
	if branches < 7 {
		t.Fatalf("found only %d error branches of Event Log writes; the scan is broken and this test is vacuous", branches)
	}
}

// writesEventLog reports whether assign takes its value from an Event Log
// write.
func writesEventLog(assign *ast.AssignStmt) bool {
	if len(assign.Rhs) != 1 {
		return false
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && eventWriteMethod.MatchString(sel.Sel.Name)
}

// errorVar returns the last variable assign sets that is not blank, which
// holds the error of a call returning (value, error) or error, or "".
func errorVar(assign *ast.AssignStmt) string {
	if len(assign.Lhs) == 0 {
		return ""
	}
	if ident, ok := assign.Lhs[len(assign.Lhs)-1].(*ast.Ident); ok && ident.Name != "_" {
		return ident.Name
	}
	return ""
}

// mentions reports whether expr refers to the identifier name.
func mentions(expr ast.Expr, name string) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && ident.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// isLogCall reports whether call is a slog-style logging call; with
// contextOnly, only the *Context variants.
func isLogCall(call *ast.CallExpr, contextOnly bool) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !logMethod.MatchString(sel.Sel.Name) {
		return false
	}
	return !contextOnly || strings.HasSuffix(sel.Sel.Name, "Context")
}

// calledName returns the name of the function or method call calls.
func calledName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

// firstArg returns call's first argument, or nil.
func firstArg(call *ast.CallExpr) ast.Expr {
	if len(call.Args) == 0 {
		return nil
	}
	return call.Args[0]
}
