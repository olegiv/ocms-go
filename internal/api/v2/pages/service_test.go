// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages_test

import (
	"context"
	"errors"
	"testing"

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
