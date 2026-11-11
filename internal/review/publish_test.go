// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package review_test

import (
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
)

// Historical ICO assignments remain visible/editable, but AI publication must
// revalidate the existing image through NormalizeUpdate. Dropping that assignment
// in publish.go must fail this test, even though ordinary PNG publication succeeds.
func TestPublishHistoricalFeaturedImagePolicy(t *testing.T) {
	f := setupLoad(t)
	ctx, now := t.Context(), time.Now()
	principal := provenance.Principal{UserID: f.author.ID, Source: "human", Transport: "admin"}
	page := f.revised(principal)
	if err := f.q.SetPageAIAccess(ctx, store.SetPageAIAccessParams{PageID: page.ID, AiEditable: true, SetBy: f.author.ID, SetAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO config (key,value,type,language_code,updated_at) VALUES ('site_url','https://example.test','string','en',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, now); err != nil {
		t.Fatal(err)
	}
	for _, mime := range []string{model.MimeTypeICO, model.MimeTypePNG} {
		t.Run(mime, func(t *testing.T) {
			extension := ".png"
			if mime == model.MimeTypeICO {
				extension = ".ico"
			}
			media, err := f.q.CreateMedia(ctx, store.CreateMediaParams{Uuid: uuid.NewString(), Filename: "featured" + extension, MimeType: mime, Size: 1, Width: sql.NullInt64{Int64: 1200, Valid: true}, Height: sql.NullInt64{Int64: 800, Valid: true}, UploadedBy: f.author.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			// Seed the historical assignment directly; new ICO assignment is refused.
			if _, err := f.db.ExecContext(ctx, "UPDATE pages SET featured_image_id=? WHERE id=?", media.ID, page.ID); err != nil {
				t.Fatal(err)
			}
			page, err = f.q.GetPageByID(ctx, page.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Follow the admin/scheduler publication transaction and its shared gate.
			_, err = pagewrite.InQueriesTx(ctx, f.q, principal, func(work *pagewrite.Tx) error {
				current, trackErr := work.Track(ctx, page.ID, &page.Revision, true)
				if trackErr != nil {
					return trackErr
				}
				if validationErr := review.ValidatePublication(ctx, work.Q, current); validationErr != nil {
					return validationErr
				}
				_, publishErr := work.Q.PublishUnpublishedPage(ctx, store.PublishUnpublishedPageParams{ID: page.ID, UpdatedAt: now, PublishedAt: sql.NullTime{Time: now, Valid: true}})
				return publishErr
			})
			wantStatus := model.PageStatusPublished
			if mime == model.MimeTypeICO {
				wantStatus = model.PageStatusDraft
				if !errors.Is(err, review.ErrHardValidation) {
					t.Fatalf("ICO publication refusal: %v", err)
				}
			} else if err != nil {
				t.Fatalf("PNG publication failed: %v", err)
			}
			stored, readErr := f.q.GetPageByID(ctx, page.ID)
			if readErr != nil || stored.Status != wantStatus || stored.FeaturedImageID.Int64 != media.ID {
				t.Fatalf("publication result=%+v %v", stored, readErr)
			}
		})
	}
}
