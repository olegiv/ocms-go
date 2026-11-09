// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestEventCleanupPrunesOldestInBatches(t *testing.T) {
	db, closeDB := testutil.TestDB(t)
	t.Cleanup(closeDB)
	logger := testutil.TestLoggerSilent()
	registry := NewRegistry(db, logger)
	s := New(db, logger, registry)
	now := time.Now().UTC()
	if _, err := db.Exec(`WITH RECURSIVE batch(n) AS (
		SELECT 1 UNION ALL SELECT n+1 FROM batch WHERE n < 1000
	) INSERT INTO events(message, created_at) SELECT 'oldest', ? FROM batch`, now.Add(-120*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events(message, created_at) VALUES ('expired', ?), ('retained', ?)`,
		now.Add(-91*24*time.Hour), now.Add(-89*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEventCleanup(); err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		var total, recent int
		if err := db.QueryRow("SELECT COUNT(*), COUNT(CASE WHEN message = 'retained' THEN 1 END) FROM events").Scan(&total, &recent); err != nil {
			t.Fatal(err)
		}
		if total != want || recent != 1 {
			t.Fatalf("cleanup left %d rows, %d recent; want %d rows, 1 recent", total, recent, want)
		}
	}
	if err := registry.TriggerNow("core", "event_cleanup"); err != nil {
		t.Fatal(err)
	}
	check(2)
	var expired int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE message = 'expired'").Scan(&expired); err != nil || expired != 1 {
		t.Fatalf("first batch did not delete the oldest rows: expired=%d, %v", expired, err)
	}
	if err := registry.TriggerNow("core", "event_cleanup"); err != nil {
		t.Fatal(err)
	}
	check(1)
}
