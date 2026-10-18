// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package taxonomy is the /api/v2/tags and /api/v2/categories domain. One
// Service owns both since they share validation patterns (name + slug +
// language code).
package taxonomy

import "time"

// TaxonomyTag is the DTO for tag responses.
type TaxonomyTag struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	LanguageCode string    `json:"language_code"`
	PageCount    int64     `json:"page_count" doc:"Number of pages linked to this tag."`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TaxonomyCategory is the DTO for category responses. When returned from the tree
// list, Children is populated recursively.
type TaxonomyCategory struct {
	ID           int64               `json:"id"`
	Name         string              `json:"name"`
	Slug         string              `json:"slug"`
	Description  string              `json:"description,omitempty"`
	ParentID     *int64              `json:"parent_id,omitempty"`
	Position     int64               `json:"position"`
	LanguageCode string              `json:"language_code"`
	PageCount    int64               `json:"page_count" doc:"Number of pages linked to this category."`
	Children     []*TaxonomyCategory `json:"children,omitempty" doc:"Populated only in tree list mode."`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

// CreateTagBody is the input for creating a tag.
type CreateTagBody struct {
	Name         string  `json:"name" required:"true" minLength:"1" maxLength:"100"`
	Slug         string  `json:"slug" required:"true" minLength:"1" maxLength:"100" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$"`
	LanguageCode *string `json:"language_code,omitempty" doc:"Falls back to system default if omitted."`
}

// UpdateTagBody is the patch input for updating a tag.
type UpdateTagBody struct {
	Name         *string `json:"name,omitempty" minLength:"1" maxLength:"100"`
	Slug         *string `json:"slug,omitempty" minLength:"1" maxLength:"100" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$"`
	LanguageCode *string `json:"language_code,omitempty"`
}

// CreateCategoryBody is the input for creating a category.
type CreateCategoryBody struct {
	Name         string  `json:"name" required:"true" minLength:"1" maxLength:"100"`
	Slug         string  `json:"slug" required:"true" minLength:"1" maxLength:"100" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$"`
	Description  string  `json:"description,omitempty"`
	ParentID     *int64  `json:"parent_id,omitempty" doc:"Parent category id; omit or null for a root category."`
	Position     *int64  `json:"position,omitempty"`
	LanguageCode *string `json:"language_code,omitempty"`
}

// UpdateCategoryBody is the patch input for updating a category. Send
// parent_id: 0 to move the category to the root.
type UpdateCategoryBody struct {
	Name         *string `json:"name,omitempty" minLength:"1" maxLength:"100"`
	Slug         *string `json:"slug,omitempty" minLength:"1" maxLength:"100" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$"`
	Description  *string `json:"description,omitempty"`
	ParentID     *int64  `json:"parent_id,omitempty" doc:"Send 0 to move to root."`
	Position     *int64  `json:"position,omitempty"`
	LanguageCode *string `json:"language_code,omitempty"`
}

// TagListResult is the paginated tag list return.
type TagListResult struct {
	Tags    []TaxonomyTag
	Total   int64
	Page    int
	PerPage int
}
