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
	"strconv"
	"strings"
	"testing"
)

// TestRepoWalksSkipLocalDirectories fails when a test walks the whole
// repository with filepath.WalkDir or filepath.Walk without skipping
// directories through repotree.Skip. A walk that reads the gitignored
// .audit/ snapshots of older trees, or the scratch programs in output/,
// fails on a developer's checkout while CI, which has neither, passes: the
// imaging drift test blocked a push from this checkout that way. A walk is
// of the whole repository when its root is a variable set from ".."
// segments that lead from the test's directory to the repository root, as
// in filepath.Join("..", "..") or filepath.Abs("../.."), or from a helper
// that finds the root (repoRoot, repositoryRoot).
//
// Bug state: drop the repotree.Skip check from the walk in
// internal/imaging/drift_test.go and this names it.
func TestRepoWalksSkipLocalDirectories(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	walks := 0
	err := walkRepoFiles(root, func(rel string) bool {
		return strings.HasSuffix(rel, "_test.go") && !strings.Contains(rel, "/testdata/")
	}, func(file, rel string) error {
		src, err := os.ReadFile(file) // #nosec G304 -- repository files
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			return err
		}
		dir := path.Dir(rel)
		ast.Inspect(parsed, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			roots := repoRootVariables(fn.Body, dir)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isFilepathWalk(call) || len(call.Args) != 2 {
					return true
				}
				arg, ok := call.Args[0].(*ast.Ident)
				if !ok || !roots[arg.Name] {
					return true
				}
				walks++
				if !callsRepotreeSkip(call.Args[1]) {
					t.Errorf("%s walks the repository from %s without repotree.Skip; it reads the gitignored .audit/ and output/ directories",
						fset.Position(call.Pos()), arg.Name)
				}
				return true
			})
			return false
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if walks == 0 {
		t.Fatal("found no walk of the whole repository; the scan is broken")
	}
}

// repoRootHelpers are the test helpers that return the repository root.
var repoRootHelpers = map[string]bool{"repoRoot": true, "repositoryRoot": true}

// repoRootVariables returns the names body assigns the repository root:
// an expression whose ".." segments lead from dir, the test's directory, to
// the root, or a call to one of repoRootHelpers.
func repoRootVariables(body *ast.BlockStmt, dir string) map[string]bool {
	roots := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) == 0 {
			return true
		}
		name, ok := assign.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		if call, ok := assign.Rhs[0].(*ast.CallExpr); ok && repoRootHelpers[calledName(call)] {
			roots[name.Name] = true
			return true
		}
		var segments []string
		ast.Inspect(assign.Rhs[0], func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if value, err := strconv.Unquote(lit.Value); err == nil && isParentPath(value) {
					segments = append(segments, value)
				}
			}
			return true
		})
		if len(segments) > 0 && path.Join(append([]string{dir}, segments...)...) == "." {
			roots[name.Name] = true
		}
		return true
	})
	return roots
}

// isParentPath reports whether value is made only of ".." segments.
func isParentPath(value string) bool {
	for segment := range strings.SplitSeq(value, "/") {
		if segment != ".." {
			return false
		}
	}
	return true
}

// isFilepathWalk reports whether call is filepath.WalkDir or filepath.Walk.
func isFilepathWalk(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "filepath" && (sel.Sel.Name == "WalkDir" || sel.Sel.Name == "Walk")
}

// callsRepotreeSkip reports whether node contains a repotree.Skip call.
func callsRepotreeSkip(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Skip" {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "repotree" {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// TestSkippedDirectoriesListedOnce fails when a Go file other than
// internal/testutil/repotree names node_modules. Eleven test walkers kept
// their own lists of directories to skip, the lists drifted apart, and the
// ones without .audit/ read gitignored copies of older trees. repotree.Skip
// holds the one list.
//
// Bug state: put a `case "node_modules", ...` skip list back into the walk
// in internal/imaging/drift_test.go and this names it.
func TestSkippedDirectoriesListedOnce(t *testing.T) {
	const listFile = "internal/testutil/repotree/repotree.go"
	marker := "node_" + "modules" // split so this file does not name it
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	listed := false
	err := walkRepoFiles(root, func(rel string) bool {
		return strings.HasSuffix(rel, ".go")
	}, func(file, rel string) error {
		src, err := os.ReadFile(file) // #nosec G304 -- repository files
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			return err
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if value, err := strconv.Unquote(lit.Value); err != nil || value != marker {
				return true
			}
			if rel == listFile {
				listed = true
				return true
			}
			t.Errorf("%s lists directories to skip; use repotree.Skip, which holds the one list", fset.Position(lit.Pos()))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !listed {
		t.Fatalf("%s no longer names %s; the scan is broken", listFile, marker)
	}
}
