// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package taxonomy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// Service owns TaxonomyTag and TaxonomyCategory operations end-to-end.
type Service struct {
	db      *sql.DB
	queries *store.Queries
}

// NewService constructs a Taxonomy service.
func NewService(db *sql.DB, queries *store.Queries) *Service {
	return &Service{db: db, queries: queries}
}

// requireWritePerm returns a domain error when the actor cannot write taxonomy.
func (s *Service) requireWritePerm(a v2.Actor) error {
	if a.APIKey == nil {
		return v2.NewError(v2.ErrUnauthorized, "API key required")
	}
	if !a.HasPermission(model.PermissionTaxonomyWrite) {
		return v2.NewError(v2.ErrForbidden, "taxonomy:write permission required")
	}
	return nil
}

// resolveLanguageCode falls back to the system default language. Explicit
// codes are validated for format and existence so unknown strings cannot enter
// language-filtered columns.
func (s *Service) resolveLanguageCode(ctx context.Context, langCode *string) (string, error) {
	if langCode != nil && *langCode != "" {
		if !util.IsValidLangCode(*langCode) {
			return "", v2.NewValidationError(
				map[string]string{"language_code": "Invalid language code format"},
				"Validation failed",
			)
		}
		if _, err := s.queries.GetLanguageByCode(ctx, *langCode); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", v2.NewValidationError(
					map[string]string{"language_code": fmt.Sprintf("Language %q is not configured", *langCode)},
					"Validation failed",
				)
			}
			return "", v2.NewError(v2.ErrInternal, "Failed to look up language")
		}
		return *langCode, nil
	}
	def, err := s.queries.GetDefaultLanguage(ctx)
	if err != nil {
		return "", fmt.Errorf("loading default language: %w", err)
	}
	return def.Code, nil
}

// -----------------------------------------------------------------------------
// Tags
// -----------------------------------------------------------------------------

// ListTags returns a paginated list of tags with page counts.
func (s *Service) ListTags(ctx context.Context, page, perPage int) (*TagListResult, error) {
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 || perPage > 100 {
		perPage = 50
	}
	offset := int64((page - 1) * perPage)
	rows, err := s.queries.GetTagUsageCounts(ctx, store.GetTagUsageCountsParams{Limit: int64(perPage), Offset: offset})
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to list tags")
	}
	total, err := s.queries.CountTags(ctx)
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to count tags")
	}
	out := make([]TaxonomyTag, 0, len(rows))
	for _, t := range rows {
		out = append(out, TaxonomyTag{
			ID:           t.ID,
			Name:         t.Name,
			Slug:         t.Slug,
			LanguageCode: t.LanguageCode,
			PageCount:    t.UsageCount,
			CreatedAt:    t.CreatedAt,
			UpdatedAt:    t.UpdatedAt,
		})
	}
	return &TagListResult{Tags: out, Total: total, Page: page, PerPage: perPage}, nil
}

// GetTag loads a single tag by ID.
func (s *Service) GetTag(ctx context.Context, id int64) (*TaxonomyTag, error) {
	t, err := s.queries.GetTagByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, v2.NewError(v2.ErrNotFound, fmt.Sprintf("tag %d not found", id))
		}
		return nil, v2.NewError(v2.ErrInternal, "Failed to load tag")
	}
	count, _ := s.queries.CountPagesForTag(ctx, t.ID)
	return &TaxonomyTag{
		ID:           t.ID,
		Name:         t.Name,
		Slug:         t.Slug,
		LanguageCode: t.LanguageCode,
		PageCount:    count,
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	}, nil
}

// CreateTag creates a new tag.
func (s *Service) CreateTag(ctx context.Context, a v2.Actor, in CreateTagBody) (*TaxonomyTag, error) {
	if err := s.requireWritePerm(a); err != nil {
		return nil, err
	}
	if err := s.ensureTagSlugUnique(ctx, in.Slug, 0); err != nil {
		return nil, err
	}
	langCode, err := s.resolveLanguageCode(ctx, in.LanguageCode)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tag, err := s.queries.CreateTag(ctx, store.CreateTagParams{
		Name: in.Name, Slug: in.Slug, LanguageCode: langCode, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to create tag")
	}
	return &TaxonomyTag{
		ID:           tag.ID,
		Name:         tag.Name,
		Slug:         tag.Slug,
		LanguageCode: tag.LanguageCode,
		PageCount:    0,
		CreatedAt:    tag.CreatedAt,
		UpdatedAt:    tag.UpdatedAt,
	}, nil
}

// UpdateTag applies a partial update.
func (s *Service) UpdateTag(ctx context.Context, a v2.Actor, id int64, in UpdateTagBody) (*TaxonomyTag, error) {
	if err := s.requireWritePerm(a); err != nil {
		return nil, err
	}
	existing, err := s.queries.GetTagByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, v2.NewError(v2.ErrNotFound, fmt.Sprintf("tag %d not found", id))
		}
		return nil, v2.NewError(v2.ErrInternal, "Failed to load tag")
	}
	params := store.UpdateTagParams{
		ID:           existing.ID,
		Name:         existing.Name,
		Slug:         existing.Slug,
		LanguageCode: existing.LanguageCode,
		UpdatedAt:    time.Now(),
	}
	if in.Name != nil {
		params.Name = *in.Name
	}
	if in.Slug != nil {
		if err := s.ensureTagSlugUnique(ctx, *in.Slug, existing.ID); err != nil {
			return nil, err
		}
		params.Slug = *in.Slug
	}
	if in.LanguageCode != nil {
		lang, err := s.resolveLanguageCode(ctx, in.LanguageCode)
		if err != nil {
			return nil, err
		}
		params.LanguageCode = lang
	}
	tag, err := s.queries.UpdateTag(ctx, params)
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to update tag")
	}
	count, _ := s.queries.CountPagesForTag(ctx, tag.ID)
	return &TaxonomyTag{
		ID:           tag.ID,
		Name:         tag.Name,
		Slug:         tag.Slug,
		LanguageCode: tag.LanguageCode,
		PageCount:    count,
		CreatedAt:    tag.CreatedAt,
		UpdatedAt:    tag.UpdatedAt,
	}, nil
}

// DeleteTag removes a tag. Page_tags associations fall to the DB cascade.
func (s *Service) DeleteTag(ctx context.Context, a v2.Actor, id int64) error {
	if err := s.requireWritePerm(a); err != nil {
		return err
	}
	if _, err := s.queries.GetTagByID(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return v2.NewError(v2.ErrNotFound, fmt.Sprintf("tag %d not found", id))
		}
		return v2.NewError(v2.ErrInternal, "Failed to load tag")
	}
	if err := s.queries.DeleteTag(ctx, id); err != nil {
		return v2.NewError(v2.ErrInternal, "Failed to delete tag")
	}
	return nil
}

func (s *Service) ensureTagSlugUnique(ctx context.Context, slug string, excludeID int64) error {
	var exists int64
	var err error
	if excludeID == 0 {
		exists, err = s.queries.TagSlugExists(ctx, slug)
	} else {
		exists, err = s.queries.TagSlugExistsExcluding(ctx, store.TagSlugExistsExcludingParams{Slug: slug, ID: excludeID})
	}
	if err != nil {
		return v2.NewError(v2.ErrInternal, "Failed to check slug uniqueness")
	}
	if exists > 0 {
		return v2.NewValidationError(map[string]string{"slug": "Slug already exists"}, "Validation failed")
	}
	return nil
}

// -----------------------------------------------------------------------------
// Categories
// -----------------------------------------------------------------------------

// ListCategories returns categories. When tree=true, they are nested by parent_id.
func (s *Service) ListCategories(ctx context.Context, tree bool) ([]*TaxonomyCategory, error) {
	rows, err := s.queries.GetCategoryUsageCounts(ctx)
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to list categories")
	}
	if tree {
		return buildCategoryTree(rows), nil
	}
	out := make([]*TaxonomyCategory, 0, len(rows))
	for _, c := range rows {
		cat := categoryRowToDTO(c)
		out = append(out, &cat)
	}
	return out, nil
}

// GetCategory loads a single category and its direct children.
func (s *Service) GetCategory(ctx context.Context, id int64) (*TaxonomyCategory, error) {
	c, err := s.queries.GetCategoryByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, v2.NewError(v2.ErrNotFound, fmt.Sprintf("category %d not found", id))
		}
		return nil, v2.NewError(v2.ErrInternal, "Failed to load category")
	}
	count, _ := s.queries.CountPagesByCategory(ctx, c.ID)
	dto := categoryToDTO(c, count)
	if children, err := s.queries.ListChildCategories(ctx, util.NullInt64FromValue(c.ID)); err == nil && len(children) > 0 {
		dto.Children = make([]*TaxonomyCategory, 0, len(children))
		for _, child := range children {
			childCount, _ := s.queries.CountPagesByCategory(ctx, child.ID)
			cd := categoryToDTO(child, childCount)
			dto.Children = append(dto.Children, &cd)
		}
	}
	return &dto, nil
}

// CreateCategory creates a category.
func (s *Service) CreateCategory(ctx context.Context, a v2.Actor, in CreateCategoryBody) (*TaxonomyCategory, error) {
	if err := s.requireWritePerm(a); err != nil {
		return nil, err
	}
	if err := s.ensureCategorySlugUnique(ctx, in.Slug, 0); err != nil {
		return nil, err
	}
	if in.ParentID != nil {
		if _, err := s.queries.GetCategoryByID(ctx, *in.ParentID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, v2.NewValidationError(map[string]string{"parent_id": "Parent category not found"}, "Validation failed")
			}
			return nil, v2.NewError(v2.ErrInternal, "Failed to validate parent category")
		}
	}
	langCode, err := s.resolveLanguageCode(ctx, in.LanguageCode)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	params := store.CreateCategoryParams{
		Name: in.Name, Slug: in.Slug, LanguageCode: langCode, CreatedAt: now, UpdatedAt: now,
	}
	if in.Description != "" {
		params.Description = util.NullStringFromValue(in.Description)
	}
	if in.ParentID != nil {
		params.ParentID = util.NullInt64FromPtr(in.ParentID)
	}
	if in.Position != nil {
		params.Position = *in.Position
	}
	cat, err := s.queries.CreateCategory(ctx, params)
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to create category")
	}
	dto := categoryToDTO(cat, 0)
	return &dto, nil
}

// UpdateCategory applies a partial update, including parent reassignment with
// circular-reference detection.
func (s *Service) UpdateCategory(ctx context.Context, a v2.Actor, id int64, in UpdateCategoryBody) (*TaxonomyCategory, error) {
	if err := s.requireWritePerm(a); err != nil {
		return nil, err
	}
	existing, err := s.queries.GetCategoryByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, v2.NewError(v2.ErrNotFound, fmt.Sprintf("category %d not found", id))
		}
		return nil, v2.NewError(v2.ErrInternal, "Failed to load category")
	}
	params := store.UpdateCategoryParams{
		ID:           existing.ID,
		Name:         existing.Name,
		Slug:         existing.Slug,
		Description:  existing.Description,
		ParentID:     existing.ParentID,
		Position:     existing.Position,
		LanguageCode: existing.LanguageCode,
		UpdatedAt:    time.Now(),
	}
	if in.Name != nil {
		params.Name = *in.Name
	}
	if in.Slug != nil {
		if err := s.ensureCategorySlugUnique(ctx, *in.Slug, existing.ID); err != nil {
			return nil, err
		}
		params.Slug = *in.Slug
	}
	if in.Description != nil {
		params.Description = util.NullStringFromValue(*in.Description)
	}
	if in.ParentID != nil {
		if *in.ParentID == existing.ID {
			return nil, v2.NewValidationError(map[string]string{"parent_id": "TaxonomyCategory cannot be its own parent"}, "Validation failed")
		}
		if *in.ParentID == 0 {
			params.ParentID = sql.NullInt64{Valid: false}
		} else {
			if _, err := s.queries.GetCategoryByID(ctx, *in.ParentID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, v2.NewValidationError(map[string]string{"parent_id": "Parent category not found"}, "Validation failed")
				}
				return nil, v2.NewError(v2.ErrInternal, "Failed to validate parent category")
			}
			descendants, _ := s.queries.GetDescendantIDs(ctx, util.NullInt64FromValue(existing.ID))
			for _, did := range descendants {
				if did == *in.ParentID {
					return nil, v2.NewValidationError(map[string]string{"parent_id": "Cannot set a descendant as parent (circular reference)"}, "Validation failed")
				}
			}
			params.ParentID = util.NullInt64FromPtr(in.ParentID)
		}
	}
	if in.Position != nil {
		params.Position = *in.Position
	}
	if in.LanguageCode != nil {
		lang, err := s.resolveLanguageCode(ctx, in.LanguageCode)
		if err != nil {
			return nil, err
		}
		params.LanguageCode = lang
	}
	cat, err := s.queries.UpdateCategory(ctx, params)
	if err != nil {
		return nil, v2.NewError(v2.ErrInternal, "Failed to update category")
	}
	count, _ := s.queries.CountPagesByCategory(ctx, cat.ID)
	dto := categoryToDTO(cat, count)
	return &dto, nil
}

// DeleteCategory removes a category, rejecting the request if any child
// categories still reference it.
func (s *Service) DeleteCategory(ctx context.Context, a v2.Actor, id int64) error {
	if err := s.requireWritePerm(a); err != nil {
		return err
	}
	cat, err := s.queries.GetCategoryByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return v2.NewError(v2.ErrNotFound, fmt.Sprintf("category %d not found", id))
		}
		return v2.NewError(v2.ErrInternal, "Failed to load category")
	}
	if children, err := s.queries.ListChildCategories(ctx, util.NullInt64FromValue(cat.ID)); err == nil && len(children) > 0 {
		return v2.NewError(v2.ErrConflict, "Cannot delete category with child categories. Delete or reassign children first.")
	}
	if err := s.queries.DeleteCategory(ctx, cat.ID); err != nil {
		return v2.NewError(v2.ErrInternal, "Failed to delete category")
	}
	return nil
}

func (s *Service) ensureCategorySlugUnique(ctx context.Context, slug string, excludeID int64) error {
	var exists int64
	var err error
	if excludeID == 0 {
		exists, err = s.queries.CategorySlugExists(ctx, slug)
	} else {
		exists, err = s.queries.CategorySlugExistsExcluding(ctx, store.CategorySlugExistsExcludingParams{Slug: slug, ID: excludeID})
	}
	if err != nil {
		return v2.NewError(v2.ErrInternal, "Failed to check slug uniqueness")
	}
	if exists > 0 {
		return v2.NewValidationError(map[string]string{"slug": "Slug already exists"}, "Validation failed")
	}
	return nil
}

// -----------------------------------------------------------------------------
// DTO helpers
// -----------------------------------------------------------------------------

func categoryToDTO(c store.Category, pageCount int64) TaxonomyCategory {
	dto := TaxonomyCategory{
		ID:           c.ID,
		Name:         c.Name,
		Slug:         c.Slug,
		Position:     c.Position,
		LanguageCode: c.LanguageCode,
		PageCount:    pageCount,
		CreatedAt:    c.CreatedAt,
		UpdatedAt:    c.UpdatedAt,
	}
	if c.Description.Valid {
		dto.Description = c.Description.String
	}
	if c.ParentID.Valid {
		dto.ParentID = &c.ParentID.Int64
	}
	return dto
}

func categoryRowToDTO(c store.GetCategoryUsageCountsRow) TaxonomyCategory {
	dto := TaxonomyCategory{
		ID:           c.ID,
		Name:         c.Name,
		Slug:         c.Slug,
		Position:     c.Position,
		LanguageCode: c.LanguageCode,
		PageCount:    c.UsageCount,
		CreatedAt:    c.CreatedAt,
		UpdatedAt:    c.UpdatedAt,
	}
	if c.Description.Valid {
		dto.Description = c.Description.String
	}
	if c.ParentID.Valid {
		dto.ParentID = &c.ParentID.Int64
	}
	return dto
}

// buildCategoryTree assembles a flat list of categories into a nested tree by
// parent_id. Categories whose parent_id is missing from the input slice become
// roots.
func buildCategoryTree(rows []store.GetCategoryUsageCountsRow) []*TaxonomyCategory {
	byID := make(map[int64]*TaxonomyCategory, len(rows))
	for _, r := range rows {
		dto := categoryRowToDTO(r)
		dto.Children = []*TaxonomyCategory{}
		byID[r.ID] = &dto
	}
	var roots []*TaxonomyCategory
	for _, r := range rows {
		cat := byID[r.ID]
		if r.ParentID.Valid {
			if parent, ok := byID[r.ParentID.Int64]; ok {
				parent.Children = append(parent.Children, cat)
			} else {
				roots = append(roots, cat)
			}
		} else {
			roots = append(roots, cat)
		}
	}
	return roots
}
