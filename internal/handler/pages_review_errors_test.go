// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestPageReviewReadFailuresAreLogged distinguishes missing records from
// storage faults and corrupt review data without exposing internal errors.
// Reverting the handler fixes leaves failures unlogged or returns a false 404.
func TestPageReviewReadFailuresAreLogged(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query, corrupt, missing, message string
		edit                                   bool
	}{
		{name: "editor access", edit: true, query: "GetPageAIAccess", message: "failed to load page AI access"},
		{name: "editor account", edit: true, query: "GetUserByID", message: "failed to load page editor account"},
		{name: "editor evidence", edit: true, query: "GetContentProvenance", message: "failed to load editorial review"},
		{name: "editor translation lookup", edit: true, query: "GetLatestTranslationProvenance", message: "failed to load editorial review"},
		{name: "editor translation AI access", edit: true, corrupt: "translation access", message: "failed to load editorial review"},
		{name: "editor corrupt evidence", edit: true, corrupt: "evidence", message: "failed to load editorial review"},
		{name: "revision page", query: "GetPageByID", message: "failed to load page for revision review"},
		{name: "revision version", query: "GetPageVersion", message: "failed to load recorded revision"},
		{name: "revision predecessor", query: "GetPreviousPageVersion", message: "failed to load previous revision"},
		{name: "revision corrupt snapshot", corrupt: "snapshot", message: "failed to compare recorded revision"},
		{name: "revision evidence", query: "GetContentProvenance", message: "failed to load revision evidence"},
		{name: "revision translation lookup", query: "GetLatestTranslationProvenance", message: "failed to load revision evidence"},
		{name: "revision translation AI access", corrupt: "translation access", message: "failed to load revision evidence"},
		{name: "revision corrupt evidence", corrupt: "evidence", message: "failed to load revision evidence"},
		{name: "editor without AI access", edit: true},
		{name: "missing editor", edit: true, query: "GetUserByID", missing: "editor"},
		{name: "first revision without predecessor"},
		{name: "missing page", missing: "page"},
		{name: "missing version", missing: "version"},
		{name: "version from another page", missing: "foreign version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, cleanup := testutil.TestDB(t)
			t.Cleanup(cleanup)
			sm := testSessionManager(t)
			user := createTestAdminUser(t, db)
			q, ctx := store.New(db), t.Context()
			create := func(slug, language string) store.Page {
				t.Helper()
				now := time.Now().UTC()
				page, insertErr := pagewrite.Insert(ctx, q, provenance.Principal{UserID: user.ID, Source: "human"}, store.CreatePageParams{
					Title: "Review page", Slug: slug, Body: "<p>Recorded content</p>", Status: "draft", AuthorID: user.ID,
					LanguageCode: language, PageType: "post", CreatedAt: now, UpdatedAt: now,
				}, nil)
				if insertErr != nil {
					t.Fatal(insertErr)
				}
				return page
			}
			page := create("review-page", "en")
			version, err := q.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{PageID: page.ID, Revision: sql.NullInt64{Int64: page.Revision, Valid: true}})
			if err != nil {
				t.Fatal(err)
			}
			fact, err := q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: page.ID, Revision: page.Revision})
			if err != nil {
				t.Fatal(err)
			}
			renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
			if err != nil {
				t.Fatal(err)
			}
			h := NewPagesHandler(db, renderer, sm)
			router := chi.NewRouter()
			router.Use(sm.LoadAndSave)
			router.Get("/admin/pages/{id}", func(w http.ResponseWriter, r *http.Request) { h.EditForm(w, withUser(r, user)) })
			router.Get("/admin/pages/{id}/versions/{versionId}", func(w http.ResponseWriter, r *http.Request) { h.RevisionReview(w, withUser(r, user)) })
			pageID, versionID := page.ID, version.ID
			switch tc.missing {
			case "page":
				pageID += 10000
			case "version":
				versionID += 10000
			case "foreign version":
				pageID = create("another-page", "en").ID
			}
			target := fmt.Sprintf("/admin/pages/%d", pageID)
			if !tc.edit {
				target += fmt.Sprintf("/versions/%d", versionID)
			}
			get := func() *httptest.ResponseRecorder {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
				return response
			}
			if tc.query != "" {
				h.queries = store.New(&conflictQueryFault{DB: db, query: "-- name: " + tc.query + " :", missing: tc.missing == "editor"})
			}
			var restoreQuery string
			var restoreValue any
			restoreID := page.ID
			cause := "unavailable_conflict_data"
			switch tc.corrupt {
			case "translation access":
				now := time.Now().UTC()
				_, languageErr := q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now})
				if languageErr != nil {
					t.Fatal(languageErr)
				}
				translated := create("translated-review", "ru")
				work, linkErr := pagewrite.InQueriesTx(ctx, q, provenance.Principal{UserID: user.ID, Source: "human"}, func(work *pagewrite.Tx) error {
					if translationErr := work.LinkTranslation(ctx, page.ID, "ru", translated.ID); translationErr != nil {
						return translationErr
					}
					return work.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: translated.ID, AiEditable: true, SetBy: user.ID, SetAt: now})
				})
				if linkErr != nil {
					t.Fatal(linkErr)
				}
				page = work.Pages[page.ID]
				if _, err = db.ExecContext(ctx, "UPDATE page_ai_access SET ai_editable='sf12_invalid_ai_access' WHERE page_id=?", translated.ID); err != nil {
					t.Fatal(err)
				}
				restoreQuery, restoreValue, restoreID = "UPDATE page_ai_access SET ai_editable=? WHERE page_id=?", true, translated.ID
				cause = "sf12_invalid_ai_access"
			case "evidence":
				if _, err = db.ExecContext(ctx, `UPDATE content_provenance SET validated_json='{"translation_basis":"broken"}' WHERE id=?`, fact.ID); err != nil {
					t.Fatal(err)
				}
				restoreQuery, restoreValue = "UPDATE content_provenance SET validated_json=? WHERE page_id=?", fact.ValidatedJson
				cause = "json: cannot unmarshal"
			case "snapshot":
				if _, err = db.ExecContext(ctx, `UPDATE page_versions SET snapshot_json='{invalid' WHERE id=?`, version.ID); err != nil {
					t.Fatal(err)
				}
				restoreQuery, restoreValue = "UPDATE page_versions SET snapshot_json=? WHERE page_id=?", version.SnapshotJson
				cause = "invalid character"
			}
			logs := captureLogs(t)
			response := get()
			wantStatus := http.StatusOK
			if tc.message != "" {
				wantStatus = http.StatusInternalServerError
			} else if tc.missing == "editor" {
				wantStatus = http.StatusUnauthorized
			} else if tc.missing != "" {
				wantStatus = http.StatusNotFound
			}
			if response.Code != wantStatus {
				t.Errorf("status=%d, want %d: %s", response.Code, wantStatus, response.Body.String())
			}
			if tc.message != "" {
				fields := []string{"level=ERROR", tc.message, "error=", cause, fmt.Sprintf("page_id=%d", pageID)}
				if tc.query == "GetUserByID" {
					fields = append(fields, fmt.Sprintf("user_id=%d", user.ID))
				}
				if !tc.edit {
					fields = append(fields, fmt.Sprintf("version_id=%d", versionID))
				}
				if tc.corrupt == "translation access" {
					fields = append(fields, fmt.Sprintf("load translation AI access for page %d", restoreID))
					if strings.Count(logs.String(), tc.message) != 1 {
						t.Errorf("translation lookup was not logged exactly once: %s", logs.String())
					}
				}
				for _, field := range fields {
					if !strings.Contains(logs.String(), field) {
						t.Errorf("failure log lacks %q: %s", field, logs.String())
					}
				}
				if strings.Contains(response.Body.String(), cause) {
					t.Errorf("internal cause leaked to response: %s", response.Body.String())
				}
				// Clearing a read fault or repairing recorded data restores the page.
				h.queries = q
				if restoreQuery != "" {
					if _, err = db.ExecContext(ctx, restoreQuery, restoreValue, restoreID); err != nil {
						t.Fatal(err)
					}
				}
				logs.Reset()
				response = get()
				wantStatus = http.StatusOK
			}
			if response.Code != wantStatus {
				t.Errorf("healthy status=%d, want %d", response.Code, wantStatus)
			}
			if wantStatus == http.StatusOK && (!strings.Contains(response.Body.String(), page.Title) || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html")) {
				t.Error("healthy response did not render the page")
			}
			if strings.Contains(logs.String(), "level=ERROR") {
				t.Errorf("healthy or missing-record response logged a server error: %s", logs.String())
			}
			if tc.corrupt == "translation access" && !strings.Contains(response.Body.String(), "<strong>ru</strong>: "+i18n.T("en", "review.ai_draft")) {
				t.Error("repaired linked translation did not render its AI draft status")
			}
			testutil.AssertPageRevision(t, q, page.ID, page.Revision)
		})
	}
}

// TestPageReviewServerErrorsUseLogging keeps review-load errors on the shared
// logging helpers. Direct account refusals require an explicit missing-row
// guard; logging helpers must carry the cause and record IDs.
func TestPageReviewServerErrorsUseLogging(t *testing.T) {
	for file, function := range map[string]string{"pages.go": "EditForm", "pages_review.go": "RevisionReview", "pages_write.go": "pageConflict"} {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, declaration := range parsed.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Name.Name != function {
				continue
			}
			found = true
			missingRowCalls := make(map[*ast.CallExpr]bool)
			for node := range ast.Preorder(fn.Body) {
				branch, ok := node.(*ast.IfStmt)
				if !ok {
					continue
				}
				condition, ok := branch.Cond.(*ast.CallExpr)
				if !ok || len(condition.Args) != 2 {
					continue
				}
				predicate, ok := condition.Fun.(*ast.SelectorExpr)
				if !ok || predicate.Sel.Name != "Is" {
					continue
				}
				pkg, ok := predicate.X.(*ast.Ident)
				if !ok || pkg.Name != "errors" {
					continue
				}
				missing, ok := condition.Args[1].(*ast.SelectorExpr)
				if !ok || missing.Sel.Name != "ErrNoRows" {
					continue
				}
				pkg, ok = missing.X.(*ast.Ident)
				if !ok || pkg.Name != "sql" {
					continue
				}
				for _, statement := range branch.Body.List {
					if expression, ok := statement.(*ast.ExprStmt); ok {
						if call, ok := expression.X.(*ast.CallExpr); ok {
							missingRowCalls[call] = true
						}
					}
				}
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if selected, ok := call.Fun.(*ast.SelectorExpr); ok && selected.Sel.Name == "Error" && len(call.Args) == 3 {
					if pkg, ok := selected.X.(*ast.Ident); ok && pkg.Name == "http" {
						status, ok := call.Args[2].(*ast.SelectorExpr)
						if function == "RevisionReview" || !ok || status.Sel.Name != "StatusUnauthorized" || !missingRowCalls[call] {
							t.Errorf("%s: use logging helpers for faults and guard account refusals with errors.Is(err, sql.ErrNoRows)", function)
						}
					}
				}
				name, ok := call.Fun.(*ast.Ident)
				if !ok || (name.Name != "logAndHTTPError" && name.Name != "logAndInternalError") {
					return true
				}
				keys := make(map[string]bool)
				firstKey := 4
				if name.Name == "logAndInternalError" {
					firstKey = 2
				}
				for i := firstKey; i+1 < len(call.Args); i += 2 {
					if literal, ok := call.Args[i].(*ast.BasicLit); ok && literal.Kind == token.STRING {
						keys[literal.Value] = true
					}
				}
				for _, key := range []string{"error", "page_id", "version_id"} {
					if key == "version_id" && function != "RevisionReview" {
						continue
					}
					if !keys[strconv.Quote(key)] {
						t.Errorf("%s: %s must include %s", function, name.Name, key)
					}
				}
				return true
			})
		}
		if !found {
			t.Fatalf("%s: missing %s", file, function)
		}
	}
}
