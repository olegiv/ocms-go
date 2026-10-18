// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestTogglePublishAppliesOnlyToTheStatusItRead posts the publish toggle for
// a draft and then for the published page, and once more while a trigger
// makes the publish update match no row, as it does when the scheduler
// published the page after the handler read it. A toggle that matched no row
// must report that and log nothing: the handler logged "Page published" even
// when its update failed, and its unconditional update republished a page
// the scheduler had already published, sending page.published again.
//
// Bug state: log the event before checking the update's error, or treat
// sql.ErrNoRows as success, and this fails.
func TestTogglePublishAppliesOnlyToTheStatusItRead(t *testing.T) {
	db, sm := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	h := NewPagesHandler(db, renderer, sm)
	var flash string
	router := chi.NewRouter()
	router.Use(sm.LoadAndSave)
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, withUser(r, user))
			flash = sm.GetString(r.Context(), "flash")
		})
	})
	router.Post("/admin/pages/{id}/publish", h.TogglePublish)
	ctx := context.Background()
	queries := store.New(db)
	now := time.Now()
	page, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Toggle", Slug: "toggle", Body: "b", Status: PageStatusDraft, AuthorID: user.ID, LanguageCode: "en",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	toggle := func() {
		t.Helper()
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/admin/pages/"+strconv.FormatInt(page.ID, 10)+"/publish", nil))
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("toggle: status %d, want a redirect", rr.Code)
		}
	}
	status := func() string {
		t.Helper()
		current, err := queries.GetPageByID(ctx, page.ID)
		if err != nil {
			t.Fatal(err)
		}
		return current.Status
	}
	loggedEvents := func() []string {
		t.Helper()
		rows, err := db.QueryContext(ctx, `SELECT message FROM events WHERE category = 'page' ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var messages []string
		for rows.Next() {
			var message string
			if err := rows.Scan(&message); err != nil {
				t.Fatal(err)
			}
			messages = append(messages, message)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return messages
	}

	toggle()
	if status() != PageStatusPublished || flash != "Page published successfully" {
		t.Fatalf("publish: status %q, flash %q", status(), flash)
	}
	toggle()
	if status() != PageStatusDraft || flash != "Page unpublished successfully" {
		t.Fatalf("unpublish: status %q, flash %q", status(), flash)
	}

	if _, err := db.ExecContext(ctx, `CREATE TRIGGER skip_publish BEFORE UPDATE OF status ON pages
		WHEN NEW.status = 'published' BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	toggle()
	if status() != PageStatusDraft || flash != "The page status changed meanwhile; check it and try again" {
		t.Errorf("raced publish: status %q, flash %q; want the page left alone and the change reported", status(), flash)
	}
	if got, want := loggedEvents(), []string{"Page published", "Page unpublished"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("logged %q, want %q: the raced publish must log nothing", got, want)
	}
}
