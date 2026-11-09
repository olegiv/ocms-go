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
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestUnchangedSaveKeepsAIAccessAttribution checks that saving a page in the
// admin editor does not re-attribute its AI-access setting to the saving user
// unless the setting changed (2026-11-03 review R2 P3-5). page_ai_access.set_by
// restricts deleting the user it names, so a rewrite on every save made
// whoever last saved the page undeletable.
//
// Bug state: call SetPageAIAccess without comparing to the stored value and the
// unchanged save overwrites set_by with the saving user.
func TestUnchangedSaveKeepsAIAccessAttribution(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	sm := testSessionManager(t)
	owner := createTestUser(t, db, testUser{Email: "owner@example.com", Name: "Owner", Role: "admin"})
	saver := createTestUser(t, db, testUser{Email: "saver@example.com", Name: "Saver", Role: "admin"})
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	h := NewPagesHandler(db, renderer, sm)
	router := chi.NewRouter()
	router.Use(sm.LoadAndSave)
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, withUser(r, saver))
		})
	})
	router.Post("/admin/pages/{id}", h.Update)

	ctx := context.Background()
	queries := store.New(db)
	setAt := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	insert := func(slug string, aiEditable bool) store.Page {
		t.Helper()
		page, insertErr := pagewrite.Insert(ctx, queries, provenance.Principal{UserID: owner.ID, Source: "human"}, store.CreatePageParams{
			Title: slug, Slug: slug, Body: "<p>Body</p>", Status: "draft", AuthorID: owner.ID, LanguageCode: "en",
			PageType: "post", CreatedAt: setAt, UpdatedAt: setAt,
		}, func(tx *pagewrite.Tx, p store.Page) error {
			if !aiEditable {
				return nil
			}
			return tx.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: p.ID, AiEditable: true, SetBy: owner.ID, SetAt: setAt})
		})
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		return page
	}
	save := func(page store.Page, title string, allowAI bool) {
		t.Helper()
		current, loadErr := queries.GetPageByID(ctx, page.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		form := url.Values{
			"title": {title}, "slug": {page.Slug}, "body": {"<p>Body</p>"}, "status": {"draft"},
			"language_code": {"en"}, "page_type": {"post"}, "base_revision": {strconv.FormatInt(current.Revision, 10)},
			"ai_access_present": {"1"},
		}
		if allowAI {
			form.Set("allow_ai_edits", "on")
		}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/pages/%d", page.ID), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("save %q: status %d, body %s", title, rr.Code, rr.Body.String())
		}
	}

	enabled := insert("ai-enabled", true)
	save(enabled, "Edited by saver", true)
	access, err := queries.GetPageAIAccess(ctx, enabled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !access.AiEditable || access.SetBy != owner.ID || !access.SetAt.Equal(setAt) {
		t.Errorf("an unchanged save rewrote the AI access: %+v, want set_by %d at %v", access, owner.ID, setAt)
	}
	if stored, loadErr := queries.GetPageByID(ctx, enabled.ID); loadErr != nil || stored.Title != "Edited by saver" {
		t.Fatalf("the save did not apply: %+v %v", stored, loadErr)
	}

	// A change is attributed to the user who made it.
	save(enabled, "Edited by saver", false)
	access, err = queries.GetPageAIAccess(ctx, enabled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if access.AiEditable || access.SetBy != saver.ID {
		t.Errorf("turning AI access off: %+v, want it off and set by %d", access, saver.ID)
	}

	// A page that never had AI access keeps having none: the unchecked box is
	// the unchanged state, not a row to create.
	plain := insert("ai-never", false)
	save(plain, "Plain page edited", false)
	if _, err = queries.GetPageAIAccess(ctx, plain.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("saving a page without AI access created a row: %v", err)
	}
}

func TestNonOwnerEditorCannotChangeAIAccess(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	sm := testSessionManager(t)
	owner := createTestUser(t, db, testUser{Email: "owner@example.test", Name: "Owner", Role: model.RoleEditor})
	editor := createTestUser(t, db, testUser{Email: "editor@example.test", Name: "Other editor", Role: model.RoleEditor})
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	h := NewPagesHandler(db, renderer, sm)
	router := chi.NewRouter()
	router.Use(sm.LoadAndSave)
	router.Post("/admin/pages/{id}", h.Update)
	ctx := t.Context()
	q := store.New(db)
	now := time.Now()
	page, err := pagewrite.Insert(ctx, q, provenance.Principal{UserID: owner.ID, Source: "human"}, store.CreatePageParams{
		Title: "Owner draft", Slug: "owner-draft", Body: "<p>Body</p>", Status: "draft", AuthorID: owner.ID,
		LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	submit := func(user store.User, revision int64, title, aiChange string) {
		t.Helper()
		form := url.Values{
			"title": {title}, "slug": {page.Slug}, "body": {page.Body}, "status": {"draft"},
			"language_code": {"en"}, "page_type": {"post"}, "base_revision": {strconv.FormatInt(revision, 10)},
		}
		if aiChange != "" {
			form.Set("ai_access_present", "1")
			if aiChange == "enable" {
				form.Set("allow_ai_edits", "on")
			}
		}
		r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/pages/%d", page.ID), strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, withUser(r, user))
		if response.Code != http.StatusSeeOther {
			t.Fatalf("%s save returned %d", user.Name, response.Code)
		}
	}
	load := func() store.Page {
		t.Helper()
		current, loadErr := q.GetPageByID(ctx, page.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		return current
	}
	submit(editor, page.Revision, "Ordinary editor save", "")
	current := load()
	if current.Revision != page.Revision+1 || current.Title != "Ordinary editor save" {
		t.Fatal("non-owner editor could not make an ordinary content edit")
	}
	submit(editor, current.Revision, "Unauthorized opt-in", "enable")
	if _, err = q.GetPageAIAccess(ctx, page.ID); !errors.Is(err, sql.ErrNoRows) || load() != current {
		t.Fatalf("non-owner opt-in changed the draft or AI access: %v", err)
	}
	submit(owner, current.Revision, current.Title, "enable")
	current = load()
	access, err := q.GetPageAIAccess(ctx, page.ID)
	if err != nil || !access.AiEditable || access.SetBy != owner.ID {
		t.Fatalf("owner opt-in: %+v, %v", access, err)
	}
	submit(editor, current.Revision, "Unauthorized opt-out", "disable")
	after, err := q.GetPageAIAccess(ctx, page.ID)
	if err != nil || after != access || load() != current {
		t.Fatalf("non-owner opt-out changed the draft or AI access: %+v, %v", after, err)
	}
}

// TestDeleteRefusesUserWhoSetAIAccess checks that a user named by a page's
// AI-access setting is refused with the content message, one by one and in
// bulk, instead of reaching the restricting foreign key (R2 P3-5).
//
// Bug state: leave page_ai_access out of CountUserOwnedContent and the delete
// reaches the foreign key and reports "Error deleting user".
func TestDeleteRefusesUserWhoSetAIAccess(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	// The migrated schema, so the restricting foreign key is enforced as in production.
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	sm := testSessionManager(t)
	admin := createTestUser(t, db, testUser{Email: "admin@example.com", Name: "Admin", Role: "admin"})
	target := createTestUser(t, db, testUser{Email: "target@example.com", Name: "Target", Role: "editor"})
	ctx := context.Background()
	queries := store.New(db)
	now := time.Now().UTC()
	// The admin owns the page; the only row naming the target is the AI access.
	page, err := pagewrite.Insert(ctx, queries, provenance.Principal{UserID: admin.ID, Source: "human"}, store.CreatePageParams{
		Title: "Owned", Slug: "owned", Body: "<p>Body</p>", Status: "draft", AuthorID: admin.ID, LanguageCode: "en",
		PageType: "post", CreatedAt: now, UpdatedAt: now,
	}, func(tx *pagewrite.Tx, p store.Page) error {
		return tx.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: p.ID, AiEditable: true, SetBy: target.ID, SetAt: now})
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewUsersHandler(db, nil, sm)
	refusal := i18n.T(i18n.GetDefaultLanguage(), "users.delete_owns_content")
	if refusal == "" || refusal == "users.delete_owns_content" {
		t.Fatalf("the refusal text is not translated: %q", refusal)
	}
	exists := func() bool {
		t.Helper()
		var count int
		if scanErr := db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", target.ID).Scan(&count); scanErr != nil {
			t.Fatal(scanErr)
		}
		return count == 1
	}

	targetID := strconv.FormatInt(target.ID, 10)
	req, w := newAuthenticatedDeleteRequest(t, sm, "/admin/users/"+targetID, map[string]string{"id": targetID}, &admin)
	req.Header.Set("HX-Request", "true")
	h.Delete(w, req)
	// The message is the body, which the admin's htmx error handler toasts
	// (2026-11-03 review: a header carried no translated text to anyone).
	if w.Code != http.StatusBadRequest || w.Body.String() != refusal {
		t.Errorf("single delete: status %d, body %q, want 400 with %q", w.Code, w.Body.String(), refusal)
	}
	if !exists() {
		t.Fatal("the user was deleted")
	}

	bulk := httptest.NewRequest(http.MethodPost, "/admin/users/bulk-delete", strings.NewReader(fmt.Sprintf(`{"ids":[%d]}`, target.ID)))
	bulk.Header.Set("Content-Type", "application/json")
	bulk = requestWithSession(sm, bulk)
	bulk = addUserToContext(bulk, &admin)
	rec := httptest.NewRecorder()
	h.BulkDelete(rec, bulk)
	resp := assertJSONResponse(t, rec, http.StatusOK, true)
	failed, _ := resp["failed"].([]any)
	if len(failed) != 1 {
		t.Fatalf("bulk delete: failed = %v, want the one user", resp["failed"])
	}
	if item, _ := failed[0].(map[string]any); item["reason"] != refusal {
		t.Errorf("bulk delete reason = %v, want %q", item["reason"], refusal)
	}
	if !exists() {
		t.Fatal("the user was deleted in bulk")
	}

	// Once the setting names someone else the user is deletable.
	if _, err = db.ExecContext(ctx, "UPDATE page_ai_access SET set_by = ? WHERE page_id = ?", admin.ID, page.ID); err != nil {
		t.Fatal(err)
	}
	req, w = newAuthenticatedDeleteRequest(t, sm, "/admin/users/"+targetID, map[string]string{"id": targetID}, &admin)
	req.Header.Set("HX-Request", "true")
	h.Delete(w, req)
	if w.Code != http.StatusOK || exists() {
		t.Errorf("delete after the setting moved: status %d, user present %v", w.Code, exists())
	}
}
