// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestAddErrorCapsAndCountsOmissions pins the cap's contract: the retained list
// stops growing, and everything dropped is still counted.
//
// Bug state: appendCapped used to overwrite the last slot with a fixed
// truncation string, so 101 failures and 100,000 failures were indistinguishable
// and the UI rendered both as "100 errors".
func TestAddErrorCapsAndCountsOmissions(t *testing.T) {
	r := &ImportResult{}
	const total = MaxTrackedMessages + 500

	for i := range total {
		r.AddError("failure %d", i)
	}

	if len(r.Errors) != MaxTrackedMessages {
		t.Errorf("retained %d errors, want the cap of %d", len(r.Errors), MaxTrackedMessages)
	}
	if want := total - MaxTrackedMessages; r.ErrorsOmitted != want {
		t.Errorf("ErrorsOmitted = %d, want %d — the dropped count is the only way to tell "+
			"a total failure from a 1%% failure", r.ErrorsOmitted, want)
	}
	if !r.HasErrors() {
		t.Error("HasErrors() = false after recording errors")
	}
}

// TestAddNoticeCapsAndCountsOmissions mirrors the error case.
func TestAddNoticeCapsAndCountsOmissions(t *testing.T) {
	r := &ImportResult{}
	const total = MaxTrackedMessages + 42

	for i := range total {
		r.AddNotice("notice %d", i)
	}

	if len(r.Notices) != MaxTrackedMessages {
		t.Errorf("retained %d notices, want the cap of %d", len(r.Notices), MaxTrackedMessages)
	}
	if want := total - MaxTrackedMessages; r.NoticesOmitted != want {
		t.Errorf("NoticesOmitted = %d, want %d", r.NoticesOmitted, want)
	}
}

// TestSummariesSurviveAnExhaustedCap is the regression test for the failure
// that motivated AddSummary.
//
// Aggregates are emitted after the per-item loops. When a systematic failure
// produced a per-item notice for every file, the cap was already spent and the
// aggregates were silently dropped — including the one reporting that media
// embeds had been deleted from page bodies.
func TestSummariesSurviveAnExhaustedCap(t *testing.T) {
	r := &ImportResult{}

	// A systematic per-item failure exhausts the budget several times over.
	for i := range MaxTrackedMessages * 3 {
		r.AddNotice("could not read file %d", i)
	}

	r.AddSummary("%d media embed(s) were removed from page bodies", 17)

	if len(r.Summaries) != 1 {
		t.Fatalf("Summaries has %d entries, want 1: an exhausted per-item budget "+
			"must not be able to drop an end-of-stage aggregate", len(r.Summaries))
	}
	if !strings.Contains(r.Summaries[0], "removed from page bodies") {
		t.Errorf("Summaries[0] = %q, want the embed-removal aggregate", r.Summaries[0])
	}
}

// TestResultMessagesGoThroughCappedHelpers fails on any direct append to
// ImportResult.Errors or .Notices outside this package.
//
// The Elefant source bypassed AddError in 18 places, so a broken source
// database could write an unbounded list of messages into the persisted job
// row — exactly what the cap exists to prevent. Fixing those 18 sites is only
// half the job; this is the half that stops a nineteenth from appearing.
func TestResultMessagesGoThroughCappedHelpers(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving modules root: %v", err)
	}

	var offenders []string
	fset := token.NewFileSet()

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// The helpers themselves are the one place allowed to append.
		if strings.HasSuffix(path, filepath.Join("migrator", "types", "types.go")) {
			return nil
		}

		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil //nolint:nilerr // unparseable files are the compiler's problem
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}

		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != 1 {
				return true
			}
			sel, ok := assign.Lhs[0].(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Errors" && sel.Sel.Name != "Notices") {
				return true
			}
			// Only `x.Errors = append(...)` is the bypass. Plain assignment is
			// legitimate — ImportJob resets these to nil when its persisted
			// JSON fails to unmarshal.
			if len(assign.Rhs) != 1 {
				return true
			}
			call, ok := assign.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "append" {
				return true
			}
			offenders = append(offenders, rel+":"+
				strconv.Itoa(fset.Position(assign.Pos()).Line)+"  ."+sel.Sel.Name+" appended directly")
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking modules: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("import messages must go through AddError/AddNotice so the "+
			"per-item cap holds and omissions are counted:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
