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
		{name: "editor evidence", edit: true, query: "GetContentProvenance", message: "failed to load editorial review"},
		{name: "editor translation lookup", edit: true, query: "GetLatestTranslationProvenance", message: "failed to load editorial review"},
		{name: "editor corrupt evidence", edit: true, corrupt: "evidence", message: "failed to load editorial review"},
		{name: "revision page", query: "GetPageByID", message: "failed to load page for revision review"},
		{name: "revision version", query: "GetPageVersion", message: "failed to load recorded revision"},
		{name: "revision predecessor", query: "GetPreviousPageVersion", message: "failed to load previous revision"},
		{name: "revision corrupt snapshot", corrupt: "snapshot", message: "failed to compare recorded revision"},
		{name: "revision evidence", query: "GetContentProvenance", message: "failed to load revision evidence"},
		{name: "revision translation lookup", query: "GetLatestTranslationProvenance", message: "failed to load revision evidence"},
		{name: "revision corrupt evidence", corrupt: "evidence", message: "failed to load revision evidence"},
		{name: "editor without AI access", edit: true},
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
			create := func(slug string) store.Page {
				t.Helper()
				now := time.Now().UTC()
				page, insertErr := pagewrite.Insert(ctx, q, provenance.Principal{UserID: user.ID, Source: "human"}, store.CreatePageParams{
					Title: "Review page", Slug: slug, Body: "<p>Recorded content</p>", Status: "draft", AuthorID: user.ID,
					LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now,
				}, nil)
				if insertErr != nil {
					t.Fatal(insertErr)
				}
				return page
			}
			page := create("review-page")
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
				pageID = create("another-page").ID
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
				h.queries = store.New(&conflictQueryFault{DB: db, query: "-- name: " + tc.query + " :"})
			}
			var restoreQuery string
			var restoreValue any
			cause := "unavailable_conflict_data"
			switch tc.corrupt {
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
			} else if tc.missing != "" {
				wantStatus = http.StatusNotFound
			}
			if response.Code != wantStatus {
				t.Errorf("status=%d, want %d: %s", response.Code, wantStatus, response.Body.String())
			}
			if tc.message != "" {
				fields := []string{"level=ERROR", tc.message, "error=", cause, fmt.Sprintf("page_id=%d", pageID)}
				if !tc.edit {
					fields = append(fields, fmt.Sprintf("version_id=%d", versionID))
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
					if _, err = db.ExecContext(ctx, restoreQuery, restoreValue, page.ID); err != nil {
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
			testutil.AssertPageRevision(t, q, page.ID, page.Revision)
		})
	}
}

// TestPageReviewServerErrorsUseLogging keeps review-load errors on the shared
// logging helpers. The editor's existing account refusal is the only allowed
// direct http.Error call; logging helpers must carry the cause and record IDs.
func TestPageReviewServerErrorsUseLogging(t *testing.T) {
	for file, function := range map[string]string{"pages.go": "EditForm", "pages_review.go": "RevisionReview"} {
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
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if selected, ok := call.Fun.(*ast.SelectorExpr); ok && selected.Sel.Name == "Error" && len(call.Args) == 3 {
					if pkg, ok := selected.X.(*ast.Ident); ok && pkg.Name == "http" {
						status, ok := call.Args[2].(*ast.SelectorExpr)
						if function != "EditForm" || !ok || status.Sel.Name != "StatusUnauthorized" {
							t.Errorf("%s: use a logging helper for review-load failures", function)
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
					if key == "version_id" && function == "EditForm" {
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
