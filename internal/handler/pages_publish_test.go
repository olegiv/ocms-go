// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
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
		current, err := queries.GetPageByID(ctx, page.ID)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/admin/pages/"+strconv.FormatInt(page.ID, 10)+"/publish", strings.NewReader("base_revision="+strconv.FormatInt(current.Revision, 10)))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		router.ServeHTTP(rr, req)
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

func TestReviewedPublishAndScheduleRequireDisplayedRevision(t *testing.T) {
	db, sm := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	createDraftPage(t, db, user.ID)
	q := store.New(db)
	p, err := q.GetPageBySlug(t.Context(), "draft-page")
	if err != nil {
		t.Fatal(err)
	}
	h := NewPagesHandler(db, nil, sm)
	stale := p.Revision
	changed := pagewrite.ParamsFor(p)
	changed.Summary = "Edited after the reviewer loaded the page"
	p, err = h.updatePageGuarded(t.Context(), changed, p.Slug, pageFormWrite{UserID: user.ID, BaseRevision: &stale})
	if err != nil {
		t.Fatal(err)
	}
	for _, schedule := range []bool{false, true} {
		params := pagewrite.ParamsFor(p)
		params.Status = "published"
		if schedule {
			params.Status = "draft"
			params.ScheduledAt = sql.NullTime{Time: time.Now().Add(time.Hour), Valid: true}
		}
		_, err = h.updatePageGuarded(t.Context(), params, p.Slug, pageFormWrite{UserID: user.ID, BaseRevision: &stale})
		var conflict *pagewrite.Conflict
		if !errors.As(err, &conflict) {
			t.Fatal("stale publication/schedule was accepted")
		}
	}
	current, err := q.GetPageByID(t.Context(), p.ID)
	if err != nil || current.Status != "draft" || current.ScheduledAt.Valid || current.Revision != p.Revision {
		t.Fatal("stale review changed lifecycle")
	}
	base := current.Revision
	params := pagewrite.ParamsFor(current)
	params.ScheduledAt = sql.NullTime{Time: time.Now().Add(time.Hour), Valid: true}
	if _, err = h.updatePageGuarded(t.Context(), params, p.Slug, pageFormWrite{UserID: user.ID, BaseRevision: &base}); err != nil {
		t.Fatal("fresh reviewed schedule failed", err)
	}
}

func TestRestorePublishedAIContentRunsSharedHardValidation(t *testing.T) {
	db, sm := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	q := store.New(db)
	now := time.Now()
	p, err := pagewrite.Insert(t.Context(), q, provenance.Principal{UserID: user.ID, Source: "api"}, store.CreatePageParams{Title: "Current valid title", Slug: "r16-restore", Body: "<p>Current valid body</p>", Status: "published", AuthorID: user.ID, LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now}, func(tx *pagewrite.Tx, p store.Page) error {
		tx.SetEvidence(provenance.Evidence{Validated: []byte(`{"prompt_contract_version":"1.2.0","site_url":"https://example.com"}`)})
		return tx.Q.SetPageAIAccess(t.Context(), store.SetPageAIAccessParams{PageID: p.ID, AiEditable: true, SetBy: user.ID, SetAt: now})
	})
	if err != nil {
		t.Fatal(err)
	}
	bad, err := q.CreatePageVersion(t.Context(), store.CreatePageVersionParams{PageID: p.ID, Title: "", Body: "<p>Invalid empty title</p>", ChangedBy: user.ID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	good, err := q.CreatePageVersion(t.Context(), store.CreatePageVersionParams{PageID: p.ID, Title: "Prior valid title", Body: "<p>Prior valid body</p>", ChangedBy: user.ID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
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
	router.Post("/admin/pages/{id}/versions/{versionId}/restore", h.RestoreVersion)
	restore := func(versionID int64) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", fmt.Sprintf("/admin/pages/%d/versions/%d/restore", p.ID, versionID), strings.NewReader("base_revision=1"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		router.ServeHTTP(w, r)
		if w.Code != 303 {
			t.Fatalf("restore status=%d", w.Code)
		}
	}
	restore(bad.ID)
	current, err := q.GetPageByID(t.Context(), p.ID)
	if err != nil || current.Title != p.Title || current.Body != p.Body || current.Revision != 1 {
		t.Fatal("invalid restore changed published content")
	}
	restore(good.ID)
	current, err = q.GetPageByID(t.Context(), p.ID)
	if err != nil || current.Title != good.Title || current.Body != good.Body || current.Status != "published" || current.Revision != 2 {
		t.Fatal("valid restore did not preserve lifecycle and create a new revision")
	}
}
