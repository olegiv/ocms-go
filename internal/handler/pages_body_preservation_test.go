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

func TestPageFormUpdatePreservesBodyAcrossFormNewlines(t *testing.T) {
	const body = "<h2>Reviewed</h2>\n<p>Keep this paragraph.</p>\n"
	for _, tc := range []struct {
		name, stored, submitted, wantBody string
		changeSummary, staleRevision      bool
		trustedHTML                       bool
		wantStale                         bool
	}{
		{name: "publish LF body", stored: body, submitted: strings.ReplaceAll(body, "\n", "\r\n"), wantBody: body},
		{name: "publish trusted LF body", stored: body, submitted: strings.ReplaceAll(body, "\n", "\r\n"), wantBody: body, trustedHTML: true},
		{name: "preserve existing CRLF", stored: strings.ReplaceAll(body, "\n", "\r\n"), submitted: body, wantBody: strings.ReplaceAll(body, "\n", "\r\n")},
		{name: "real edit", stored: body, submitted: body + "<p>Actual edit.</p>", wantBody: body + "<p>Actual edit.</p>", wantStale: true},
		{name: "translated metadata edit", stored: body, submitted: strings.ReplaceAll(body, "\n", "\r\n"), wantBody: body, changeSummary: true, wantStale: true},
		{name: "body policy still applies", stored: body + "<script>alert(1)</script>", submitted: strings.ReplaceAll(body, "\n", "\r\n") + "<script>alert(1)</script>", wantBody: body, wantStale: true},
		{name: "stale publication refused", stored: body, submitted: strings.ReplaceAll(body, "\n", "\r\n"), wantBody: body, staleRevision: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			user, err := q.CreateUser(ctx, store.CreateUserParams{Email: "body-reviewer@example.test", Name: "Reviewer", Role: "editor", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			lang, err := q.CreateLanguage(ctx, store.CreateLanguageParams{Code: "ru", Name: "Russian", NativeName: "Русский", IsActive: true, Direction: "ltr", CreatedAt: now, UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			create := func(language, slug, html string) store.Page {
				t.Helper()
				page, e := pagewrite.Insert(ctx, q, provenance.Principal{UserID: user.ID, Source: "human", Transport: "admin"}, store.CreatePageParams{Title: slug, Slug: slug, Body: html, Status: "draft", AuthorID: user.ID, LanguageCode: language, PageType: "post", CreatedAt: now, UpdatedAt: now}, nil)
				if e != nil {
					t.Fatal(e)
				}
				return page
			}
			source, target := create("en", "source", tc.stored), create("ru", "translation", "<p>Перевод</p>")
			if _, err = q.CreateTranslation(ctx, store.CreateTranslationParams{EntityType: "page", EntityID: source.ID, LanguageID: lang.ID, TranslationID: target.ID, CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			basis, err := json.Marshal(review.Validation{TranslationBasis: &review.Basis{SourcePageID: source.ID, SourceRevision: source.Revision, Fingerprint: provenance.PageFingerprint(source)}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("UPDATE content_provenance SET validated_json=? WHERE page_id=?", string(basis), target.ID); err != nil {
				t.Fatal(err)
			}
			h := NewPagesHandler(db, renderer, sm)
			h.sanitizePageHTML = !tc.trustedHTML
			router := chi.NewRouter()
			router.Use(sm.LoadAndSave, middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
			router.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, withUser(r, user)) })
			})
			router.Post("/admin/pages/{id}", h.Update)
			revision := source.Revision
			if tc.staleRevision {
				revision++
			}
			form := url.Values{"title": {source.Title}, "slug": {source.Slug}, "body": {tc.submitted}, "status": {"published"}, "page_type": {"post"}, "base_revision": {fmt.Sprint(revision)}}
			if tc.changeSummary {
				form.Set("summary", "Reviewed summary change")
			}
			request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("https://example.test/admin/pages/%d", source.ID), strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, request)
			wantCode := http.StatusSeeOther
			if tc.staleRevision {
				wantCode = http.StatusConflict
			}
			if w.Code != wantCode {
				t.Fatalf("status=%d, want %d: %s", w.Code, wantCode, w.Body.String())
			}
			after, err := q.GetPageByID(ctx, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Body != tc.wantBody {
				t.Fatalf("body=%q, want %q", after.Body, tc.wantBody)
			}
			if tc.staleRevision {
				if after.Status != "draft" || after.Revision != source.Revision {
					t.Fatal("stale submission changed the page")
				}
			} else if after.Status != "published" || !after.PublishedAt.Valid || after.Revision != source.Revision+1 {
				t.Fatal("publication did not create the expected revision")
			}
			translations, err := review.Translations(ctx, q, after)
			if err != nil {
				t.Fatal(err)
			}
			if len(translations) != 2 {
				t.Fatalf("translation component has %d pages, want 2", len(translations))
			}
			for _, translation := range translations {
				if translation.PageID == target.ID && (translation.Freshness == "stale") != tc.wantStale {
					t.Fatalf("translation freshness=%s, want stale=%v", translation.Freshness, tc.wantStale)
				}
			}
		})
	}
}
