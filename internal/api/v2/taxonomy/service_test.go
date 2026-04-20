// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package taxonomy_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/taxonomy"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func newTestService(t *testing.T) (*taxonomy.Service, func()) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	return taxonomy.NewService(db, store.New(db), nil), cleanup
}

func writerActor(t *testing.T) v2.Actor {
	t.Helper()
	perms, err := json.Marshal([]string{model.PermissionTaxonomyWrite})
	if err != nil {
		t.Fatalf("marshal perms: %v", err)
	}
	key := &store.ApiKey{ID: 1, Name: "test", KeyPrefix: "abcd", Permissions: string(perms), IsActive: true}
	return v2.Actor{APIKey: key, Permissions: []string{model.PermissionTaxonomyWrite}}
}

func TestCreateTagWritesThrough(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	got, err := svc.CreateTag(context.Background(), writerActor(t), taxonomy.CreateTagBody{
		Name: "Go", Slug: "go",
	})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if got.ID == 0 || got.Slug != "go" || got.Name != "Go" {
		t.Fatalf("unexpected tag: %+v", got)
	}
	if got.LanguageCode == "" {
		t.Error("expected LanguageCode to resolve to the seeded default, got empty")
	}
}

func TestCreateTagRejectsDuplicateSlug(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	actor := writerActor(t)
	if _, err := svc.CreateTag(context.Background(), actor, taxonomy.CreateTagBody{Name: "Go", Slug: "go"}); err != nil {
		t.Fatalf("first CreateTag: %v", err)
	}
	_, err := svc.CreateTag(context.Background(), actor, taxonomy.CreateTagBody{Name: "Golang", Slug: "go"})
	var domainErr *v2.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected *v2.Error, got %T: %v", err, err)
	}
	if domainErr.Kind != v2.ErrValidation {
		t.Errorf("expected validation error, got kind=%d", domainErr.Kind)
	}
	if _, ok := domainErr.Fields["slug"]; !ok {
		t.Errorf("expected 'slug' field in error, got: %+v", domainErr.Fields)
	}
}

func TestCreateTagRequiresWritePermission(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	tests := map[string]v2.Actor{
		"anonymous":   {},
		"readOnlyKey": {APIKey: &store.ApiKey{ID: 2}, Permissions: []string{model.PermissionTaxonomyRead}},
	}
	for name, actor := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := svc.CreateTag(context.Background(), actor, taxonomy.CreateTagBody{Name: "Go", Slug: "go"})
			var de *v2.Error
			if !errors.As(err, &de) {
				t.Fatalf("want *v2.Error, got %T", err)
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

func TestListTagsPagination(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	actor := writerActor(t)
	for _, slug := range []string{"go", "rust", "zig"} {
		if _, err := svc.CreateTag(context.Background(), actor, taxonomy.CreateTagBody{Name: slug, Slug: slug}); err != nil {
			t.Fatalf("CreateTag %q: %v", slug, err)
		}
	}

	res, err := svc.ListTags(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if res.Total != 3 {
		t.Errorf("expected total=3, got %d", res.Total)
	}
	if len(res.Tags) != 2 {
		t.Errorf("expected 2 tags on page 1, got %d", len(res.Tags))
	}
	if res.Page != 1 || res.PerPage != 2 {
		t.Errorf("expected page=1 per_page=2, got page=%d per_page=%d", res.Page, res.PerPage)
	}
}

func TestListCategoriesTreeShape(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	actor := writerActor(t)
	parent, err := svc.CreateCategory(context.Background(), actor, taxonomy.CreateCategoryBody{Name: "Tech", Slug: "tech"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := svc.CreateCategory(context.Background(), actor, taxonomy.CreateCategoryBody{
		Name: "Go", Slug: "go", ParentID: &parent.ID,
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}

	tree, err := svc.ListCategories(context.Background(), true)
	if err != nil {
		t.Fatalf("ListCategories(tree): %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("expected 1 root in tree, got %d: %+v", len(tree), tree)
	}
	if len(tree[0].Children) != 1 || tree[0].Children[0].Slug != "go" {
		t.Errorf("expected one child 'go' under 'tech', got %+v", tree[0].Children)
	}

	flat, err := svc.ListCategories(context.Background(), false)
	if err != nil {
		t.Fatalf("ListCategories(flat): %v", err)
	}
	if len(flat) != 2 {
		t.Fatalf("expected 2 categories in flat list, got %d", len(flat))
	}
}

func TestDeleteCategoryRejectsIfChildrenExist(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	actor := writerActor(t)
	parent, err := svc.CreateCategory(context.Background(), actor, taxonomy.CreateCategoryBody{Name: "Tech", Slug: "tech"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := svc.CreateCategory(context.Background(), actor, taxonomy.CreateCategoryBody{
		Name: "Go", Slug: "go", ParentID: &parent.ID,
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}

	err = svc.DeleteCategory(context.Background(), actor, parent.ID)
	var de *v2.Error
	if !errors.As(err, &de) {
		t.Fatalf("expected *v2.Error, got %T: %v", err, err)
	}
	if de.Kind != v2.ErrConflict {
		t.Errorf("expected Conflict (409), got kind=%d: %s", de.Kind, de.Msg)
	}
}
