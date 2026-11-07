// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/web"
)

func TestMenuBuilderPageCheckboxSavesUnderCSP(t *testing.T) {
	cli := os.Getenv("OCMS_PLAYWRIGHT_CLI")
	if cli == "" {
		t.Skip("set OCMS_PLAYWRIGHT_CLI to run the Chrome menu regression")
	}
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	ctx, queries := t.Context(), store.New(db)
	user := createTestUser(t, db, testUser{Email: "menu-editor@example.test", Name: "Menu editor", Role: model.RoleEditor})
	now := time.Now().UTC()
	page, err := pagewrite.Insert(ctx, queries, provenance.Principal{UserID: user.ID, Source: "human"}, store.CreatePageParams{
		Title: "Page to add", Slug: "page-to-add", Body: "<p>Fixture</p>", Status: "published", AuthorID: user.ID,
		LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	menu, err := queries.CreateMenu(ctx, store.CreateMenuParams{
		Name: "Fixture menu", Slug: "fixture-menu", LanguageCode: "en", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	sm := testSessionManager(t)
	renderer, err := render.New(render.Config{TemplatesFS: web.Templates, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewMenusHandler(db, renderer, sm)
	router := chi.NewRouter()
	router.Use(middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(false)))
	router.Handle("/static/*", http.FileServer(http.FS(web.Static)))
	router.Group(func(r chi.Router) {
		r.Use(sm.LoadAndSave, middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { next.ServeHTTP(w, withUser(req, user)) })
		})
		r.Get("/admin/menus/{id}", handler.EditForm)
		r.Post("/admin/menus/{id}/items", handler.AddItem)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	formURL := fmt.Sprintf("%s/admin/menus/%d", server.URL, menu.ID)
	browser := openAdminChrome(t, cli, "menu-checkbox", formURL)
	browser(t, "run-code", fmt.Sprintf(`async page => {
		const errors = [];
		page.on('pageerror', error => errors.push(error.message));
		page.on('console', message => {
			if (message.text().includes('Alpine Expression Error')) errors.push(message.text());
		});
		const response = await page.reload();
		const policy = (await response.allHeaders())['content-security-policy'];
		if (!policy?.includes("script-src 'self' 'nonce-") || policy.includes("'unsafe-eval'")) {
			throw new Error('Expected production CSP without unsafe-eval');
		}
		const checkbox = page.locator('input[type=checkbox][data-page-id="%d"]');
		const add = page.getByRole('button', {name: 'Add Selected to Menu', exact: true});
		if (!await add.isDisabled()) throw new Error('Expected an empty selection');
		await checkbox.check();
		if (!await add.isEnabled()) throw new Error('Page checkbox did not select a page: ' + errors.join(', '));
		await checkbox.uncheck();
		if (!await add.isDisabled()) throw new Error('Page checkbox did not clear the selection');
		await checkbox.check();
		const [saved] = await Promise.all([
			page.waitForResponse(response => response.url() === %s && response.request().method() === 'POST'),
			add.click(),
		]);
		if (!saved.ok()) throw new Error('Saving the selected page failed: ' + saved.status());
		await page.reload();
		await page.locator('#menu-items').getByText('Page to add', {exact: true}).waitFor();
		if (errors.length) throw new Error(errors.join(', '));
		return 'oCMS browser step passed';
	}`, page.ID, strconv.Quote(formURL+"/items")))
	items, err := queries.ListMenuItems(ctx, menu.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].PageID.Valid || items[0].PageID.Int64 != page.ID {
		t.Fatalf("saved menu items = %+v, want one item linked to page %d", items, page.ID)
	}
	t.Log("Chrome checked and unchecked the page, saved it to the menu, and found the persisted page after reload")
}
