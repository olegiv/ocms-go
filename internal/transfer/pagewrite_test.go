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
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
)

// aiEditablePage inserts a revision-1 draft with AI access on.
func aiEditablePage(t *testing.T, ts *testSetup, slug string) store.Page {
	t.Helper()
	page, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{Title: "Original", Slug: slug, Body: "original body", Status: "draft", AuthorID: ts.User.ID, LanguageCode: "en", PageType: "post", CreatedAt: ts.Now, UpdatedAt: ts.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err = ts.Queries.SetPageAIAccess(ts.Ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: ts.User.ID, SetAt: ts.Now}); err != nil {
		t.Fatal(err)
	}
	return page
}

// exportedPage round-trips the page through the exporter, so the import sees
// exactly what an export file holds, and lets the caller edit it.
func exportedPage(t *testing.T, ts *testSetup, edit func(*ExportPage)) *ExportData {
	t.Helper()
	data, err := NewExporter(ts.Queries, slog.Default()).Export(ts.Ctx, DefaultExportOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Pages) != 1 {
		t.Fatalf("exported %d pages, want 1", len(data.Pages))
	}
	edit(&data.Pages[0])
	return data
}

var overwriteOptions = ImportOptions{ImportPages: true, ConflictStrategy: ConflictOverwrite}

// TestImportOverwritesAIEditablePage checks that an import file can replace an
// AI-editable page without the importer naming a revision, which no caller can
// supply (2026-11-03 review R2 P3-6), and that the result is one complete
// revision recorded for the import's initiator, with AI access untouched.
//
// Bug state: track overwritten pages with a required revision again
// (Track(..., base, true)) and the import fails with revision_required.
func TestImportOverwritesAIEditablePage(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	page := aiEditablePage(t, ts, "import-revision")
	data := exportedPage(t, ts, func(p *ExportPage) { p.Title, p.Body = "Imported", "imported body" })

	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetPrincipal(provenance.Principal{UserID: ts.User.ID, Source: "human", Transport: "admin"})
	result, err := importer.Import(ts.Ctx, data, overwriteOptions)
	if err != nil {
		t.Fatalf("overwrite of an AI-editable page: %v", err)
	}
	if result.Updated["pages"] != 1 {
		t.Errorf("updated pages = %d, want 1", result.Updated["pages"])
	}
	updated, err := ts.Queries.GetPageByID(ts.Ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Imported" || updated.Body != "imported body" || updated.Revision != 2 {
		t.Fatalf("overwrite: %+v", updated)
	}
	version, err := ts.Queries.GetPageVersionByRevision(ts.Ctx, store.GetPageVersionByRevisionParams{PageID: page.ID, Revision: sql.NullInt64{Int64: 2, Valid: true}})
	if err != nil || version.Legacy || !version.SnapshotJson.Valid {
		t.Fatalf("import snapshot: %+v %v", version, err)
	}
	actor, err := ts.Queries.GetContentProvenance(ts.Ctx, store.GetContentProvenanceParams{PageID: page.ID, Revision: 2})
	if err != nil || actor.Source != "human" || actor.Transport != "admin" || actor.UserID.Int64 != ts.User.ID {
		t.Fatalf("import initiator lost: %+v %v", actor, err)
	}
	access, err := ts.Queries.GetPageAIAccess(ts.Ctx, page.ID)
	if err != nil || !access.AiEditable {
		t.Fatalf("the import changed the AI access: %+v %v", access, err)
	}

	// The same file again changes nothing, so it adds no revision.
	if _, err = importer.Import(ts.Ctx, data, overwriteOptions); err != nil {
		t.Fatalf("repeated import: %v", err)
	}
	if again, loadErr := ts.Queries.GetPageByID(ts.Ctx, page.ID); loadErr != nil || again.Revision != 2 {
		t.Fatalf("repeated import: %+v %v", again, loadErr)
	}
}

// TestImportOverwriteWithoutPrincipalIsRecordedAsImport pins the provenance of
// an import with no authenticated initiator: the system's own import.
func TestImportOverwriteWithoutPrincipalIsRecordedAsImport(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	page := aiEditablePage(t, ts, "system-import")
	data := exportedPage(t, ts, func(p *ExportPage) { p.Title = "Imported by the system" })

	if _, err := NewImporter(ts.Queries, ts.DB, slog.Default()).Import(ts.Ctx, data, overwriteOptions); err != nil {
		t.Fatal(err)
	}
	actor, err := ts.Queries.GetContentProvenance(ts.Ctx, store.GetContentProvenanceParams{PageID: page.ID, Revision: 2})
	if err != nil || actor.Source != "system" || actor.Transport != "import" || actor.UserID.Valid {
		t.Fatalf("system import provenance: %+v %v", actor, err)
	}
}

// TestImportOverwriteHonorsAnExplicitBaseRevision keeps the optional
// compare-and-swap for a caller that does hold a revision: a matching one
// saves, a stale one is refused with the conflict and changes nothing.
func TestImportOverwriteHonorsAnExplicitBaseRevision(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	page := aiEditablePage(t, ts, "import-cas")
	data := exportedPage(t, ts, func(p *ExportPage) { p.Title = "Imported" })
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetPrincipal(provenance.Principal{UserID: ts.User.ID, Source: "human", Transport: "admin"})

	opts := overwriteOptions
	opts.BaseRevisions = map[string]int64{page.Slug: page.Revision}
	if _, err := importer.Import(ts.Ctx, data, opts); err != nil {
		t.Fatalf("matching revision: %v", err)
	}
	data.Pages[0].Title = "Imported again"
	_, err := importer.Import(ts.Ctx, data, opts)
	var conflict *pagewrite.Conflict
	if !errors.As(err, &conflict) || conflict.CurrentRevision != 2 {
		t.Fatalf("stale revision returned %v, want a conflict at revision 2", err)
	}
	if stored, loadErr := ts.Queries.GetPageByID(ts.Ctx, page.ID); loadErr != nil || stored.Title != "Imported" || stored.Revision != 2 {
		t.Fatalf("a refused import changed the page: %+v %v", stored, loadErr)
	}
}

// TestImportOverwriteValidatesPublication checks that an import cannot publish
// an AI draft that fails the checks every other publish path runs, and leaves
// ordinary pages under the existing editor policy.
//
// Bug state: drop validateOverwrittenPublication from importPages and the
// invalid draft is published.
func TestImportOverwriteValidatesPublication(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	page := aiEditablePage(t, ts, "import-publish")
	data := exportedPage(t, ts, func(p *ExportPage) { p.Status, p.Body = "published", "<p>ok</p><script>alert(1)</script>" })
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())

	if _, err := importer.Import(ts.Ctx, data, overwriteOptions); !errors.Is(err, review.ErrHardValidation) {
		t.Fatalf("publishing an invalid AI draft through an import returned %v, want %v", err, review.ErrHardValidation)
	}
	stored, err := ts.Queries.GetPageByID(ts.Ctx, page.ID)
	if err != nil || stored.Status != "draft" || stored.Revision != 1 || stored.Body != "original body" {
		t.Fatalf("a refused import changed the draft: %+v %v", stored, err)
	}

	// A page without AI access or authoring history keeps the editor's policy.
	if _, err = ts.DB.ExecContext(ts.Ctx, "DELETE FROM page_ai_access WHERE page_id = ?", page.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = importer.Import(ts.Ctx, data, overwriteOptions); err != nil {
		t.Fatalf("overwrite of an ordinary page: %v", err)
	}
	if stored, err = ts.Queries.GetPageByID(ts.Ctx, page.ID); err != nil || stored.Status != "published" {
		t.Fatalf("ordinary page: %+v %v", stored, err)
	}
}
