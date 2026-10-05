// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package media_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/media"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func newTestService(t *testing.T) (*media.Service, func()) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	return media.NewService(db, store.New(db), nil, t.TempDir()), cleanup
}

func TestListMediaEmpty(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	res, err := svc.List(context.Background(), v2.Actor{}, media.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 0 || len(res.Media) != 0 {
		t.Errorf("expected empty result, got total=%d len=%d", res.Total, len(res.Media))
	}
	if res.Page != 1 || res.PerPage != 20 {
		t.Errorf("expected defaulted pagination page=1 per_page=20, got page=%d per_page=%d", res.Page, res.PerPage)
	}
}

func TestListMediaSearchPagination(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := media.NewService(db, queries, nil, t.TempDir())
	ctx := context.Background()
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	uploader, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "media@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "Media",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	var ids []int64
	for _, seed := range []struct {
		filename string
		alt      string
		created  time.Time
	}{
		{"needle-filename.png", "", now},
		{"alt-only.png", "needle alt", now.Add(time.Hour)},
		{"needle-both.png", "needle both", now.Add(time.Hour)},
		{"unmatched.png", "unmatched", now.Add(2 * time.Hour)},
	} {
		row, err := queries.CreateMedia(ctx, store.CreateMediaParams{
			Uuid: seed.filename, Filename: seed.filename, MimeType: "image/png", Size: 1,
			Alt: sql.NullString{String: seed.alt, Valid: true}, UploadedBy: uploader.ID,
			LanguageCode: "en", CreatedAt: seed.created, UpdatedAt: seed.created,
		})
		if err != nil {
			t.Fatalf("CreateMedia(%q): %v", seed.filename, err)
		}
		ids = append(ids, row.ID)
	}

	for _, tt := range []struct {
		name    string
		filter  media.ListFilter
		wantIDs []int64
		total   int64
		page    int
		perPage int
	}{
		{"first_page", media.ListFilter{Search: "needle", Page: 1, PerPage: 2}, []int64{ids[2], ids[1]}, 3, 1, 2},
		{"second_page", media.ListFilter{Search: "needle", Page: 2, PerPage: 2}, []int64{ids[0]}, 3, 2, 2},
		{"beyond_last_page", media.ListFilter{Search: "needle", Page: 3, PerPage: 2}, nil, 3, 3, 2},
		{"no_matches", media.ListFilter{Search: "absent", Page: 1, PerPage: 2}, nil, 0, 1, 2},
		{"default_paging", media.ListFilter{Search: "needle"}, []int64{ids[2], ids[1], ids[0]}, 3, 1, 20},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res, err := svc.List(ctx, v2.Actor{}, tt.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			gotIDs := make([]int64, len(res.Media))
			for i, item := range res.Media {
				gotIDs[i] = item.ID
			}
			if !slices.Equal(gotIDs, tt.wantIDs) {
				t.Errorf("IDs = %v, want %v", gotIDs, tt.wantIDs)
			}
			if res.Total != tt.total || res.Page != tt.page || res.PerPage != tt.perPage {
				t.Errorf("pagination = (%d, %d, %d), want total=%d page=%d per_page=%d",
					res.Total, res.Page, res.PerPage, tt.total, tt.page, tt.perPage)
			}
		})
	}

	t.Run("legacy_search_without_offset", func(t *testing.T) {
		rows, err := queries.SearchMedia(ctx, store.SearchMediaParams{
			Filename: "%needle%", Alt: sql.NullString{String: "%needle%", Valid: true}, Limit: 2,
		})
		if err != nil {
			t.Fatalf("SearchMedia: %v", err)
		}
		gotIDs := make([]int64, len(rows))
		for i, row := range rows {
			gotIDs[i] = row.ID
		}
		if wantIDs := []int64{ids[2], ids[1]}; !slices.Equal(gotIDs, wantIDs) {
			t.Errorf("IDs = %v, want %v", gotIDs, wantIDs)
		}
	})
}

func TestListMediaRejectsInvalidType(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	_, err := svc.List(context.Background(), v2.Actor{}, media.ListFilter{Type: "bogus"})
	var de *v2.Error
	if !errors.As(err, &de) {
		t.Fatalf("want *v2.Error, got %T: %v", err, err)
	}
	if de.Kind != v2.ErrValidation {
		t.Errorf("expected ErrValidation, got kind=%d", de.Kind)
	}
	if _, ok := de.Fields["type"]; !ok {
		t.Errorf("expected 'type' in fields, got %+v", de.Fields)
	}
}

func TestGetMediaNotFound(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	_, err := svc.Get(context.Background(), v2.Actor{}, 99999, media.ListFilter{})
	var de *v2.Error
	if !errors.As(err, &de) {
		t.Fatalf("want *v2.Error, got %T: %v", err, err)
	}
	if de.Kind != v2.ErrNotFound {
		t.Errorf("expected ErrNotFound, got kind=%d", de.Kind)
	}
}

func TestDeleteMediaWithoutAuth(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	tests := map[string]v2.Actor{
		"anonymous":   {},
		"readOnlyKey": {APIKey: &store.ApiKey{ID: 1}, Permissions: []string{model.PermissionMediaRead}},
	}
	for name, actor := range tests {
		t.Run(name, func(t *testing.T) {
			err := svc.Delete(context.Background(), actor, 1)
			var de *v2.Error
			if !errors.As(err, &de) {
				t.Fatalf("want *v2.Error, got %T: %v", err, err)
			}
			if actor.APIKey == nil && de.Kind != v2.ErrUnauthorized {
				t.Errorf("anonymous caller should get Unauthorized, got %d", de.Kind)
			}
			if actor.APIKey != nil && de.Kind != v2.ErrForbidden {
				t.Errorf("read-only caller should get Forbidden, got %d", de.Kind)
			}
		})
	}
}
