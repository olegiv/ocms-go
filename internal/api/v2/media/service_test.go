// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package media_test

import (
	"context"
	"errors"
	"testing"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/media"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func newTestService(t *testing.T) (*media.Service, func()) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	return media.NewService(db, store.New(db), t.TempDir()), cleanup
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
