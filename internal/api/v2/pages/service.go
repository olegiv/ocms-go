// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/handler"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/security"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
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
	db      *sql.DB
	queries *store.Queries
	cache   *cache.Manager
	events  *service.EventService
	policy  Policy
}

// NewService constructs a Pages service. Cache and events may be nil for tests.
func NewService(db *sql.DB, queries *store.Queries, cache *cache.Manager, events *service.EventService, policy Policy) *Service {
	return &Service{db: db, queries: queries, cache: cache, events: events, policy: policy}
}

// requireWritePerm returns a forbidden domain error if the actor can't write pages.
func (s *Service) requireWritePerm(a v2.Actor) error {
	if a.APIKey == nil {
		return v2.NewError(v2.ErrUnauthorized, "API key required")
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
// didn't pin one in the request.
func (s *Service) resolveLanguageCode(ctx context.Context, langCode *string) (string, error) {
	if langCode != nil && *langCode != "" {
		return *langCode, nil
	}
	def, err := s.queries.GetDefaultLanguage(ctx)
	if err != nil {
		return "", fmt.Errorf("loading default language: %w", err)
	}
	return def.Code, nil
}

// ensureSlugUnique reports a conflict error if slug is taken by a page whose
// id is NOT exceptID. Pass 0 to check uniqueness across all pages.
func (s *Service) ensureSlugUnique(ctx context.Context, slug string, exceptID int64) error {
	existing, err := s.queries.GetPageBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("checking slug uniqueness: %w", err)
	}
	if existing.ID == exceptID {
		return nil
	}
	return v2.NewValidationError(
		map[string]string{"slug": "Slug already exists"},
		"Validation failed",
	)
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

// pageNotFound is the canonical "page not found" response (used for both
// missing rows and published-only visibility).
func pageNotFound(ref any) error {
	return v2.NewError(v2.ErrNotFound, fmt.Sprintf("page %v not found", ref))
}

// Create inserts a new page with its category and tag links.
func (s *Service) Create(ctx context.Context, a v2.Actor, in CreatePageBody) (*Page, error) {
	if err := s.requireWritePerm(a); err != nil {
		return nil, err
	}
	if err := handler.ValidateSlugFormat(in.Slug); err != "" {
		return nil, v2.NewValidationError(map[string]string{"slug": err}, "Validation failed")
	}
	summary, err := validateSummary(in.Summary)
	if err != nil {
		return nil, err
	}
	if err := s.validateBodyMarkup(in.Body); err != nil {
		return nil, err
	}
	if err := s.ensureSlugUnique(ctx, in.Slug, 0); err != nil {
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
		return nil, v2.NewError(v2.ErrInternal, "Failed to resolve default language")
	}

	for _, catID := range in.CategoryIDs {
		if _, err := s.queries.GetCategoryByID(ctx, catID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, v2.NewValidationError(
					map[string]string{"category_ids": fmt.Sprintf("Category %d not found", catID)},
					"Validation failed",
				)
			}
			return nil, v2.NewError(v2.ErrInternal, "Failed to validate category")
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
		AuthorID:          a.APIKey.CreatedBy,
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to start transaction")
	}
	defer tx.Rollback() //nolint:errcheck
	txq := s.queries.WithTx(tx)

	canCreateTags := a.HasPermission(model.PermissionTaxonomyWrite)
	tagIDs, err := resolveTagIDs(ctx, txq, in.TagIDs, in.TagNames, langCode, canCreateTags)
	if err != nil {
		return nil, err
	}

	page, err := txq.CreatePage(ctx, params)
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to create page")
	}
	if err := linkCategories(ctx, txq, page.ID, in.CategoryIDs); err != nil {
		return nil, err
	}
	if err := linkTags(ctx, txq, page.ID, tagIDs); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to commit page")
	}
	s.invalidatePageCache(page.ID)

	dto := dtoFromStore(page)
	s.populateIncludes(ctx, &dto, page.ID, true, ListFilter{IncludeCategories: true, IncludeTags: true})
	return &dto, nil
}

// Update applies a partial update to a page.
func (s *Service) Update(ctx context.Context, a v2.Actor, id int64, in UpdatePageBody) (*Page, error) {
	if err := s.requireWritePerm(a); err != nil {
		return nil, err
	}
	existing, err := s.queries.GetPageByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, pageNotFound(id)
		}
		return nil, v2.NewError(v2.ErrInternal, "Failed to load page")
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
				return nil, v2.NewError(v2.ErrInternal, "Failed to validate category")
			}
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to start transaction")
	}
	defer tx.Rollback() //nolint:errcheck
	txq := s.queries.WithTx(tx)

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

	page, err := txq.UpdatePage(ctx, params)
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to update page")
	}
	if in.CategoryIDs != nil {
		if err := txq.ClearPageCategories(ctx, page.ID); err != nil {
			return nil, v2.NewError(v2.ErrInternal, "Failed to clear categories")
		}
		if err := linkCategories(ctx, txq, page.ID, *in.CategoryIDs); err != nil {
			return nil, err
		}
	}
	if hasTagChange {
		if err := txq.ClearPageTags(ctx, page.ID); err != nil {
			return nil, v2.NewError(v2.ErrInternal, "Failed to clear tags")
		}
		if err := linkTags(ctx, txq, page.ID, newTagIDs); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to commit page update")
	}
	s.invalidatePageCache(page.ID)

	dto := dtoFromStore(page)
	s.populateIncludes(ctx, &dto, page.ID, true, ListFilter{IncludeCategories: true, IncludeTags: true})
	return &dto, nil
}

// applyUpdate copies validated fields from UpdatePageBody onto the UpdatePageParams.
func (s *Service) applyUpdate(ctx context.Context, a v2.Actor, in *UpdatePageBody, params *store.UpdatePageParams, existing store.Page) error {
	if in.Title != nil {
		params.Title = *in.Title
	}
	if in.Slug != nil {
		if msg := handler.ValidateSlugFormat(*in.Slug); msg != "" {
			return v2.NewValidationError(map[string]string{"slug": msg}, "Validation failed")
		}
		if err := s.ensureSlugUnique(ctx, *in.Slug, existing.ID); err != nil {
			return err
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
		return v2.NewError(v2.ErrInternal, "Failed to load page")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v2.NewError(v2.ErrInternal, "Failed to start transaction")
	}
	defer tx.Rollback() //nolint:errcheck
	txq := s.queries.WithTx(tx)
	if err := txq.ClearPageCategories(ctx, page.ID); err != nil {
		return v2.NewError(v2.ErrInternal, "Failed to clear categories")
	}
	if err := txq.ClearPageTags(ctx, page.ID); err != nil {
		return v2.NewError(v2.ErrInternal, "Failed to clear tags")
	}
	if err := txq.DeletePageVersions(ctx, page.ID); err != nil {
		return v2.NewError(v2.ErrInternal, "Failed to delete page versions")
	}
	if err := txq.DeletePage(ctx, page.ID); err != nil {
		return v2.NewError(v2.ErrInternal, "Failed to delete page")
	}
	if err := tx.Commit(); err != nil {
		return v2.NewError(v2.ErrInternal, "Failed to commit delete")
	}
	s.invalidatePageCache(page.ID)
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
		return nil, v2.NewError(v2.ErrInternal, "Failed to load page")
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
		return nil, v2.NewError(v2.ErrInternal, "Failed to load page")
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
	switch {
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
		return nil, v2.NewError(v2.ErrInternal, "Failed to list pages")
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
