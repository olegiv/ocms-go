// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package phpnuke

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

// Reader reads content from a PHP-Nuke MySQL database.
//
// Every PHP-Nuke install picks its own table prefix at setup time — the
// installer defaults to "nuke_" but nothing enforces it, so the prefix is
// configuration rather than a constant.
type Reader struct {
	db     *sql.DB
	prefix string
}

// sanitizeTablePrefix validates a table prefix and returns the sanitized value.
// The implementation is shared with every other migrator source.
var sanitizeTablePrefix = shared.SanitizeTablePrefix

// NewReader opens a bounded connection to a PHP-Nuke database.
func NewReader(ctx context.Context, dsn string, tablePrefix string) (*Reader, error) {
	// Keep the sanitizer's own output, never the raw config string, so a
	// tainted value cannot survive on the struct.
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

	// Test the connection under its own deadline so a black-holed host cannot
	// pin this goroutine and its socket for the OS TCP timeout.
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

// Close closes the database connection.
func (r *Reader) Close() error {
	return r.db.Close()
}

// Prefix returns the sanitized table prefix in use.
func (r *Reader) Prefix() string {
	return r.prefix
}

// table returns a safely quoted prefixed table name.
//
// Table names cannot be bound as query parameters, so the only defence is to
// prove both halves are plain identifiers before interpolating them.
func (r *Reader) table(name string) (string, error) {
	safePrefix, err := sanitizeTablePrefix(r.prefix)
	if err != nil {
		return "", fmt.Errorf("invalid table prefix: %w", err)
	}
	safeName, err := shared.SanitizeIdentifier(name)
	if err != nil {
		return "", fmt.Errorf("invalid table name: %w", err)
	}
	return "`" + safePrefix + safeName + "`", nil
}

// countRows runs a COUNT(*) against a prefixed table under the caller's context.
func (r *Reader) countRows(ctx context.Context, name, failMsg string) (int, error) {
	table, err := r.table(name)
	if err != nil {
		return 0, err
	}
	var count int
	query := "SELECT COUNT(*) FROM " + table
	if err := r.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("%s: %w", failMsg, err)
	}
	return count, nil
}

// closeRows closes a result set, logging rather than dropping a close failure.
func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		slog.Error("failed to close rows", "error", err)
	}
}

// StoryCount returns the number of news stories.
func (r *Reader) StoryCount(ctx context.Context) (int, error) {
	return r.countRows(ctx, "stories", "failed to count stories")
}

// StaticPageCount returns the number of static pages.
func (r *Reader) StaticPageCount(ctx context.Context) (int, error) {
	return r.countRows(ctx, "pages", "failed to count pages")
}

// TopicCount returns the number of topics.
func (r *Reader) TopicCount(ctx context.Context) (int, error) {
	return r.countRows(ctx, "topics", "failed to count topics")
}

// GetStories retrieves every news story, oldest first so that a re-run
// allocates slug suffixes in a stable order.
func (r *Reader) GetStories(ctx context.Context) ([]Story, error) {
	table, err := r.table("stories")
	if err != nil {
		return nil, err
	}
	query := "SELECT sid, catid, aid, title, time, hometext, bodytext, topic, informant FROM " +
		table + " ORDER BY sid"

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query stories: %w", err)
	}
	defer closeRows(rows)

	var stories []Story
	for rows.Next() {
		var s Story
		if err := rows.Scan(&s.ID, &s.CategoryID, &s.AuthorID, &s.Title, &s.Time,
			&s.HomeText, &s.BodyText, &s.TopicID, &s.Informant); err != nil {
			return nil, fmt.Errorf("failed to scan story: %w", err)
		}
		stories = append(stories, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating stories: %w", err)
	}
	return stories, nil
}

// GetStaticPages retrieves every static page.
func (r *Reader) GetStaticPages(ctx context.Context) ([]StaticPage, error) {
	table, err := r.table("pages")
	if err != nil {
		return nil, err
	}
	query := "SELECT pid, cid, title, subtitle, active, page_header, text, page_footer, signature, date FROM " +
		table + " ORDER BY pid"

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query pages: %w", err)
	}
	defer closeRows(rows)

	var pages []StaticPage
	for rows.Next() {
		var p StaticPage
		if err := rows.Scan(&p.ID, &p.CategoryID, &p.Title, &p.Subtitle, &p.Active,
			&p.Header, &p.Text, &p.Footer, &p.Signature, &p.Date); err != nil {
			return nil, fmt.Errorf("failed to scan page: %w", err)
		}
		pages = append(pages, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating pages: %w", err)
	}
	return pages, nil
}

// GetTopics retrieves every topic.
func (r *Reader) GetTopics(ctx context.Context) ([]Topic, error) {
	table, err := r.table("topics")
	if err != nil {
		return nil, err
	}
	query := "SELECT topicid, topicname, topictext FROM " + table + " ORDER BY topicid"

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query topics: %w", err)
	}
	defer closeRows(rows)

	var topics []Topic
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.Name, &t.Text); err != nil {
			return nil, fmt.Errorf("failed to scan topic: %w", err)
		}
		topics = append(topics, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating topics: %w", err)
	}
	return topics, nil
}

// getCategories reads an id/title category table.
func (r *Reader) getCategories(ctx context.Context, name, idColumn string) ([]Category, error) {
	table, err := r.table(name)
	if err != nil {
		return nil, err
	}
	safeID, err := shared.SanitizeIdentifier(idColumn)
	if err != nil {
		return nil, fmt.Errorf("invalid id column: %w", err)
	}
	query := fmt.Sprintf("SELECT `%s`, title FROM %s ORDER BY `%s`", safeID, table, safeID)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query %s: %w", name, err)
	}
	defer closeRows(rows)

	var categories []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Title); err != nil {
			return nil, fmt.Errorf("failed to scan %s row: %w", name, err)
		}
		categories = append(categories, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating %s: %w", name, err)
	}
	return categories, nil
}

// GetStoryCategories retrieves the story category table.
func (r *Reader) GetStoryCategories(ctx context.Context) ([]Category, error) {
	return r.getCategories(ctx, "stories_cat", "catid")
}

// GetPageCategories retrieves the static page category table.
func (r *Reader) GetPageCategories(ctx context.Context) ([]Category, error) {
	return r.getCategories(ctx, "pages_categories", "cid")
}

// GetEncyclopediaEntries retrieves every encyclopedia container.
func (r *Reader) GetEncyclopediaEntries(ctx context.Context) ([]EncyclopediaEntry, error) {
	table, err := r.table("encyclopedia")
	if err != nil {
		return nil, err
	}
	query := "SELECT eid, title, description, active FROM " + table + " ORDER BY eid"

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query encyclopedia: %w", err)
	}
	defer closeRows(rows)

	var entries []EncyclopediaEntry
	for rows.Next() {
		var e EncyclopediaEntry
		if err := rows.Scan(&e.ID, &e.Title, &e.Description, &e.Active); err != nil {
			return nil, fmt.Errorf("failed to scan encyclopedia entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating encyclopedia: %w", err)
	}
	return entries, nil
}

// GetEncyclopediaTerms retrieves every encyclopedia definition, grouped by the
// encyclopedia it belongs to.
func (r *Reader) GetEncyclopediaTerms(ctx context.Context) (map[int64][]EncyclopediaTerm, error) {
	table, err := r.table("encyclopedia_text")
	if err != nil {
		return nil, err
	}
	query := "SELECT tid, eid, title, text FROM " + table + " ORDER BY eid, tid"

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query encyclopedia terms: %w", err)
	}
	defer closeRows(rows)

	terms := make(map[int64][]EncyclopediaTerm)
	for rows.Next() {
		var t EncyclopediaTerm
		if err := rows.Scan(&t.ID, &t.EntryID, &t.Title, &t.Text); err != nil {
			return nil, fmt.Errorf("failed to scan encyclopedia term: %w", err)
		}
		terms[t.Entry()] = append(terms[t.Entry()], t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating encyclopedia terms: %w", err)
	}
	return terms, nil
}

// GetStoryAuthors retrieves only the accounts credited on a story, either as
// the publishing author (stories.aid) or the submitter (stories.informant).
//
// PHP-Nuke sites accumulate large registration tables — most of which never
// wrote anything — so importing `users` wholesale would create hundreds of
// dormant accounts. Both columns hold a username, which is what joins them to
// the users table; neither is a foreign key.
func (r *Reader) GetStoryAuthors(ctx context.Context) ([]User, error) {
	users, err := r.table("users")
	if err != nil {
		return nil, err
	}
	stories, err := r.table("stories")
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT u.user_id, u.username, u.name, u.user_email
		FROM %s u
		WHERE u.username IN (SELECT s.informant FROM %s s WHERE s.informant <> '')
		   OR u.username IN (SELECT s.aid FROM %s s WHERE s.aid <> '')
		ORDER BY u.user_id`, users, stories, stories)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query story authors: %w", err)
	}
	defer closeRows(rows)

	var authors []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Name, &u.Email); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		authors = append(authors, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating story authors: %w", err)
	}
	return authors, nil
}
