// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/webhook"
)

// TestMediaDeleteRunsPageSideEffects checks that deleting media in the admin
// library, one item or in bulk, tells the pages it clears the image from what
// every other page write tells them: their cached copies are dropped and the
// page.updated webhook is sent for each page that gained a revision, and for no
// other (2026-11-03 review R2 P3-8).
//
// Bug state: drop the h.pagesChanged call from recordMediaDelete and the cache
// still serves the page with its deleted image and no page event is sent.
func TestMediaDeleteRunsPageSideEffects(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	sm := testSessionManager(t)
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()
	admin := createTestUser(t, db, testUser{Email: "media-admin@example.com", Name: "Media Admin", Role: "admin"})

	pages := cache.NewManager(queries)
	recorder := &testutil.WebhookRecorder{}
	h := NewMediaHandler(db, nil, sm, t.TempDir())
	h.SetCacheManager(pages)
	h.dispatcher = recorder

	newMedia := func(uuid, name string) store.Medium {
		t.Helper()
		item, err := queries.CreateMedia(ctx, store.CreateMediaParams{
			Uuid: uuid, Filename: name, MimeType: "image/jpeg", Size: 100,
			UploadedBy: admin.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	newPage := func(slug string, featured, og int64) store.Page {
		t.Helper()
		page, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title: slug, Slug: slug, Body: "<p>body</p>", Status: "published", AuthorID: admin.ID, LanguageCode: "en", PageType: "post",
			FeaturedImageID: sql.NullInt64{Int64: featured, Valid: featured != 0}, OgImageID: sql.NullInt64{Int64: og, Valid: og != 0},
			CreatedAt: now, UpdatedAt: now, PublishedAt: sql.NullTime{Time: now, Valid: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	anonymous := cache.Context{LanguageCode: "en", Role: "anonymous"}
	warm := func(page store.Page) {
		t.Helper()
		cached, err := pages.Page.GetBySlug(ctx, anonymous, page.Slug)
		if err != nil || cached.ID != page.ID {
			t.Fatalf("warming the page cache for %q: %+v %v", page.Slug, cached, err)
		}
	}
	// pageEvents counts the page.updated events sent so far, per page, and the
	// email they name as the actor.
	pageEvents := func() (map[int64]int, map[string]bool) {
		t.Helper()
		counts, emails := make(map[int64]int), make(map[string]bool)
		for _, event := range recorder.Events() {
			if event.Type != model.EventPageUpdated {
				continue
			}
			data, ok := event.Data.(webhook.PageEventData)
			if !ok {
				t.Fatalf("page.updated carried %T", event.Data)
			}
			counts[data.ID]++
			emails[data.AuthorEmail] = true
		}
		return counts, emails
	}

	t.Run("single delete", func(t *testing.T) {
		item := newMedia("11111111-1111-4111-8111-111111111111", "one.jpg")
		withFeatured := newPage("with-featured", item.ID, 0)
		withOG := newPage("with-og", 0, item.ID)
		unrelated := newPage("unrelated", 0, 0)
		warm(withFeatured)
		warm(unrelated)

		id := fmt.Sprint(item.ID)
		req, w := newAuthenticatedDeleteRequest(t, sm, "/admin/media/"+id, map[string]string{"id": id}, &admin)
		req.Header.Set("HX-Request", "true")
		h.Delete(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("Delete: status %d, body %s", w.Code, w.Body.String())
		}

		fresh, err := pages.Page.GetBySlug(ctx, anonymous, withFeatured.Slug)
		if err != nil || fresh.FeaturedImageID.Valid {
			t.Errorf("the page cache still serves %q with its deleted image: %+v %v", withFeatured.Slug, fresh, err)
		}
		counts, emails := pageEvents()
		if len(counts) != 2 || counts[withFeatured.ID] != 1 || counts[withOG.ID] != 1 {
			t.Errorf("page.updated sent for %v, want once each for pages %d and %d, not %d", counts, withFeatured.ID, withOG.ID, unrelated.ID)
		}
		if !emails[admin.Email] || len(emails) != 1 {
			t.Errorf("page.updated named the actors %v, want the signed-in admin %q", emails, admin.Email)
		}
	})

	t.Run("bulk delete", func(t *testing.T) {
		first := newMedia("22222222-2222-4222-8222-222222222222", "two.jpg")
		second := newMedia("33333333-3333-4333-8333-333333333333", "three.jpg")
		usesFirst := newPage("uses-first", first.ID, 0)
		usesSecond := newPage("uses-second", 0, second.ID)
		warm(usesFirst)
		before, _ := pageEvents()

		req := httptest.NewRequest(http.MethodPost, "/admin/media/bulk-delete", strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d]}`, first.ID, second.ID)))
		req.Header.Set("Content-Type", "application/json")
		req = requestWithSession(sm, req)
		req = addUserToContext(req, &admin)
		w := httptest.NewRecorder()
		h.BulkDelete(w, req)
		resp := assertJSONResponse(t, w, http.StatusOK, true)
		if deleted, _ := resp["deleted"].(float64); int(deleted) != 2 {
			t.Fatalf("BulkDelete: %v", resp)
		}

		fresh, err := pages.Page.GetBySlug(ctx, anonymous, usesFirst.Slug)
		if err != nil || fresh.FeaturedImageID.Valid {
			t.Errorf("the page cache still serves %q with its deleted image: %+v %v", usesFirst.Slug, fresh, err)
		}
		after, _ := pageEvents()
		if after[usesFirst.ID]-before[usesFirst.ID] != 1 || after[usesSecond.ID]-before[usesSecond.ID] != 1 {
			t.Errorf("page.updated per page after the bulk delete = %v, want one each for pages %d and %d", after, usesFirst.ID, usesSecond.ID)
		}
	})
}
