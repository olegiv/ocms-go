// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import (
	"database/sql"
	"time"
)

// BlogPost represents a blog post from Elefant CMS.
type BlogPost struct {
	ID          int64          // Post ID
	Title       string         // Post title
	Slug        string         // URL slug
	Body        string         // HTML body content
	Timestamp   time.Time      // Publication timestamp
	Author      string         // Author name or ID
	Published   string         // "yes", "no", or "que" (queued)
	Tags        string         // JSON array of tag strings
	Thumbnail   sql.NullString // Featured image path (nullable)
	Description sql.NullString // Meta description (nullable)
	Keywords    sql.NullString // Meta keywords (nullable)
	Extra       sql.NullString // JSON extended fields (nullable)
}

// IsPublished returns true if the post is published.
func (p *BlogPost) IsPublished() bool {
	return p.Published == "yes"
}

// BlogTag represents a tag from Elefant CMS.
type BlogTag struct {
	ID string // Tag name/slug (same field in Elefant)
}

// User represents a user from Elefant CMS.
type User struct {
	ID    int64  // User ID
	Email string // Email address
	Name  string // Display name
}
