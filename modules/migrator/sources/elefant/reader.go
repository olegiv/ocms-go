// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
)

// pingTimeout bounds the initial connectivity check.
const pingTimeout = 10 * time.Second

// Reader reads data from an Elefant CMS MySQL database.
type Reader struct {
	db     *sql.DB
	prefix string // Table prefix (e.g., "elefant_")

	// Schema version detection (columns added in Elefant v1.1.5)
	hasSlug        bool
	hasDescription bool
	hasKeywords    bool
	schemaDetected bool
}

// sanitizeTablePrefix validates a table prefix and returns the sanitized value.
// The implementation is shared with every other migrator source.
var sanitizeTablePrefix = shared.SanitizeTablePrefix

// NewReader creates a new Elefant database reader.
func NewReader(ctx context.Context, dsn string, tablePrefix string) (*Reader, error) {
	// Validate the table prefix and keep the sanitizer's own output, never the
	// raw config string, so a tainted value cannot survive on the struct.
	safePrefix, err := sanitizeTablePrefix(tablePrefix)
	if err != nil {
		return nil, fmt.Errorf("invalid table prefix: %w", err)
	}

	db, openErr := sql.Open("mysql", dsn)
	if openErr != nil {
		return nil, fmt.Errorf("failed to open database: %w", openErr)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetConnMaxLifetime(connMaxLifetime)

	// Test connection under its own deadline so a black-holed host cannot pin
	// this goroutine and its socket for the OS TCP timeout.
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Error("failed to close database after ping failure", "error", closeErr)
		}
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return &Reader{db: db, prefix: safePrefix}, nil
}

// countRows runs a COUNT(*) against a prefixed table under the caller's context.
func (r *Reader) countRows(ctx context.Context, table, whereClause, failMsg string) (int, error) {
	safePrefix, err := sanitizeTablePrefix(r.prefix)
	if err != nil {
		return 0, fmt.Errorf("invalid table prefix: %w", err)
	}

	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM `%s%s`%s", safePrefix, table, whereClause)
	if err := r.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("%s: %w", failMsg, err)
	}
	return count, nil
}

// Close closes the database connection.
func (r *Reader) Close() error {
	return r.db.Close()
}

// detectColumns checks which columns exist in the blog_post table.
// Columns slug, description, and keywords were added in Elefant v1.1.5.
func (r *Reader) detectColumns(ctx context.Context) error {
	if r.schemaDetected {
		return nil
	}

	query := `
		SELECT COLUMN_NAME
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
		AND TABLE_NAME = ?
	`

	tableName := r.prefix + "blog_post"
	rows, err := r.db.QueryContext(ctx, query, tableName)
	if err != nil {
		return fmt.Errorf("failed to query column information: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var columnName string
		if err := rows.Scan(&columnName); err != nil {
			return fmt.Errorf("failed to scan column name: %w", err)
		}

		switch columnName {
		case "slug":
			r.hasSlug = true
		case "description":
			r.hasDescription = true
		case "keywords":
			r.hasKeywords = true
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating columns: %w", err)
	}

	r.schemaDetected = true
	return nil
}

// buildBlogPostColumns returns the column list for blog_post queries based on detected schema.
func (r *Reader) buildBlogPostColumns() string {
	cols := "id, title, body, ts, author, published, tags, thumbnail, extra"
	if r.hasSlug {
		cols = "id, title, slug, body, ts, author, published, tags, thumbnail"
		if r.hasDescription {
			cols += ", description"
		}
		if r.hasKeywords {
			cols += ", keywords"
		}
		cols += ", extra"
	}
	return cols
}

// scanBlogPost scans a single blog post row based on the detected schema.
func (r *Reader) scanBlogPost(rows *sql.Rows) (BlogPost, error) {
	var p BlogPost
	var err error

	switch {
	case r.hasSlug && r.hasDescription && r.hasKeywords:
		// Schema v1.1.5+ with slug, description, and keywords columns
		err = rows.Scan(
			&p.ID, &p.Title, &p.Slug, &p.Body, &p.Timestamp, &p.Author, &p.Published,
			&p.Tags, &p.Thumbnail, &p.Description, &p.Keywords, &p.Extra,
		)
	case r.hasSlug && r.hasDescription:
		err = rows.Scan(
			&p.ID, &p.Title, &p.Slug, &p.Body, &p.Timestamp, &p.Author, &p.Published,
			&p.Tags, &p.Thumbnail, &p.Description, &p.Extra,
		)
	case r.hasSlug && r.hasKeywords:
		err = rows.Scan(
			&p.ID, &p.Title, &p.Slug, &p.Body, &p.Timestamp, &p.Author, &p.Published,
			&p.Tags, &p.Thumbnail, &p.Keywords, &p.Extra,
		)
	case r.hasSlug:
		err = rows.Scan(
			&p.ID, &p.Title, &p.Slug, &p.Body, &p.Timestamp, &p.Author, &p.Published,
			&p.Tags, &p.Thumbnail, &p.Extra,
		)
	default:
		// Older schema without slug/description/keywords
		err = rows.Scan(
			&p.ID, &p.Title, &p.Body, &p.Timestamp, &p.Author, &p.Published,
			&p.Tags, &p.Thumbnail, &p.Extra,
		)
		// Slug will be generated from title in importer
	}

	return p, err
}

// queryBlogPosts executes a blog post query and returns the results.
func (r *Reader) queryBlogPosts(ctx context.Context, whereClause string) ([]BlogPost, error) {
	// Sanitize prefix for SQL injection protection (CodeQL requires returned value)
	safePrefix, err := sanitizeTablePrefix(r.prefix)
	if err != nil {
		return nil, fmt.Errorf("invalid table prefix: %w", err)
	}

	// Detect schema to know which columns exist
	if err := r.detectColumns(ctx); err != nil {
		return nil, fmt.Errorf("failed to detect schema: %w", err)
	}

	cols := r.buildBlogPostColumns()
	query := fmt.Sprintf("SELECT %s FROM `%sblog_post` %s ORDER BY ts DESC", cols, safePrefix, whereClause)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query blog posts: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var posts []BlogPost
	for rows.Next() {
		p, err := r.scanBlogPost(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan blog post: %w", err)
		}
		posts = append(posts, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating blog posts: %w", err)
	}

	return posts, nil
}

// GetBlogPosts retrieves all blog posts from the database.
func (r *Reader) GetBlogPosts(ctx context.Context) ([]BlogPost, error) {
	return r.queryBlogPosts(ctx, "")
}

// GetPublishedBlogPosts retrieves only published blog posts.
func (r *Reader) GetPublishedBlogPosts(ctx context.Context) ([]BlogPost, error) {
	return r.queryBlogPosts(ctx, " WHERE published = 'yes'")
}

// GetWebpages retrieves all webpages from the database.
func (r *Reader) GetWebpages(ctx context.Context) ([]Webpage, error) {
	safePrefix, err := sanitizeTablePrefix(r.prefix)
	if err != nil {
		return nil, fmt.Errorf("invalid table prefix: %w", err)
	}

	query := fmt.Sprintf(
		"SELECT id, title, menu_title, window_title, access, layout, description, keywords, body, extra FROM `%swebpage` ORDER BY id",
		safePrefix,
	)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query webpages: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var pages []Webpage
	for rows.Next() {
		var p Webpage
		if err := rows.Scan(&p.ID, &p.Title, &p.MenuTitle, &p.WindowTitle, &p.Access, &p.Layout, &p.Description, &p.Keywords, &p.Body, &p.Extra); err != nil {
			return nil, fmt.Errorf("failed to scan webpage: %w", err)
		}
		pages = append(pages, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating webpages: %w", err)
	}

	return pages, nil
}

// GetWebpageCount returns the total number of webpages.
func (r *Reader) GetWebpageCount(ctx context.Context) (int, error) {
	return r.countRows(ctx, "webpage", "", "failed to count webpages")
}

// GetTags retrieves all unique tags from the blog_tag table.
func (r *Reader) GetTags(ctx context.Context) ([]BlogTag, error) {
	// Sanitize prefix for SQL injection protection (CodeQL requires returned value)
	safePrefix, err := sanitizeTablePrefix(r.prefix)
	if err != nil {
		return nil, fmt.Errorf("invalid table prefix: %w", err)
	}

	query := fmt.Sprintf("SELECT id FROM `%sblog_tag` ORDER BY id", safePrefix)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tags: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var tags []BlogTag
	for rows.Next() {
		var t BlogTag
		if err := rows.Scan(&t.ID); err != nil {
			return nil, fmt.Errorf("failed to scan tag: %w", err)
		}
		tags = append(tags, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tags: %w", err)
	}

	return tags, nil
}

// GetUsers retrieves all users from the database.
func (r *Reader) GetUsers(ctx context.Context) ([]User, error) {
	// Sanitize prefix for SQL injection protection (CodeQL requires returned value)
	safePrefix, err := sanitizeTablePrefix(r.prefix)
	if err != nil {
		return nil, fmt.Errorf("invalid table prefix: %w", err)
	}

	query := fmt.Sprintf("SELECT id, email, name FROM `%suser` ORDER BY id", safePrefix)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating users: %w", err)
	}

	return users, nil
}

// GetPostCount returns the total number of blog posts.
func (r *Reader) GetPostCount(ctx context.Context) (int, error) {
	return r.countRows(ctx, "blog_post", "", "failed to count posts")
}

// GetPublishedPostCount returns the number of published blog posts.
func (r *Reader) GetPublishedPostCount(ctx context.Context) (int, error) {
	return r.countRows(ctx, "blog_post", " WHERE published = 'yes'", "failed to count published posts")
}

// GetTagCount returns the total number of tags.
func (r *Reader) GetTagCount(ctx context.Context) (int, error) {
	return r.countRows(ctx, "blog_tag", "", "failed to count tags")
}

// allowedMediaMimeTypes defines MIME types that can be imported.
var allowedMediaMimeTypes = shared.AllowedMediaMimeTypes

// ScanMediaFiles scans the Elefant files directory for media files.
var ScanMediaFiles = shared.ScanMediaFiles

// getMimeTypeFromExt returns the MIME type for a file based on its extension.
var getMimeTypeFromExt = shared.MimeTypeFromExt
