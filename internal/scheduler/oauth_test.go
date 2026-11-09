// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestOAuthCleanupUsesRegistryOverrideAndReportsFailure(t *testing.T) {
	db, closeDB := testutil.TestDB(t)
	defer closeDB()
	logger := testutil.TestLoggerSilent()
	r := NewRegistry(db, logger)
	if _, err := db.Exec(`INSERT INTO scheduler_overrides(source,name,override_schedule) VALUES ('core','oauth_cleanup','*/30 * * * *')`); err != nil {
		t.Fatal(err)
	}
	s := New(db, logger, r)
	if err := s.AddOAuthCleanup(); err != nil {
		t.Fatal(err)
	}
	jobs := r.List()
	if len(jobs) != 1 || jobs[0].Name != "oauth_cleanup" || jobs[0].Schedule != "*/30 * * * *" || !jobs[0].CanTrigger {
		t.Fatalf("job not registered with override: %+v", jobs)
	}
	if err := r.TriggerNow("core", "oauth_cleanup"); err != nil {
		t.Fatal("manual maintenance failed", err)
	}
	if _, err := db.Exec(`DROP TABLE oauth_codes`); err != nil {
		t.Fatal(err)
	}
	if err := r.TriggerNow("core", "oauth_cleanup"); err == nil {
		t.Fatal("cleanup failure was swallowed")
	}
}
