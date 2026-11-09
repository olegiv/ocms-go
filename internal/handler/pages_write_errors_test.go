// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestPageSaveKeepsSubmittedFormOnTransactionFailure(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
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
			router.Post("/admin/pages", h.Create)
			router.Post("/admin/pages/{id}", h.Update)
			ctx := t.Context()
			q := store.New(db)
			now := time.Now()
			tag, err := q.CreateTag(ctx, store.CreateTagParams{
				Name: "Keep this tag", Slug: "keep-this-tag", LanguageCode: "en", CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			const body = "<p>Keep my submitted text &amp; formatting.</p>"
			target := "/admin/pages"
			form := url.Values{
				"title": {"Submitted title"}, "slug": {"submitted-title"}, "body": {body},
				"status": {"draft"}, "language_code": {"en"}, "page_type": {"post"},
				"tags[]": {strconv.FormatInt(tag.ID, 10), "9999"},
			}
			if operation == "update" {
				page, err := pagewrite.Insert(ctx, q, provenance.Principal{UserID: user.ID, Source: "human"}, store.CreatePageParams{
					Title: "Original title", Slug: "submitted-title", Body: "<p>Original text.</p>",
					Status: "draft", LanguageCode: "en", PageType: "post", AuthorID: user.ID, CreatedAt: now, UpdatedAt: now,
				}, nil)
				if err != nil {
					t.Fatal(err)
				}
				target += "/" + strconv.FormatInt(page.ID, 10)
				form.Set("base_revision", strconv.FormatInt(page.Revision, 10))
			}
			post := func() *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rr := httptest.NewRecorder()
				router.ServeHTTP(rr, withUser(req, user))
				return rr
			}
			assertUnchanged := func() {
				t.Helper()
				page, err := q.GetPageBySlug(ctx, "submitted-title")
				if operation == "create" {
					if !errors.Is(err, sql.ErrNoRows) {
						t.Fatalf("refused create left a page: %v", err)
					}
				} else if err != nil || page.Title != "Original title" || page.Revision != 1 {
					t.Fatalf("refused update changed the page: %v", err)
				}
			}
			rr := post()
			rendered := html.UnescapeString(rr.Body.String())
			if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rendered, body) ||
				!strings.Contains(rendered, i18n.T("en", "pages.conflict_missing_tag", 9999)) ||
				!strings.Contains(rendered, tag.Name) {
				t.Errorf("missing tag: status %d, want 422 with the reason, body and valid tag", rr.Code)
			}
			assertUnchanged()
			form["tags[]"] = form["tags[]"][:1]
			if _, err = db.Exec(`CREATE TRIGGER fail_form_assignment BEFORE INSERT ON page_tags
				BEGIN SELECT RAISE(ABORT, 'fixture private error'); END`); err != nil {
				t.Fatal(err)
			}
			rr = post()
			rendered = html.UnescapeString(rr.Body.String())
			if rr.Code != http.StatusServiceUnavailable || rr.Header().Get("Cache-Control") != "no-store" ||
				!strings.Contains(rendered, body) || !strings.Contains(rendered, `action="`+target+`"`) ||
				!strings.Contains(rendered, `name="tags[]"`) || strings.Contains(rendered, "fixture private error") {
				t.Errorf("storage fault: status %d, want 503 with safe retry and the submitted form", rr.Code)
			}
			assertUnchanged()
			if _, err = db.Exec("DROP TRIGGER fail_form_assignment"); err != nil {
				t.Fatal(err)
			}
			if rr := post(); rr.Code != http.StatusSeeOther {
				t.Fatalf("retry status %d, want successful redirect", rr.Code)
			}
			page, err := q.GetPageBySlug(ctx, "submitted-title")
			if err != nil || page.Title != "Submitted title" || page.Body != body {
				t.Fatalf("retry failed to save the original submission: %v", err)
			}
			tags, err := q.GetTagsForPage(ctx, page.ID)
			if err != nil || len(tags) != 1 || tags[0].ID != tag.ID {
				t.Fatalf("retry lost the valid tag: %v", err)
			}
		})
	}
}

// TestRefusedPageWritesExplainWhy checks that the admin editor tells an
// editor why a page write was refused instead of "Error updating page", keeps
// the edits on a refused save, and never shows raw error text (2026-11-03
// review R2-3, S5, S6).
//
// Bug state: drop the pageWriteMessage handling from Update, TogglePublish
// or RestoreVersion, or flash err.Error() again, and this fails.
func TestRefusedPageWritesExplainWhy(t *testing.T) {
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
	var flash string
	adminLang := "en"
	router := chi.NewRouter()
	router.Use(sm.LoadAndSave)
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			renderer.SetAdminLang(r, adminLang)
			next.ServeHTTP(w, withUser(r, user))
			flash = sm.GetString(r.Context(), "flash")
		})
	})
	router.Post("/admin/pages/{id}", h.Update)
	router.Post("/admin/pages/{id}/publish", h.TogglePublish)
	router.Post("/admin/pages/{id}/versions/{versionId}/restore", h.RestoreVersion)
	post := func(target string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		flash = ""
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}
	ctx := context.Background()
	queries := store.New(db)
	now := time.Now()
	aiPage := func(title, slug, status string) store.Page {
		t.Helper()
		p, insertErr := pagewrite.Insert(ctx, queries, provenance.Principal{UserID: user.ID, Source: "api", APIKeyID: 1}, store.CreatePageParams{
			Title: title, Slug: slug, Body: "<p>Body</p>", Status: status, AuthorID: user.ID, LanguageCode: "en",
			PageType: "post", CreatedAt: now, UpdatedAt: now,
		}, func(tx *pagewrite.Tx, p store.Page) error {
			tx.SetEvidence(provenance.Evidence{Validated: []byte(`{"site_url":"https://example.com"}`)})
			return tx.Q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: p.ID, AiEditable: true, SetBy: user.ID, SetAt: now})
		})
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		return p
	}
	checks := i18n.T("en", "pages.error_publication_checks")
	changed := i18n.T("en", "pages.error_changed_meanwhile")

	// Update: a summary over the draft contract's limit is refused with the
	// reason, and the form comes back with the editor's changes.
	draft := aiPage("Valid title", "ai-update", "draft")
	rr := post("/admin/pages/"+strconv.FormatInt(draft.ID, 10), url.Values{
		"title": {"Edited title"}, "slug": {draft.Slug}, "body": {"<p>Body</p>"}, "summary": {strings.Repeat("s", 501)},
		"status": {"published"}, "language_code": {"en"}, "page_type": {"post"},
		"base_revision": {strconv.FormatInt(draft.Revision, 10)}, "ai_access_present": {"1"}, "allow_ai_edits": {"on"},
	})
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), checks) || !strings.Contains(rr.Body.String(), "Edited title") {
		t.Errorf("update: status %d, want 422 with the reason and the edited title", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `id="allow_ai_edits"`) || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/html") {
		t.Errorf("update: the re-rendered form lost the AI-access control or its content type %q", rr.Header().Get("Content-Type"))
	}
	if stored, loadErr := queries.GetPageByID(ctx, draft.ID); loadErr != nil || stored.Status != "draft" || stored.Title != "Valid title" {
		t.Errorf("update: refused publish changed the page: %+v %v", stored, loadErr)
	}

	// TogglePublish: a contract failure and a stale revision each say why.
	invalid := aiPage("", "ai-toggle", "draft")
	toggle := func(revision int64) {
		t.Helper()
		if rr := post(fmt.Sprintf("/admin/pages/%d/publish", invalid.ID), url.Values{"base_revision": {strconv.FormatInt(revision, 10)}}); rr.Code != http.StatusSeeOther {
			t.Fatalf("toggle: status %d", rr.Code)
		}
	}
	toggle(invalid.Revision)
	if flash != checks {
		t.Errorf("toggle invalid AI draft: flash %q, want %q", flash, checks)
	}
	toggle(invalid.Revision + 5)
	if flash != changed {
		t.Errorf("toggle stale revision: flash %q, want %q", flash, changed)
	}

	// RestoreVersion: the reason is translated, never the raw error text.
	published := aiPage("Published title", "ai-restore", "published")
	bad, err := queries.CreatePageVersion(ctx, store.CreatePageVersionParams{PageID: published.ID, Title: "", Body: "<p>Empty title</p>", ChangedBy: user.ID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	post(fmt.Sprintf("/admin/pages/%d/versions/%d/restore", published.ID, bad.ID), url.Values{"base_revision": {strconv.FormatInt(published.Revision, 10)}})
	if want := i18n.T("en", "pages.error_version_not_restored") + " " + checks; flash != want {
		t.Errorf("restore: flash %q, want %q", flash, want)
	}

	// A restore against a stale revision is told the page changed meanwhile:
	// the restore's conflict message (the editor's full conflict form belongs to
	// the edit screen, which a restore does not come from).
	post(fmt.Sprintf("/admin/pages/%d/versions/%d/restore", published.ID, bad.ID), url.Values{"base_revision": {strconv.FormatInt(published.Revision+5, 10)}})
	if want := i18n.T("en", "pages.error_version_not_restored") + " " + changed; flash != want {
		t.Errorf("restore with a stale revision: flash %q, want %q", flash, want)
	}

	// A deleted tag in a complete snapshot is explained in the editor's UI language.
	tag, err := queries.CreateTag(ctx, store.CreateTagParams{Name: "Recorded tag", Slug: "recorded-tag", LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	work, err := pagewrite.InTx(ctx, db, provenance.Principal{UserID: user.ID, Source: "human"}, func(work *pagewrite.Tx) error {
		if _, trackErr := work.Track(ctx, draft.ID, &draft.Revision, true); trackErr != nil {
			return trackErr
		}
		return work.Q.AddTagToPage(ctx, store.AddTagToPageParams{PageID: draft.ID, TagID: tag.ID})
	})
	if err != nil {
		t.Fatal(err)
	}
	current := work.Pages[draft.ID]
	saved, err := queries.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{PageID: current.ID, Revision: sql.NullInt64{Int64: current.Revision, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err = queries.DeleteTag(ctx, tag.ID); err != nil {
		t.Fatal(err)
	}
	if count, countErr := queries.CountPagesForTag(ctx, tag.ID); countErr != nil || count != 0 {
		t.Errorf("deleted tag still has %d page links, want 0: %v", count, countErr)
	}
	var constraint sqlite3.Error
	addErr := queries.AddTagToPage(ctx, store.AddTagToPageParams{PageID: published.ID, TagID: tag.ID})
	if !errors.As(addErr, &constraint) || constraint.ExtendedCode != sqlite3.ErrConstraintForeignKey {
		t.Errorf("linking a page to a deleted tag returned %v, want a foreign key violation", addErr)
	}
	for lang, reason := range map[string]string{"en": "Choose another version.", "ru": "Выберите другую версию."} {
		adminLang = lang
		response := post(fmt.Sprintf("/admin/pages/%d/versions/%d/restore", current.ID, saved.ID), url.Values{"base_revision": {strconv.FormatInt(current.Revision, 10)}})
		if response.Code != http.StatusSeeOther || !strings.Contains(flash, reason) || !strings.Contains(flash, strconv.FormatInt(tag.ID, 10)) {
			t.Errorf("restore with deleted tag in %s: status=%d flash=%q", lang, response.Code, flash)
		}
	}
}

// TestPageWriteMessageExplainsOnlyRefusals pins which page-write errors the
// editor is told about: typed refusals and known conditions get their text,
// anything else (a storage fault) is not shown.
func TestPageWriteMessageExplainsOnlyRefusals(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"refusal", fmt.Errorf("restore: %w", &pagewrite.Refusal{Reason: "recorded alias is occupied"}), "recorded alias is occupied"},
		{"deleted account", fmt.Errorf("%w: %w", pagewrite.ErrEditorRequired, sql.ErrNoRows), i18n.T("en", "pages.error_editor_required")},
		{"translation", pagewrite.ErrTranslationExists, i18n.T("en", "pages.error_translation_exists")},
		{"image size", &pagewrite.FeaturedImageSizeError{Width: 10, Height: 10}, i18n.T("en", "pages.error.featured_image_too_small", model.MinFeaturedImageWidth, model.MinFeaturedImageHeight, 10, 10)},
		{"site url", fmt.Errorf("%w: bad", review.ErrSiteURLRequired), i18n.T("en", "pages.error_site_url_required")},
	} {
		if got, ok := pageWriteMessage("en", tc.err); !ok || got != tc.want {
			t.Errorf("%s: got %q %v, want %q", tc.name, got, ok, tc.want)
		}
	}
	if got, ok := pageWriteMessage("en", errors.New("database is locked")); ok {
		t.Errorf("storage fault explained as %q", got)
	}
}
