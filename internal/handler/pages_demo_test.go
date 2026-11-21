// Copyright (c) 2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// Demo mode is cached for the life of the process. Exercise the regular
// editor and the shared editor through their actual GET, POST and PUT handlers.
func TestPageAuthoringInSharedDemo(t *testing.T) {
	mode := os.Getenv("OCMS_DEMO_DRAFT_TEST")
	if mode == "" {
		for _, mode := range []string{"true", "false"} {
			t.Run("demo="+mode, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestPageAuthoringInSharedDemo$", "-test.count=1")
				cmd.Env = append(os.Environ(), "OCMS_DEMO_MODE="+mode, "OCMS_DEMO_DRAFT_TEST="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}

	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, sm := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	q := store.New(db)
	h := NewPagesHandler(db, renderer, sm)
	router := chi.NewRouter()
	router.Use(sm.LoadAndSave)
	router.Get("/admin/pages/new", h.NewForm)
	router.Post("/admin/pages", h.Create)
	router.Get("/admin/pages/{id}", h.EditForm)
	router.Post("/admin/pages/{id}", h.Update)
	router.Put("/admin/pages/{id}", h.Update)
	router.Delete("/admin/pages/{id}", h.Delete)
	router.Post("/admin/pages/{id}/publish", h.TogglePublish)
	router.Post("/admin/pages/{id}/versions/{versionId}/restore", h.RestoreVersion)

	request := func(method, path string, form url.Values, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, withUser(req, user))
		if w.Code != want {
			t.Fatalf("%s %s: status %d, want %d; response %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	form := url.Values{"title": {"Demo article"}, "body": {"<p>First draft</p>"}, "slug": {"demo-article"}, "language_code": {"en"}}
	if mode == "false" {
		form.Set("status", "published")
		request("POST", "/admin/pages", form, http.StatusSeeOther)
		published, err := q.GetPublishedPageBySlug(ctx, "demo-article")
		if err != nil {
			t.Fatal(err)
		}
		form.Set("base_revision", strconv.FormatInt(published.Revision, 10))
		form.Set("body", "<p>Normal publication edited</p>")
		request("POST", fmt.Sprintf("/admin/pages/%d", published.ID), form, http.StatusSeeOther)
		updated, err := q.GetPublishedPageBySlug(ctx, "demo-article")
		if err != nil || updated.Body != form.Get("body") {
			t.Fatalf("edit published page outside demo: %+v, %v", updated, err)
		}
		return
	}

	editor := request("GET", "/admin/pages/new", nil, http.StatusOK).Body.String()
	if !strings.Contains(editor, "01:00 UTC") || strings.Contains(editor, `name="scheduled_at"`) || !strings.Contains(editor, "Save draft") {
		t.Fatal("demo new page must explain reset, offer draft saving and omit scheduling")
	}

	for _, field := range []string{"status", "scheduled_at", "published_at"} {
		value := "2030-10-07T12:00"
		if field == "status" {
			value = "published"
		}
		form.Set(field, value)
		request("POST", "/admin/pages", form, http.StatusForbidden)
		form.Del(field)
		if _, err := q.GetPageBySlug(ctx, "demo-article"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("blocked %s create left a page: %v", field, err)
		}
	}
	resp := request("POST", "/admin/pages", form, http.StatusSeeOther)
	first, err := q.GetPageBySlug(ctx, "demo-article")
	if err != nil {
		t.Fatal(err)
	}
	editPath := fmt.Sprintf("/admin/pages/%d", first.ID)
	if resp.Header().Get("Location") != editPath {
		t.Fatalf("expected saved draft at %s; redirect %q", editPath, resp.Header().Get("Location"))
	}
	editor = request("GET", editPath, nil, http.StatusOK).Body.String()
	if !strings.Contains(editor, "First draft") || !strings.Contains(editor, "Save draft") || strings.Contains(editor, `name="ai_access_present"`) {
		t.Fatal("reopened demo draft must show the saved content and allow a draft-only edit")
	}
	photo := createTestMedia(t, db, user.ID, 1920, 1280)
	if _, err := db.ExecContext(ctx, "UPDATE media SET alt = 'Shared photo description' WHERE id = ?", photo.ID); err != nil {
		t.Fatal(err)
	}
	form.Set("base_revision", strconv.FormatInt(first.Revision, 10))
	form.Set("featured_image_id", strconv.FormatInt(photo.ID, 10))
	form.Set("body", "<h2>Second draft</h2><p>Saved paragraph</p><ul><li>Checklist item</li></ul>")
	request("POST", editPath, form, http.StatusSeeOther)
	updated, err := q.GetPageByID(ctx, first.ID)
	if err != nil || updated.Body != form.Get("body") || updated.Revision != first.Revision+1 || !updated.CreatedAt.Equal(first.CreatedAt) || updated.Status != PageStatusDraft || updated.ScheduledAt.Valid || updated.PublishedAt.Valid {
		t.Fatalf("second draft save: %+v, %v", updated, err)
	}
	anonymous := httptest.NewRecorder()
	frontend := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	frontend.Page(anonymous, newFrontendPageRequest(first.Slug))
	if anonymous.Code != http.StatusNotFound || strings.Contains(anonymous.Body.String(), "Second draft") {
		t.Fatalf("anonymous draft URL returned status %d or exposed draft content", anonymous.Code)
	}
	request("PUT", editPath, form, http.StatusConflict)
	form.Set("base_revision", strconv.FormatInt(updated.Revision, 10))
	form.Set("status", "published")
	request("PUT", editPath, form, http.StatusForbidden)
	form.Set("status", "draft")
	form.Set("scheduled_at", "2030-10-07T12:00")
	request("POST", editPath, form, http.StatusForbidden)
	form.Del("scheduled_at")
	request("POST", editPath+"/publish", form, http.StatusSeeOther)
	after, err := q.GetPageByID(ctx, first.ID)
	if err != nil || after != updated {
		t.Fatalf("blocked actions changed draft: %+v, %v", after, err)
	}

	// Another writer can change the row between a form read and its save.
	// Use the same captured revision to exercise the writer's in-transaction
	// check independently of its earlier advisory read and revision check.
	for _, status := range []string{"scheduled", "published"} {
		if status == "scheduled" {
			_, err = db.ExecContext(ctx, "UPDATE pages SET scheduled_at = CURRENT_TIMESTAMP WHERE id = ?", first.ID)
		} else {
			_, err = db.ExecContext(ctx, "UPDATE pages SET status = 'published', scheduled_at = NULL WHERE id = ?", first.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		before, err := q.GetPageByID(ctx, first.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = h.updatePageGuarded(ctx, pagewrite.ParamsFor(updated), updated.Slug, pageFormWrite{UserID: user.ID, BaseRevision: &updated.Revision})
		if !errors.Is(err, errProtectedDemoPage) {
			t.Fatalf("%s in-transaction demo policy: %v", status, err)
		}
		editor = request("GET", editPath, nil, http.StatusOK).Body.String()
		if !strings.Contains(editor, "read-only") || strings.Contains(editor, `name="body"`) || !strings.Contains(editor, "Second draft") {
			t.Fatalf("%s page must render saved content and the read-only explanation without editable fields", status)
		}
		for _, want := range []string{"<h2>Second draft</h2>", "<p>Saved paragraph</p>", "<li>Checklist item</li>", `alt="Shared photo description"`} {
			if !strings.Contains(editor, want) {
				t.Errorf("%s read-only article missing %s", status, want)
			}
		}
		if strings.Contains(editor, `href="/demo-article"`) != (status == "published") {
			t.Fatalf("public article link on %s page is incorrect", status)
		}
		request("PUT", editPath, form, http.StatusForbidden)
		request("POST", editPath+"/publish", form, http.StatusSeeOther)
		request("POST", editPath+"/versions/1/restore", form, http.StatusSeeOther)
		request("DELETE", editPath, nil, http.StatusForbidden)
		after, err := q.GetPageByID(ctx, first.ID)
		if err != nil || after != before {
			t.Fatalf("blocked actions changed %s page: %+v, %v", status, after, err)
		}
	}
}
