// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/net/html"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestPageEditorTranslationFormCreatesLinkedDraft(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, sm := testHandlerSetup(t)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	q, ctx := store.New(db), t.Context()
	user := createTestAdminUser(t, db)
	now := time.Now().UTC()
	lang, err := q.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "ru", Name: "Russian", NativeName: "Русский", Direction: "ltr", IsActive: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := pagewrite.Insert(ctx, q, provenance.Principal{UserID: user.ID, Source: "human", Transport: "admin"},
		store.CreatePageParams{
			Title: "Original page", Slug: "original-page", Body: "<p>Original content</p>",
			Status: "draft", AuthorID: user.ID, LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now,
		}, nil)
	if err != nil {
		t.Fatal(err)
	}

	h := NewPagesHandler(db, renderer, sm)
	router := chi.NewRouter()
	router.Use(sm.LoadAndSave, middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(false)),
		middleware.CSRF(middleware.DefaultCSRFConfig(nil, false)))
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, withUser(r, user)) })
	})
	router.Get("/admin/pages/{id}", h.EditForm)
	router.Post("/admin/pages/{id}/translate/{langCode}", h.Translate)
	editPath := fmt.Sprintf("/admin/pages/%d", source.ID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://cms.test"+editPath, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("render editor: status=%d, body=%s", w.Code, w.Body.String())
	}

	doc, err := html.Parse(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	action := editPath + "/translate/ru"
	var submit, form *html.Node
	for node := range doc.Descendants() {
		if node.Type != html.ElementNode {
			continue
		}
		if node.Data == "button" && nodeAttr(node, "title") == lang.Name {
			submit = node
		}
		if node.Data == "form" && nodeAttr(node, "action") == action {
			form = node
		}
	}
	if submit == nil || nodeAttr(submit, "onclick") != "" || nodeAttr(submit, "type") != "submit" {
		t.Fatal("translation button must submit without an inline handler")
	}
	if form == nil || nodeAttr(form, "method") != "POST" || nodeAttr(form, "id") == "" ||
		nodeAttr(submit, "form") != nodeAttr(form, "id") {
		t.Fatal("translation button must submit its own POST form")
	}
	values := make(url.Values)
	for field := range form.Descendants() {
		if name := nodeAttr(field, "name"); field.Data == "input" && name != "" {
			values.Add(name, nodeAttr(field, "value"))
		}
	}
	if revisions := values["base_revision"]; len(revisions) != 1 || revisions[0] != fmt.Sprint(source.Revision) {
		t.Fatalf("translation form revision=%v, want %d", revisions, source.Revision)
	}

	post := func(values url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "https://cms.test"+nodeAttr(form, "action"), strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	for _, invalid := range []url.Values{nil, {"base_revision": {fmt.Sprint(source.Revision + 1)}}} {
		w := post(invalid)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != editPath {
			t.Fatalf("invalid revision %v: status=%d, location=%q", invalid, w.Code, w.Header().Get("Location"))
		}
		if count, err := q.CountPages(ctx); err != nil || count != 1 {
			t.Fatalf("invalid revision created a page: count=%d, err=%v", count, err)
		}
	}

	w = post(values)
	link, err := q.GetTranslation(ctx, store.GetTranslationParams{
		EntityType: model.EntityTypePage, EntityID: source.ID, LanguageID: lang.ID,
	})
	if err != nil {
		t.Fatalf("load translation link: %v (status=%d, location=%q)", err, w.Code, w.Header().Get("Location"))
	}
	translated, err := q.GetPageByID(ctx, link.TranslationID)
	if err != nil || translated.LanguageCode != lang.Code || translated.Status != "draft" || translated.Body != "" {
		t.Fatalf("expected an empty draft in %s: %+v, %v", lang.Code, translated, err)
	}
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != fmt.Sprintf("/admin/pages/%d", translated.ID) {
		t.Fatalf("translation status=%d, location=%q", w.Code, w.Header().Get("Location"))
	}
}
