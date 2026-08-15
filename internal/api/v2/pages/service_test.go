// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages_test

import (
	"context"
	"errors"
	"testing"
	"time"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func newTestService(t *testing.T) (*pages.Service, func()) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	return pages.NewService(db, store.New(db), nil, nil, pages.Policy{}), cleanup
}

func TestListPagesEmptyShowsOnlyPublishedWithoutReadPerm(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	res, err := svc.List(context.Background(), v2.Actor{}, pages.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 0 {
		t.Errorf("expected no pages, got total=%d", res.Total)
	}
	if res.Page != 1 || res.PerPage != 20 {
		t.Errorf("expected defaulted pagination, got page=%d per_page=%d", res.Page, res.PerPage)
	}
}

func TestListPagesRejectsDraftStatusWithoutReadPerm(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	_, err := svc.List(context.Background(), v2.Actor{}, pages.ListFilter{Status: model.PageStatusDraft})
	var de *v2.Error
	if !errors.As(err, &de) {
		t.Fatalf("want *v2.Error, got %T: %v", err, err)
	}
	if de.Kind != v2.ErrForbidden {
		t.Errorf("expected ErrForbidden, got kind=%d", de.Kind)
	}
}

func TestGetPageReturnsNotFound(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	_, err := svc.Get(context.Background(), v2.Actor{}, 99999, pages.ListFilter{})
	var de *v2.Error
	if !errors.As(err, &de) {
		t.Fatalf("want *v2.Error, got %T: %v", err, err)
	}
	if de.Kind != v2.ErrNotFound {
		t.Errorf("expected ErrNotFound, got kind=%d", de.Kind)
	}
}

func TestCreatePageRequiresWritePermission(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	tests := map[string]v2.Actor{
		"anonymous":   {},
		"readOnlyKey": {APIKey: &store.ApiKey{ID: 1}, Permissions: []string{model.PermissionPagesRead}},
	}
	for name, actor := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), actor, pages.CreatePageBody{
				Title: "T", Slug: "t", Body: "b",
			})
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

func TestDeletePageReturnsNotFoundForUnknownID(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	writer := v2.Actor{
		APIKey:      &store.ApiKey{ID: 1},
		Permissions: []string{model.PermissionPagesWrite},
	}
	err := svc.Delete(context.Background(), writer, 99999)
	var de *v2.Error
	if !errors.As(err, &de) {
		t.Fatalf("want *v2.Error, got %T: %v", err, err)
	}
	if de.Kind != v2.ErrNotFound {
		t.Errorf("expected ErrNotFound, got kind=%d", de.Kind)
	}
}

// TestCreateAndUpdateRejectSlugsShadowedByLanguagePrefix covers the API half of
// a cross-namespace collision the admin form already refuses.
//
// The language middleware strips a leading segment matching an active language
// code before the frontend router runs, so a page written at that segment is
// answered by the language homepage and never by itself. Slug validation here
// checks format and page/alias uniqueness, neither of which sees a language, so
// an API client could create or rename a page straight into oblivion.
func TestCreateAndUpdateRejectSlugsShadowedByLanguagePrefix(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := pages.NewService(db, queries, nil, nil, pages.Policy{})
	ctx := context.Background()
	now := time.Now()

	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "eng", Name: "English (legacy)", NativeName: "English", IsActive: true,
		Direction: "ltr", Position: 3, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateLanguage: %v", err)
	}
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "api@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "API",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	writer := v2.Actor{
		APIKey:      &store.ApiKey{ID: 1, CreatedBy: author.ID},
		Permissions: []string{model.PermissionPagesWrite},
	}

	_, err = svc.Create(ctx, writer, pages.CreatePageBody{Title: "Engineering", Slug: "eng", Body: "b"})
	var de *v2.Error
	if !errors.As(err, &de) || de.Kind != v2.ErrValidation {
		t.Fatalf("Create() error = %v, want a validation error for the language prefix", err)
	}

	created, err := svc.Create(ctx, writer, pages.CreatePageBody{Title: "Engineering", Slug: "engineering", Body: "b"})
	if err != nil {
		t.Fatalf("Create() error = %v, want a free slug to be accepted", err)
	}
	shadowed := "eng"
	_, err = svc.Update(ctx, writer, created.ID, pages.UpdatePageBody{Slug: &shadowed})
	if !errors.As(err, &de) || de.Kind != v2.ErrValidation {
		t.Fatalf("Update() error = %v, want a validation error for the language prefix", err)
	}
	unchanged, err := queries.GetPageByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Slug != "engineering" {
		t.Fatalf("stored slug = %q, want the rejected rename not to have landed", unchanged.Slug)
	}
}
