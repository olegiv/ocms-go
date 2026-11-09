// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package media_test

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/media"
	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/webhook"
)

// TestMediaDeleteRunsPageSideEffects checks that deleting media through the
// REST API tells the pages it clears the image from what every other page
// write tells them: their cached copies are dropped and the page.updated
// webhook is sent, once for each page that gained a revision and for no other
// (2026-11-03 review R2 P3-8: the delete revisioned those pages and did neither).
//
// Bug state: drop the s.pagesChanged call from Service.Delete and the cache
// still serves the page with its deleted image and no event is sent.
func TestMediaDeleteRunsPageSideEffects(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := media.NewService(db, queries, nil, t.TempDir())
	recorder := &testutil.WebhookRecorder{}
	svc.SetWebhookDispatcher(recorder)
	pages := cache.NewManager(queries)
	svc.SetCacheManager(pages)
	ctx := context.Background()
	now := time.Now()
	owner, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "media-pages@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "Media", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := v2.Actor{APIKey: &store.ApiKey{ID: 1, CreatedBy: owner.ID}, Permissions: []string{model.PermissionMediaWrite}}
	uploaded, err := svc.Upload(ctx, writer, media.UploadMediaMetadata{}, media.UploadedFile{
		Reader: memoryFile{bytes.NewReader(pngBytes(t))}, Filename: "dot.png", Size: int64(len(pngBytes(t))), ContentType: "image/png",
	})
	if err != nil {
		t.Fatal(err)
	}
	used := sql.NullInt64{Int64: uploaded.ID, Valid: true}
	insert := func(slug, status string, featured, og sql.NullInt64) store.Page {
		t.Helper()
		page, insertErr := queries.CreatePage(ctx, store.CreatePageParams{
			Title: slug, Slug: slug, Body: "<p>body</p>", Status: status, AuthorID: owner.ID, LanguageCode: "en", PageType: "post",
			FeaturedImageID: featured, OgImageID: og, CreatedAt: now, UpdatedAt: now,
			PublishedAt: sql.NullTime{Time: now, Valid: status == "published"},
		})
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		return page
	}
	published := insert("published-with-image", "published", used, sql.NullInt64{})
	draft := insert("draft-with-og", "draft", sql.NullInt64{}, used)
	unrelated := insert("unrelated", "published", sql.NullInt64{}, sql.NullInt64{})

	anonymous := cache.Context{LanguageCode: "en", Role: "anonymous"}
	warm, err := pages.Page.GetBySlug(ctx, anonymous, published.Slug)
	if err != nil || !warm.FeaturedImageID.Valid {
		t.Fatalf("warming the page cache: %+v %v", warm, err)
	}
	if _, err = pages.Page.GetBySlug(ctx, anonymous, unrelated.Slug); err != nil {
		t.Fatal(err)
	}

	if err = svc.Delete(ctx, writer, uploaded.ID); err != nil {
		t.Fatal(err)
	}

	fresh, err := pages.Page.GetBySlug(ctx, anonymous, published.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.FeaturedImageID.Valid {
		t.Errorf("the page cache still serves %q with its deleted featured image %v", fresh.Slug, fresh.FeaturedImageID)
	}
	updated := make(map[int64]int)
	for _, event := range recorder.Events() {
		if event.Type != model.EventPageUpdated {
			continue
		}
		data, ok := event.Data.(webhook.PageEventData)
		if !ok {
			t.Fatalf("page.updated carried %T", event.Data)
		}
		updated[data.ID]++
		if data.AuthorEmail != "" {
			t.Errorf("an API key's page.updated carries author_email %q, like the pages service", data.AuthorEmail)
		}
	}
	if len(updated) != 2 || updated[published.ID] != 1 || updated[draft.ID] != 1 {
		t.Errorf("page.updated sent for %v, want exactly once each for pages %d and %d, not for %d", updated, published.ID, draft.ID, unrelated.ID)
	}
	if last := recorder.Events()[len(recorder.Events())-1]; last.Type != model.EventMediaDeleted {
		t.Errorf("last event = %q, want %q after the page events", last.Type, model.EventMediaDeleted)
	}
}

// pngBytes returns a 4x4 PNG, small enough to upload in a test.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
