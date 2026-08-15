// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"database/sql"
	"fmt"
	"log/slog"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestMediaLookupPaginationIsCompleteWithEqualTimestamps(t *testing.T) {
	const total = mediaLookupBatchSize + 1
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	for index := int64(0); index < total; index++ {
		mediaUUID := fmt.Sprintf("%08x-0000-4000-8000-%012x", index, index)
		if _, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
			Uuid: mediaUUID, Filename: fmt.Sprintf("file-%d.pdf", index), MimeType: model.MimeTypePDF,
			Size: 1, Width: sql.NullInt64{}, Height: sql.NullInt64{}, UploadedBy: ts.User.ID,
			LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
		}); err != nil {
			t.Fatalf("CreateMedia(%d): %v", index, err)
		}
	}

	media, err := listAllMediaForLookup(ts.Ctx, ts.Queries)
	if err != nil || int64(len(media)) != total {
		t.Fatalf("listAllMediaForLookup() count = %d, error = %v", len(media), err)
	}
	for index := 1; index < len(media); index++ {
		if media[index-1].ID <= media[index].ID {
			t.Fatalf("unstable equal-timestamp order at %d: %d before %d", index, media[index-1].ID, media[index].ID)
		}
	}

	importerMap, err := NewImporter(ts.Queries, ts.DB, slog.Default()).buildLookupMap(ts.Ctx, ts.Queries, entityMedia)
	if err != nil || int64(len(importerMap)) != total {
		t.Fatalf("importer media map count = %d, error = %v", len(importerMap), err)
	}
	exporterMap, err := NewExporter(ts.Queries, slog.Default()).buildMediaMap(ts.Ctx)
	if err != nil || int64(len(exporterMap)) != total {
		t.Fatalf("exporter media map count = %d, error = %v", len(exporterMap), err)
	}
}
