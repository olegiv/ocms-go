// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/store"
)

// TestPageFormWritersRequireWriteContext prevents the guarded form writers from
// regaining an optional editor/revision context, including through their shared
// write boundary. Refusals must leave content, history and provenance unchanged.
func TestPageFormWritersRequireWriteContext(t *testing.T) {
	t.Run("required parameters", func(t *testing.T) {
		for name, method := range map[string]any{
			"create": (*PagesHandler).createPageGuarded,
			"update": (*PagesHandler).updatePageGuarded,
		} {
			signature := reflect.TypeOf(method)
			if signature.IsVariadic() || signature.In(signature.NumIn()-1) != reflect.TypeFor[pageFormWrite]() {
				t.Errorf("%s must require a pageFormWrite argument", name)
			}
		}
	})

	for _, writer := range []string{"create", "update", "shared"} {
		for _, role := range []string{"admin", "editor", "public", "missing", "zero", "negative"} {
			t.Run(writer+"/"+role, func(t *testing.T) {
				db, sm := testHandlerSetup(t)
				owner := createTestAdminUser(t, db)
				actor := createTestUser(t, db, testUser{Email: "actor@example.com", Name: "Actor", Role: "editor"})
				actorID := actor.ID
				switch role {
				case "admin", "public":
					if _, err := db.ExecContext(t.Context(), "UPDATE users SET role = ? WHERE id = ?", role, actorID); err != nil {
						t.Fatal(err)
					}
				case "missing":
					if _, err := db.ExecContext(t.Context(), "DELETE FROM users WHERE id = ?", actorID); err != nil {
						t.Fatal(err)
					}
				case "zero":
					actorID = 0
				case "negative":
					actorID = -1
				}
				h := NewPagesHandler(db, nil, sm)
				q := store.New(db)
				now := time.Now().UTC()
				params := store.CreatePageParams{Title: "Original", Slug: "form-write", Body: "Body", Status: "draft", AuthorID: owner.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now}
				original, err := q.CreatePage(t.Context(), params)
				if err != nil {
					t.Fatal(err)
				}
				state := func() string {
					t.Helper()
					var result string
					if err := db.QueryRowContext(t.Context(), `SELECT
						(SELECT COUNT(*) FROM pages) || ':' ||
						(SELECT COUNT(*) FROM page_versions) || ':' ||
						(SELECT COUNT(*) FROM content_provenance)`).Scan(&result); err != nil {
						t.Fatal(err)
					}
					snapshot, err := q.GetPageSnapshot(t.Context(), original.ID)
					if err != nil {
						t.Fatal(err)
					}
					return result + snapshot
				}
				before := state()
				relatedCalled := false
				option := pageFormWrite{UserID: actorID, BaseRevision: &original.Revision, Related: func(*pagewrite.Tx, store.Page) error {
					relatedCalled = true
					return nil
				}}
				var saved store.Page
				switch writer {
				case "create":
					params.Slug = "created-form-write"
					saved, err = h.createPageGuarded(t.Context(), params, option)
				case "update":
					update := pagewrite.ParamsFor(original)
					update.Title = "Updated"
					saved, err = h.updatePageGuarded(t.Context(), update, original.Slug, option)
				case "shared":
					params.Slug = "shared-form-write"
					saved, err = h.writePageGuarded(t.Context(), params.Slug, "", option, func(work *pagewrite.Tx) (store.Page, error) {
						page, createErr := work.Q.CreatePage(t.Context(), params)
						if createErr == nil {
							work.Created(page)
						}
						return page, createErr
					})
				}
				if role != "admin" && role != "editor" {
					if !errors.Is(err, pagewrite.ErrEditorRequired) || relatedCalled || state() != before {
						t.Fatalf("unauthorized write: error=%v, related=%v, state unchanged=%v", err, relatedCalled, state() == before)
					}
					return
				}
				if err != nil || !relatedCalled || saved.AuthorID != owner.ID {
					t.Fatalf("valid editor write: page=%+v, error=%v, related=%v", saved, err, relatedCalled)
				}
				record, err := q.GetContentProvenance(t.Context(), store.GetContentProvenanceParams{PageID: saved.ID, Revision: saved.Revision})
				if err != nil || !record.UserID.Valid || record.UserID.Int64 != actorID || record.Source != "human" || record.Transport != "admin" {
					t.Fatalf("write provenance: %+v, error=%v", record, err)
				}
			})
		}
	}

	t.Run("revision and related work", func(t *testing.T) {
		db, sm := testHandlerSetup(t)
		user := createTestAdminUser(t, db)
		h := NewPagesHandler(db, nil, sm)
		q := store.New(db)
		now := time.Now().UTC()
		page, err := h.createPageGuarded(t.Context(), store.CreatePageParams{Title: "Original", Slug: "form-cas", Body: "Body", Status: "draft", AuthorID: user.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now}, pageFormWrite{UserID: user.ID})
		if err != nil {
			t.Fatal(err)
		}
		params := pagewrite.ParamsFor(page)
		params.Title = "Updated"
		_, err = h.updatePageGuarded(t.Context(), params, page.Slug, pageFormWrite{UserID: user.ID})
		if !errors.Is(err, pagewrite.ErrRevisionRequired) {
			t.Fatalf("missing revision: %v", err)
		}
		stale := page.Revision - 1
		_, err = h.updatePageGuarded(t.Context(), params, page.Slug, pageFormWrite{UserID: user.ID, BaseRevision: &stale})
		var conflict *pagewrite.Conflict
		if !errors.As(err, &conflict) || conflict.CurrentRevision != page.Revision {
			t.Fatalf("stale revision: %v", err)
		}
		refusal := errors.New("related write refused")
		_, err = h.updatePageGuarded(t.Context(), params, page.Slug, pageFormWrite{UserID: user.ID, BaseRevision: &page.Revision, Related: func(*pagewrite.Tx, store.Page) error { return refusal }})
		if !errors.Is(err, refusal) {
			t.Fatalf("related write refusal: %v", err)
		}
		current, err := q.GetPageByID(t.Context(), page.ID)
		if err != nil || current.Title != page.Title || current.Revision != page.Revision {
			t.Fatalf("refused write changed page: %+v, error=%v", current, err)
		}
		var versions, records int
		if err := db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM page_versions), (SELECT COUNT(*) FROM content_provenance)`).Scan(&versions, &records); err != nil {
			t.Fatal(err)
		}
		if versions != 1 || records != 1 {
			t.Fatalf("refused write changed history: versions=%d, provenance=%d", versions, records)
		}
		saved, err := h.updatePageGuarded(t.Context(), params, page.Slug, pageFormWrite{UserID: user.ID, BaseRevision: &page.Revision})
		if err != nil || saved.Title != params.Title || saved.Revision != page.Revision+1 {
			t.Fatalf("matching revision: %+v, error=%v", saved, err)
		}
	})
}
