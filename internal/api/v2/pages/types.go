// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package pages is the /api/v2/pages domain. The Service holds all business
// logic (validation, transactions, cache invalidation, event logging). The
// operations.go adapters are thin: parse huma input → call Service → format
// output or error. No HTTP types leak into the service layer.
package pages

import "time"

// Page is the DTO returned by every page response. Derived from the sqlc
// store.Page but with nullable columns replaced by pointers for clean JSON.
type Page struct {
	ID                int64      `json:"id"`
	Title             string     `json:"title"`
	Slug              string     `json:"slug"`
	Body              string     `json:"body"`
	Summary           string     `json:"summary,omitempty"`
	Status            string     `json:"status" enum:"draft,published"`
	PageType          string     `json:"page_type" enum:"post,page"`
	AuthorID          int64      `json:"author_id"`
	LanguageCode      string     `json:"language_code"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	PublishedAt       *time.Time `json:"published_at,omitempty"`
	FeaturedImageID   *int64     `json:"featured_image_id,omitempty"`
	HideFeaturedImage bool       `json:"hide_featured_image"`
	ExcludeFromLists  bool       `json:"exclude_from_lists"`
	MetaTitle         string     `json:"meta_title,omitempty"`
	MetaDescription   string     `json:"meta_description,omitempty"`
	MetaKeywords      string     `json:"meta_keywords,omitempty"`
	OGImageID         *int64     `json:"og_image_id,omitempty"`
	NoIndex           bool       `json:"no_index"`
	NoFollow          bool       `json:"no_follow"`
	CanonicalURL      string     `json:"canonical_url,omitempty"`
	ScheduledAt       *time.Time `json:"scheduled_at,omitempty"`
	VideoURL          string     `json:"video_url,omitempty"`
	VideoTitle        string     `json:"video_title,omitempty"`
	Author            *Author    `json:"author,omitempty"`
	Categories        []Category `json:"categories,omitempty"`
	Tags              []Tag      `json:"tags,omitempty"`
}

// Author is the inline author reference on a Page response. Email is only
// populated when the caller is authenticated, to prevent enumeration.
type Author struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

// Category is the inline category reference on a Page response.
type Category struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
}

// Tag is the inline tag reference on a Page response.
type Tag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// CreatePageBody is the validated service-level input for creating a page. The
// huma operation parses the request body into this type via its struct tags.
type CreatePageBody struct {
	Title             string    `json:"title" required:"true" minLength:"1" maxLength:"255"`
	Slug              string    `json:"slug" required:"true" minLength:"1" maxLength:"255" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$" doc:"Lowercase alphanumeric with dashes. Must be unique."`
	Body              string    `json:"body"`
	Summary           string    `json:"summary,omitempty" maxLength:"500" doc:"Short plaintext summary, trimmed. Max 500 characters (runes)."`
	Status            string    `json:"status,omitempty" enum:"draft,published" default:"draft"`
	PageType          string    `json:"page_type,omitempty" enum:"post,page" default:"post"`
	LanguageCode      *string   `json:"language_code,omitempty" doc:"Falls back to system default if omitted."`
	FeaturedImageID   *int64    `json:"featured_image_id,omitempty"`
	HideFeaturedImage bool      `json:"hide_featured_image,omitempty"`
	ExcludeFromLists  bool      `json:"exclude_from_lists,omitempty"`
	MetaTitle         string    `json:"meta_title,omitempty"`
	MetaDescription   string    `json:"meta_description,omitempty"`
	MetaKeywords      string    `json:"meta_keywords,omitempty"`
	OGImageID         *int64    `json:"og_image_id,omitempty"`
	NoIndex           bool      `json:"no_index,omitempty"`
	NoFollow          bool      `json:"no_follow,omitempty"`
	CanonicalURL      string    `json:"canonical_url,omitempty" format:"uri" maxLength:"2048"`
	ScheduledAt       *string   `json:"scheduled_at,omitempty" format:"date-time" doc:"RFC3339 timestamp."`
	CategoryIDs       []int64   `json:"category_ids,omitempty"`
	TagIDs            []int64   `json:"tag_ids,omitempty"`
	TagNames          []string  `json:"tags,omitempty" doc:"Tag names; new tags are created if the actor has taxonomy:write."`
	VideoURL          string    `json:"video_url,omitempty" format:"uri" maxLength:"2048"`
	VideoTitle        string    `json:"video_title,omitempty"`
}

// UpdatePageBody is the patch-style input for updating a page. Pointer fields
// distinguish "not provided" from zero values. CategoryIDs / TagIDs / TagNames
// pointers let callers explicitly clear collections by sending an empty array.
type UpdatePageBody struct {
	Title             *string   `json:"title,omitempty" minLength:"1" maxLength:"255"`
	Slug              *string   `json:"slug,omitempty" minLength:"1" maxLength:"255" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$"`
	Body              *string   `json:"body,omitempty"`
	Summary           *string   `json:"summary,omitempty" maxLength:"500"`
	Status            *string   `json:"status,omitempty" enum:"draft,published"`
	PageType          *string   `json:"page_type,omitempty" enum:"post,page"`
	FeaturedImageID   *int64    `json:"featured_image_id,omitempty"`
	HideFeaturedImage *bool     `json:"hide_featured_image,omitempty"`
	ExcludeFromLists  *bool     `json:"exclude_from_lists,omitempty"`
	MetaTitle         *string   `json:"meta_title,omitempty"`
	MetaDescription   *string   `json:"meta_description,omitempty"`
	MetaKeywords      *string   `json:"meta_keywords,omitempty"`
	OGImageID         *int64    `json:"og_image_id,omitempty"`
	NoIndex           *bool     `json:"no_index,omitempty"`
	NoFollow          *bool     `json:"no_follow,omitempty"`
	CanonicalURL      *string   `json:"canonical_url,omitempty" format:"uri" maxLength:"2048"`
	ScheduledAt       *string   `json:"scheduled_at,omitempty" format:"date-time"`
	CategoryIDs       *[]int64  `json:"category_ids,omitempty"`
	TagIDs            *[]int64  `json:"tag_ids,omitempty"`
	TagNames          *[]string `json:"tags,omitempty"`
	VideoURL          *string   `json:"video_url,omitempty" format:"uri" maxLength:"2048"`
	VideoTitle        *string   `json:"video_title,omitempty"`
}

// ListFilter is the input for Service.List.
type ListFilter struct {
	Page              int
	PerPage           int
	Status            string // "", "draft", "published"
	CategoryID        int64  // 0 = unset
	TagID             int64  // 0 = unset
	IncludeAuthor     bool
	IncludeCategories bool
	IncludeTags       bool
}

// ListResult is the paginated list return value from Service.List.
type ListResult struct {
	Pages   []Page
	Total   int64
	Page    int
	PerPage int
}
