// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// testSetup contains common test dependencies.
type testSetup struct {
	DB      *sql.DB
	Queries *store.Queries
	Ctx     context.Context
	User    store.User
	Now     time.Time
	Cleanup func()
}

// setupTest creates common test dependencies: database, queries, context, and a test user.
func setupTest(t *testing.T) *testSetup {
	t.Helper()

	db, cleanup := testutil.TestDB(t)
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "test@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Test User",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		cleanup()
		t.Fatalf("failed to create user: %v", err)
	}

	return &testSetup{
		DB:      db,
		Queries: queries,
		Ctx:     ctx,
		User:    user,
		Now:     now,
		Cleanup: cleanup,
	}
}
