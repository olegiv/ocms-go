// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
)

// TestMissingTargetsFallBackToPageAliases checks the premise behind
// aliasFallbackRoutes (cmd/ocms): a request under /tag/, /category/, /page/
// or /forms/ whose target does not exist is answered by the page alias with
// that path, so the page form may accept such aliases.
func TestMissingTargetsFallBackToPageAliases(t *testing.T) {
	db, sm := testHandlerSetup(t)
	admin := createTestAdminUser(t, db)
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	forms := NewFormsHandler(db, nil, sm, nil, nil, nil, nil, h)
	frontend := chi.NewRouter()
	frontend.Use(middleware.Language(db))
	frontend.Get(RouteCategorySlug, h.Category)
	frontend.Get(RouteTagSlug, h.Tag)
	frontend.Get(RoutePageByID, h.PageByID)
	frontend.Get(RouteFormsSlug, forms.Show)
	frontend.Get("/{slug}", h.Page)
	frontend.NotFound(h.NotFound)
	root := chi.NewRouter()
	root.Mount("/", frontend)

	for i, alias := range []string{"tag/no-such-tag", "category/no-such-category", "page/no-such-page", "forms/no-such-form"} {
		slug := "alias-target-" + strconv.Itoa(i)
		createPublishedAliasPage(t, db, slug, "en", alias, admin.ID)
		w := httptest.NewRecorder()
		root.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/"+alias, nil))
		if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/"+slug {
			t.Errorf("GET /%s = %d, Location %q; want a 301 to /%s", alias, w.Code, w.Header().Get("Location"), slug)
		}
	}
}
