// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
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

func TestTranslationReviewHTTPRequiresExplicitCurrentSameOriginReview(t *testing.T) {
	for _, scenario := range []string{"accepted", "no acknowledgment", "duplicate acknowledgment", "missing revision", "duplicate revision", "query selector", "oversized", "JSON", "cross site", "changed source", "demoted account", "historical form"} {
		t.Run(scenario, func(t *testing.T) {
			db, cleanup := testutil.TestDB(t)
			t.Cleanup(cleanup)
			q, ctx := store.New(db), context.Background()
			sm := testSessionManager(t)
			if err := i18n.Init(nil); err != nil {
				t.Fatal(err)
			}
			renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			user, err := q.CreateUser(ctx, store.CreateUserParams{Email: "reviewer@example.test", Name: "Reviewer", Role: "editor", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			lang, err := q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			create := func(language, slug string) store.Page {
				t.Helper()
				page, e := pagewrite.Insert(ctx, q, provenance.Principal{UserID: user.ID, Source: "human", Transport: "admin"}, store.CreatePageParams{Title: slug, Slug: slug, Body: "<p>Reviewed content</p>", Status: "draft", AuthorID: user.ID, LanguageCode: language, PageType: "post", CreatedAt: now, UpdatedAt: now}, nil)
				if e != nil {
					t.Fatal(e)
				}
				return page
			}
			source, target := create("en", "source"), create("ru", "translation")
			if _, err = q.CreateTranslation(ctx, store.CreateTranslationParams{EntityType: "page", EntityID: source.ID, LanguageID: lang.ID, TranslationID: target.ID, CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			validation, err := json.Marshal(review.Validation{TranslationBasis: &review.Basis{SourcePageID: source.ID, SourceRevision: source.Revision, Fingerprint: "previous-source"}})
			if err != nil {
				t.Fatal(err)
			}
			// Initial imported fixture evidence, before exercising the real endpoint.
			if _, err = db.Exec("UPDATE content_provenance SET validated_json=? WHERE page_id=?", string(validation), target.ID); err != nil {
				t.Fatal(err)
			}
			h := NewPagesHandler(db, renderer, sm)
			router := chi.NewRouter()
			router.Use(sm.LoadAndSave, middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
			router.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, withUser(r, user)) })
			})
			router.Post("/admin/pages/{id}/translation-review", h.ReviewTranslation)
			form := url.Values{"acknowledge": {"on"}, "base_revision": {fmt.Sprint(target.Revision)}, "source_page_id": {fmt.Sprint(source.ID)}, "source_revision": {fmt.Sprint(source.Revision)}}
			path := fmt.Sprintf("https://example.test/admin/pages/%d/translation-review", target.ID)
			want := http.StatusSeeOther
			contentType, fetchSite := "application/x-www-form-urlencoded", "same-origin"
			switch scenario {
			case "no acknowledgment":
				form.Del("acknowledge")
				want = http.StatusForbidden
			case "duplicate acknowledgment":
				form.Add("acknowledge", "on")
				want = http.StatusForbidden
			case "missing revision":
				form.Del("source_revision")
				want = http.StatusBadRequest
			case "duplicate revision":
				form.Add("source_revision", "1")
				want = http.StatusBadRequest
			case "query selector":
				path += "?source_revision=1"
				want = http.StatusBadRequest
			case "oversized":
				form.Set("padding", strings.Repeat("x", 5000))
				want = http.StatusBadRequest
			case "JSON":
				contentType = "application/json"
				want = http.StatusBadRequest
			case "cross site":
				fetchSite = "cross-site"
				want = http.StatusForbidden
			case "changed source":
				form.Set("source_revision", fmt.Sprint(source.Revision+1))
				want = http.StatusConflict
			case "demoted account":
				if _, err = db.Exec("UPDATE users SET role='public' WHERE id=?", user.ID); err != nil {
					t.Fatal(err)
				}
				want = http.StatusForbidden
			case "historical form":
				form.Set("base_revision", fmt.Sprint(target.Revision+1))
				want = http.StatusConflict
			}
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", contentType)
			request.Header.Set("Sec-Fetch-Site", fetchSite)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, request)
			if w.Code != want {
				t.Fatalf("status=%d, want %d: %s", w.Code, want, w.Body.String())
			}
			after, err := q.GetPageByID(ctx, target.ID)
			if err != nil {
				t.Fatal(err)
			}
			if want != http.StatusSeeOther {
				if !reflect.DeepEqual(after, target) {
					t.Fatal("refused request changed draft")
				}
				var events int
				if err = db.QueryRow("SELECT COUNT(*) FROM events WHERE message='Translation reviewed'").Scan(&events); err != nil || events != 0 {
					t.Fatalf("refused review logged success: %d %v", events, err)
				}
				return
			}
			if after.Revision != target.Revision+1 || after.Body != target.Body || after.Status != "draft" || after.ScheduledAt.Valid {
				t.Fatal("review failed to add only a review revision")
			}
			if w.Header().Get("Location") != fmt.Sprintf("/admin/pages/%d", target.ID) {
				t.Fatal("wrong redirect")
			}
			fact, err := q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: target.ID, Revision: after.Revision})
			if err != nil || fact.UserID.Int64 != user.ID || fact.Transport != "translation_review" {
				t.Fatalf("missing reviewer: %+v %v", fact, err)
			}
			testutil.AssertPageRevision(t, q, target.ID, after.Revision)
			// An already-submitted form must not create another revision.
			replay := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
			replay.Header.Set("Content-Type", contentType)
			replay.Header.Set("Sec-Fetch-Site", fetchSite)
			w = httptest.NewRecorder()
			router.ServeHTTP(w, replay)
			if w.Code != http.StatusConflict {
				t.Fatalf("form replay=%d", w.Code)
			}
			testutil.AssertPageRevision(t, q, target.ID, after.Revision)
		})
	}
}
