// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package dbmanager

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestActiveByDefaultFalse fails when the SQL console goes back to being
// switched on in every fresh install (finding M-2, 2026-10-11).
func TestActiveByDefaultFalse(t *testing.T) {
	var m module.Module = New()
	defaulter, ok := m.(module.ActivationDefaulter)
	if !ok {
		t.Fatal("dbmanager must implement module.ActivationDefaulter")
	}
	if defaulter.ActiveByDefault() {
		t.Fatal("dbmanager must be inactive until an administrator enables it")
	}
}

// TestExecuteQueryCapsRows fails when a SELECT can return more than
// MaxResultRows rows into the page.
func TestExecuteQueryCapsRows(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	m := testModule(t, db)

	result := m.executeQuery(context.Background(),
		"WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < 2500) SELECT n FROM seq", 1)
	if result.Error != "" {
		t.Fatalf("executeQuery error: %s", result.Error)
	}
	if len(result.Rows) != MaxResultRows || result.RowsAffected != MaxResultRows {
		t.Errorf("rows = %d (reported %d), want %d", len(result.Rows), result.RowsAffected, MaxResultRows)
	}
	if !result.Truncated {
		t.Error("result must be flagged as truncated")
	}

	small := m.executeQuery(context.Background(), "SELECT 1", 1)
	if small.Truncated || len(small.Rows) != 1 {
		t.Errorf("small result: truncated=%v rows=%d", small.Truncated, len(small.Rows))
	}
}

// TestExecuteQueryHonoursTimeout fails when a statement no longer runs under
// the module's own deadline: an already-expired timeout must stop both query
// paths before they reach the database, and the attempt must still be logged.
func TestExecuteQueryHonoursTimeout(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	m := testModule(t, db)

	original := queryTimeout
	queryTimeout = -time.Second
	t.Cleanup(func() { queryTimeout = original })

	for _, query := range []string{"SELECT 1", "CREATE TABLE timeout_probe (id INTEGER)"} {
		result := m.executeQuery(context.Background(), query, 1)
		if !strings.Contains(result.Error, "context deadline exceeded") {
			t.Errorf("%q: error = %q, want context deadline exceeded", query, result.Error)
		}
	}

	history, err := m.getQueryHistory(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history entries = %d, want 2 (timed-out statements must be recorded)", len(history))
	}
}
