// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProvenanceGuardsPreserveHistoryAndUserDeletion(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "provenance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	ctx := t.Context()
	const before = "20261102020000"
	if err = RunMigrationCommand(ctx, db, "up-to", before); err != nil {
		t.Fatal(err)
	}
	q, now := New(db), time.Now().UTC()
	owner := createTestUser(t, q, ctx, "owner@example.test")
	editor := createTestUser(t, q, ctx, "editor@example.test")
	page, err := q.CreatePage(ctx, CreatePageParams{Title: "History", Slug: "history", Body: "<p>Retained</p>", Status: "draft", AuthorID: owner.ID, LanguageCode: getDefaultLangCode(t, q, ctx), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for index, fields := range []struct {
		source, grant, client string
		key                   int64
		anonymous             bool
	}{
		{source: "human"},
		{source: "api", key: 17},
		{source: "oauth", grant: "grant", client: "client"},
		{source: "oauth", grant: "legacy-mixed", key: 17},
		{source: "human", anonymous: true},
		{source: "", anonymous: true},
	} {
		if err = q.CreateContentProvenance(ctx, CreateContentProvenanceParams{
			PageID: page.ID, Revision: int64(index + 1), Source: fields.source,
			UserID:       sql.NullInt64{Int64: editor.ID, Valid: !fields.anonymous},
			ApiKeyID:     sql.NullInt64{Int64: fields.key, Valid: fields.key > 0},
			OauthGrantID: fields.grant, OauthClientID: fields.client, Transport: "retained",
			ValidatedJson: `{"legacy":true}`, ReportedJson: `{"retained":"claims"}`, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO claims_checks(provenance_id,claim_index,checked_by,checked_at) SELECT id,0,?,1 FROM content_provenance WHERE revision IN (1,4)`, owner.ID); err != nil {
		t.Fatal(err)
	}
	read := func(query string) string {
		t.Helper()
		var result string
		if readErr := db.QueryRowContext(ctx, query).Scan(&result); readErr != nil {
			t.Fatal(readErr)
		}
		return result
	}
	const history = `SELECT json_group_array(json_array(id,page_id,revision,user_id,source,api_key_id,oauth_grant_id,oauth_client_id,transport,validated_json,reported_json,created_at)) FROM (SELECT * FROM content_provenance ORDER BY id)`
	const claims = `SELECT json_group_array(json_array(provenance_id,claim_index,checked_by,checked_at)) FROM (SELECT * FROM claims_checks ORDER BY provenance_id,claim_index)`
	prior, checks := read(history), read(claims)
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	if read(history) != prior || read(claims) != checks {
		t.Fatal("upgrade rewrote provenance or dropped retained claim checks")
	}
	refuse := func(query string, args ...any) {
		t.Helper()
		_, refused := db.ExecContext(ctx, query, args...)
		if refused == nil || !strings.Contains(refused.Error(), "invalid provenance principal") {
			t.Fatalf("SQL must refuse invalid new attribution: %v", refused)
		}
	}
	const badInsert = `INSERT INTO content_provenance(page_id,revision,source,created_at) VALUES (?,100,'human',CURRENT_TIMESTAMP)`
	refuse(badInsert, page.ID)
	refuse(`UPDATE content_provenance SET source='unknown' WHERE page_id=? AND revision=1`, page.ID)
	refuse(`UPDATE content_provenance SET user_id=NULL WHERE page_id=? AND revision=1`, page.ID)
	if read(history) != prior || read(claims) != checks {
		t.Fatal("refused identity write changed retained history")
	}
	// Rewriting an unchanged identity in a legacy row is not new attribution.
	if _, err = db.ExecContext(ctx, `UPDATE content_provenance SET source=source WHERE page_id=?`, page.ID); err != nil {
		t.Fatal(err)
	}
	// This editor owns no content. The existing FK must anonymize their past
	// edits, including legacy inconsistent rows, without deleting evidence.
	if err = q.DeleteUser(ctx, editor.ID); err != nil {
		t.Fatalf("retained attribution blocked editor deletion: %v", err)
	}
	for revision := int64(1); revision <= 6; revision++ {
		fact, loadErr := q.GetContentProvenance(ctx, GetContentProvenanceParams{PageID: page.ID, Revision: revision})
		if loadErr != nil || fact.UserID.Valid || fact.ValidatedJson != `{"legacy":true}` || fact.ReportedJson != `{"retained":"claims"}` {
			t.Fatalf("anonymization lost revision %d: %+v, %v", revision, fact, loadErr)
		}
	}
	if read(claims) != checks {
		t.Fatal("editor deletion changed another user's retained claim checks")
	}
	refuse(badInsert, page.ID)
	refuse(`UPDATE content_provenance SET source='api',api_key_id=17 WHERE page_id=? AND revision=1`, page.ID)
	if _, err = db.ExecContext(ctx, `UPDATE content_provenance SET user_id=? WHERE page_id=? AND revision=1`, owner.ID, page.ID); err != nil {
		t.Fatalf("valid identity correction refused: %v", err)
	}
	if err = RunMigrationCommand(ctx, db, "down-to", before); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, badInsert, page.ID); err != nil {
		t.Fatalf("rollback did not remove the new guards: %v", err)
	}
	prior = read(history)
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	if read(history) != prior || read(claims) != checks {
		t.Fatal("re-upgrade changed historical rows")
	}
	refuse(`UPDATE content_provenance SET source='api' WHERE page_id=? AND revision=100`, page.ID)
	if got := read(`SELECT COUNT(*) FROM pragma_foreign_key_check`); got != "0" {
		t.Fatalf("foreign key violations after upgrade/rollback: %s", got)
	}
}
