// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
			testSlugRetry(t, router, slugRetryForm{tc.path, "#" + tc.kind + "-form", "name", "Name", tc.slugExists})
		})
	}
}

func TestPageSlugSurvivesValidation(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	user := createTestAdminUser(t, db)
	sm := testSessionManager(t)
	renderer, err := render.New(render.Config{TemplatesFS: web.Templates, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	h := NewPagesHandler(db, renderer, sm)
	router := chi.NewRouter()
	router.Use(middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(false)))
	router.Handle("/static/*", http.FileServer(http.FS(web.Static)))
	const path = "/admin" + RoutePages
	router.Group(func(r chi.Router) {
		r.Use(sm.LoadAndSave, middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { next.ServeHTTP(w, withUser(req, user)) })
		})
		r.Get(path+RouteSuffixNew, h.NewForm)
		r.Post(path, h.Create)
		r.Get(path, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})
	testSlugRetry(t, router, slugRetryForm{path, "form[x-data=slugForm]", "title", "Title", store.New(db).SlugExists})
}

type slugRetryForm struct {
	path, selector, field, label string
	slugExists                   func(context.Context, string) (int64, error)
}

func testSlugRetry(t *testing.T, router http.Handler, target slugRetryForm) {
	t.Helper()
	fresh := httptest.NewRecorder()
	router.ServeHTTP(fresh, httptest.NewRequest(http.MethodGet, target.path+RouteSuffixNew, nil))
	if fresh.Code != http.StatusOK || !strings.Contains(fresh.Body.String(), `data-slug-edited="false"`) {
		t.Fatalf("blank create form must allow automatic slugs, status %d", fresh.Code)
	}

	for _, tc := range []struct {
		name, typedSlug, initialSlug, savedSlug string
	}{
		{"typed", "keep-my-slug", "keep-my-slug", "keep-my-slug"},
		{"generated", "", "x", "corrected-name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edited := strconv.FormatBool(tc.typedSlug != "")
			validation := target.label + " must be at least 2 characters"
			// Leave the generated slug blank to also exercise the server fallback.
			form := url.Values{target.field: {"X"}, "slug": {tc.typedSlug}, "slug_edited": {edited}}
			req := httptest.NewRequest(http.MethodPost, target.path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			retry := httptest.NewRecorder()
			router.ServeHTTP(retry, req)
			body := retry.Body.String()
			if retry.Code != http.StatusOK || !strings.Contains(body, validation) ||
				!strings.Contains(body, `value="`+tc.initialSlug+`"`) {
				t.Fatalf("validation did not return the form with slug %q, status %d", tc.initialSlug, retry.Code)
			}
			if !strings.Contains(body, `data-slug-edited="`+edited+`"`) {
				t.Errorf("validation retry did not preserve slug_edited=%s", edited)
			}

			t.Run("chrome", func(t *testing.T) {
				cli := requireChromeCLI(t)
				server := httptest.NewServer(router)
				t.Cleanup(server.Close)
				action := server.URL + target.path
				browser := openAdminChrome(t, cli, "slug-retry", action+RouteSuffixNew)
				browser(t, "run-code", fmt.Sprintf(`async page => {
					const response = await page.reload();
					const policy = (await response.allHeaders())['content-security-policy'];
					if (!policy?.includes("script-src 'self' 'nonce-") || policy.includes("'unsafe-eval'")) {
						throw new Error('Expected production CSP without unsafe-eval');
					}
					const form = page.locator(%s);
					const name = form.locator(%s);
					const slug = form.locator('#slug');
					const custom = %s;
					const submit = form.locator('button[type=submit]').first();
					const post = () => page.waitForResponse(response => response.url() === %s && response.request().method() === 'POST');
					await name.fill('First Name');
					if (await slug.inputValue() !== 'first-name') throw new Error('Blank form did not generate a slug');
					await name.fill('X');
					if (custom) await slug.fill(custom);
					const [rejected] = await Promise.all([post(), submit.click()]);
					if (!rejected.ok()) throw new Error('Validation response failed: ' + rejected.status());
					await form.getByText(%s, {exact: true}).first().waitFor();
					if (await slug.inputValue() !== %s) throw new Error('Validation response lost the original slug');
					await name.fill('Corrected Name');
					const actual = await slug.inputValue();
					if (actual !== %s) throw new Error('Unexpected slug after correcting the name: ' + actual);
					const [saved] = await Promise.all([post(), submit.click()]);
					if (saved.status() !== 303) throw new Error('Retry did not save: ' + saved.status());
					return 'oCMS browser step passed';
				}`, strconv.Quote(target.selector), strconv.Quote("#"+target.field), strconv.Quote(tc.typedSlug),
					strconv.Quote(action), strconv.Quote(validation), strconv.Quote(tc.initialSlug), strconv.Quote(tc.savedSlug)))
				if count, lookupErr := target.slugExists(t.Context(), tc.savedSlug); lookupErr != nil || count != 1 {
					t.Fatalf("saved slug %q count=%d, want 1: %v", tc.savedSlug, count, lookupErr)
				}
				t.Logf("Chrome saved %s slug %q after validation", tc.name, tc.savedSlug)
			})
		})
	}
}
