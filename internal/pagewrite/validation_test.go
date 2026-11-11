// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestFeaturedImageRejectsICOBeforeUnknownDimensions(t *testing.T) {
	for _, mimeType := range []string{model.MimeTypeICO, "image/vnd.microsoft.icon", model.MimeTypeJPEG} {
		for _, known := range []bool{false, true} {
			t.Run(mimeType+"/"+map[bool]string{false: "unknown", true: "known"}[known], func(t *testing.T) {
				f := setup(t)
				media, err := f.q.CreateMedia(t.Context(), store.CreateMediaParams{
					Uuid: mimeType, Filename: "image", MimeType: mimeType, Size: 1, UploadedBy: f.user.ID, LanguageCode: "en",
					Width: sql.NullInt64{Int64: 1200, Valid: known}, Height: sql.NullInt64{Int64: 800, Valid: known}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
				})
				if err != nil {
					t.Fatal(err)
				}
				err = pagewrite.FeaturedImageSize(t.Context(), f.q, sql.NullInt64{Int64: media.ID, Valid: true})
				if mimeType == model.MimeTypeJPEG {
					if err != nil {
						t.Fatalf("raster compatibility: %v", err)
					}
					return
				}
				var refusal *pagewrite.Refusal
				if !errors.As(err, &refusal) || refusal.MessageKey != "pages.error.featured_image_ico" {
					t.Fatalf("ICO must be refused before dimension grandfathering: %v", err)
				}
			})
		}
	}
}
