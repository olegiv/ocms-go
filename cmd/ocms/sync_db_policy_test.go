// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/scheduler"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// Update this only after reviewing every changed built-in field and adding
// any needed credential dispositions to sync-db-policy.sql. Names alone
// cannot identify a new generic column that stores credentials.
const reviewedSyncSchema = "2e7a67685af390c317234ff758def25eae3afc14fe68f7ad88d5ed17d8b047a0"

// Changes to built-in credential storage need an explicit production-copy
// policy. The test reads the same classification CTE the shell script uses.
func TestSyncDatabasePolicyClassifiesCredentialSchema(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	custom := make(map[string]bool)
	for _, mod := range module.CustomModules() {
		custom[mod.Name()] = true
	}
	for _, mod := range allModules() {
		if !custom[mod.Name()] {
			moduleutil.RunMigrations(t, db, mod.Migrations())
		}
	}

	policy, _, ok := strings.Cut(readSyncDBPolicy(t), "-- Emit statements for the installed schema only.")
	if !ok {
		t.Fatal("could not find the policy definition")
	}
	type field struct{ table, column string }
	disposition := make(map[field]string)
	rows, err := db.Query(policy + "SELECT table_name, column_name, action, typeof(value) FROM policy")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key field
		var action, valueType string
		if err := rows.Scan(&key.table, &key.column, &action, &valueType); err != nil {
			t.Fatal(err)
		}
		if disposition[key] != "" {
			t.Errorf("duplicate policy for %s.%s", key.table, key.column)
		}
		switch action {
		case "delete":
			if key.column != "" || valueType != "null" {
				t.Errorf("delete must classify a whole table: %s.%s", key.table, key.column)
			}
		case "set":
			if key.column == "" || valueType == "null" {
				t.Errorf("set requires a column and replacement: %s.%s", key.table, key.column)
			}
		case "keep":
			if valueType != "null" {
				t.Errorf("keep cannot have a replacement: %s.%s", key.table, key.column)
			}
		default:
			t.Errorf("unrecognized policy action %q", action)
		}
		disposition[key] = action
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(disposition) == 0 {
		t.Fatal("no policy entries found")
	}

	columns, err := db.Query(`SELECT m.name, p.name, p.type FROM sqlite_master m
		JOIN pragma_table_info(m.name) p WHERE m.type = 'table'
		ORDER BY m.name, p.name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = columns.Close() }()
	exists := make(map[field]bool)
	var schema strings.Builder
	credential := regexp.MustCompile(`(^|_)(secret|password|token|bearer|authorization|credentials?|headers|settings|options|api_key|private_key|access_key|key_hash|key_lookup|access_hash|refresh_hash|code_hash)(_|$)`)
	credentialTable := regexp.MustCompile(`(^|_)(secrets?|passwords?|tokens?|credentials?)(_|$)`)
	for columns.Next() {
		var key field
		var columnType string
		if err := columns.Scan(&key.table, &key.column, &columnType); err != nil {
			t.Fatal(err)
		}
		schema.WriteString(key.table + "." + key.column + "\t" + columnType + "\n")
		whole := field{table: key.table}
		exists[key], exists[whole] = true, true
		// A reference such as api_key_id identifies a row, not a credential.
		bearsCredential := credentialTable.MatchString(key.table) ||
			(credential.MatchString(key.column) && !strings.HasSuffix(key.column, "_id"))
		if bearsCredential && disposition[key] == "" && disposition[whole] == "" {
			t.Errorf("%s.%s needs a production-copy credential disposition", key.table, key.column)
		}
	}
	if err := columns.Err(); err != nil {
		t.Fatal(err)
	}
	if fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(schema.String()))); fingerprint != reviewedSyncSchema {
		t.Errorf("built-in schema changed; review credential dispositions before recording schema digest %s", fingerprint)
	}
	for key := range disposition {
		if !exists[key] {
			t.Errorf("production-copy policy references missing schema entry %s.%s", key.table, key.column)
		}
	}
}

func readSyncDBPolicy(t *testing.T) string {
	t.Helper()
	policy, err := os.ReadFile("../../scripts/deploy/sync-db-policy.sql")
	if err != nil {
		t.Fatal(err)
	}
	return string(policy)
}

func TestSyncDatabasePolicyDisablesOutboundConsumers(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	if _, err := db.Exec(`
		INSERT INTO users (id, email, name, password_hash, role)
		VALUES (1, 'operator@example.test', 'Operator', 'synthetic', 'admin');
		INSERT INTO webhooks (name, url, secret, created_by)
		VALUES ('hook', 'https://example.test/hook', 'synthetic', 1);
		INSERT INTO scheduled_tasks (name, url, schedule)
		VALUES ('task', 'https://example.test/task', '*/5 * * * *');
	`); err != nil {
		t.Fatal(err)
	}
	q := store.New(db)
	ctx := t.Context()
	active := func(want int) {
		t.Helper()
		hooks, hookErr := q.ListActiveWebhooks(ctx)
		tasks, taskErr := q.ListActiveScheduledTasks(ctx)
		if hookErr != nil || taskErr != nil || len(hooks) != want || len(tasks) != want {
			t.Fatalf("active outbound consumers hooks=%d tasks=%d, want %d each, errors=%v/%v",
				len(hooks), len(tasks), want, hookErr, taskErr)
		}
	}
	active(1)
	rows, err := db.Query(readSyncDBPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var statements strings.Builder
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			t.Fatal(err)
		}
		statements.WriteString(statement)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(statements.String()); err != nil {
		t.Fatal(err)
	}
	active(0)
	task, err := q.GetScheduledTask(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.ValidateTaskURL(task.Url); err == nil || err.Error() != "URL is required" {
		t.Fatalf("scrubbed task still has a destination for manual execution: %v", err)
	}
}
