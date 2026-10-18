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
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestPageFormRejectsShadowedAliasesBeforeSaving posts the page form with an
// alias an application route answers. Create must not add the page, Update
// must keep the stored aliases, and both must re-render the form with the
// error and the submitted aliases: the aliases used to be dropped silently on
// save, and a re-rendered form without them deleted them on the next submit.
func TestPageFormRejectsShadowedAliasesBeforeSaving(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, sm := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	h := NewPagesHandler(db, renderer, sm)
	router := chi.NewRouter()
	router.Use(sm.LoadAndSave)
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, withUser(r, user)) })
	})
	router.Post("/admin/pages", h.Create)
	router.Post("/admin/pages/{id}", h.Update)
	post := func(target string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}
	queries := store.New(db)
	ctx := context.Background()
	countPages := func() int64 {
		var n int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pages").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	before := countPages()
	rr := post("/admin/pages", url.Values{
		"title": {"Shadow test"}, "slug": {"shadow-test"}, "body": {"<p>x</p>"},
		"status": {"draft"}, "language_code": {"en"}, "aliases[]": {"about/kept", "login"},
	})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Alias /login") {
		t.Fatalf("create: status %d, want the form again with the /login alias error", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "about/kept") {
		t.Error("create: the re-rendered form lost the submitted aliases")
	}
	if after := countPages(); after != before {
		t.Errorf("create: %d pages were added despite the alias error", after-before)
	}

	page, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Stored", Slug: "stored-page", Body: "Content", Status: "draft", AuthorID: user.ID, LanguageCode: "en",
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if _, err := queries.CreatePageAlias(ctx, store.CreatePageAliasParams{PageID: page.ID, Alias: "about/stored", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("CreatePageAlias: %v", err)
	}
	rr = post("/admin/pages/"+strconv.FormatInt(page.ID, 10), url.Values{
		"title": {"Stored"}, "slug": {"stored-page"}, "body": {"Content"},
		"status": {"draft"}, "language_code": {"en"}, "aliases[]": {"about/stored", "uploads/2016/report"},
	})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Alias /uploads/2016/report") {
		t.Fatalf("update: status %d, want the form again with the uploads alias error", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "about/stored") {
		t.Error("update: the re-rendered form lost the submitted aliases")
	}
	aliases, err := queries.GetAliasesForPage(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 1 || aliases[0].Alias != "about/stored" {
		t.Errorf("update: stored aliases = %v, want about/stored untouched", aliases)
	}
}
