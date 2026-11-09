// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package idempotency

import "time"

// Receipt admits only approved replay shapes. Store also checks the concrete
// type so embedding an approved receipt cannot add credentials to its JSON.
type Receipt interface {
	idempotencyReceipt()
}

// Page is the approved page response and replay shape. Derived from the sqlc
// store.Page but with nullable columns replaced by pointers for clean JSON.
type Page struct {
	Revision          int64      `json:"revision"`
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

func (Page) idempotencyReceipt() {}
