// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import (
	"database/sql"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

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

// NewReader creates a new Elefant database reader.
func NewReader(dsn string, tablePrefix string) (*Reader, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			slog.Error("failed to close database after ping failure", "error", closeErr)
		}
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return &Reader{db: db, prefix: tablePrefix}, nil
}

// Close closes the database connection.
func (r *Reader) Close() error {
	return r.db.Close()
}

// detectColumns checks which columns exist in the blog_post table.
// Columns slug, description, and keywords were added in Elefant v1.1.5.
func (r *Reader) detectColumns() error {
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
	rows, err := r.db.Query(query, tableName)
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
func (r *Reader) queryBlogPosts(whereClause string) ([]BlogPost, error) {
	// Detect schema to know which columns exist
	if err := r.detectColumns(); err != nil {
		return nil, fmt.Errorf("failed to detect schema: %w", err)
	}

	cols := r.buildBlogPostColumns()
	query := fmt.Sprintf(`SELECT %s FROM %sblog_post%s ORDER BY ts DESC`, cols, r.prefix, whereClause)

	rows, err := r.db.Query(query)
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
func (r *Reader) GetBlogPosts() ([]BlogPost, error) {
	return r.queryBlogPosts("")
}

// GetPublishedBlogPosts retrieves only published blog posts.
func (r *Reader) GetPublishedBlogPosts() ([]BlogPost, error) {
	return r.queryBlogPosts(" WHERE published = 'yes'")
}

// GetTags retrieves all unique tags from the blog_tag table.
func (r *Reader) GetTags() ([]BlogTag, error) {
	query := fmt.Sprintf(`SELECT id FROM %sblog_tag ORDER BY id`, r.prefix)

	rows, err := r.db.Query(query)
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
func (r *Reader) GetUsers() ([]User, error) {
	query := fmt.Sprintf(`SELECT id, email, name FROM %suser ORDER BY id`, r.prefix)

	rows, err := r.db.Query(query)
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
func (r *Reader) GetPostCount() (int, error) {
	var count int
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %sblog_post`, r.prefix)
	err := r.db.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count posts: %w", err)
	}
	return count, nil
}

// GetPublishedPostCount returns the number of published blog posts.
func (r *Reader) GetPublishedPostCount() (int, error) {
	var count int
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %sblog_post WHERE published = 'yes'`, r.prefix)
	err := r.db.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count published posts: %w", err)
	}
	return count, nil
}

// GetTagCount returns the total number of tags.
func (r *Reader) GetTagCount() (int, error) {
	var count int
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %sblog_tag`, r.prefix)
	err := r.db.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count tags: %w", err)
	}
	return count, nil
}

// allowedMediaMimeTypes defines MIME types that can be imported.
var allowedMediaMimeTypes = map[string]bool{
	"image/jpeg":    true,
	"image/png":     true,
	"image/gif":     true,
	"image/webp":    true,
	"application/pdf": true,
	"video/mp4":     true,
	"video/webm":    true,
}

// ScanMediaFiles scans the Elefant files directory for media files.
// It returns a list of MediaFile structs for files that match allowed MIME types.
// Note: filesPath comes from admin configuration, reducing injection risk,
// but we still validate it for defense in depth.
func ScanMediaFiles(filesPath string) ([]MediaFile, error) {
	if filesPath == "" {
		return nil, fmt.Errorf("files path is empty")
	}

	// Clean the path and check for traversal attempts
	cleanPath := filepath.Clean(filesPath)
	if strings.Contains(cleanPath, "..") {
		return nil, fmt.Errorf("invalid files path: path traversal detected")
	}

	// Verify directory exists
	info, err := os.Stat(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to access files directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("files path is not a directory: %s", cleanPath)
	}

	var files []MediaFile

	err = filepath.Walk(cleanPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get MIME type from extension
		mimeType := getMimeTypeFromExt(path)
		if mimeType == "" || !allowedMediaMimeTypes[mimeType] {
			return nil
		}

		// Get relative path from cleanPath
		relPath, err := filepath.Rel(cleanPath, path)
		if err != nil {
			relPath = filepath.Base(path)
		}

		files = append(files, MediaFile{
			Path:     relPath,
			FullPath: path,
			Filename: info.Name(),
			Size:     info.Size(),
			MimeType: mimeType,
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to scan files directory: %w", err)
	}

	return files, nil
}

// getMimeTypeFromExt returns the MIME type for a file based on its extension.
func getMimeTypeFromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return ""
	}

	// Use standard library first
	mimeType := mime.TypeByExtension(ext)
	if mimeType != "" {
		// Strip charset suffix if present (e.g., "text/plain; charset=utf-8")
		if idx := strings.Index(mimeType, ";"); idx != -1 {
			mimeType = strings.TrimSpace(mimeType[:idx])
		}
		return mimeType
	}

	// Fallback for common types
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	}

	return ""
}
