// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"database/sql"
	"time"
)

// Page statuses
const (
	PageStatusDraft     = "draft"
	PageStatusPublished = "published"
)

// Page represents a CMS page.
type Page struct {
	ID          int64        `json:"id"`
	Title       string       `json:"title"`
	Slug        string       `json:"slug"`
	Body        string       `json:"body"`
	Status      string       `json:"status"`
	AuthorID    int64        `json:"author_id"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	PublishedAt sql.NullTime `json:"published_at,omitempty"`
}

// IsPublished returns true if the page is published.
func (p *Page) IsPublished() bool {
	return p.Status == PageStatusPublished
}

// IsDraft returns true if the page is a draft.
func (p *Page) IsDraft() bool {
	return p.Status == PageStatusDraft
}

// PageVersion represents a historical version of a page.
type PageVersion struct {
	ID        int64     `json:"id"`
	PageID    int64     `json:"page_id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	ChangedBy int64     `json:"changed_by"`
	CreatedAt time.Time `json:"created_at"`
}
