// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestMigrationBackfillsCurrentStateWithoutFabricatingHistory(t *testing.T) {
	ctx := context.Background()
	db, err := store.NewDB(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = store.RunMigrationCommand(ctx, db, "up-to", "20261030170000"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO users(id,email,password_hash,role,name) VALUES (1,'legacy@example.test','unused','editor','Legacy')`,
		`INSERT INTO pages(id,title,slug,body,summary,status,author_id,language_code,page_type,meta_title) VALUES (1,'Current','current','current body','current summary','draft',1,'en','post','Current SEO')`,
		`INSERT INTO page_versions(page_id,title,body,changed_by) VALUES (1,'Historical','old body',1)`,
		`INSERT INTO tags(id,name,slug,language_code) VALUES(1,'Tag','tag','en')`,
		`INSERT INTO page_tags(page_id,tag_id) VALUES(1,1)`,
	} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	q := store.New(db)
	page, err := q.GetPageByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Revision != 1 {
		t.Fatalf("initial revision=%d", page.Revision)
	}
	old, err := q.GetPageVersion(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !old.Legacy || old.SnapshotJson.Valid || old.Revision.Valid {
		t.Fatalf("fabricated historical fields: %+v", old)
	}
	current, err := q.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{PageID: 1, Revision: sql.NullInt64{Int64: 1, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Summary   string  `json:"summary"`
		MetaTitle string  `json:"meta_title"`
		Tags      []int64 `json:"tag_ids"`
	}
	if err = json.Unmarshal([]byte(current.SnapshotJson.String), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Summary != "current summary" || snapshot.MetaTitle != "Current SEO" || len(snapshot.Tags) != 1 || snapshot.Tags[0] != 1 {
		t.Fatalf("backfill omitted current fields: %+v", snapshot)
	}
	provenance, err := q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: 1, Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if provenance.Source != "migration" || provenance.UserID.Valid {
		t.Fatalf("backfill fabricated an actor: %+v", provenance)
	}
}
