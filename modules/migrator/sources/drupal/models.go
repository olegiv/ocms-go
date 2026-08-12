// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"database/sql"
	"strings"
)

// Node is one row of node_field_data — a single content item in its default
// language. Drupal stores created/changed as Unix timestamps.
type Node struct {
	NID       int64
	Type      string // bundle: "article", "page", …
	Langcode  string
	Title     string
	Status    int64 // 1 = published
	UID       int64
	Created   int64
	Changed   int64
	Body      sql.NullString
	Summary   sql.NullString
	Format    sql.NullString
	ImageFID  sql.NullInt64
	ImageAlt  sql.NullString
	TermIDs   []int64 // taxonomy terms referenced by this node
	AliasPath sql.NullString
}

// IsPublished reports whether the node is published.
func (n *Node) IsPublished() bool { return n.Status == 1 }

// Term is one row of taxonomy_term_field_data plus its parent link.
type Term struct {
	TID         int64
	Vocabulary  string // vid: "tags", "categories", …
	Name        string
	Description sql.NullString
	Weight      int64
	ParentTID   int64 // 0 = root
}

// File is one row of file_managed. URI carries a Drupal stream wrapper such as
// "public://2026-01/photo.jpg".
type File struct {
	FID      int64
	UUID     string
	Filename string
	URI      string
	MimeType string
	Size     int64
	Created  int64
	Alt      sql.NullString
}

// Scheme returns the stream wrapper of the file URI ("public", "private",
// "temporary"), or "" when the URI carries no wrapper.
func (f *File) Scheme() string {
	idx := strings.Index(f.URI, "://")
	if idx < 0 {
		return ""
	}
	return f.URI[:idx]
}

// RelPath returns the path of the file relative to its stream-wrapper root.
func (f *File) RelPath() string {
	idx := strings.Index(f.URI, "://")
	if idx < 0 {
		return f.URI
	}
	return f.URI[idx+len("://"):]
}

// User is one row of users_field_data. Drupal password hashes are never read:
// they are not portable to oCMS's Argon2id verifier.
type User struct {
	UID     int64
	Name    string
	Mail    string
	Status  int64
	Created int64
}

// PathAlias is one row of path_alias, e.g. path "/node/12", alias "/about-us".
type PathAlias struct {
	ID       int64
	Path     string
	Alias    string
	Langcode string
	Status   int64
}

// NodeID extracts the node ID from a "/node/<nid>" system path. ok is false for
// aliases pointing at anything else (taxonomy terms, users, views…).
func (p *PathAlias) NodeID() (int64, bool) {
	return parseNodePath(p.Path)
}

// MenuLink is one row of menu_link_content_data.
type MenuLink struct {
	ID       int64
	UUID     string
	Title    string
	MenuName string
	LinkURI  string
	Parent   sql.NullString // "menu_link_content:<uuid>" or empty for a root item
	Weight   int64
	Enabled  int64
}

// ParentUUID returns the UUID of the parent menu link, or "" for a root item.
func (m *MenuLink) ParentUUID() string {
	if !m.Parent.Valid || m.Parent.String == "" {
		return ""
	}
	const prefix = "menu_link_content:"
	if strings.HasPrefix(m.Parent.String, prefix) {
		return strings.TrimPrefix(m.Parent.String, prefix)
	}
	return ""
}
