// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package phpnuke

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
)

// newSourceStub builds a Reader over SQLite standing in for MySQL. The reader
// only uses portable SQL and backtick-quoted identifiers, both of which SQLite
// accepts, so the query shapes can be exercised without a MySQL server.
func newSourceStub(t *testing.T, schema ...string) *Reader {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("failed to apply schema %q: %v", statement, err)
		}
	}
	return &Reader{db: db, prefix: "tr_"}
}

func TestNewReaderRejectsUnsafeTablePrefix(t *testing.T) {
	for _, prefix := range []string{
		"tr_`; DROP TABLE users; --",
		"tr-",
		"tr ",
		"tr'",
		strings.Repeat("a", 21),
	} {
		t.Run(prefix, func(t *testing.T) {
			// The prefix is validated before any connection is attempted, so an
			// unreachable DSN cannot mask the rejection.
			_, err := NewReader(context.Background(), "user:pass@tcp(127.0.0.1:1)/db", prefix)
			if err == nil {
				t.Fatalf("prefix %q was accepted", prefix)
			}
			if !strings.Contains(err.Error(), "invalid table prefix") {
				t.Errorf("expected a prefix rejection, got: %v", err)
			}
		})
	}
}

func TestReaderTableQuotesPrefixedName(t *testing.T) {
	reader := &Reader{prefix: "tr_"}
	got, err := reader.table("stories")
	if err != nil {
		t.Fatalf("table() error = %v", err)
	}
	if got != "`tr_stories`" {
		t.Errorf("table() = %q, want %q", got, "`tr_stories`")
	}

	if _, err := reader.table("stories; DROP TABLE users"); err == nil {
		t.Error("an unsafe table name was accepted")
	}
}

func TestReaderPrefixIsSanitizerOutput(t *testing.T) {
	reader := &Reader{prefix: "nuke_"}
	if got := reader.Prefix(); got != "nuke_" {
		t.Errorf("Prefix() = %q", got)
	}
}

func TestGetStoriesReadsBothBodyColumns(t *testing.T) {
	reader := newSourceStub(t, `
		CREATE TABLE tr_stories (
			sid INTEGER PRIMARY KEY, catid INTEGER, aid TEXT, title TEXT, time DATETIME,
			hometext TEXT, bodytext TEXT, topic INTEGER, informant TEXT, notes TEXT
		)`,
		`INSERT INTO tr_stories VALUES
			(2, 5, 'Olegiv', 'Second', '2010-01-02 03:04:05', '<p>teaser</p>', '<p>rest</p>', 8, 'sveta', ''),
			(1, 0, 'Olegiv', NULL, NULL, NULL, '<p>only body</p>', 3, '', '')`)

	stories, err := reader.GetStories(context.Background())
	if err != nil {
		t.Fatalf("GetStories() error = %v", err)
	}
	if len(stories) != 2 {
		t.Fatalf("got %d stories, want 2", len(stories))
	}

	// Ordered by sid so slug suffixes are allocated deterministically.
	if stories[0].ID != 1 || stories[1].ID != 2 {
		t.Errorf("stories are not ordered by sid: %d, %d", stories[0].ID, stories[1].ID)
	}
	if stories[0].Title.Valid {
		t.Error("a NULL title should scan as invalid, not empty string")
	}
	if got := shared.NullString(stories[0].BodyText); got != "<p>only body</p>" {
		t.Errorf("bodytext = %q", got)
	}
	if stories[1].HomeText.String != "<p>teaser</p>" || shared.NullString(stories[1].BodyText) != "<p>rest</p>" {
		t.Errorf("both body columns should be read: %+v", stories[1])
	}
	if shared.NullString(stories[1].Informant) != "sveta" || shared.NullString(stories[1].AuthorID) != "Olegiv" {
		t.Errorf("author columns = %q / %q", shared.NullString(stories[1].Informant), shared.NullString(stories[1].AuthorID))
	}
	if stories[1].Topic() != 8 || stories[1].Category() != 5 {
		t.Errorf("taxonomy columns = topic %d category %d", stories[1].Topic(), stories[1].Category())
	}
}

func TestGetTopicsHandlesNullLabels(t *testing.T) {
	reader := newSourceStub(t, `
		CREATE TABLE tr_topics (topicid INTEGER PRIMARY KEY, topicname TEXT, topictext TEXT, topicimage TEXT)`,
		`INSERT INTO tr_topics VALUES
			(1, 'rHotels', 'Отели', 'hotels.gif'),
			(2, 'rOnlyName', NULL, ''),
			(3, NULL, NULL, '')`)

	topics, err := reader.GetTopics(context.Background())
	if err != nil {
		t.Fatalf("GetTopics() error = %v", err)
	}
	if len(topics) != 3 {
		t.Fatalf("got %d topics, want 3", len(topics))
	}
	if got := topics[0].Label(); got != "Отели" {
		t.Errorf("Label() = %q, want the human-readable topictext", got)
	}
	if got := topics[1].Label(); got != "rOnlyName" {
		t.Errorf("Label() = %q, want the topicname fallback", got)
	}
	if got := topics[2].Label(); got != "" {
		t.Errorf("Label() = %q, want empty for an unnamed topic", got)
	}
}

func TestGetEncyclopediaTermsGroupsByEntry(t *testing.T) {
	reader := newSourceStub(t, `
		CREATE TABLE tr_encyclopedia_text (tid INTEGER PRIMARY KEY, eid INTEGER, title TEXT, text TEXT)`,
		`INSERT INTO tr_encyclopedia_text VALUES
			(1, 1, 'Привет!', 'hello'),
			(2, 1, 'Как дела?', 'how are you'),
			(3, 2, 'Number', 'one')`)

	terms, err := reader.GetEncyclopediaTerms(context.Background())
	if err != nil {
		t.Fatalf("GetEncyclopediaTerms() error = %v", err)
	}
	if len(terms[1]) != 2 {
		t.Errorf("entry 1 has %d terms, want 2", len(terms[1]))
	}
	if len(terms[2]) != 1 {
		t.Errorf("entry 2 has %d terms, want 1", len(terms[2]))
	}
	if shared.NullString(terms[1][0].Title) != "Привет!" {
		t.Errorf("terms are not ordered by tid: %q", shared.NullString(terms[1][0].Title))
	}
}

// TestGetStoryAuthorsSelectsOnlyCreditedAccounts is the point of the join: a
// PHP-Nuke users table is mostly dormant registrations, and importing it
// wholesale would create hundreds of accounts that never wrote anything.
func TestGetStoryAuthorsSelectsOnlyCreditedAccounts(t *testing.T) {
	reader := newSourceStub(t, `
		CREATE TABLE tr_users (user_id INTEGER PRIMARY KEY, username TEXT, name TEXT, user_email TEXT)`,
		`CREATE TABLE tr_stories (
			sid INTEGER PRIMARY KEY, catid INTEGER, aid TEXT, title TEXT, time DATETIME,
			hometext TEXT, bodytext TEXT, topic INTEGER, informant TEXT, notes TEXT
		)`,
		`INSERT INTO tr_users VALUES
			(1, 'Olegiv', 'Oleg', 'olegiv@tunisie.ru'),
			(2, 'sveta', 'Sveta', 'sveta@example.com'),
			(3, 'lurker', 'Lurker', 'lurker@example.com')`,
		`INSERT INTO tr_stories VALUES
			(1, 0, 'Olegiv', 'A', NULL, '', '', 1, 'sveta', ''),
			(2, 0, 'Olegiv', 'B', NULL, '', '', 1, '', '')`)

	authors, err := reader.GetStoryAuthors(context.Background())
	if err != nil {
		t.Fatalf("GetStoryAuthors() error = %v", err)
	}
	if len(authors) != 2 {
		t.Fatalf("got %d authors, want 2 (the lurker must be excluded): %+v", len(authors), authors)
	}
	names := map[string]bool{}
	for _, author := range authors {
		names[author.Login()] = true
	}
	if !names["Olegiv"] || !names["sveta"] {
		t.Errorf("expected both the publisher and the submitter, got %v", names)
	}
	if names["lurker"] {
		t.Error("an account credited on no story was imported")
	}
}

func TestCountsUsePrefixedTables(t *testing.T) {
	reader := newSourceStub(t,
		`CREATE TABLE tr_stories (sid INTEGER PRIMARY KEY)`,
		`CREATE TABLE tr_topics (topicid INTEGER PRIMARY KEY)`,
		`CREATE TABLE tr_pages (pid INTEGER PRIMARY KEY)`,
		`INSERT INTO tr_stories VALUES (1), (2), (3)`,
		`INSERT INTO tr_topics VALUES (1)`)

	ctx := context.Background()
	if got, err := reader.StoryCount(ctx); err != nil || got != 3 {
		t.Errorf("StoryCount() = %d, %v; want 3, nil", got, err)
	}
	if got, err := reader.TopicCount(ctx); err != nil || got != 1 {
		t.Errorf("TopicCount() = %d, %v; want 1, nil", got, err)
	}
	if got, err := reader.StaticPageCount(ctx); err != nil || got != 0 {
		t.Errorf("StaticPageCount() = %d, %v; want 0, nil", got, err)
	}
}

func TestCountsReportMissingTable(t *testing.T) {
	reader := newSourceStub(t)
	if _, err := reader.StoryCount(context.Background()); err == nil {
		t.Fatal("expected an error when the stories table is absent")
	}
}

func TestGetCategoriesReadsBothTaxonomyTables(t *testing.T) {
	reader := newSourceStub(t,
		`CREATE TABLE tr_stories_cat (catid INTEGER PRIMARY KEY, title TEXT)`,
		`CREATE TABLE tr_pages_categories (cid INTEGER PRIMARY KEY, title TEXT, description TEXT)`,
		`INSERT INTO tr_stories_cat VALUES (2, 'Новости'), (3, 'Information')`,
		`INSERT INTO tr_pages_categories VALUES (1, 'Информация', '')`)

	ctx := context.Background()
	storyCats, err := reader.GetStoryCategories(ctx)
	if err != nil {
		t.Fatalf("GetStoryCategories() error = %v", err)
	}
	if len(storyCats) != 2 || storyCats[0].Name() != "Новости" {
		t.Errorf("story categories = %+v", storyCats)
	}

	pageCats, err := reader.GetPageCategories(ctx)
	if err != nil {
		t.Fatalf("GetPageCategories() error = %v", err)
	}
	if len(pageCats) != 1 || pageCats[0].Name() != "Информация" {
		t.Errorf("page categories = %+v", pageCats)
	}
}

func TestGetStaticPagesReadsEverySection(t *testing.T) {
	reader := newSourceStub(t, `
		CREATE TABLE tr_pages (
			pid INTEGER PRIMARY KEY, cid INTEGER, title TEXT, subtitle TEXT, active INTEGER,
			page_header TEXT, text TEXT, page_footer TEXT, signature TEXT, date DATETIME
		)`,
		`INSERT INTO tr_pages VALUES
			(1, 2, 'Title', 'Subtitle', 1, '<h1>h</h1>', '<p>body</p>', '<p>f</p>', 'sig', '2012-01-01 00:00:00')`)

	pages, err := reader.GetStaticPages(context.Background())
	if err != nil {
		t.Fatalf("GetStaticPages() error = %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	page := pages[0]
	if !page.IsActive() {
		t.Error("active flag was not read")
	}
	if page.Category() != 2 || shared.NullString(page.Subtitle) != "Subtitle" {
		t.Errorf("page = %+v", page)
	}
	if got := assembleStaticPageBody(&page); !strings.Contains(got, "<h1>h</h1>") ||
		!strings.Contains(got, "<p>body</p>") || !strings.Contains(got, "sig") {
		t.Errorf("assembled body dropped a section: %q", got)
	}
}

// TestReadsSurviveNullColumns covers a PR review finding (P1). Every non-key
// column was scanned as a plain string, so a single NULL failed the whole query
// with "converting NULL to string is unsupported". Because the content reads
// happen after users and taxonomy are already written, that aborted an import
// mid-way — on exactly the input this importer exists for: a twenty-year-old
// database that has been through upgrades and hand-written SQL.
func TestReadsSurviveNullColumns(t *testing.T) {
	reader := newSourceStub(t,
		`CREATE TABLE tr_stories (sid INTEGER PRIMARY KEY, catid INTEGER, aid TEXT, title TEXT,
			time DATETIME, hometext TEXT, bodytext TEXT, topic INTEGER, informant TEXT)`,
		`INSERT INTO tr_stories VALUES (1,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL)`,
		`CREATE TABLE tr_pages (pid INTEGER PRIMARY KEY, cid INTEGER, title TEXT, subtitle TEXT,
			active INTEGER, page_header TEXT, text TEXT, page_footer TEXT, signature TEXT, date DATETIME)`,
		`INSERT INTO tr_pages VALUES (1,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL)`,
		`CREATE TABLE tr_topics (topicid INTEGER PRIMARY KEY, topicname TEXT, topictext TEXT)`,
		`INSERT INTO tr_topics VALUES (1,NULL,NULL)`,
		`CREATE TABLE tr_stories_cat (catid INTEGER PRIMARY KEY, title TEXT)`,
		`INSERT INTO tr_stories_cat VALUES (1,NULL)`,
		`CREATE TABLE tr_pages_categories (cid INTEGER PRIMARY KEY, title TEXT, description TEXT)`,
		`INSERT INTO tr_pages_categories VALUES (1,NULL,NULL)`,
		`CREATE TABLE tr_encyclopedia (eid INTEGER PRIMARY KEY, title TEXT, description TEXT, active INTEGER)`,
		`INSERT INTO tr_encyclopedia VALUES (1,NULL,NULL,NULL)`,
		`CREATE TABLE tr_encyclopedia_text (tid INTEGER PRIMARY KEY, eid INTEGER, title TEXT, text TEXT)`,
		`INSERT INTO tr_encyclopedia_text VALUES (1,NULL,NULL,NULL)`,
		`CREATE TABLE tr_users (user_id INTEGER PRIMARY KEY, username TEXT, name TEXT, user_email TEXT)`,
		`INSERT INTO tr_users VALUES (1,NULL,NULL,NULL)`)

	ctx := context.Background()
	for _, tc := range []struct {
		name string
		read func() error
	}{
		{"stories", func() error { _, err := reader.GetStories(ctx); return err }},
		{"pages", func() error { _, err := reader.GetStaticPages(ctx); return err }},
		{"topics", func() error { _, err := reader.GetTopics(ctx); return err }},
		{"story categories", func() error { _, err := reader.GetStoryCategories(ctx); return err }},
		{"page categories", func() error { _, err := reader.GetPageCategories(ctx); return err }},
		{"encyclopedia", func() error { _, err := reader.GetEncyclopediaEntries(ctx); return err }},
		{"encyclopedia terms", func() error { _, err := reader.GetEncyclopediaTerms(ctx); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.read(); err != nil {
				t.Errorf("a NULL column aborted the read: %v", err)
			}
		})
	}
}

// TestNullColumnsDegradeToEmptyValues proves the accessors, not just the scan.
func TestNullColumnsDegradeToEmptyValues(t *testing.T) {
	reader := newSourceStub(t,
		`CREATE TABLE tr_stories (sid INTEGER PRIMARY KEY, catid INTEGER, aid TEXT, title TEXT,
			time DATETIME, hometext TEXT, bodytext TEXT, topic INTEGER, informant TEXT)`,
		`INSERT INTO tr_stories VALUES (1,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL)`)

	stories, err := reader.GetStories(context.Background())
	if err != nil {
		t.Fatalf("GetStories() error = %v", err)
	}
	story := &stories[0]
	if got := assembleStoryBody(story); got != "" {
		t.Errorf("assembleStoryBody() = %q, want empty for an all-NULL row", got)
	}
	if story.Topic() != 0 || story.Category() != 0 {
		t.Errorf("NULL taxonomy ids should read as 0, got %d/%d", story.Topic(), story.Category())
	}
	if namesAnAuthor(story) {
		t.Error("an all-NULL row credits nobody and must not count as losing an author")
	}
}

// authorsSchema is the reference PHP-Nuke shape for the two tables that credit
// a story, reduced to the columns the reader selects.
func authorsSchema(rows ...string) []string {
	return append([]string{
		`CREATE TABLE tr_users (user_id INTEGER PRIMARY KEY, username TEXT, name TEXT, user_email TEXT)`,
		`CREATE TABLE tr_authors (aid TEXT PRIMARY KEY, name TEXT, email TEXT)`,
		`CREATE TABLE tr_stories (
			sid INTEGER PRIMARY KEY, catid INTEGER, aid TEXT, title TEXT, time DATETIME,
			hometext TEXT, bodytext TEXT, topic INTEGER, informant TEXT, notes TEXT
		)`,
	}, rows...)
}

// TestGetStoryAuthorsReadsBothCreditTables covers a Codex review finding.
//
// The two credit columns name rows in different tables: `informant` is a
// users.username, but `aid` is an authors.aid — PHP-Nuke's separate table for
// accounts allowed to publish. Searching for both in `users` meant that on any
// install where an administrator has no users row, every story they published
// lost its byline and was attributed to the fallback oCMS account instead.
func TestGetStoryAuthorsReadsBothCreditTables(t *testing.T) {
	reader := newSourceStub(t, authorsSchema(
		`INSERT INTO tr_users VALUES
			(1, 'sveta', 'Sveta', 'sveta@example.com'),
			(2, 'lurker', 'Lurker', 'lurker@example.com')`,
		`INSERT INTO tr_authors VALUES ('God', 'Site Admin', 'admin@tunisie.ru')`,
		`INSERT INTO tr_stories VALUES
			(1, 0, 'God', 'Published by an admin with no users row', NULL, '', '', 1, '', ''),
			(2, 0, '', 'Submitted', NULL, '', '', 1, 'sveta', '')`)...)

	authors, err := reader.GetStoryAuthors(context.Background())
	if err != nil {
		t.Fatalf("GetStoryAuthors() error = %v", err)
	}
	found := make(map[string]User, len(authors))
	for _, author := range authors {
		found[author.Login()] = author
	}

	admin, ok := found["God"]
	if !ok {
		t.Fatalf("the publishing admin lost their byline; got %v", keysOf(found))
	}
	if admin.DisplayName() != "Site Admin" || admin.Address() != "admin@tunisie.ru" {
		t.Errorf("admin author = %q <%s>, want the authors-table profile",
			admin.DisplayName(), admin.Address())
	}
	if _, ok := found["sveta"]; !ok {
		t.Errorf("the submitter was lost; got %v", keysOf(found))
	}
	if _, ok := found["lurker"]; ok {
		t.Error("an account credited on no story was imported")
	}
}

// TestGetStoryAuthorsPrefersTheUsersRow pins the merge order. An administrator
// usually has a users row as well, and that row carries the profile the site
// displays — authors.email is very often the shared webmaster address, so
// letting it win would attribute several people's work to one mailbox and
// collapse them into a single oCMS account.
func TestGetStoryAuthorsPrefersTheUsersRow(t *testing.T) {
	reader := newSourceStub(t, authorsSchema(
		`INSERT INTO tr_users VALUES (1, 'sveta', 'Sveta', 'sveta@example.com')`,
		`INSERT INTO tr_authors VALUES ('sveta', 'Webmaster', 'webmaster@example.com')`,
		`INSERT INTO tr_stories VALUES (1, 0, 'sveta', 'A', NULL, '', '', 1, '', '')`)...)

	authors, err := reader.GetStoryAuthors(context.Background())
	if err != nil {
		t.Fatalf("GetStoryAuthors() error = %v", err)
	}
	if len(authors) != 1 {
		t.Fatalf("got %d authors, want 1 — the same person was imported twice: %+v",
			len(authors), authors)
	}
	if authors[0].Address() != "sveta@example.com" {
		t.Errorf("author email = %q, want the users-table address", authors[0].Address())
	}
}

// TestGetStoryAuthorsMatchesCreditsCaseInsensitively guards the merge key. MySQL
// string comparison is case-insensitive under the default collation, so
// stories.aid = 'God' finds users.username = 'god'; a case-sensitive merge in Go
// would then import that person twice.
func TestGetStoryAuthorsMatchesCreditsCaseInsensitively(t *testing.T) {
	reader := newSourceStub(t, authorsSchema(
		`INSERT INTO tr_users VALUES (1, 'God', 'Oleg', 'oleg@example.com')`,
		`INSERT INTO tr_authors VALUES ('god', 'Oleg', 'oleg@example.com')`,
		`INSERT INTO tr_stories VALUES (1, 0, 'God', 'A', NULL, '', '', 1, '', '')`)...)

	authors, err := reader.GetStoryAuthors(context.Background())
	if err != nil {
		t.Fatalf("GetStoryAuthors() error = %v", err)
	}
	if len(authors) != 1 {
		t.Errorf("got %d authors, want 1: %+v", len(authors), authors)
	}
}

// TestGetStoryAuthorsToleratesMissingAuthorsTable keeps an optional table
// optional. Losing admin bylines degrades an import; failing it outright loses
// the archive.
func TestGetStoryAuthorsToleratesMissingAuthorsTable(t *testing.T) {
	reader := newSourceStub(t,
		`CREATE TABLE tr_users (user_id INTEGER PRIMARY KEY, username TEXT, name TEXT, user_email TEXT)`,
		`CREATE TABLE tr_stories (
			sid INTEGER PRIMARY KEY, catid INTEGER, aid TEXT, title TEXT, time DATETIME,
			hometext TEXT, bodytext TEXT, topic INTEGER, informant TEXT, notes TEXT
		)`,
		`INSERT INTO tr_users VALUES (1, 'sveta', 'Sveta', 'sveta@example.com')`,
		`INSERT INTO tr_stories VALUES (1, 0, '', 'A', NULL, '', '', 1, 'sveta', '')`)

	authors, err := reader.GetStoryAuthors(context.Background())
	if err != nil {
		t.Fatalf("an absent authors table failed the whole read: %v", err)
	}
	if len(authors) != 1 || authors[0].Login() != "sveta" {
		t.Errorf("got %+v, want the one submitter", authors)
	}
}

// TestCreditedAdminAuthorsReportsRealFailures is the other half of that
// tolerance: only absence is survivable. A query that fails for any other
// reason must surface, or a broken read looks exactly like a site that never
// had administrators.
func TestCreditedAdminAuthorsReportsRealFailures(t *testing.T) {
	reader := newSourceStub(t,
		`CREATE TABLE tr_authors (aid TEXT PRIMARY KEY, name TEXT, email TEXT)`)

	if _, err := reader.creditedAdminAuthors(context.Background()); err == nil {
		t.Fatal("a readable authors table with an unreadable stories table " +
			"returned no error, so the failure would pass as 'no admins'")
	}
}

func keysOf(m map[string]User) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}
