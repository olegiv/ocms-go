// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/security"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	"github.com/olegiv/ocms-go/internal/webhook"
)

// maxSummaryRunes caps the summary length (measured in unicode runes, not bytes).
const maxSummaryRunes = 500

// Policy is the subset of site-wide config that gates Page body markup.
type Policy struct {
	BlockSuspiciousMarkup bool // if true, reject bodies containing security.SuspiciousPageHTMLTokens
	SanitizeHTML          bool // if true, run security.SanitizePageHTML before persistence
}

// Service owns Page business logic end-to-end: validation, transactions,
// cache invalidation, event logging.
type Service struct {
	db       *sql.DB
	queries  *store.Queries
	cache    *cache.Manager
	events   *service.EventService
	policy   Policy
	webhooks webhook.EventDispatcher
}

// NewService constructs a Pages service. Cache and events may be nil for tests.
func NewService(db *sql.DB, queries *store.Queries, cacheManager *cache.Manager, events *service.EventService, policy Policy) *Service {
	return &Service{db: db, queries: queries, cache: cacheManager, events: events, policy: policy}
}

// SetWebhookDispatcher makes page writes send the page.created, page.updated
// and page.deleted webhook events the admin UI sends. Call it before serving.
func (s *Service) SetWebhookDispatcher(d webhook.EventDispatcher) {
	s.webhooks = d
}

// dispatchPageEvent sends a page webhook event when a dispatcher is set. The
// actor is an API key, not a signed-in user, so like the scheduler's events it
// carries no author_email.
func (s *Service) dispatchPageEvent(ctx context.Context, eventType string, page store.Page) {
	if s.webhooks != nil {
		webhook.DispatchPageEvent(ctx, s.webhooks, eventType, page, "")
	}
}

// requireWritePerm returns a forbidden domain error if the actor can't write pages.
func (s *Service) requireWritePerm(a v2.Actor) error {
	if a.APIKey == nil && a.ActingUserID() <= 0 {
		return v2.NewError(v2.ErrUnauthorized, "Authenticated user required")
	}
	if !a.HasPermission(model.PermissionPagesWrite) {
		return v2.NewError(v2.ErrForbidden, "pages:write permission required")
	}
	return nil
}

// canReadNonPublished is true when the actor has pages:read (required for drafts).
func canReadNonPublished(a v2.Actor) bool {
	return a.HasPermission(model.PermissionPagesRead)
}

// validateSafeURL rejects URLs whose scheme would be dangerous to render in a
// page template (javascript:, data:, vbscript:, file:, …). An empty string is
// accepted — the caller may want to clear the field.
func validateSafeURL(field, raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return v2.NewValidationError(map[string]string{field: "Invalid URL"}, "Validation failed")
	}
	switch strings.ToLower(u.Scheme) {
	case "", "http", "https":
		return nil
	}
	return v2.NewValidationError(
		map[string]string{field: fmt.Sprintf("URL scheme %q is not allowed; use http or https", u.Scheme)},
		"Validation failed",
	)
}

// validateSummary trims whitespace and enforces maxSummaryRunes.
func validateSummary(summary string) (string, error) {
	trimmed := strings.TrimSpace(summary)
	if utf8.RuneCountInString(trimmed) > maxSummaryRunes {
		return "", v2.NewValidationError(
			map[string]string{"summary": fmt.Sprintf("Summary must be %d characters or less", maxSummaryRunes)},
			"Validation failed",
		)
	}
	return trimmed, nil
}

// validateBodyMarkup enforces the "block suspicious markup" policy.
func (s *Service) validateBodyMarkup(body string) error {
	if !s.policy.BlockSuspiciousMarkup {
		return nil
	}
	if tokens := security.DetectSuspiciousHTMLTokens(body); len(tokens) > 0 {
		return v2.NewValidationError(
			map[string]string{"body": fmt.Sprintf("Body contains disallowed markup: %s", strings.Join(tokens, ", "))},
			"Validation failed",
		)
	}
	return nil
}

// normalizeBody applies HTML sanitization if enabled.
func (s *Service) normalizeBody(body string) string {
	if s.policy.SanitizeHTML {
		return security.SanitizePageHTML(body)
	}
	return body
}

// resolveLanguageCode falls back to the site's default language when the caller
// didn't pin one in the request. Explicit codes are validated for format and
// existence so unknown strings cannot enter language-filtered columns.
func (s *Service) resolveLanguageCode(ctx context.Context, langCode *string) (string, error) {
	if langCode != nil && *langCode != "" {
		if !util.IsValidLangCode(*langCode) {
			return "", v2.NewValidationError(
				map[string]string{"language_code": "Invalid language code format"},
				"Validation failed",
			)
		}
		language, err := s.queries.GetLanguageByCode(ctx, *langCode)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", v2.NewValidationError(
					map[string]string{"language_code": fmt.Sprintf("Language %q is not configured", *langCode)},
					"Validation failed",
				)
			}
			return "", v2.NewInternalError("Failed to look up language", err)
		}
		if err := validateRoutableLanguage(language); err != nil {
			return "", err
		}
		return language.Code, nil
	}
	def, err := s.queries.GetDefaultLanguage(ctx)
	if err != nil {
		return "", fmt.Errorf("loading default language: %w", err)
	}
	if err := validateRoutableLanguage(def); err != nil {
		return "", err
	}
	return def.Code, nil
}

// validateRoutableLanguage refuses a language the public router will not serve.
//
// Existence is not enough. An inactive language, or a legacy row whose code is
// malformed or owned by an application route, keeps its rows visible in the
// admin UI but is never installed as a public language, so the frontend
// answers 404 for every page filed under it. Creating published content there
// through the API produced pages that were unreachable from the moment they
// were written.
func validateRoutableLanguage(language store.Language) error {
	if !language.IsActive {
		return v2.NewValidationError(
			map[string]string{"language_code": fmt.Sprintf("Language %q is inactive", language.Code)},
			"Validation failed",
		)
	}
	if !util.IsRoutableLanguageCode(language.Code) {
		return v2.NewValidationError(
			map[string]string{"language_code": fmt.Sprintf("Language %q cannot be served on a public route", language.Code)},
			"Validation failed",
		)
	}
	return nil
}

// ensureSlugAvailable reports a conflict error when the slug cannot serve as
// this page's route: an exact application route or an active language owns
// it, or another page whose id is NOT exceptID already holds it. Pass 0 to
// check against all pages.
func (s *Service) ensureSlugAvailable(ctx context.Context, slug string, exceptID int64) error {
	return refusalError(pagewrite.SlugAvailable(ctx, s.queries, slug, exceptID), "slug", "Failed to check slug")
}

// refusalError maps a pagewrite check: a refusal the caller can fix is a 422
// on field, with its fixed text; anything else is a storage fault, reported
// as an internal error so its text never reaches the client and a retryable
// fault is not mistaken for a taken slug.
func refusalError(err error, field, msg string) error {
	var refusal *pagewrite.Refusal
	var size *pagewrite.FeaturedImageSizeError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &refusal), errors.As(err, &size):
		return v2.NewValidationError(map[string]string{field: err.Error()}, "Validation failed")
	default:
		return v2.NewInternalError(msg, err)
	}
}

// parseScheduledAt parses an RFC3339 timestamp. Empty string returns sql.NullTime{}.
func parseScheduledAt(s *string) (sql.NullTime, error) {
	if s == nil {
		return sql.NullTime{}, nil
	}
	if *s == "" {
		return sql.NullTime{Valid: false}, nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return sql.NullTime{}, v2.NewValidationError(
			map[string]string{"scheduled_at": "Invalid date format. Use RFC3339 (e.g. 2024-01-01T00:00:00Z)."},
			"Validation failed",
		)
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// dtoFromStore builds a Page DTO from a sqlc-generated store.Page.
func dtoFromStore(p store.Page) Page {
	dto := Page{
		ID:                p.ID,
		Revision:          p.Revision,
		Title:             p.Title,
		Slug:              p.Slug,
		Body:              p.Body,
		Summary:           p.Summary,
		Status:            p.Status,
		PageType:          p.PageType,
		AuthorID:          p.AuthorID,
		LanguageCode:      p.LanguageCode,
		CreatedAt:         p.CreatedAt,
		UpdatedAt:         p.UpdatedAt,
		HideFeaturedImage: p.HideFeaturedImage != 0,
		ExcludeFromLists:  p.ExcludeFromLists != 0,
		MetaTitle:         p.MetaTitle,
		MetaDescription:   p.MetaDescription,
		MetaKeywords:      p.MetaKeywords,
		NoIndex:           p.NoIndex != 0,
		NoFollow:          p.NoFollow != 0,
		CanonicalURL:      p.CanonicalUrl,
		VideoURL:          p.VideoUrl,
		VideoTitle:        p.VideoTitle,
	}
	if p.PublishedAt.Valid {
		dto.PublishedAt = &p.PublishedAt.Time
	}
	if p.FeaturedImageID.Valid {
		dto.FeaturedImageID = &p.FeaturedImageID.Int64
	}
	if p.OgImageID.Valid {
		dto.OGImageID = &p.OgImageID.Int64
	}
	if p.ScheduledAt.Valid {
		dto.ScheduledAt = &p.ScheduledAt.Time
	}
	return dto
}

// populateIncludes fetches and attaches optional relations (author / categories /
// tags) to a Page DTO. The authenticated flag gates whether the author's email
// is returned.
func (s *Service) populateIncludes(ctx context.Context, dto *Page, pageID int64, authenticated bool, want ListFilter) {
	if want.IncludeAuthor {
		if author, err := s.queries.GetPageAuthor(ctx, pageID); err == nil {
			a := &Author{ID: author.ID, Name: author.Name}
			if authenticated {
				a.Email = author.Email
			}
			dto.Author = a
		}
	}
	if want.IncludeCategories {
		if cats, err := s.queries.GetCategoriesForPage(ctx, pageID); err == nil {
			dto.Categories = make([]Category, 0, len(cats))
			for _, c := range cats {
				cat := Category{ID: c.ID, Name: c.Name, Slug: c.Slug}
				if c.Description.Valid {
					cat.Description = c.Description.String
				}
				dto.Categories = append(dto.Categories, cat)
			}
		}
	}
	if want.IncludeTags {
		if tags, err := s.queries.GetTagsForPage(ctx, pageID); err == nil {
			dto.Tags = make([]Tag, 0, len(tags))
			for _, t := range tags {
				dto.Tags = append(dto.Tags, Tag{ID: t.ID, Name: t.Name, Slug: t.Slug})
			}
		}
	}
}

// invalidatePageCache flushes cache entries after a mutation.
func (s *Service) invalidatePageCache(pageID int64) {
	if s.cache != nil {
		s.cache.InvalidatePage(pageID)
	}
}

// logPageAudit records an audit-level event for a successful Page mutation.
// Best-effort: logging failures are swallowed because rolling back a completed
// page write on a missed audit row would be worse than the audit gap. Mirrors
// v1 `/api/v1/pages/*` event logging so admins keep seeing API-driven page
// changes in /admin/events.
func (s *Service) logPageAudit(ctx context.Context, a v2.Actor, message string, meta map[string]any) {
	if s.events == nil || a.ActingUserID() <= 0 {
		return
	}
	userID := a.ActingUserID()
	_ = s.events.LogPageEvent(ctx, model.EventLevelInfo, message, &userID, "", "", meta)
}

// pageNotFound is the canonical "page not found" response (used for both
// missing rows and published-only visibility).
func pageNotFound(ref any) error {
	return v2.NewError(v2.ErrNotFound, fmt.Sprintf("page %v not found", ref))
}

// Create inserts a new page with its category and tag links.
func (s *Service) create(ctx context.Context, a v2.Actor, in CreatePageBody, work *pagewrite.Tx) (*Page, error) {
	if err := s.requireWritePerm(a); err != nil {
		return nil, err
	}
	if err := pagewrite.ValidateSlugFormat(in.Slug); err != "" {
		return nil, v2.NewValidationError(map[string]string{"slug": err}, "Validation failed")
	}
	in.Title = strings.TrimSpace(in.Title)
	if err := validateTitle(in.Title); err != nil {
		return nil, err
	}
	if err := validateEnums(in.Status, in.PageType); err != nil {
		return nil, err
	}
	if err := refusalError(pagewrite.FeaturedImageSize(ctx, s.queries, util.NullInt64FromPtr(in.FeaturedImageID)), "featured_image_id", "Failed to check featured image"); err != nil {
		return nil, err
	}
	summary, err := validateSummary(in.Summary)
	if err != nil {
		return nil, err
	}
	if err := validateSafeURL("canonical_url", in.CanonicalURL); err != nil {
		return nil, err
	}
	if err := validateSafeURL("video_url", in.VideoURL); err != nil {
		return nil, err
	}
	if err := s.validateBodyMarkup(in.Body); err != nil {
		return nil, err
	}
	if err := s.ensureSlugAvailable(ctx, in.Slug, 0); err != nil {
		return nil, err
	}
	status := in.Status
	if status == "" {
		status = model.PageStatusDraft
	}
	pageType := in.PageType
	if pageType == "" {
		pageType = "post"
	}
	scheduledAt, err := parseScheduledAt(in.ScheduledAt)
	if err != nil {
		return nil, err
	}
	langCode, err := s.resolveLanguageCode(ctx, in.LanguageCode)
	if err != nil {
		return nil, err
	}

	for _, catID := range in.CategoryIDs {
		if _, err := s.queries.GetCategoryByID(ctx, catID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, v2.NewValidationError(
					map[string]string{"category_ids": fmt.Sprintf("Category %d not found", catID)},
					"Validation failed",
				)
			}
			return nil, v2.NewInternalError("Failed to validate category", err)
		}
	}

	now := time.Now()
	params := store.CreatePageParams{
		Title:             in.Title,
		Slug:              in.Slug,
		Body:              s.normalizeBody(in.Body),
		Summary:           summary,
		Status:            status,
		PageType:          pageType,
		AuthorID:          a.ActingUserID(),
		LanguageCode:      langCode,
		CreatedAt:         now,
		UpdatedAt:         now,
		FeaturedImageID:   util.NullInt64FromPtr(in.FeaturedImageID),
		OgImageID:         util.NullInt64FromPtr(in.OGImageID),
		ScheduledAt:       scheduledAt,
		MetaTitle:         in.MetaTitle,
		MetaDescription:   in.MetaDescription,
		MetaKeywords:      in.MetaKeywords,
		CanonicalUrl:      in.CanonicalURL,
		NoIndex:           boolToInt64(in.NoIndex),
		NoFollow:          boolToInt64(in.NoFollow),
		HideFeaturedImage: boolToInt64(in.HideFeaturedImage),
		ExcludeFromLists:  boolToInt64(in.ExcludeFromLists),
		VideoUrl:          in.VideoURL,
		VideoTitle:        in.VideoTitle,
	}
	if status == model.PageStatusPublished {
		params.PublishedAt = sql.NullTime{Time: now, Valid: true}
	}

	txq := work.Q

	canCreateTags := a.HasPermission(model.PermissionTaxonomyWrite)
	tagIDs, err := resolveTagIDs(ctx, txq, in.TagIDs, in.TagNames, langCode, canCreateTags)
	if err != nil {
		return nil, err
	}

	page, err := txq.CreatePage(ctx, params)
	if err != nil {
		return nil, v2.NewInternalError("Failed to create page", err)
	}
	work.Created(page)
	if err := linkCategories(ctx, work, page.ID, in.CategoryIDs); err != nil {
		return nil, err
	}
	if err := linkTags(ctx, work, page.ID, tagIDs); err != nil {
		return nil, err
	}

	dto := dtoFromStore(page)
	s.populateIncludes(ctx, &dto, page.ID, true, ListFilter{IncludeCategories: true, IncludeTags: true})
	return &dto, nil
}

// Update applies a partial update to a page.
func (s *Service) update(ctx context.Context, a v2.Actor, id int64, in UpdatePageBody, work *pagewrite.Tx, forceDraft bool) (*Page, error) {
	if err := s.requireWritePerm(a); err != nil {
		return nil, err
	}
	existing, err := s.queries.GetPageByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, pageNotFound(id)
		}
		return nil, v2.NewInternalError("Failed to load page", err)
	}

	if _, trackErr := work.Track(ctx, id, in.BaseRevision, false); trackErr != nil {
		return nil, writeError(trackErr)
	}
	params := store.UpdatePageParams{
		ID:                existing.ID,
		Title:             existing.Title,
		Slug:              existing.Slug,
		Body:              existing.Body,
		Summary:           existing.Summary,
		Status:            existing.Status,
		PageType:          existing.PageType,
		FeaturedImageID:   existing.FeaturedImageID,
		OgImageID:         existing.OgImageID,
		MetaTitle:         existing.MetaTitle,
		MetaDescription:   existing.MetaDescription,
		MetaKeywords:      existing.MetaKeywords,
		CanonicalUrl:      existing.CanonicalUrl,
		NoIndex:           existing.NoIndex,
		NoFollow:          existing.NoFollow,
		ScheduledAt:       existing.ScheduledAt,
		LanguageCode:      existing.LanguageCode,
		HideFeaturedImage: existing.HideFeaturedImage,
		ExcludeFromLists:  existing.ExcludeFromLists,
		PublishedAt:       existing.PublishedAt,
		VideoUrl:          existing.VideoUrl,
		VideoTitle:        existing.VideoTitle,
		UpdatedAt:         time.Now(),
	}
	if err := s.applyUpdate(ctx, a, &in, &params, existing); err != nil {
		return nil, err
	}
	if in.CategoryIDs != nil {
		for _, catID := range *in.CategoryIDs {
			if _, err := s.queries.GetCategoryByID(ctx, catID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, v2.NewValidationError(
						map[string]string{"category_ids": fmt.Sprintf("Category %d not found", catID)},
						"Validation failed",
					)
				}
				return nil, v2.NewInternalError("Failed to validate category", err)
			}
		}
	}

	txq := work.Q

	hasTagChange := in.TagIDs != nil || in.TagNames != nil
	var newTagIDs []int64
	if hasTagChange {
		var ids []int64
		var names []string
		if in.TagIDs != nil {
			ids = *in.TagIDs
		}
		if in.TagNames != nil {
			names = *in.TagNames
		}
		canCreateTags := a.HasPermission(model.PermissionTaxonomyWrite)
		newTagIDs, err = resolveTagIDs(ctx, txq, ids, names, existing.LanguageCode, canCreateTags)
		if err != nil {
			return nil, err
		}
	}

	if forceDraft {
		params.Status = model.PageStatusDraft
		params.PublishedAt = sql.NullTime{}
		params.ScheduledAt = sql.NullTime{}
	}

	page, err := txq.UpdatePage(ctx, params)
	if err != nil {
		return nil, v2.NewInternalError("Failed to update page", err)
	}
	if in.CategoryIDs != nil {
		if err := txq.ClearPageCategories(ctx, page.ID); err != nil {
			return nil, v2.NewInternalError("Failed to clear categories", err)
		}
		if err := linkCategories(ctx, work, page.ID, *in.CategoryIDs); err != nil {
			return nil, err
		}
	}
	if hasTagChange {
		if err := txq.ClearPageTags(ctx, page.ID); err != nil {
			return nil, v2.NewInternalError("Failed to clear tags", err)
		}
		if err := linkTags(ctx, work, page.ID, newTagIDs); err != nil {
			return nil, err
		}
	}

	dto := dtoFromStore(page)
	s.populateIncludes(ctx, &dto, page.ID, true, ListFilter{IncludeCategories: true, IncludeTags: true})
	return &dto, nil
}

// applyUpdate copies validated fields from UpdatePageBody onto the UpdatePageParams.
func (s *Service) applyUpdate(ctx context.Context, a v2.Actor, in *UpdatePageBody, params *store.UpdatePageParams, existing store.Page) error {
	if in.Title != nil {
		params.Title = strings.TrimSpace(*in.Title)
		if err := validateTitle(params.Title); err != nil {
			return err
		}
	}
	if in.Slug != nil {
		if msg := pagewrite.ValidateSlugFormat(*in.Slug); msg != "" {
			return v2.NewValidationError(map[string]string{"slug": msg}, "Validation failed")
		}
		// An unchanged slug stays valid even when a later release reserved it,
		// as in the admin form, so a GET-then-PATCH round trip keeps working.
		if *in.Slug != existing.Slug {
			if err := s.ensureSlugAvailable(ctx, *in.Slug, existing.ID); err != nil {
				return err
			}
		}
		params.Slug = *in.Slug
	}
	if in.Body != nil {
		if err := s.validateBodyMarkup(*in.Body); err != nil {
			return err
		}
		params.Body = s.normalizeBody(*in.Body)
	}
	if in.Summary != nil {
		summary, err := validateSummary(*in.Summary)
		if err != nil {
			return err
		}
		params.Summary = summary
	}
	if in.Status != nil {
		if *in.Status != model.PageStatusDraft && *in.Status != model.PageStatusPublished {
			return v2.NewValidationError(map[string]string{"status": "Status must be 'draft' or 'published'"}, "Validation failed")
		}
		if middleware.IsDemoMode() && existing.Status == model.PageStatusPublished && *in.Status != model.PageStatusPublished {
			return v2.NewError(v2.ErrForbidden, middleware.DemoModeMessageDetailed(middleware.RestrictionUnpublishContent))
		}
		params.Status = *in.Status
		switch *in.Status {
		case model.PageStatusPublished:
			if existing.Status != model.PageStatusPublished {
				params.PublishedAt = sql.NullTime{Time: time.Now(), Valid: true}
			}
		default:
			params.PublishedAt = sql.NullTime{Valid: false}
		}
	}
	if in.PageType != nil {
		if *in.PageType != "post" && *in.PageType != "page" {
			return v2.NewValidationError(map[string]string{"page_type": "Page type must be 'post' or 'page'"}, "Validation failed")
		}
		params.PageType = *in.PageType
	}
	if in.FeaturedImageID != nil {
		params.FeaturedImageID = util.NullInt64FromPtr(in.FeaturedImageID)
		if err := refusalError(pagewrite.FeaturedImageSize(ctx, s.queries, params.FeaturedImageID), "featured_image_id", "Failed to check featured image"); err != nil {
			return err
		}
	}
	if in.ClearFeaturedImage {
		params.FeaturedImageID = sql.NullInt64{}
	}
	if in.OGImageID != nil {
		params.OgImageID = util.NullInt64FromPtr(in.OGImageID)
	}
	if in.MetaTitle != nil {
		params.MetaTitle = *in.MetaTitle
	}
	if in.MetaDescription != nil {
		params.MetaDescription = *in.MetaDescription
	}
	if in.MetaKeywords != nil {
		params.MetaKeywords = *in.MetaKeywords
	}
	if in.CanonicalURL != nil {
		if err := validateSafeURL("canonical_url", *in.CanonicalURL); err != nil {
			return err
		}
		params.CanonicalUrl = *in.CanonicalURL
	}
	if in.NoIndex != nil {
		params.NoIndex = boolToInt64(*in.NoIndex)
	}
	if in.NoFollow != nil {
		params.NoFollow = boolToInt64(*in.NoFollow)
	}
	if in.HideFeaturedImage != nil {
		params.HideFeaturedImage = boolToInt64(*in.HideFeaturedImage)
	}
	if in.ExcludeFromLists != nil {
		params.ExcludeFromLists = boolToInt64(*in.ExcludeFromLists)
	}
	if in.ScheduledAt != nil {
		scheduled, err := parseScheduledAt(in.ScheduledAt)
		if err != nil {
			return err
		}
		params.ScheduledAt = scheduled
	}
	if in.VideoURL != nil {
		if err := validateSafeURL("video_url", *in.VideoURL); err != nil {
			return err
		}
		params.VideoUrl = *in.VideoURL
	}
	if in.VideoTitle != nil {
		params.VideoTitle = *in.VideoTitle
	}
	_ = a
	return nil
}

// Delete removes a page and cascades the related link / version rows.
func (s *Service) Delete(ctx context.Context, a v2.Actor, id int64) error {
	if err := s.requireWritePerm(a); err != nil {
		return err
	}
	page, err := s.queries.GetPageByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return pageNotFound(id)
		}
		return v2.NewInternalError("Failed to load page", err)
	}
	work, err := pagewrite.InTx(ctx, s.db, a.Principal(a.Transport), func(work *pagewrite.Tx) error {
		if authorizeErr := s.authorizeWrite(ctx, work.Q, a, WriteOptions{}); authorizeErr != nil {
			return authorizeErr
		}
		return work.Delete(ctx, id)
	})
	if err != nil {
		return err
	}
	for changedID := range work.Pages {
		s.invalidatePageCache(changedID)
	}

	s.invalidatePageCache(page.ID)
	s.logPageAudit(ctx, a, "API: Page deleted", map[string]any{
		"page_id": page.ID,
		"slug":    page.Slug,
		"title":   page.Title,
	})
	s.dispatchPageEvent(ctx, model.EventPageDeleted, page)
	return nil
}

// Get returns a page by ID, enforcing the published-only visibility rule for
// unauthenticated / non-pages:read callers.
func (s *Service) Get(ctx context.Context, a v2.Actor, id int64, includes ListFilter) (*Page, error) {
	page, err := s.queries.GetPageByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, pageNotFound(id)
		}
		return nil, v2.NewInternalError("Failed to load page", err)
	}
	if page.Status != model.PageStatusPublished && !canReadNonPublished(a) {
		return nil, pageNotFound(id)
	}
	dto := dtoFromStore(page)
	s.populateIncludes(ctx, &dto, page.ID, canReadNonPublished(a), includes)
	return &dto, nil
}

// GetBySlug is the slug variant of Get.
func (s *Service) GetBySlug(ctx context.Context, a v2.Actor, slug string, includes ListFilter) (*Page, error) {
	page, err := s.queries.GetPageBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, pageNotFound(slug)
		}
		return nil, v2.NewInternalError("Failed to load page", err)
	}
	if page.Status != model.PageStatusPublished && !canReadNonPublished(a) {
		return nil, pageNotFound(slug)
	}
	dto := dtoFromStore(page)
	s.populateIncludes(ctx, &dto, page.ID, canReadNonPublished(a), includes)
	return &dto, nil
}

// List returns a paginated slice of pages subject to the filter and to the
// actor's visibility (non-pages:read callers only ever see published pages).
func (s *Service) List(ctx context.Context, a v2.Actor, f ListFilter) (*ListResult, error) {
	readAll := canReadNonPublished(a)
	status := f.Status
	if !readAll && status != "" && status != model.PageStatusPublished {
		return nil, v2.NewError(v2.ErrForbidden, "pages:read permission required to view non-published pages")
	}
	if !readAll {
		status = model.PageStatusPublished
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PerPage <= 0 || f.PerPage > 100 {
		f.PerPage = 20
	}
	limit := int64(f.PerPage)
	offset := int64((f.Page - 1) * f.PerPage)
	publishedOnly := status == model.PageStatusPublished

	var rows []store.Page
	var total int64
	var err error
	if f.LanguageCode != "" {
		if _, languageErr := s.resolveLanguageCode(ctx, &f.LanguageCode); languageErr != nil {
			return nil, languageErr
		}
	}
	switch {
	case f.LanguageCode != "":
		rows, err = s.queries.ListPagesInLanguage(ctx, store.ListPagesInLanguageParams{LanguageCode: f.LanguageCode, Status: status, CategoryID: f.CategoryID, TagID: f.TagID, ResultLimit: limit, ResultOffset: offset})
		if err == nil {
			total, err = s.queries.CountPagesInLanguage(ctx, store.CountPagesInLanguageParams{LanguageCode: f.LanguageCode, Status: status, CategoryID: f.CategoryID, TagID: f.TagID})
		}

	case f.CategoryID > 0:
		rows, total, err = s.listByCategory(ctx, publishedOnly, f.CategoryID, limit, offset)
	case f.TagID > 0:
		rows, total, err = s.listByTag(ctx, publishedOnly, f.TagID, limit, offset)
	case status != "":
		rows, err = s.queries.ListPagesByStatus(ctx, store.ListPagesByStatusParams{Status: status, Limit: limit, Offset: offset})
		if err == nil {
			total, err = s.queries.CountPagesByStatus(ctx, status)
		}
	default:
		rows, err = s.queries.ListPages(ctx, store.ListPagesParams{Limit: limit, Offset: offset})
		if err == nil {
			total, err = s.queries.CountPages(ctx)
		}
	}
	if err != nil {
		return nil, v2.NewInternalError("Failed to list pages", err)
	}

	dtos := make([]Page, 0, len(rows))
	for _, p := range rows {
		dto := dtoFromStore(p)
		s.populateIncludes(ctx, &dto, p.ID, readAll, f)
		dtos = append(dtos, dto)
	}
	return &ListResult{Pages: dtos, Total: total, Page: f.Page, PerPage: f.PerPage}, nil
}

func (s *Service) listByCategory(ctx context.Context, publishedOnly bool, categoryID, limit, offset int64) ([]store.Page, int64, error) {
	if publishedOnly {
		rows, err := s.queries.ListPublishedPagesByCategory(ctx, store.ListPublishedPagesByCategoryParams{CategoryID: categoryID, Limit: limit, Offset: offset})
		if err != nil {
			return nil, 0, err
		}
		total, err := s.queries.CountPublishedPagesByCategory(ctx, categoryID)
		return rows, total, err
	}
	rows, err := s.queries.ListPagesByCategory(ctx, store.ListPagesByCategoryParams{CategoryID: categoryID, Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.queries.CountPagesByCategory(ctx, categoryID)
	return rows, total, err
}

func (s *Service) listByTag(ctx context.Context, publishedOnly bool, tagID, limit, offset int64) ([]store.Page, int64, error) {
	if publishedOnly {
		rows, err := s.queries.ListPublishedPagesForTag(ctx, store.ListPublishedPagesForTagParams{TagID: tagID, Limit: limit, Offset: offset})
		if err != nil {
			return nil, 0, err
		}
		total, err := s.queries.CountPublishedPagesForTag(ctx, tagID)
		return rows, total, err
	}
	rows, err := s.queries.GetPagesForTag(ctx, store.GetPagesForTagParams{TagID: tagID, Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.queries.CountPagesForTag(ctx, tagID)
	return rows, total, err
}
