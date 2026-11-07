// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/web"
)

func TestTaxonomySlugSurvivesValidation(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	q := store.New(db)
	user := createTestAdminUser(t, db)
	sm := testSessionManager(t)
	renderer, err := render.New(render.Config{TemplatesFS: web.Templates, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	h := NewTaxonomyHandler(db, renderer, sm)
	h.SetCacheManager(cache.NewManager(q))
	router := chi.NewRouter()
	router.Use(middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(false)))
	router.Handle("/static/*", http.FileServer(http.FS(web.Static)))
	router.Route("/admin", func(r chi.Router) {
		r.Use(sm.LoadAndSave, middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { next.ServeHTTP(w, withUser(req, user)) })
		})
		r.Get(RouteTags+RouteSuffixNew, h.NewTagForm)
		r.Get(RouteTags, h.ListTags)
		r.Post(RouteTags, h.CreateTag)
		r.Get(RouteCategories+RouteSuffixNew, h.NewCategoryForm)
		r.Get(RouteCategories, h.ListCategories)
		r.Post(RouteCategories, h.CreateCategory)
	})

	for _, tc := range []struct {
		kind, path string
		slugExists func(context.Context, string) (int64, error)
	}{
		{"tag", "/admin" + RouteTags, q.TagSlugExists},
		{"category", "/admin" + RouteCategories, q.CategorySlugExists},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			const customSlug = "keep-my-slug"
			fresh := httptest.NewRecorder()
			router.ServeHTTP(fresh, httptest.NewRequest(http.MethodGet, tc.path+RouteSuffixNew, nil))
			if fresh.Code != http.StatusOK || !strings.Contains(fresh.Body.String(), `data-slug-edited="false"`) {
				t.Fatalf("blank create form must allow automatic slugs, status %d", fresh.Code)
			}

			form := url.Values{"name": {"X"}, "slug": {customSlug}}
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			retry := httptest.NewRecorder()
			router.ServeHTTP(retry, req)
			body := retry.Body.String()
			if retry.Code != http.StatusOK || !strings.Contains(body, "Name must be at least 2 characters") ||
				!strings.Contains(body, `value="`+customSlug+`"`) {
				t.Fatalf("validation did not return the form with the submitted slug, status %d", retry.Code)
			}
			if !strings.Contains(body, `data-slug-edited="true"`) {
				t.Error("validation retry did not mark the submitted slug as edited")
			}

			t.Run("chrome", func(t *testing.T) {
				cli := os.Getenv("OCMS_PLAYWRIGHT_CLI")
				if cli == "" {
					t.Skip("set OCMS_PLAYWRIGHT_CLI to run the Chrome slug retry")
				}
				server := httptest.NewServer(router)
				t.Cleanup(server.Close)
				action := server.URL + tc.path
				browser := openAdminChrome(t, cli, tc.kind+"-slug-retry", action+RouteSuffixNew)
				browser(t, "run-code", fmt.Sprintf(`async page => {
					const response = await page.reload();
					const policy = (await response.allHeaders())['content-security-policy'];
					if (!policy?.includes("script-src 'self' 'nonce-") || policy.includes("'unsafe-eval'")) {
						throw new Error('Expected production CSP without unsafe-eval');
					}
					const form = page.locator(%s);
					const name = form.locator('#name');
					const slug = form.locator('#slug');
					const custom = %s;
					const submit = form.locator('button[type=submit]');
					const post = () => page.waitForResponse(response => response.url() === %s && response.request().method() === 'POST');
					await name.fill('First Name');
					if (await slug.inputValue() !== 'first-name') throw new Error('Blank form did not generate a slug');
					await name.fill('X');
					await slug.fill(custom);
					const [rejected] = await Promise.all([post(), submit.click()]);
					if (!rejected.ok()) throw new Error('Validation response failed: ' + rejected.status());
					await form.getByText('Name must be at least 2 characters', {exact: true}).waitFor();
					if (await slug.inputValue() !== custom) throw new Error('Validation response lost the custom slug');
					await name.fill('Corrected Name');
					if (await slug.inputValue() !== custom) throw new Error('Editing the name overwrote the submitted slug');
					const [saved] = await Promise.all([post(), submit.click()]);
					if (saved.status() !== 303) throw new Error('Retry did not save: ' + saved.status());
					await page.getByText('Corrected Name', {exact: true}).waitFor();
					return 'oCMS browser step passed';
				}`, strconv.Quote("#"+tc.kind+"-form"), strconv.Quote(customSlug), strconv.Quote(action)))
				if count, lookupErr := tc.slugExists(t.Context(), customSlug); lookupErr != nil || count != 1 {
					t.Fatalf("saved custom slug count=%d, want 1: %v", count, lookupErr)
				}
				t.Log("Chrome generated a fresh slug, preserved the custom slug after validation, and saved the retry")
			})
		})
	}
}
