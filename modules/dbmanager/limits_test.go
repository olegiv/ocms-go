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
// switched on in every fresh install.
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
// maxResultRows rows into the page.
func TestExecuteQueryCapsRows(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	m := testModule(t, db)

	result := m.executeQuery(context.Background(),
		"WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < 2500) SELECT n FROM seq", 1)
	if result.Error != "" {
		t.Fatalf("executeQuery error: %s", result.Error)
	}
	if len(result.Rows) != maxResultRows || result.RowsAffected != maxResultRows {
		t.Errorf("rows = %d (reported %d), want %d", len(result.Rows), result.RowsAffected, maxResultRows)
	}
	if !result.Truncated {
		t.Error("result must be flagged as truncated")
	}

	small := m.executeQuery(context.Background(), "SELECT 1", 1)
	if small.Truncated || len(small.Rows) != 1 {
		t.Errorf("small result: truncated=%v rows=%d", small.Truncated, len(small.Rows))
	}

	// The history row records that the result was cut off (rows executed in
	// the same second have no stable order, so match by query text).
	history, err := m.getQueryHistory(context.Background(), 2)
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %d, %v", len(history), err)
	}
	for _, item := range history {
		if wantTruncated := strings.Contains(item.Query, "seq"); item.Truncated != wantTruncated {
			t.Fatalf("history row %q truncated = %v, want %v", item.Query, item.Truncated, wantTruncated)
		}
	}
}

// TestExecuteQueryHonoursTimeout fails when a statement no longer runs under
// the module's own deadline: an already-expired timeout must stop both query
// paths before they reach the database, and the attempt must still be logged.
func TestExecuteQueryHonoursTimeout(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	m := testModule(t, db)

	m.queryTimeout = -time.Second

	for _, query := range []string{"SELECT 1", "CREATE TABLE timeout_probe (id INTEGER)"} {
		result := m.executeQuery(context.Background(), query, 1)
		if !strings.Contains(result.Error, "query cancelled after") {
			t.Errorf("%q: error = %q, want the timeout message", query, result.Error)
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

// TestTruncatedQueryReleasesConnectionBeforeHistory fails when a truncated
// SELECT keeps its result set, and so its pool connection, open while the
// history insert waits for another connection: with a single-connection
// pool that wait only ends when the query deadline cancels the cursor.
func TestTruncatedQueryReleasesConnectionBeforeHistory(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	db.SetMaxOpenConns(1)
	m := testModule(t, db)
	m.queryTimeout = 3 * time.Second

	start := time.Now()
	result := m.executeQuery(context.Background(),
		"WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < 2500) SELECT n FROM seq", 1)
	elapsed := time.Since(start)
	if result.Error != "" || !result.Truncated {
		t.Fatalf("executeQuery: error=%q truncated=%v", result.Error, result.Truncated)
	}
	if elapsed >= m.queryTimeout {
		t.Fatalf("truncated query took %v, at least the %v deadline: the history insert waited for the cursor's connection", elapsed, m.queryTimeout)
	}
	history, err := m.getQueryHistory(context.Background(), 1)
	if err != nil || len(history) != 1 || !history[0].Truncated {
		t.Fatalf("history after a truncated query = %+v, %v; want one truncated entry", history, err)
	}
}
