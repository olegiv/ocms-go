// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestAuditRequiredAPIKeyMaxTTLPosture checks the production start check for
// API key lifetimes against keys stored in both time formats: the current
// one and the time.Time.String() text the driver wrote before
// _time_format=sqlite. The check used to compare with SQLite's datetime(),
// which cannot read the old text, so it skipped every key the app had
// created (review of the 2026-10-17 audit fixes). A key outside the policy
// is logged, not a reason to refuse the start: v0.29.0 stopped a production
// instance over one never-used key that authentication rejected anyway.
//
// Bug state: compare with datetime(expires_at) > datetime(created_at, ...)
// in SQL again and the old-format case logs nothing; return an error for a
// key outside the policy and the start is refused again.
func TestAuditRequiredAPIKeyMaxTTLPosture(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users (id, email, password_hash, role, name) VALUES (1, 'admin@test.com', 'x', 'admin', 'Admin')`); err != nil {
		t.Fatal(err)
	}
	logs := captureDefaultLog(t)
	created := time.Date(2026, 1, 10, 12, 0, 0, 0, time.FixedZone("CET", 3600))
	cases := []struct {
		name            string
		active          bool
		created, expiry any
		warned          bool
	}{
		{"30 days", true, created, created.AddDate(0, 0, 30), false},
		{"400 days", true, created, created.AddDate(0, 0, 400), true},
		{"400 days in the old format", true,
			"2026-01-10 12:00:00 +0100 CET m=+12.5", "2027-02-14 12:00:00 +0100 CET m=+13.1", true},
		{"30 days in the old format", true,
			"2026-01-10 12:00:00.25 +0100 CET", "2026-02-09 12:00:00.25 +0100 CET", false},
		{"no expiry", true, created, nil, true},
		{"inactive and 400 days", false, created, created.AddDate(0, 0, 400), false},
	}
	for _, tc := range cases {
		if _, err := db.Exec(`DELETE FROM api_keys`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO api_keys (name, key_hash, key_prefix, is_active, created_by, created_at, expires_at)
			VALUES ('k', 'hash', 'pfx', ?, 1, ?, ?)`, tc.active, tc.created, tc.expiry); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		logs.Reset()
		if err := auditRequiredAPIKeyMaxTTLPosture(ctx, db, 90); err != nil {
			t.Errorf("%s: audit = %v, want the start to go ahead", tc.name, err)
		}
		if warned := strings.Contains(logs.String(), "exceed OCMS_API_KEY_MAX_TTL_DAYS"); warned != tc.warned {
			t.Errorf("%s: warned = %v, want %v; log:\n%s", tc.name, warned, tc.warned, logs.String())
		}
		if tc.warned && !strings.Contains(logs.String(), ": k") {
			t.Errorf("%s: the warning does not name the key:\n%s", tc.name, logs.String())
		}
	}

	// Several keys outside the policy go into one record: the Event Log
	// keeps only the first of a run of warnings with one message.
	if _, err := db.Exec(`DELETE FROM api_keys`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		if _, err := db.Exec(`INSERT INTO api_keys (name, key_hash, key_prefix, is_active, created_by, created_at, expires_at)
			VALUES (?, 'hash-' || ?, 'pfx', 1, 1, ?, ?)`, name, name, created, created.AddDate(0, 0, 400)); err != nil {
			t.Fatal(err)
		}
	}
	logs.Reset()
	if err := auditRequiredAPIKeyMaxTTLPosture(ctx, db, 90); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(logs.String(), "exceed OCMS_API_KEY_MAX_TTL_DAYS"); got != 1 || !strings.Contains(logs.String(), ": first; ") || !strings.Contains(logs.String(), ": second") {
		t.Errorf("want one warning naming both keys, got %d:\n%s", got, logs.String())
	}
	if err := auditRequiredAPIKeyMaxTTLPosture(ctx, db, 0); err != nil {
		t.Errorf("a zero limit audits nothing, got %v", err)
	}
}
