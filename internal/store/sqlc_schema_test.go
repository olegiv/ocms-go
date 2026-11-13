// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	sqlLineComment  = regexp.MustCompile(`--[^\n]*`)
	sqlBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	sqlFromWord     = regexp.MustCompile(`(?i)^from\b`)
)

// topLevelUpdateFrom reports whether stmt is an UPDATE with a FROM clause
// outside any parenthesised subquery.
func topLevelUpdateFrom(stmt string) bool {
	stmt = strings.TrimSpace(stmt)
	if len(stmt) < 6 || !strings.EqualFold(stmt[:6], "update") {
		return false
	}
	depth := 0
	for i := 0; i < len(stmt); i++ {
		switch stmt[i] {
		case '(':
			depth++
		case ')':
			depth--
		case 'f', 'F':
			boundary := i == 0 || !(stmt[i-1] == '_' || stmt[i-1] >= '0' && stmt[i-1] <= '9' ||
				stmt[i-1] >= 'a' && stmt[i-1] <= 'z' || stmt[i-1] >= 'A' && stmt[i-1] <= 'Z')
			if depth == 0 && boundary && sqlFromWord.MatchString(stmt[i:]) {
				return true
			}
		}
	}
	return false
}

// TestMigrationsAvoidUpdateFrom keeps the schema readable by sqlc. sqlc reads
// every migration as its schema, and its SQLite parser rejects the
// UPDATE ... FROM form ("extraneous input 'FROM'"), so `sqlc generate` fails
// for the whole store package. Use correlated subqueries instead.
//
// Bug state: restore the UPDATE ... FROM in
// 20261109170000_bound_preview_key_expiry.sql and this fails.
func TestMigrationsAvoidUpdateFrom(t *testing.T) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, readErr := fs.ReadFile(migrations, "migrations/"+entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		text := sqlBlockComment.ReplaceAllString(sqlLineComment.ReplaceAllString(string(data), ""), "")
		for _, stmt := range strings.Split(text, ";") {
			if topLevelUpdateFrom(stmt) {
				t.Errorf("%s: UPDATE ... FROM is not supported by sqlc's SQLite parser; use correlated subqueries", entry.Name())
			}
		}
	}
}

func TestTopLevelUpdateFrom(t *testing.T) {
	for stmt, want := range map[string]bool{
		"UPDATE a AS x SET v = 1 FROM b AS y WHERE x.id = y.id": true,
		"update a set v = 1\nfrom b where a.id = b.id":          true,
		"UPDATE a SET v = (SELECT id FROM b WHERE b.x = a.x)":   false,
		"UPDATE a SET v = 1 WHERE EXISTS (SELECT 1 FROM b)":     false,
		"UPDATE a SET from_id = 1, v_from = 2":                  false,
		"SELECT 1 FROM b":                                       false,
	} {
		if got := topLevelUpdateFrom(stmt); got != want {
			t.Errorf("topLevelUpdateFrom(%q) = %v, want %v", stmt, got, want)
		}
	}
}

// TestBoundPreviewKeyExpiryBackfill checks the migration's one-time backfill:
// a live preview link is capped at its issuing key's expiry, a malformed
// expiry caps at zero, and links that are shorter, revoked, key-less or whose
// key never expires are left alone.
func TestBoundPreviewKeyExpiryBackfill(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "preview-expiry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	ctx := t.Context()
	const before = "20261109160000"
	if err = RunMigrationCommand(ctx, db, "up-to", before); err != nil {
		t.Fatal(err)
	}
	q, now := New(db), time.Now().UTC()
	owner := createTestUser(t, q, ctx, "owner@example.test")
	page, err := q.CreatePage(ctx, CreatePageParams{Title: "Preview", Slug: "preview", Body: "<p>x</p>", Status: "draft", AuthorID: owner.ID, LanguageCode: getDefaultLangCode(t, q, ctx), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]any{"capped": "2030-01-01 00:00:00", "malformed": "not-a-date", "never": nil}
	keyID := map[string]int64{}
	for name, expiry := range keys {
		res, execErr := db.ExecContext(ctx, `INSERT INTO api_keys(name,key_hash,key_prefix,created_by,expires_at) VALUES(?,?,?,?,?)`, name, "hash-"+name, name, owner.ID, expiry)
		if execErr != nil {
			t.Fatal(execErr)
		}
		if keyID[name], err = res.LastInsertId(); err != nil {
			t.Fatal(err)
		}
	}
	capped := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	far := capped + 1000
	insert := func(token string, key any, expires int64, revoked int64) {
		t.Helper()
		if _, execErr := db.ExecContext(ctx, `INSERT INTO preview_links(token_hash,page_id,revision,created_by,session_version,issuing_key_id,created_at,expires_at,revoked_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			token, page.ID, 1, owner.ID, 1, key, 1, expires, revoked); execErr != nil {
			t.Fatal(execErr)
		}
	}
	insert("long", keyID["capped"], far, 0)
	insert("short", keyID["capped"], capped-500, 0)
	insert("revoked", keyID["capped"], far, 5)
	insert("malformed", keyID["malformed"], far, 0)
	insert("never", keyID["never"], far, 0)
	insert("keyless", nil, far, 0)

	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"long": capped, "short": capped - 500, "revoked": far, "malformed": 0, "never": far, "keyless": far}
	for token, expires := range want {
		var got int64
		if err = db.QueryRowContext(ctx, `SELECT expires_at FROM preview_links WHERE token_hash=?`, token).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != expires {
			t.Errorf("link %q expires_at = %d, want %d", token, got, expires)
		}
	}
}
