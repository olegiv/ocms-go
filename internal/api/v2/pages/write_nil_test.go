// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages

import (
	"context"
	"strings"
	"testing"
	"time"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestMissingMutationResultRollsBack(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	q := store.New(db)
	ctx := context.Background()
	user, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "nil-write@example.test", Name: "Before", PasswordHash: "unused",
		Role: model.RoleEditor, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := v2.ActorForUser(user, []string{model.PermissionPagesWrite})
	svc := NewService(db, q, nil, nil, Policy{})
	called := false
	result, err := svc.write(ctx, actor, WriteOptions{}, "create_page", nil,
		func(_ *Service, _ v2.Actor, work *pagewrite.Tx) (*Page, error) {
			called = true
			if _, execErr := work.Q.DBHandle().ExecContext(ctx, `UPDATE users SET name = 'After' WHERE id = ?`, user.ID); execErr != nil {
				return nil, execErr
			}
			return nil, nil
		})
	if !called || err == nil || !strings.Contains(err.Error(), "page mutation returned no result") || result != nil {
		t.Fatalf("write = %+v, %v; callback called = %v", result, err, called)
	}
	stored, err := q.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Before" {
		t.Fatal("missing mutation result committed its transaction")
	}
}
