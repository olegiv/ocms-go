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
	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/webhook"
)

func TestTranslationReviewHTTPRequiresExplicitCurrentSameOriginReview(t *testing.T) {
	for _, scenario := range []string{"accepted", "storage fault", "no acknowledgment", "duplicate acknowledgment", "missing revision", "duplicate revision", "query selector", "oversized", "JSON", "cross site", "changed source", "demoted account", "historical form"} {
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
			caches := cache.NewManager(q)
			if _, err = caches.Sitemap.Get(ctx, "https://example.test"); err != nil || !caches.Sitemap.IsCached() {
				t.Fatalf("warm sitemap cache: %v", err)
			}
			h.SetCacheManager(caches)
			hook, err := q.CreateWebhook(ctx, store.CreateWebhookParams{Name: "Review webhook", Url: "http://127.0.0.1:1/",
				Secret: "unused", Events: `["page.updated"]`, IsActive: true, Headers: "{}", CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			dispatcher := webhook.NewDispatcher(db, nil, webhook.Config{Workers: 1})
			// Exercise queueing without running outbound delivery workers.
			workerCtx, cancelWorkers := context.WithCancel(ctx)
			cancelWorkers()
			dispatcher.Start(workerCtx)
			t.Cleanup(dispatcher.Stop)
			h.SetDispatcher(dispatcher)
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
			case "storage fault":
				if _, err = db.Exec(`CREATE TRIGGER fail_translation_evidence BEFORE INSERT ON content_provenance
					BEGIN SELECT RAISE(ABORT, 'translation fixture fault'); END`); err != nil {
					t.Fatal(err)
				}
				want = http.StatusInternalServerError
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
			if scenario == "storage fault" && (!strings.Contains(w.Body.String(), i18n.T("en", "review.translation_failed")) ||
				strings.Contains(w.Body.String(), "translation fixture fault")) {
				t.Fatalf("storage fault returned an incorrect notice: %s", w.Body.String())
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
				var deliveries int
				if err = db.QueryRow("SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id=?", hook.ID).Scan(&deliveries); err != nil || deliveries != 0 || !caches.Sitemap.IsCached() {
					t.Fatal("refused review invalidated cache or dispatched a webhook")
				}
				return
			}
			if after.Revision != target.Revision+1 || after.Body != target.Body || after.Status != "draft" || after.ScheduledAt.Valid {
				t.Fatal("review failed to add only a review revision")
			}
			if w.Header().Get("Location") != fmt.Sprintf("/admin/pages/%d", target.ID) {
				t.Fatal("wrong redirect")
			}
			var events int
			if err = db.QueryRow(`SELECT COUNT(*) FROM events WHERE message='Translation reviewed' AND user_id=?
				AND json_extract(metadata, '$.page_id')=? AND json_extract(metadata, '$.revision')=?
				AND json_extract(metadata, '$.source_page_id')=? AND json_extract(metadata, '$.source_revision')=?`,
				user.ID, target.ID, after.Revision, source.ID, source.Revision).Scan(&events); err != nil || events != 1 {
				t.Fatalf("translation review logged %d matching events: %v", events, err)
			}
			if caches.Sitemap.IsCached() {
				t.Fatal("translation review left the sitemap cached")
			}
			var deliveries int
			var eventType, payload string
			if err = db.QueryRow("SELECT COUNT(*), COALESCE(MAX(event), ''), COALESCE(MAX(payload), '') FROM webhook_deliveries WHERE webhook_id=?", hook.ID).
				Scan(&deliveries, &eventType, &payload); err != nil || deliveries != 1 || eventType != model.EventPageUpdated {
				t.Fatalf("translation review dispatched %d %q deliveries: %v", deliveries, eventType, err)
			}
			var dispatched struct{ Data webhook.PageEventData }
			if err = json.Unmarshal([]byte(payload), &dispatched); err != nil || dispatched.Data.ID != target.ID || dispatched.Data.AuthorEmail != user.Email {
				t.Fatalf("wrong page.updated data: %+v %v", dispatched.Data, err)
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
