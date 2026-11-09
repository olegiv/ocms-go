// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package review_test

import (
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestTranslationAIAccessFaultsDoNotBecomeDrafts(t *testing.T) {
	for _, fault := range []string{"query failure", "unreadable linked row"} {
		t.Run(fault, func(t *testing.T) {
			f := setupLoad(t)
			ctx, now := t.Context(), time.Now().UTC()
			principal := provenance.Principal{UserID: f.author.ID, Source: "human"}
			source := f.revised(principal)
			_, err := f.q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			translated, err := pagewrite.Insert(ctx, f.q, principal, store.CreatePageParams{Title: "Translated draft", Slug: "translated", Body: "<p>Translation</p>", Status: "draft", AuthorID: f.author.ID, LanguageCode: "ru", PageType: "post", CreatedAt: now, UpdatedAt: now}, nil)
			if err != nil {
				t.Fatal(err)
			}
			work, err := pagewrite.InQueriesTx(ctx, f.q, principal, func(work *pagewrite.Tx) error {
				if linkErr := work.LinkTranslation(ctx, source.ID, "ru", translated.ID); linkErr != nil {
					return linkErr
				}
				return work.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: translated.ID, AiEditable: true, SetBy: f.author.ID, SetAt: now})
			})
			if err != nil {
				t.Fatal(err)
			}
			source, translated = work.Pages[source.ID], work.Pages[translated.ID]
			allow := func(enabled bool) {
				t.Helper()
				if writeErr := f.q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: translated.ID, AiEditable: enabled, SetBy: f.author.ID, SetAt: now}); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			exec := func(statement string, args ...any) {
				t.Helper()
				if _, writeErr := f.db.ExecContext(ctx, statement, args...); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			assertStatus := func(want string) {
				t.Helper()
				items, readErr := review.Translations(ctx, f.q, source)
				if readErr != nil {
					t.Fatal(readErr)
				}
				var found bool
				for _, item := range items {
					if item.PageID == translated.ID {
						found = true
						if item.Status != want || item.Freshness != "not_tracked" || item.Revision != translated.Revision {
							t.Errorf("translation=%+v, want status=%s", item, want)
						}
					}
				}
				if !found {
					t.Fatal("linked translation omitted")
				}
				panel, loadErr := review.Load(ctx, f.q, source, f.viewer.ID)
				if loadErr != nil || !reflect.DeepEqual(panel.Translations, items) {
					t.Fatalf("review consumer differs: %+v %v", panel.Translations, loadErr)
				}
			}
			allow(true)
			assertStatus("ai_draft")
			cause := "sf12_invalid_ai_access"
			if fault == "query failure" {
				exec("ALTER TABLE page_ai_access RENAME COLUMN ai_editable TO sf12_unavailable")
				cause = "no such column"
			} else {
				exec("UPDATE page_ai_access SET ai_editable=? WHERE page_id=?", cause, translated.ID)
			}
			items, readErr := review.Translations(ctx, f.q, source)
			panel, loadErr := review.Load(ctx, f.q, source, f.viewer.ID)
			for _, lookupErr := range []error{readErr, loadErr} {
				if lookupErr == nil {
					t.Error("lookup fault became a successful translation list")
				} else if errors.Is(lookupErr, sql.ErrNoRows) || !strings.Contains(lookupErr.Error(), cause) || !strings.Contains(lookupErr.Error(), "load translation AI access for page ") {
					t.Errorf("fault lost classification/context: %v", lookupErr)
				}
			}
			if items != nil || panel.Translations != nil {
				t.Errorf("fault returned a partial/mislabelled list: %+v %+v", items, panel.Translations)
			}
			if fault == "query failure" {
				exec("ALTER TABLE page_ai_access RENAME COLUMN sf12_unavailable TO ai_editable")
			} else {
				allow(true)
			}
			assertStatus("ai_draft")
			for _, page := range []store.Page{source, translated} {
				after, pageErr := f.q.GetPageByID(ctx, page.ID)
				if pageErr != nil || !reflect.DeepEqual(after, page) {
					t.Fatalf("read fault/recovery changed a page: %+v %v", after, pageErr)
				}
				testutil.AssertPageRevision(t, f.q, page.ID, page.Revision)
			}
			exec("DELETE FROM page_ai_access WHERE page_id=?", translated.ID)
			assertStatus("draft")
			allow(false)
			assertStatus("draft")
			allow(true)
			exec("UPDATE pages SET status='published' WHERE id=?", translated.ID)
			assertStatus("published")
		})
	}
}

// Reject the success-only query guards that discard the failure path. Scan
// lookup calls rather than a fixed list of method names, including a query
// assigned immediately before its guard as well as an inline initializer.
// This is a direct syntax guard, not interprocedural control-flow analysis.
func TestTranslationLookupsRejectSuccessOnlyGuards(t *testing.T) {
	positions := token.NewFileSet()
	parsed, err := parser.ParseFile(positions, "review.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, declaration := range parsed.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == "Translations" {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatal("Translations missing; update lookup drift coverage")
	}
	check := func(assignment *ast.AssignStmt, branch *ast.IfStmt) {
		if assignment == nil || branch == nil || branch.Else != nil || len(assignment.Rhs) != 1 || len(assignment.Lhs) < 2 {
			return
		}
		call, ok := assignment.Rhs[0].(*ast.CallExpr)
		if !ok {
			return
		}
		selected, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}
		owner, ok := selected.X.(*ast.Ident)
		if !ok || owner.Name != "q" {
			return
		}
		bound, ok := assignment.Lhs[len(assignment.Lhs)-1].(*ast.Ident)
		if !ok {
			return
		}
		for node := range ast.Preorder(branch.Cond) {
			comparison, ok := node.(*ast.BinaryExpr)
			if !ok || comparison.Op != token.EQL {
				continue
			}
			left, leftOK := comparison.X.(*ast.Ident)
			right, rightOK := comparison.Y.(*ast.Ident)
			if leftOK && rightOK && (left.Name == bound.Name && right.Name == "nil" || right.Name == bound.Name && left.Name == "nil") {
				t.Errorf("%s: %s discards its lookup failure; handle errors before using the result", positions.Position(branch.Pos()), selected.Sel.Name)
			}
		}
	}
	for node := range ast.Preorder(body) {
		if branch, ok := node.(*ast.IfStmt); ok {
			assignment, _ := branch.Init.(*ast.AssignStmt)
			check(assignment, branch)
		}
		if block, ok := node.(*ast.BlockStmt); ok {
			for i := 1; i < len(block.List); i++ {
				assignment, _ := block.List[i-1].(*ast.AssignStmt)
				branch, _ := block.List[i].(*ast.IfStmt)
				check(assignment, branch)
			}
		}
	}
	// Also make a missing query surface a coverage change, not a vacuous pass.
	queries := 0
	for node := range ast.Preorder(body) {
		if selected, ok := node.(*ast.SelectorExpr); ok {
			if owner, named := selected.X.(*ast.Ident); named && owner.Name == "q" {
				queries++
			}
		}
	}
	if queries == 0 {
		t.Fatal("no translation queries found; update drift coverage")
	}
}
