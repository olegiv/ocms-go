// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"database/sql"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// TestEventDataFromStoreRows pins the payload fields the admin UI, the REST
// API and the scheduler now all build through NewPageEventData and
// NewMediaEventData, so dropping or reformatting one shows here.
func TestEventDataFromStoreRows(t *testing.T) {
	published := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	page := NewPageEventData(store.Page{
		ID: 7, Title: "Hello", Slug: "hello", Status: "published", AuthorID: 3,
		LanguageCode: "ru", PublishedAt: sql.NullTime{Time: published, Valid: true},
	}, "editor@example.com")
	if page.ID != 7 || page.Title != "Hello" || page.Slug != "hello" || page.Status != "published" || page.AuthorID != 3 || page.AuthorEmail != "editor@example.com" {
		t.Errorf("page event data = %+v", page)
	}
	if page.LanguageCode == nil || *page.LanguageCode != "ru" {
		t.Errorf("LanguageCode = %v, want ru", page.LanguageCode)
	}
	if page.PublishedAt == nil || *page.PublishedAt != "2026-10-18T09:30:00Z" {
		t.Errorf("PublishedAt = %v, want the RFC 3339 time", page.PublishedAt)
	}
	draft := NewPageEventData(store.Page{ID: 8}, "")
	if draft.LanguageCode != nil || draft.PublishedAt != nil || draft.AuthorEmail != "" {
		t.Errorf("an unpublished page without a language gave %+v, want no language, publication time or email", draft)
	}

	media := NewMediaEventData(store.Medium{ID: 5, Uuid: "u-1", Filename: "a.png", MimeType: "image/png", Size: 42, UploadedBy: 3})
	if media != (MediaEventData{ID: 5, UUID: "u-1", Filename: "a.png", MimeType: "image/png", Size: 42, UploaderID: 3}) {
		t.Errorf("media event data = %+v", media)
	}
}
