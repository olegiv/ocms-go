// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestReviewMissingRowsDistinguishFaultsFromRefusals(t *testing.T) {
	for _, operation := range []string{"claim", "translation"} {
		for _, fault := range []string{"evidence", "snapshot"} {
			if operation == "claim" && fault == "snapshot" {
				continue
			}
			t.Run(operation+"/"+fault, func(t *testing.T) {
				db, cleanup := testutil.TestDB(t)
				t.Cleanup(cleanup)
				q, ctx := store.New(db), t.Context()
				sm := testSessionManager(t)
				if err := i18n.Init(nil); err != nil {
					t.Fatal(err)
				}
				renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
				if err != nil {
					t.Fatal(err)
				}
				user := createTestAdminUser(t, db)
				now := time.Now()
				language, err := q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now})
				if err != nil {
					t.Fatal(err)
				}
				principal := provenance.Principal{UserID: user.ID, Source: "human", Transport: "test"}
				create := func(code, slug string) store.Page {
					t.Helper()
					page, createErr := pagewrite.Insert(ctx, q, principal, store.CreatePageParams{Title: "SF11_PRIVATE_TITLE", Slug: slug, Body: "<p>SF11_PRIVATE_BODY</p>", Status: "draft", AuthorID: user.ID, LanguageCode: code, PageType: "post", CreatedAt: now, UpdatedAt: now}, nil)
					if createErr != nil {
						t.Fatal(createErr)
					}
					return page
				}
				source, target := create("en", "sf11-source"), create("ru", "sf11-target")
				if _, err = q.CreateTranslation(ctx, store.CreateTranslationParams{EntityType: "page", EntityID: source.ID, TranslationID: target.ID, LanguageID: language.ID, CreatedAt: now}); err != nil {
					t.Fatal(err)
				}
				validation, err := json.Marshal(review.Validation{TranslationBasis: &review.Basis{SourcePageID: source.ID, SourceRevision: source.Revision, Fingerprint: "previous-source"}})
				if err != nil {
					t.Fatal(err)
				}
				reported := `{"agent":{"claims_to_verify":["SF11_PRIVATE_CLAIM"]}}`
				// Keep an older translation basis when the current evidence row
				// is hidden: source resolution must reach the later failing read.
				if _, err = db.ExecContext(ctx, "UPDATE content_provenance SET validated_json=?, reported_json=? WHERE page_id=?", string(validation), reported, target.ID); err != nil {
					t.Fatal(err)
				}
				work, err := pagewrite.InQueriesTx(ctx, q, principal, func(work *pagewrite.Tx) error {
					if _, trackErr := work.Track(ctx, target.ID, &target.Revision, true); trackErr != nil {
						return trackErr
					}
					work.SetPageEvidence(target.ID, provenance.Evidence{Validated: validation, Reported: json.RawMessage(reported)})
					_, writeErr := work.Q.DBHandle().ExecContext(ctx, "UPDATE pages SET summary='Second recorded revision' WHERE id=?", target.ID)
					return writeErr
				})
				if err != nil {
					t.Fatal(err)
				}
				target = work.Pages[target.ID]
				h := NewPagesHandler(db, renderer, sm)
				router := chi.NewRouter()
				router.Use(sm.LoadAndSave, middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
				router.Post("/admin/pages/{id}/translation-review", h.ReviewTranslation)
				router.Post("/admin/pages/{id}/claims/{index}", h.CheckClaim)
				post := func(pageID, revision, selector, reviewerID int64) *httptest.ResponseRecorder {
					form := url.Values{"base_revision": {fmt.Sprint(revision)}, "checked": {"on"}, "acknowledge": {"on"}, "source_page_id": {fmt.Sprint(selector)}, "source_revision": {fmt.Sprint(source.Revision)}}
					path := fmt.Sprintf("https://example.test/admin/pages/%d/translation-review", pageID)
					if operation == "claim" {
						path = fmt.Sprintf("https://example.test/admin/pages/%d/claims/%d", pageID, selector)
					}
					req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					req.Header.Set("Sec-Fetch-Site", "same-origin")
					currentUser := user
					currentUser.ID = reviewerID
					response := httptest.NewRecorder()
					router.ServeHTTP(response, withUser(req, currentUser))
					return response
				}
				selector := source.ID
				message, key := "failed to review translation", "review.translation_failed"
				if operation == "claim" {
					selector, message, key = 0, "failed to record claim check", "review.check_failed"
				}
				var breakSQL, repairSQL string
				if fault == "evidence" {
					breakSQL = fmt.Sprintf("UPDATE content_provenance SET revision=revision+1000 WHERE page_id=%d AND revision=%d", target.ID, target.Revision)
					repairSQL = fmt.Sprintf("UPDATE content_provenance SET revision=revision-1000 WHERE page_id=%d AND revision=%d", target.ID, target.Revision+1000)
				} else {
					// INSERT RETURNING receives no row inside Finish, after the
					// revision advance. The whole write must roll back.
					breakSQL = fmt.Sprintf("CREATE TRIGGER sf11_skip_revision BEFORE INSERT ON page_versions WHEN NEW.page_id=%d BEGIN SELECT RAISE(IGNORE); END", target.ID)
					repairSQL = "DROP TRIGGER sf11_skip_revision"
				}
				before := reviewRowCounts(t, db, target.ID)
				if _, err = db.ExecContext(ctx, breakSQL); err != nil {
					t.Fatal(err)
				}
				logs := captureLogs(t)
				response := post(target.ID, target.Revision, selector, user.ID)
				if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), i18n.T("en", key)) {
					t.Errorf("fault response=%d %s, want translated 500", response.Code, response.Body.String())
				}
				diagnostic := logs.String()
				for _, required := range []string{"level=ERROR", message, sql.ErrNoRows.Error(), fmt.Sprintf("page_id=%d", target.ID), fmt.Sprintf("user_id=%d", user.ID)} {
					if !strings.Contains(diagnostic, required) {
						t.Errorf("fault diagnostic lacks %q: %s", required, diagnostic)
					}
				}
				if strings.Count(diagnostic, message) != 1 {
					t.Errorf("fault diagnostic count: %s", diagnostic)
				}
				after, err := q.GetPageByID(ctx, target.ID)
				if err != nil || !reflect.DeepEqual(after, target) || reviewRowCounts(t, db, target.ID) != before {
					t.Fatalf("failed operation changed page/revisions/claims: %+v %v", after, err)
				}
				if _, err = db.ExecContext(ctx, repairSQL); err != nil {
					t.Fatal(err)
				}
				for _, private := range []string{"SF11_PRIVATE_TITLE", "SF11_PRIVATE_BODY", "SF11_PRIVATE_CLAIM"} {
					if strings.Contains(diagnostic+response.Body.String(), private) {
						t.Errorf("private review input exposed: %s", private)
					}
				}
				if strings.Contains(response.Body.String(), sql.ErrNoRows.Error()) {
					t.Error("internal cause exposed to editor")
				}
				logs.Reset()
				if response = post(target.ID, target.Revision, selector, user.ID); response.Code != http.StatusSeeOther {
					t.Fatalf("recovery=%d: %s", response.Code, response.Body.String())
				}
				after, err = q.GetPageByID(ctx, target.ID)
				if err != nil {
					t.Fatal(err)
				}
				wantCounts := before
				wantReplay := http.StatusSeeOther
				if operation == "translation" {
					wantCounts[0]++
					wantCounts[1]++
					wantCounts[3]++
					wantReplay = http.StatusConflict
					if after.Revision != target.Revision+1 {
						t.Fatal("review did not add exactly one revision")
					}
				} else {
					wantCounts[2]++
					if !reflect.DeepEqual(after, target) {
						t.Fatal("claim check changed content or revision")
					}
				}
				if response = post(target.ID, target.Revision, selector, user.ID); response.Code != wantReplay || reviewRowCounts(t, db, target.ID) != wantCounts {
					t.Fatalf("replay=%d, counts=%v want=%v", response.Code, reviewRowCounts(t, db, target.ID), wantCounts)
				}
				testutil.AssertPageRevision(t, q, target.ID, after.Revision)
				for _, control := range []struct {
					name                                   string
					pageID, revision, selector, reviewerID int64
					status                                 int
				}{
					{"missing target", target.ID + 10000, after.Revision, selector, user.ID, http.StatusConflict},
					{"stale form", target.ID, after.Revision + 10000, selector, user.ID, http.StatusConflict},
					{"unavailable selection", target.ID, after.Revision, 10000, user.ID, http.StatusConflict},
					{"missing reviewer", target.ID, after.Revision, selector, user.ID + 10000, http.StatusForbidden},
				} {
					if response = post(control.pageID, control.revision, control.selector, control.reviewerID); response.Code != control.status {
						t.Errorf("%s=%d, want %d: %s", control.name, response.Code, control.status, response.Body.String())
					}
				}
				if strings.Contains(logs.String(), "level=ERROR") || reviewRowCounts(t, db, target.ID) != wantCounts {
					t.Errorf("recovery/refusal added a fault or mutation: %s", logs.String())
				}
			})
		}
	}
}

func reviewRowCounts(t *testing.T, db *sql.DB, pageID int64) [4]int {
	t.Helper()
	var counts [4]int
	err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM page_versions WHERE page_id=?1), (SELECT COUNT(*) FROM content_provenance WHERE page_id=?1), (SELECT COUNT(*) FROM claims_checks), (SELECT COUNT(*) FROM events WHERE message='Translation reviewed')`, pageID).Scan(&counts[0], &counts[1], &counts[2], &counts[3])
	if err != nil {
		t.Fatal(err)
	}
	return counts
}
