// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/ai/draft"
	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestNormalizedDraftSurvivesSharedPersistence(t *testing.T) {
	for _, linkPolicy := range []draft.LinkPolicy{draft.Keep, draft.NoFollow, draft.Strip} {
		t.Run(string(linkPolicy), func(t *testing.T) {
			db, cleanup := testutil.TestDB(t)
			t.Cleanup(cleanup)
			q, ctx, now := store.New(db), context.Background(), time.Now()
			user, err := q.CreateUser(ctx, store.CreateUserParams{Email: "draft-agent@example.test", PasswordHash: "unused", Role: "editor", Name: "Draft Agent", CreatedAt: now, UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			permissions := []string{model.PermissionMCPAccess, model.PermissionMCPDrafts}
			rawPermissions, err := json.Marshal(permissions)
			if err != nil {
				t.Fatal(err)
			}
			key, err := q.CreateAPIKey(ctx, store.CreateAPIKeyParams{Name: "Draft fixture", KeyHash: "unused", KeyPrefix: "draft", Permissions: string(rawPermissions), CreatedBy: user.ID, IsActive: true, CreatedAt: now, UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			media, err := q.CreateMedia(ctx, store.CreateMediaParams{Uuid: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Filename: "draft.jpg", MimeType: model.MimeTypeJPEG, Width: sql.NullInt64{Int64: 1536, Valid: true}, Height: sql.NullInt64{Int64: 1024, Valid: true}, Alt: sql.NullString{String: "Library", Valid: true}, Size: 1, UploadedBy: user.ID, LanguageCode: "en", CreatedAt: now, UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			_, err = q.CreateMediaVariant(ctx, store.CreateMediaVariantParams{MediaID: media.ID, Type: model.VariantLarge, Width: 1536, Height: 1024, Size: 1, CreatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			resolver := draft.StoreResolver{Queries: q, CheckLink: func(context.Context, string) (bool, error) { return false, nil }}
			available, err := resolver.SlugAvailable(ctx, "admin", 0)
			if err != nil || available {
				t.Fatalf("reserved route accepted: %v %v", available, err)
			}
			opt := draft.Options{Resolver: resolver, DefaultLanguage: "en", UILanguage: "en", SiteURL: "https://example.test", LinkPolicy: linkPolicy}
			input := draft.PageDraft{Title: "Stored draft", BodyMarkdown: "![Alt](media:1)\n\n[Review](https://outside.test/article)", FeaturedImageID: draft.SetNullable(media.ID), Reported: draft.Set(draft.AgentReport{Model: draft.Set("reported-model"), Notes: draft.Set("Unverified report")})}
			normalized, err := draft.NormalizeCreate(ctx, input, opt)
			if err != nil || normalized.Content == nil || normalized.HasErrors() {
				t.Fatalf("normalize: %+v %v", normalized, err)
			}
			validated, err := json.Marshal(struct {
				Issues []draft.Issue `json:"issues"`
				Links  []draft.Link  `json:"links"`
			}{normalized.Issues, normalized.Links})
			if err != nil {
				t.Fatal(err)
			}
			reported, err := json.Marshal(normalized.Reported)
			if err != nil {
				t.Fatal(err)
			}
			service := pages.NewService(db, q, nil, nil, pages.Policy{SanitizeHTML: true})
			actor := v2.Actor{APIKey: &key, Permissions: permissions, Transport: "mcp"}
			options := pages.WriteOptions{ForceDraft: true, RequestID: uuid.NewString(), Authorize: func(context.Context, *store.Queries) error { return nil }, Evidence: provenance.Evidence{Validated: validated, Reported: reported}}
			content := normalized.Content
			saved, err := service.CreateWith(ctx, actor, pages.CreatePageBody{Title: content.Title, Slug: content.Slug, Summary: content.Summary, Body: content.BodyHTML, PageType: content.PageType, FeaturedImageID: content.FeaturedImageID}, options)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Status != "draft" || saved.ScheduledAt != nil || saved.PublishedAt != nil || saved.Body != content.BodyHTML {
				t.Fatalf("storage changed the contract: %+v", saved)
			}
			facts, err := q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: saved.ID, Revision: 1})
			if err != nil {
				t.Fatal(err)
			}
			if facts.Source != "api" || facts.ApiKeyID.Int64 != key.ID || facts.UserID.Int64 != user.ID || !strings.Contains(facts.ReportedJson, "reported-model") || strings.Contains(facts.ValidatedJson, "reported-model") {
				t.Fatalf("evidence classes mixed: %+v", facts)
			}
			current := *content
			current.ID = saved.ID
			changed, err := draft.NormalizeUpdate(ctx, draft.Changes{Summary: draft.Set("Metadata only")}, current, opt)
			if err != nil || changed.Content == nil || changed.BodyChanged || changed.Content.BodyHTML != saved.Body {
				t.Fatalf("metadata: %+v %v", changed, err)
			}
			options.RequestID = uuid.NewString()
			options.Evidence = provenance.Evidence{}
			updated, err := service.UpdateWith(ctx, actor, saved.ID, pages.UpdatePageBody{Summary: &changed.Content.Summary, BaseRevision: &saved.Revision}, options)
			if err != nil || updated.Body != saved.Body || updated.Revision != 2 {
				t.Fatalf("metadata storage: %+v %v", updated, err)
			}
			testutil.AssertPageRevision(t, q, saved.ID, 2)
			derived, err := draft.DeriveMarkdown(ctx, `<img src="/uploads/large/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/draft.jpg" alt="Human image">`, opt)
			if err != nil || !strings.Contains(derived.Markdown, "media:1") || has(derived.Issues, "media_conversion_lossy") {
				t.Fatalf("existing library URL not preserved: %+v %v", derived, err)
			}
			if _, err := resolver.InternalLink(ctx, "/hidden"); err != nil {
				t.Fatal(err)
			}
			if _, err := (draft.StoreResolver{Queries: q}).InternalLink(ctx, "/hidden"); err == nil {
				t.Fatal("missing visibility resolver did not fail closed")
			}
			if _, err := resolver.MediaForURL(ctx, "https://other.test/uploads/large/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/draft.jpg"); !errors.Is(err, draft.ErrUnavailable) {
				t.Fatalf("URL resolver accepted remote URL: %v", err)
			}
			if _, err := resolver.MediaForURL(ctx, "/uploads/large/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/wrong.jpg"); !errors.Is(err, draft.ErrUnavailable) {
				t.Fatalf("URL resolver trusted UUID without filename: %v", err)
			}
		})
	}
}

func TestFeaturedImageMinimumParityAndLegacyAssignments(t *testing.T) {
	for _, dimensions := range []struct {
		width, height    int64
		known, wantError bool
	}{{1536, 1024, true, false}, {1200, 800, true, false}, {1199, 800, true, true}, {1200, 799, true, true}, {0, 0, false, false}, {0, 0, true, true}} {
		opt, resolver := options()
		media := resolver.media[1]
		media.Width, media.Height, media.DimensionsKnown = dimensions.width, dimensions.height, dimensions.known
		resolver.media[1] = media
		result, err := draft.NormalizeCreate(context.Background(), draft.PageDraft{Title: "Image", FeaturedImageID: draft.SetNullable(int64(1))}, opt)
		shared := pagewrite.CheckFeaturedImage(media.MimeType, dimensions.width, dimensions.height, dimensions.known)
		if err != nil || has(result.Issues, "image_too_small") != dimensions.wantError || (shared != nil) != dimensions.wantError {
			t.Fatalf("dimensions %+v: %+v %v shared=%v", dimensions, result, err, shared)
		}
		current := draft.Content{ID: 2, Title: "Existing", Slug: "existing", LanguageCode: "en", FeaturedImageID: func() *int64 { id := int64(1); return &id }()}
		updated, err := draft.NormalizeUpdate(context.Background(), draft.Changes{FeaturedImageID: draft.SetNullable(int64(1))}, current, opt)
		if err != nil || updated.Content == nil || has(updated.Issues, "image_too_small") {
			t.Fatalf("same historical assignment was not grandfathered: %+v %v", updated, err)
		}
	}
}

func TestFeaturedICORejectedByDraftEngine(t *testing.T) {
	for _, mimeType := range []string{model.MimeTypeICO, "image/vnd.microsoft.icon"} {
		opt, resolver := options()
		media := resolver.media[1]
		media.MimeType, media.DimensionsKnown = mimeType, false
		resolver.media[1] = media
		input := draft.PageDraft{Title: "ICO", FeaturedImageID: draft.SetNullable(int64(1))}
		result, err := draft.NormalizeCreate(t.Context(), input, opt)
		if err != nil || !has(result.Issues, "image_not_found") {
			t.Fatalf("draft accepted featured ICO %s: %+v %v", mimeType, result, err)
		}
		current := draft.Content{ID: 2, Title: "Existing", Slug: "existing", LanguageCode: "en"}
		result, err = draft.NormalizeUpdate(t.Context(), draft.Changes{FeaturedImageID: input.FeaturedImageID}, current, opt)
		if err != nil || !has(result.Issues, "image_not_found") {
			t.Fatalf("draft change accepted ICO: %+v %v", result, err)
		}
		current.FeaturedImageID = &media.ID
		// Publication re-sends the stored ID, so validImage still refuses ICO.
		result, err = draft.NormalizeUpdate(t.Context(), draft.Changes{FeaturedImageID: input.FeaturedImageID}, current, opt)
		if err != nil || !has(result.Issues, "image_not_found") {
			t.Fatalf("publication-style revalidation accepted the same ICO: %+v %v", result, err)
		}
		result, err = draft.NormalizeUpdate(t.Context(), draft.Changes{Summary: draft.Set("Metadata")}, current, opt)
		if err != nil || result.HasErrors() || result.Content == nil || result.Content.FeaturedImageID == nil || *result.Content.FeaturedImageID != media.ID {
			t.Fatalf("metadata-only update omitting featured_image_id lost ICO: %+v %v", result, err)
		}
	}
}

func TestResolverFailuresAndForgedMediaNeverProduceTrustedContent(t *testing.T) {
	opt, resolver := options()
	injected := errors.New("database unavailable")
	resolver.mediaErr = injected
	result, err := draft.NormalizeCreate(context.Background(), draft.PageDraft{Title: "Draft", BodyMarkdown: "![x](media:1)"}, opt)
	if !errors.Is(err, injected) || result.Content != nil {
		t.Fatalf("operational failure masked: %+v %v", result, err)
	}
	resolver.mediaErr = nil
	for _, source := range []string{`<img src="https://other.test/uploads/large/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/image%20with%20space.jpg" data-media-id="1">`, `<img src="/uploads/large/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/wrong.jpg" data-media-id="1">`} {
		derived, err := draft.DeriveMarkdown(context.Background(), source, opt)
		if err != nil || !has(derived.Issues, "media_conversion_lossy") || strings.Contains(derived.Markdown, "media:1") {
			t.Fatalf("forged media identity trusted: %+v %v", derived, err)
		}
	}
}
