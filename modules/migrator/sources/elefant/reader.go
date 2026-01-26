// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import (
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

// Reader reads data from an Elefant CMS MySQL database.
type Reader struct {
	db     *sql.DB
	prefix string // Table prefix (e.g., "elefant_")
}

// NewReader creates a new Elefant database reader.
func NewReader(dsn string, tablePrefix string) (*Reader, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return &Reader{db: db, prefix: tablePrefix}, nil
}

// Close closes the database connection.
func (r *Reader) Close() error {
	return r.db.Close()
}

// GetBlogPosts retrieves all blog posts from the database.
func (r *Reader) GetBlogPosts() ([]BlogPost, error) {
	query := fmt.Sprintf(`
		SELECT
			id, title, slug, body, ts, author, published,
			tags, thumbnail, description, keywords, extra
		FROM %sblog_post
		ORDER BY ts DESC
	`, r.prefix)

	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query blog posts: %w", err)
	}
	defer rows.Close()

	var posts []BlogPost
	for rows.Next() {
		var p BlogPost
		if err := rows.Scan(
			&p.ID, &p.Title, &p.Slug, &p.Body, &p.Timestamp, &p.Author, &p.Published,
			&p.Tags, &p.Thumbnail, &p.Description, &p.Keywords, &p.Extra,
		); err != nil {
			return nil, fmt.Errorf("failed to scan blog post: %w", err)
		}
		posts = append(posts, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating blog posts: %w", err)
	}

	return posts, nil
}

// GetPublishedBlogPosts retrieves only published blog posts.
func (r *Reader) GetPublishedBlogPosts() ([]BlogPost, error) {
	query := fmt.Sprintf(`
		SELECT
			id, title, slug, body, ts, author, published,
			tags, thumbnail, description, keywords, extra
		FROM %sblog_post
		WHERE published = 'yes'
		ORDER BY ts DESC
	`, r.prefix)

	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query published blog posts: %w", err)
	}
	defer rows.Close()

	var posts []BlogPost
	for rows.Next() {
		var p BlogPost
		if err := rows.Scan(
			&p.ID, &p.Title, &p.Slug, &p.Body, &p.Timestamp, &p.Author, &p.Published,
			&p.Tags, &p.Thumbnail, &p.Description, &p.Keywords, &p.Extra,
		); err != nil {
			return nil, fmt.Errorf("failed to scan blog post: %w", err)
		}
		posts = append(posts, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating blog posts: %w", err)
	}

	return posts, nil
}

// GetTags retrieves all unique tags from the blog_tag table.
func (r *Reader) GetTags() ([]BlogTag, error) {
	query := fmt.Sprintf(`SELECT id FROM %sblog_tag ORDER BY id`, r.prefix)

	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tags: %w", err)
	}
	defer rows.Close()

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
	defer rows.Close()

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
