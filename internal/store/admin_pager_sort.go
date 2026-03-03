// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"strings"
)

const (
	sortDirAsc  = "asc"
	sortDirDesc = "desc"
)

func normalizeSortDirection(dir string) string {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case sortDirAsc:
		return sortDirAsc
	case sortDirDesc:
		return sortDirDesc
	default:
		return sortDirDesc
	}
}

type ListPagesSortedParams struct {
	Status        sql.NullString `json:"status"`
	LanguageCode  sql.NullString `json:"language_code"`
	CategoryID    sql.NullInt64  `json:"category_id"`
	SearchPattern sql.NullString `json:"search_pattern"`
	ScheduledOnly bool           `json:"scheduled_only"`
	Limit         int64          `json:"limit"`
	Offset        int64          `json:"offset"`
	SortField     string         `json:"sort_field"`
	SortDir       string         `json:"sort_dir"`
}

// ListPagesSorted lists pages with optional filters and safe whitelist sorting.
func (q *Queries) ListPagesSorted(ctx context.Context, arg ListPagesSortedParams) ([]Page, error) {
	var (
		clauses []string
		args    []any
	)

	query := `
SELECT
	p.id, p.title, p.slug, p.body, p.status, p.author_id, p.created_at, p.updated_at,
	p.published_at, p.featured_image_id, p.meta_title, p.meta_description, p.meta_keywords,
	p.og_image_id, p.no_index, p.no_follow, p.canonical_url, p.scheduled_at, p.language_code,
	p.hide_featured_image, p.page_type, p.exclude_from_lists
FROM pages p
`

	if arg.CategoryID.Valid {
		query += "INNER JOIN page_categories pc ON pc.page_id = p.id\n"
		clauses = append(clauses, "pc.category_id = ?")
		args = append(args, arg.CategoryID.Int64)
	}
	if arg.ScheduledOnly {
		clauses = append(clauses, "p.scheduled_at IS NOT NULL", "p.status = 'draft'")
	}
	if arg.Status.Valid {
		clauses = append(clauses, "p.status = ?")
		args = append(args, arg.Status.String)
	}
	if arg.LanguageCode.Valid {
		clauses = append(clauses, "p.language_code = ?")
		args = append(args, arg.LanguageCode.String)
	}
	if arg.SearchPattern.Valid {
		clauses = append(clauses, "(p.title LIKE ? OR p.body LIKE ? OR p.slug LIKE ?)")
		args = append(args, arg.SearchPattern.String, arg.SearchPattern.String, arg.SearchPattern.String)
	}
	if len(clauses) > 0 {
		query += "WHERE " + strings.Join(clauses, " AND ") + "\n"
	}

	query += "ORDER BY " + pagesOrderExpr(arg.SortField, arg.SortDir) + "\n"
	query += "LIMIT ? OFFSET ?"
	args = append(args, arg.Limit, arg.Offset)

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []Page{}
	for rows.Next() {
		var i Page
		if err := rows.Scan(
			&i.ID,
			&i.Title,
			&i.Slug,
			&i.Body,
			&i.Status,
			&i.AuthorID,
			&i.CreatedAt,
			&i.UpdatedAt,
			&i.PublishedAt,
			&i.FeaturedImageID,
			&i.MetaTitle,
			&i.MetaDescription,
			&i.MetaKeywords,
			&i.OgImageID,
			&i.NoIndex,
			&i.NoFollow,
			&i.CanonicalUrl,
			&i.ScheduledAt,
			&i.LanguageCode,
			&i.HideFeaturedImage,
			&i.PageType,
			&i.ExcludeFromLists,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func pagesOrderExpr(field, dir string) string {
	direction := normalizeSortDirection(dir)
	switch field {
	case "title":
		return "p.title " + direction + ", p.id DESC"
	case "status":
		return "p.status " + direction + ", p.updated_at DESC, p.id DESC"
	case "updated_at":
		return "p.updated_at " + direction + ", p.id DESC"
	case "scheduled_at":
		return "CASE WHEN p.scheduled_at IS NULL THEN 1 ELSE 0 END ASC, p.scheduled_at " + direction + ", p.id DESC"
	case "created_at":
		fallthrough
	default:
		return "p.created_at " + direction + ", p.id DESC"
	}
}

type GetTagUsageCountsSortedParams struct {
	Limit     int64  `json:"limit"`
	Offset    int64  `json:"offset"`
	SortField string `json:"sort_field"`
	SortDir   string `json:"sort_dir"`
}

// GetTagUsageCountsSorted lists tags with usage counts and safe whitelist sorting.
func (q *Queries) GetTagUsageCountsSorted(ctx context.Context, arg GetTagUsageCountsSortedParams) ([]GetTagUsageCountsRow, error) {
	query := `
SELECT
	t.id, t.name, t.slug, t.language_code, t.created_at, t.updated_at, COUNT(pt.page_id) AS usage_count
FROM tags t
LEFT JOIN page_tags pt ON pt.tag_id = t.id
GROUP BY t.id, t.name, t.slug, t.language_code, t.created_at, t.updated_at
ORDER BY ` + tagsOrderExpr(arg.SortField, arg.SortDir) + `
LIMIT ? OFFSET ?`

	rows, err := q.db.QueryContext(ctx, query, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []GetTagUsageCountsRow{}
	for rows.Next() {
		var i GetTagUsageCountsRow
		if err := rows.Scan(
			&i.ID,
			&i.Name,
			&i.Slug,
			&i.LanguageCode,
			&i.CreatedAt,
			&i.UpdatedAt,
			&i.UsageCount,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func tagsOrderExpr(field, dir string) string {
	direction := normalizeSortDirection(dir)
	switch field {
	case "name":
		return "t.name " + direction + ", t.id DESC"
	case "created_at":
		return "t.created_at " + direction + ", t.id DESC"
	case "usage_count":
		fallthrough
	default:
		return "usage_count " + direction + ", t.name ASC, t.id DESC"
	}
}

type ListUsersSortedParams struct {
	Limit     int64  `json:"limit"`
	Offset    int64  `json:"offset"`
	SortField string `json:"sort_field"`
	SortDir   string `json:"sort_dir"`
}

// ListUsersSorted lists users with safe whitelist sorting.
func (q *Queries) ListUsersSorted(ctx context.Context, arg ListUsersSortedParams) ([]User, error) {
	query := `
SELECT
	u.id, u.email, u.password_hash, u.role, u.name, u.created_at, u.updated_at, u.last_login_at
FROM users u
ORDER BY ` + usersOrderExpr(arg.SortField, arg.SortDir) + `
LIMIT ? OFFSET ?`

	rows, err := q.db.QueryContext(ctx, query, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []User{}
	for rows.Next() {
		var i User
		if err := rows.Scan(
			&i.ID,
			&i.Email,
			&i.PasswordHash,
			&i.Role,
			&i.Name,
			&i.CreatedAt,
			&i.UpdatedAt,
			&i.LastLoginAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func usersOrderExpr(field, dir string) string {
	direction := normalizeSortDirection(dir)
	switch field {
	case "name":
		return "u.name " + direction + ", u.id DESC"
	case "email":
		return "u.email " + direction + ", u.id DESC"
	case "role":
		return "u.role " + direction + ", u.id DESC"
	case "last_login_at":
		return "CASE WHEN u.last_login_at IS NULL THEN 1 ELSE 0 END ASC, u.last_login_at " + direction + ", u.id DESC"
	case "created_at":
		fallthrough
	default:
		return "u.created_at " + direction + ", u.id DESC"
	}
}

type ListAPIKeysSortedParams struct {
	Limit     int64  `json:"limit"`
	Offset    int64  `json:"offset"`
	SortField string `json:"sort_field"`
	SortDir   string `json:"sort_dir"`
}

// ListAPIKeysSorted lists API keys with safe whitelist sorting.
func (q *Queries) ListAPIKeysSorted(ctx context.Context, arg ListAPIKeysSortedParams) ([]ApiKey, error) {
	query := `
SELECT
	k.id, k.name, k.key_hash, k.key_prefix, k.permissions, k.last_used_at, k.expires_at, k.is_active,
	k.created_by, k.created_at, k.updated_at
FROM api_keys k
ORDER BY ` + apiKeysOrderExpr(arg.SortField, arg.SortDir) + `
LIMIT ? OFFSET ?`

	rows, err := q.db.QueryContext(ctx, query, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ApiKey{}
	for rows.Next() {
		var i ApiKey
		if err := rows.Scan(
			&i.ID,
			&i.Name,
			&i.KeyHash,
			&i.KeyPrefix,
			&i.Permissions,
			&i.LastUsedAt,
			&i.ExpiresAt,
			&i.IsActive,
			&i.CreatedBy,
			&i.CreatedAt,
			&i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func apiKeysOrderExpr(field, dir string) string {
	direction := normalizeSortDirection(dir)
	switch field {
	case "name":
		return "k.name " + direction + ", k.id DESC"
	case "is_active":
		return "k.is_active " + direction + ", k.created_at DESC, k.id DESC"
	case "last_used_at":
		return "CASE WHEN k.last_used_at IS NULL THEN 1 ELSE 0 END ASC, k.last_used_at " + direction + ", k.id DESC"
	case "expires_at":
		return "CASE WHEN k.expires_at IS NULL THEN 1 ELSE 0 END ASC, k.expires_at " + direction + ", k.id DESC"
	case "created_at":
		fallthrough
	default:
		return "k.created_at " + direction + ", k.id DESC"
	}
}

type ListMediaSortedParams struct {
	SearchPattern sql.NullString `json:"search_pattern"`
	MimeType      sql.NullString `json:"mime_type"`
	FolderID      sql.NullInt64  `json:"folder_id"`
	Limit         int64          `json:"limit"`
	Offset        int64          `json:"offset"`
	SortField     string         `json:"sort_field"`
	SortDir       string         `json:"sort_dir"`
}

// ListMediaSorted lists media with optional filters and safe whitelist sorting.
func (q *Queries) ListMediaSorted(ctx context.Context, arg ListMediaSortedParams) ([]Medium, error) {
	var (
		clauses []string
		args    []any
	)

	query := `
SELECT
	m.id, m.uuid, m.filename, m.mime_type, m.size, m.width, m.height, m.alt, m.caption, m.folder_id,
	m.uploaded_by, m.language_code, m.created_at, m.updated_at
FROM media m
`

	switch {
	case arg.SearchPattern.Valid:
		clauses = append(clauses, "(m.filename LIKE ? OR m.alt LIKE ?)")
		args = append(args, arg.SearchPattern.String, arg.SearchPattern.String)
	case arg.MimeType.Valid:
		clauses = append(clauses, "m.mime_type LIKE ?")
		args = append(args, arg.MimeType.String)
	case arg.FolderID.Valid:
		clauses = append(clauses, "m.folder_id = ?")
		args = append(args, arg.FolderID.Int64)
	}

	if len(clauses) > 0 {
		query += "WHERE " + strings.Join(clauses, " AND ") + "\n"
	}
	query += "ORDER BY " + mediaOrderExpr(arg.SortField, arg.SortDir) + "\n"
	query += "LIMIT ? OFFSET ?"
	args = append(args, arg.Limit, arg.Offset)

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []Medium{}
	for rows.Next() {
		var i Medium
		if err := rows.Scan(
			&i.ID,
			&i.Uuid,
			&i.Filename,
			&i.MimeType,
			&i.Size,
			&i.Width,
			&i.Height,
			&i.Alt,
			&i.Caption,
			&i.FolderID,
			&i.UploadedBy,
			&i.LanguageCode,
			&i.CreatedAt,
			&i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func mediaOrderExpr(field, dir string) string {
	direction := normalizeSortDirection(dir)
	switch field {
	case "filename":
		return "m.filename " + direction + ", m.id DESC"
	case "mime_type":
		return "m.mime_type " + direction + ", m.id DESC"
	case "size":
		return "m.size " + direction + ", m.id DESC"
	case "created_at":
		fallthrough
	default:
		return "m.created_at " + direction + ", m.id DESC"
	}
}

type GetFormSubmissionsSortedParams struct {
	FormID    int64  `json:"form_id"`
	Limit     int64  `json:"limit"`
	Offset    int64  `json:"offset"`
	SortField string `json:"sort_field"`
	SortDir   string `json:"sort_dir"`
}

// GetFormSubmissionsSorted lists form submissions with safe whitelist sorting.
func (q *Queries) GetFormSubmissionsSorted(ctx context.Context, arg GetFormSubmissionsSortedParams) ([]FormSubmission, error) {
	query := `
SELECT
	fs.id, fs.form_id, fs.data, fs.ip_address, fs.user_agent, fs.is_read, fs.language_code, fs.created_at
FROM form_submissions fs
WHERE fs.form_id = ?
ORDER BY ` + formSubmissionsOrderExpr(arg.SortField, arg.SortDir) + `
LIMIT ? OFFSET ?`

	rows, err := q.db.QueryContext(ctx, query, arg.FormID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []FormSubmission{}
	for rows.Next() {
		var i FormSubmission
		if err := rows.Scan(
			&i.ID,
			&i.FormID,
			&i.Data,
			&i.IpAddress,
			&i.UserAgent,
			&i.IsRead,
			&i.LanguageCode,
			&i.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func formSubmissionsOrderExpr(field, dir string) string {
	direction := normalizeSortDirection(dir)
	switch field {
	case "id":
		return "fs.id " + direction
	case "is_read":
		return "fs.is_read " + direction + ", fs.created_at DESC, fs.id DESC"
	case "ip_address":
		return "CASE WHEN fs.ip_address IS NULL OR fs.ip_address = '' THEN 1 ELSE 0 END ASC, fs.ip_address " + direction + ", fs.id DESC"
	case "created_at":
		fallthrough
	default:
		return "fs.created_at " + direction + ", fs.id DESC"
	}
}
