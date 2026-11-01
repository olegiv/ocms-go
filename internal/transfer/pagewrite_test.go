// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"database/sql"
	"errors"
	"log/slog"
	"testing"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestImportOverwriteRequiresCapturedAIRevision(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	page, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{Title: "Original", Slug: "import-revision", Body: "original body", Status: "draft", AuthorID: ts.User.ID, LanguageCode: "en", PageType: "post", CreatedAt: ts.Now, UpdatedAt: ts.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err = ts.Queries.SetPageAIAccess(ts.Ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: ts.User.ID, SetAt: ts.Now}); err != nil {
		t.Fatal(err)
	}
	data := &ExportData{Version: ExportVersion, Pages: []ExportPage{{ID: 99, Title: "Imported", Slug: page.Slug, Body: "imported body", Status: "draft", AuthorEmail: ts.User.Email, LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now}}}
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetPrincipal(provenance.Principal{UserID: ts.User.ID, Source: "human", Transport: "admin"})
	opts := ImportOptions{ImportPages: true, ConflictStrategy: ConflictOverwrite}
	if _, err = importer.Import(ts.Ctx, data, opts); !errors.Is(err, pagewrite.ErrRevisionRequired) {
		t.Fatalf("unconditional AI overwrite: %v", err)
	}
	unchanged, err := ts.Queries.GetPageByID(ts.Ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Title != page.Title || unchanged.Revision != 1 {
		t.Fatal("rejected import changed the page")
	}
	opts.BaseRevisions = map[string]int64{page.Slug: page.Revision}
	if _, err = importer.Import(ts.Ctx, data, opts); err != nil {
		t.Fatal(err)
	}
	updated, err := ts.Queries.GetPageByID(ts.Ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Imported" || updated.Revision != 2 {
		t.Fatalf("matching overwrite: %+v", updated)
	}
	version, err := ts.Queries.GetPageVersionByRevision(ts.Ctx, store.GetPageVersionByRevisionParams{PageID: page.ID, Revision: sql.NullInt64{Int64: 2, Valid: true}})
	if err != nil || version.Legacy || !version.SnapshotJson.Valid {
		t.Fatalf("import snapshot: %+v %v", version, err)
	}
	actor, err := ts.Queries.GetContentProvenance(ts.Ctx, store.GetContentProvenanceParams{PageID: page.ID, Revision: 2})
	if err != nil || actor.Source != "human" || actor.UserID.Int64 != ts.User.ID {
		t.Fatalf("import initiator lost: %+v %v", actor, err)
	}
	if _, err = importer.Import(ts.Ctx, data, opts); err == nil {
		t.Fatal("stale import revision was accepted")
	} else {
		var conflict *pagewrite.Conflict
		if !errors.As(err, &conflict) {
			t.Fatalf("stale import returned %v", err)
		}
	}
}
