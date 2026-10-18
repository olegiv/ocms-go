// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestCategoryFormSavesPosition posts the category form: create and update
// save the position it sends, a blank one keeps the current position, and a
// negative or non-numeric one re-renders the form with an error and changes
// nothing. The form showed a Position field that the handlers never read,
// so every category was created at 0 and kept its position on save (review
// of the 2026-10-17 audit fixes).
//
// Bug state: pass Position: 0 instead of the parsed position in
// CreateCategory, or existingCategory.Position in UpdateCategory, and this
// fails.
func TestCategoryFormSavesPosition(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, sm := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	h := NewTaxonomyHandler(db, renderer, sm)
	router := chi.NewRouter()
	router.Use(sm.LoadAndSave)
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, withUser(r, user)) })
	})
	router.Post("/admin/categories", h.CreateCategory)
	router.Post("/admin/categories/{id}", h.UpdateCategory)
	post := func(target string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}
	queries := store.New(db)
	ctx := context.Background()
	position := func(slug string) int64 {
		t.Helper()
		category, err := queries.GetCategoryBySlug(ctx, slug)
		if err != nil {
			t.Fatalf("category %s: %v", slug, err)
		}
		return category.Position
	}

	for _, bad := range []string{"-5", "first", "99999999999999999999"} {
		rr := post("/admin/categories", url.Values{"name": {"Bad"}, "slug": {"bad"}, "position": {bad}})
		if !strings.Contains(rr.Body.String(), "Position must be a whole number") {
			t.Errorf("create with position %q was not refused with the position error (status %d)", bad, rr.Code)
		}
		if _, err := queries.GetCategoryBySlug(ctx, "bad"); err == nil {
			t.Fatalf("create with position %q created the category", bad)
		}
	}
	if rr := post("/admin/categories", url.Values{"name": {"News"}, "slug": {"news"}, "position": {"7"}}); rr.Code != http.StatusSeeOther {
		t.Fatalf("create returned %d, want a redirect", rr.Code)
	}
	if got := position("news"); got != 7 {
		t.Errorf("created category position = %d, want 7", got)
	}
	category, err := queries.GetCategoryBySlug(ctx, "news")
	if err != nil {
		t.Fatal(err)
	}
	update := "/admin/categories/" + strconv.FormatInt(category.ID, 10)
	for _, step := range []struct {
		name     string
		position string
		want     int64
	}{
		{"set", "3", 3},
		{"blank keeps it", "", 3},
		{"negative is refused", "-1", 3},
		{"text is refused", "first", 3},
	} {
		rr := post(update, url.Values{"name": {"News"}, "slug": {"news"}, "position": {step.position}})
		if got := position("news"); got != step.want {
			t.Errorf("%s: position = %d after posting %q, want %d", step.name, got, step.position, step.want)
		}
		refused := strings.HasSuffix(step.name, "refused")
		if refused && !strings.Contains(rr.Body.String(), "Position must be a whole number") {
			t.Errorf("%s: the form was not re-rendered with the position error (status %d)", step.name, rr.Code)
		}
		if !refused && rr.Code != http.StatusSeeOther {
			t.Errorf("%s: update returned %d, want a redirect", step.name, rr.Code)
		}
	}
}
