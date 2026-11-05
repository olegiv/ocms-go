// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/net/html"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

type conflictQueryFault struct {
	*sql.DB
	query string
	skip  int
}

func (db *conflictQueryFault) shouldFail(query string) bool {
	if strings.Contains(query, db.query) {
		if db.skip == 0 {
			return true
		}
		db.skip--
	}
	return false
}

func (db *conflictQueryFault) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if db.shouldFail(query) {
		return db.DB.QueryRowContext(ctx, "SELECT * FROM unavailable_conflict_data")
	}
	return db.DB.QueryRowContext(ctx, query, args...)
}

func (db *conflictQueryFault) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if db.shouldFail(query) {
		return db.DB.QueryContext(ctx, "SELECT * FROM unavailable_conflict_data")
	}
	return db.DB.QueryContext(ctx, query, args...)
}

func TestPageConflictReadFaultPreservesSubmittedEdits(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query, imageField string
		skip                    int
	}{
		{name: "current page", query: "GetPageByID", skip: 1},
		{name: "second tag", query: "GetTagByID", skip: 1},
		{name: "categories", query: "ListCategories"},
		{name: "editor", query: "GetUserByID"},
		{name: "featured image", query: "GetMediaByID", imageField: "featured_image_id", skip: 1},
		{name: "Open Graph image", query: "GetMediaByID", imageField: "og_image_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, sm := testHandlerSetup(t)
			user := createTestAdminUser(t, db)
			q := store.New(db)
			ctx := t.Context()
			now := time.Now().UTC()
			renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
			if err != nil {
				t.Fatal(err)
			}
			h := NewPagesHandler(db, renderer, sm)
			principal := provenance.Principal{UserID: user.ID, Source: "human"}
			original, err := pagewrite.Insert(ctx, q, principal, store.CreatePageParams{
				Title: "Original page", Slug: "current-page", Body: "Current body", Status: "draft", AuthorID: user.ID,
				LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			work, err := pagewrite.InTx(ctx, db, principal, func(work *pagewrite.Tx) error {
				current, trackErr := work.Track(ctx, original.ID, &original.Revision, true)
				if trackErr != nil {
					return trackErr
				}
				params := pagewrite.ParamsFor(current)
				params.Title = "Current page"
				_, updateErr := work.Q.UpdatePage(ctx, params)
				return updateErr
			})
			if err != nil {
				t.Fatal(err)
			}
			page := work.Pages[original.ID]
			form := url.Values{
				"_method": {"PUT"}, "title": {"Submitted title"}, "slug": {page.Slug},
				"body": {"\n<p>Submitted &amp; preserved</p>"}, "summary": {"Submitted summary"},
				"status": {"draft"}, "language_code": {"en"}, "page_type": {"post"},
				"base_revision": {strconv.FormatInt(original.Revision, 10)}, "aliases[]": {"submitted-alias"},
			}
			if tc.imageField != "" {
				media, mediaErr := q.CreateMedia(ctx, store.CreateMediaParams{Uuid: "conflict-image", Filename: "example.png", MimeType: "image/png", Size: 1, UploadedBy: user.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
				if mediaErr != nil {
					t.Fatal(mediaErr)
				}
				form.Set(tc.imageField, strconv.FormatInt(media.ID, 10))
			}
			for _, name := range []string{"submitted-first", "submitted-second"} {
				tag, tagErr := q.CreateTag(ctx, store.CreateTagParams{Name: name, Slug: name, LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
				if tagErr != nil {
					t.Fatal(tagErr)
				}
				form.Add("tags[]", strconv.FormatInt(tag.ID, 10))
			}
			path := fmt.Sprintf("/admin/pages/%d", page.ID)
			router := chi.NewRouter()
			router.Use(sm.LoadAndSave)
			router.Post("/admin/pages/{id}", func(w http.ResponseWriter, r *http.Request) { h.Update(w, withUser(r, user)) })
			post := func(values url.Values) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, req)
				return response
			}
			var logs bytes.Buffer
			originalLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(originalLogger) })
			// Fault the query while rendering the conflict. The transaction's
			// revision check uses a separate query handle and refuses the write.
			h.queries = store.New(&conflictQueryFault{DB: db, query: "-- name: " + tc.query + " :", skip: tc.skip})
			response := post(form)
			if response.Code != http.StatusServiceUnavailable || response.Header().Get("Location") != "" {
				t.Fatalf("%s fault: status=%d redirect=%q", tc.query, response.Code, response.Header().Get("Location"))
			}
			if !strings.Contains(logs.String(), "unavailable_conflict_data") || !strings.Contains(logs.String(), fmt.Sprintf("page_id=%d", page.ID)) {
				t.Fatalf("fault log omitted the cause or page ID: %q", logs.String())
			}
			retained := recoveryFormValues(t, response.Body.String(), path)
			if retained.Encode() != form.Encode() {
				t.Fatalf("recovery form values = %v, want %v", retained, form)
			}
			if response = post(retained); response.Code != http.StatusServiceUnavailable {
				t.Fatalf("retry during continuing fault: status=%d", response.Code)
			}
			h.queries = q
			response = post(retained)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "submitted-first") || !strings.Contains(response.Body.String(), "submitted-second") {
				t.Fatalf("healthy retry did not restore the conflict form and both tags: status=%d", response.Code)
			}
			if tc.query == "GetTagByID" {
				retained.Add("tags[]", "9999")
				if missing := post(retained); missing.Code != http.StatusConflict || !strings.Contains(missing.Body.String(), "Tag 9999 is unavailable") {
					t.Fatalf("deleted tag was dropped from the conflict form: status=%d", missing.Code)
				}
				retained["tags[]"] = form["tags[]"]
			}
			if current, loadErr := q.GetPageByID(ctx, page.ID); loadErr != nil || current.Revision != page.Revision || current.Body != page.Body {
				t.Fatalf("conflict retry mutated the page: %+v, %v", current, loadErr)
			}
			retained.Set("overwrite_revision", strconv.FormatInt(page.Revision, 10))
			if response = post(retained); response.Code != http.StatusSeeOther {
				t.Fatalf("confirmed overwrite: status=%d", response.Code)
			}
			if tags, loadErr := q.GetTagsForPage(ctx, page.ID); loadErr != nil || len(tags) != 2 {
				t.Fatalf("confirmed save did not preserve both tags: %v, %v", tags, loadErr)
			}
		})
	}
}

func recoveryFormValues(t *testing.T, markup, action string) url.Values {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	for form := range doc.Descendants() {
		if form.Type != html.ElementNode || form.Data != "form" || nodeAttr(form, "action") != action {
			continue
		}
		values := make(url.Values)
		for field := range form.Descendants() {
			name := nodeAttr(field, "name")
			if name == "" {
				continue
			}
			switch field.Data {
			case "input":
				values.Add(name, nodeAttr(field, "value"))
			case "textarea":
				if field.FirstChild == nil {
					values.Add(name, "")
				} else {
					values.Add(name, field.FirstChild.Data)
				}
			}
		}
		return values
	}
	t.Fatal("missing edit recovery form")
	return nil
}

func nodeAttr(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}
