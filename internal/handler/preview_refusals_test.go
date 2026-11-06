// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestAdminPreviewRefusalsExplainRecovery(t *testing.T) {
	for _, lang := range []string{"en", "ru"} {
		for _, scenario := range []string{"invalid URL", "unset URL", "active", "hourly", "unavailable"} {
			t.Run(lang+"/"+scenario, func(t *testing.T) {
				db, cleanup := testutil.TestDB(t)
				t.Cleanup(cleanup)
				q, ctx, now := store.New(db), t.Context(), time.Now().UTC()
				user := createTestAdminUser(t, db)
				page, err := pagewrite.Insert(ctx, q, provenance.Principal{UserID: user.ID, Source: "human"}, store.CreatePageParams{Title: "SF13_PRIVATE_TITLE", Slug: "preview-refusal", Body: "<p>SF13_PRIVATE_BODY</p>", Status: "draft", AuthorID: user.ID, LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now}, func(work *pagewrite.Tx, created store.Page) error {
					return work.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: created.ID, AiEditable: true, SetBy: user.ID, SetAt: now})
				})
				if err != nil {
					t.Fatal(err)
				}
				configure := func(origin string) {
					t.Helper()
					if _, configErr := q.UpsertConfig(ctx, store.UpsertConfigParams{Key: model.ConfigKeySiteURL, Value: origin, Type: "string", UpdatedAt: now}); configErr != nil {
						t.Fatal(configErr)
					}
				}
				configure("https://cms.test")
				wantCode, messageKey, count := http.StatusTooManyRequests, "preview.active_limit", 0
				switch scenario {
				case "invalid URL":
					configure("http://SF13_PRIVATE_HOST.test")
					wantCode, messageKey = http.StatusServiceUnavailable, "preview.site_url_required"
				case "unset URL":
					if err = q.DeleteConfig(ctx, model.ConfigKeySiteURL); err != nil {
						t.Fatal(err)
					}
					wantCode, messageKey = http.StatusServiceUnavailable, "preview.site_url_required"
				case "active":
					count = preview.MaxActiveLinks
				case "hourly":
					count, messageKey = preview.MaxIssuedPerHour, "preview.rate_limit"
				case "unavailable":
					wantCode, messageKey = http.StatusConflict, "preview.unavailable"
				}
				for i := range count {
					if _, err = q.CreatePreviewLink(ctx, store.CreatePreviewLinkParams{TokenHash: fmt.Sprintf("%064d", i), PageID: page.ID, Revision: page.Revision, CreatedBy: user.ID, SessionVersion: user.SessionVersion, CreatedAt: now.Add(-20 * time.Minute).Unix(), ExpiresAt: now.Add(time.Hour).Unix()}); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "hourly" {
					if _, err = db.ExecContext(ctx, "UPDATE preview_links SET revoked_at=1"); err != nil {
						t.Fatal(err)
					}
				}
				if err = i18n.Init(nil); err != nil {
					t.Fatal(err)
				}
				sm := testSessionManager(t)
				renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
				if err != nil {
					t.Fatal(err)
				}
				h := NewPagesHandler(db, renderer, sm)
				router := chi.NewRouter()
				router.Use(sm.LoadAndSave, middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
				router.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						sm.Put(r.Context(), render.SessionKeyAdminLang, lang)
						next.ServeHTTP(w, r)
					})
				})
				router.Post("/admin/pages/{id}/preview-links", h.CreatePreviewLink)
				post := func(revision int64) *httptest.ResponseRecorder {
					form := url.Values{"acknowledge": {"on"}, "revision": {fmt.Sprint(revision)}, "base_revision": {fmt.Sprint(page.Revision)}}
					req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("https://cms.test/admin/pages/%d/preview-links", page.ID), strings.NewReader(form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					req.Header.Set("Sec-Fetch-Site", "same-origin")
					response := httptest.NewRecorder()
					router.ServeHTTP(response, withUser(req, user))
					return response
				}
				logs := captureLogs(t)
				revision := page.Revision
				if scenario == "unavailable" {
					revision = 999
				}
				response := post(revision)
				var args []any
				if scenario == "active" {
					args = []any{preview.MaxActiveLinks}
				} else if scenario == "hourly" {
					delay, parseErr := strconv.ParseInt(response.Header().Get("Retry-After"), 10, 64)
					if parseErr != nil || delay < 2395 || delay > 2401 {
						t.Fatalf("hourly retry delay=%d error=%v", delay, parseErr)
					}
					args = []any{preview.MaxIssuedPerHour, delay}
				}
				message := i18n.T(lang, messageKey, args...)
				if message == messageKey || response.Code != wantCode || strings.TrimSpace(response.Body.String()) != message {
					t.Errorf("refusal: status=%d body=%q, want %d %q", response.Code, response.Body.String(), wantCode, message)
				}
				if scenario != "hourly" && response.Header().Get("Retry-After") != "" {
					t.Error("non-hourly refusal advertised an automatic retry")
				}
				if wantCode == http.StatusServiceUnavailable {
					if strings.Count(logs.String(), "invalid site URL for a preview link") != 1 {
						t.Errorf("missing/duplicate configuration diagnostic: %s", logs.String())
					}
					for _, field := range []string{"level=ERROR", "error=", "preview_site_url_required", fmt.Sprintf("page_id=%d", page.ID), fmt.Sprintf("user_id=%d", user.ID)} {
						if !strings.Contains(logs.String(), field) {
							t.Errorf("configuration diagnostic lacks %s", field)
						}
					}
				} else if strings.Contains(logs.String(), "level=ERROR") {
					t.Errorf("normal refusal logged a server fault: %s", logs.String())
				}
				if strings.Contains(logs.String()+response.Body.String(), "SF13_PRIVATE_") {
					t.Error("refusal exposed private configuration/content")
				}
				var links, events int
				if err = db.QueryRow("SELECT (SELECT COUNT(*) FROM preview_links), (SELECT COUNT(*) FROM events WHERE message='Preview share link created')").Scan(&links, &events); err != nil || links != count || events != 0 {
					t.Fatalf("refusal mutated links/events: %d/%d %v", links, events, err)
				}
				configure("https://cms.test")
				if scenario == "active" || scenario == "hourly" {
					if _, err = db.ExecContext(ctx, "UPDATE preview_links SET revoked_at=1, created_at=?", now.Add(-2*time.Hour).Unix()); err != nil {
						t.Fatal(err)
					}
				}
				logs.Reset()
				if repaired := post(page.Revision); repaired.Code != http.StatusOK {
					t.Fatalf("repaired request: status=%d", repaired.Code)
				}
				if err = db.QueryRow("SELECT (SELECT COUNT(*) FROM preview_links), (SELECT COUNT(*) FROM events WHERE message='Preview share link created')").Scan(&links, &events); err != nil || links != count+1 || events != 1 {
					t.Fatalf("recovery did not create exactly one link/event: %d/%d %v", links, events, err)
				}
				testutil.AssertPageRevision(t, q, page.ID, page.Revision)
			})
		}
	}
}
