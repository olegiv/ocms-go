// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package phpnuke

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// fakeReader drives the import stages without a MySQL server.
type fakeReader struct {
	stories     []Story
	staticPages []StaticPage
	topics      []Topic
	storyCats   []Category
	pageCats    []Category
	encEntries  []EncyclopediaEntry
	encTerms    map[int64][]EncyclopediaTerm
	authors     []User

	// errs fails one named read, so every "failed to read X" branch is
	// reachable without a database.
	errs        map[string]error
	pageCatsErr error
}

func (f *fakeReader) err(method string) error { return f.errs[method] }

func (f *fakeReader) GetStories(context.Context) ([]Story, error) {
	return f.stories, f.err("GetStories")
}
func (f *fakeReader) GetStaticPages(context.Context) ([]StaticPage, error) {
	return f.staticPages, f.err("GetStaticPages")
}
func (f *fakeReader) GetTopics(context.Context) ([]Topic, error) {
	return f.topics, f.err("GetTopics")
}
func (f *fakeReader) GetStoryCategories(context.Context) ([]Category, error) {
	return f.storyCats, f.err("GetStoryCategories")
}
func (f *fakeReader) GetPageCategories(context.Context) ([]Category, error) {
	return f.pageCats, f.pageCatsErr
}
func (f *fakeReader) GetEncyclopediaEntries(context.Context) ([]EncyclopediaEntry, error) {
	return f.encEntries, f.err("GetEncyclopediaEntries")
}
func (f *fakeReader) GetEncyclopediaTerms(context.Context) (map[int64][]EncyclopediaTerm, error) {
	return f.encTerms, f.err("GetEncyclopediaTerms")
}
func (f *fakeReader) GetStoryAuthors(context.Context) ([]User, error) {
	return f.authors, f.err("GetStoryAuthors")
}
func (f *fakeReader) Prefix() string { return "tr_" }

// mockTracker records what an import claimed so the undo path can find it.
type mockTracker struct {
	items    []trackedItem
	progress []types.Progress
}

type trackedItem struct {
	entityType string
	entityID   int64
}

func (m *mockTracker) TrackImportedItem(_ context.Context, _, entityType string, entityID int64) error {
	m.items = append(m.items, trackedItem{entityType, entityID})
	return nil
}

// ReportProgress makes mockTracker a types.ProgressReporter, so the phase and
// total each stage announces can be asserted. Without it every types.Report
// call is a no-op in tests and a stage could report the wrong phase, or a total
// that does not match the work it does, with nothing failing.
func (m *mockTracker) ReportProgress(_ context.Context, p types.Progress) {
	m.progress = append(m.progress, p)
}

// totalFor returns the total announced for a phase, and whether it was reported.
func (m *mockTracker) totalFor(phase types.EntityType) (int, bool) {
	for _, p := range m.progress {
		if p.Phase == phase {
			return p.Total, true
		}
	}
	return 0, false
}

func (m *mockTracker) countOf(entityType types.EntityType) int {
	count := 0
	for _, item := range m.items {
		if item.entityType == string(entityType) {
			count++
		}
	}
	return count
}

// setupDB returns a fully migrated oCMS database plus the ID of a seed user
// that imported pages can be attributed to.
func setupDB(t *testing.T) (*store.Queries, int64) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)

	queries := store.New(db)
	now := time.Now()
	admin, err := queries.CreateUser(context.Background(), store.CreateUserParams{
		Email:        "admin@example.com",
		PasswordHash: "x",
		Role:         "admin",
		Name:         "Admin",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to seed admin user: %v", err)
	}
	return queries, admin.ID
}

func defaultLang(t *testing.T, queries *store.Queries) string {
	t.Helper()
	languages, err := queries.ListLanguages(context.Background())
	if err != nil {
		t.Fatalf("failed to list languages: %v", err)
	}
	for _, language := range languages {
		if language.IsDefault {
			return language.Code
		}
	}
	t.Fatal("no default language in a migrated database")
	return ""
}

func TestSourceIdentity(t *testing.T) {
	s := NewSource()
	if got := s.Name(); got != "phpnuke" {
		t.Errorf("Name() = %q, want %q", got, "phpnuke")
	}
	if got := s.DisplayName(); got != "PHP-Nuke" {
		t.Errorf("DisplayName() = %q, want %q", got, "PHP-Nuke")
	}
	if got := s.Description(); got != "phpnuke.description" {
		t.Errorf("Description() = %q, want a translation key", got)
	}
}

// TestSupportedImportOptionsExcludeMenus records why menus are absent: PHP-Nuke
// keeps navigation in `blocks` as PHP snippets, so there is nothing to read.
func TestSupportedImportOptionsExcludeMenus(t *testing.T) {
	supported := NewSource().SupportedImportOptions()
	for _, key := range supported {
		if key == "import_menus" {
			t.Fatal("phpnuke declares import_menus but reads no menu table")
		}
	}
	for _, want := range []string{"import_posts", "import_pages", "import_categories", "import_users"} {
		if !contains(supported, want) {
			t.Errorf("missing supported option %q", want)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestDSNRequestsUTF8MB4 is the mechanical guard on the encoding invariant.
//
// Every PHP-Nuke database of this vintage stores text in a single-byte charset
// such as cp1251. MySQL transcodes it to UTF-8 on read only because the
// connection asks for utf8mb4; without that the driver negotiates the server
// default and every Cyrillic character arrives as a literal "?", with no error
// raised anywhere. A regression here corrupts an entire import silently.
func TestDSNRequestsUTF8MB4(t *testing.T) {
	dsn, err := NewSource().buildDSN(map[string]string{
		"mysql_host":     "127.0.0.1",
		"mysql_port":     "3306",
		"mysql_user":     "u",
		"mysql_password": "p",
		"mysql_database": "nuke",
	})
	if err != nil {
		t.Fatalf("buildDSN() error = %v", err)
	}
	if !strings.Contains(dsn, "charset=utf8mb4") {
		t.Errorf("DSN does not request utf8mb4, legacy charsets will be read as '?': %s", dsn)
	}
}

// TestCyrillicSurvivesImport proves a Cyrillic story reaches the destination
// intact, with an ASCII slug transliterated from the title.
func TestCyrillicSurvivesImport(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)
	source := NewSource()
	result := &types.ImportResult{}
	tracker := &mockTracker{}

	stories := []Story{{
		ID:        1,
		Title:     ns("Отель Royal Azur"),
		HomeText:  ns("<p>Хаммамет, Тунис.</p>"),
		BodyText:  ns("<p>В отеле 220 номеров.</p>"),
		Time:      sql.NullTime{Time: time.Date(2004, 2, 22, 3, 19, 44, 0, time.UTC), Valid: true},
		Informant: ns("Olegiv"),
	}}

	source.importStories(ctx, queries, stories, map[string]int64{}, adminID, lang,
		map[int64]int64{}, map[int64]int64{}, nil, nil, types.ImportOptions{}, result, tracker, nil)

	if result.PostsImported != 1 {
		t.Fatalf("PostsImported = %d, want 1; errors: %v", result.PostsImported, result.Errors)
	}
	pages, err := queries.ListPages(ctx, store.ListPagesParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("failed to list pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	page := pages[0]

	if page.Title != "Отель Royal Azur" {
		t.Errorf("title = %q, want the original Cyrillic", page.Title)
	}
	if strings.Contains(page.Title, "?") {
		t.Error("title contains '?', the signature of a lost charset conversion")
	}
	for _, want := range []string{"Хаммамет", "220 номеров"} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("body is missing %q; got %q", want, page.Body)
		}
	}
	if page.Slug != "otel-royal-azur" {
		t.Errorf("slug = %q, want a transliterated ASCII slug", page.Slug)
	}
}

func TestImportStoriesSetsStatusTimestampsAndTaxonomy(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)
	source := NewSource()
	result := &types.ImportResult{}
	tracker := &mockTracker{}

	published := time.Date(2010, 5, 4, 12, 0, 0, 0, time.UTC)
	category, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name: "Hotels", Slug: "hotels", LanguageCode: lang,
		Description: sql.NullString{String: "", Valid: true},
		CreatedAt:   time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create category: %v", err)
	}
	tag, err := queries.CreateTag(ctx, store.CreateTagParams{
		Name: "News", Slug: "news", LanguageCode: lang,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}

	stories := []Story{{
		ID:         7,
		Title:      ns("Hotel Review"),
		HomeText:   ns("<p>Teaser text</p>"),
		BodyText:   ns("<p>Full text</p>"),
		Time:       sql.NullTime{Time: published, Valid: true},
		TopicID:    ni(3),
		CategoryID: ni(9),
	}}

	source.importStories(ctx, queries, stories, map[string]int64{}, adminID, lang,
		map[int64]int64{3: category.ID}, map[int64]int64{9: tag.ID}, nil, nil,
		types.ImportOptions{}, result, tracker, nil)

	if result.PostsImported != 1 {
		t.Fatalf("PostsImported = %d, errors: %v", result.PostsImported, result.Errors)
	}
	page, err := queries.GetPageBySlug(ctx, "hotel-review")
	if err != nil {
		t.Fatalf("failed to load imported page: %v", err)
	}

	if page.Status != model.PageStatusPublished {
		t.Errorf("status = %q, want published", page.Status)
	}
	if !page.PublishedAt.Valid || !page.PublishedAt.Time.Equal(published) {
		t.Errorf("published_at = %v, want %v", page.PublishedAt, published)
	}
	if !page.CreatedAt.Equal(published) {
		t.Errorf("created_at = %v, want the source timestamp %v", page.CreatedAt, published)
	}
	if page.PageType != pageTypePost {
		t.Errorf("page_type = %q, want %q", page.PageType, pageTypePost)
	}
	if page.Summary != "Teaser text" {
		t.Errorf("summary = %q, want the hometext teaser", page.Summary)
	}

	categories, err := queries.GetCategoriesForPage(ctx, page.ID)
	if err != nil {
		t.Fatalf("failed to list page categories: %v", err)
	}
	if len(categories) != 1 || categories[0].ID != category.ID {
		t.Errorf("topic was not attached as a category: %v", categories)
	}
	tags, err := queries.GetTagsForPage(ctx, page.ID)
	if err != nil {
		t.Fatalf("failed to list page tags: %v", err)
	}
	if len(tags) != 1 || tags[0].ID != tag.ID {
		t.Errorf("story category was not attached as a tag: %v", tags)
	}
	if tracker.countOf(types.EntityPost) != 1 {
		t.Errorf("post was not tracked, so it would be invisible to undo")
	}
}

func TestImportStoriesGivesCollidingTitlesDistinctSlugs(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)
	result := &types.ImportResult{}

	title := sql.NullString{String: "Полезные советы", Valid: true}
	stories := []Story{
		{ID: 1, Title: title, BodyText: ns("<p>one</p>")},
		{ID: 2, Title: title, BodyText: ns("<p>two</p>")},
		{ID: 3, Title: title, BodyText: ns("<p>three</p>")},
	}
	NewSource().importStories(ctx, queries, stories, map[string]int64{}, adminID, lang,
		map[int64]int64{}, map[int64]int64{}, nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)

	if result.PostsImported != 3 {
		t.Fatalf("PostsImported = %d, want 3; errors: %v", result.PostsImported, result.Errors)
	}
	pages, err := queries.ListPages(ctx, store.ListPagesParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("failed to list pages: %v", err)
	}
	seen := make(map[string]bool, len(pages))
	for _, page := range pages {
		if seen[page.Slug] {
			t.Errorf("duplicate slug %q", page.Slug)
		}
		seen[page.Slug] = true
	}
	if len(seen) != 3 {
		t.Errorf("got %d distinct slugs, want 3: %v", len(seen), seen)
	}
}

func TestImportStoriesFallsBackWhenTitleIsMissing(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	result := &types.ImportResult{}

	stories := []Story{{ID: 42, Title: sql.NullString{Valid: false}, BodyText: ns("<p>body</p>")}}
	NewSource().importStories(ctx, queries, stories, map[string]int64{}, adminID,
		defaultLang(t, queries), map[int64]int64{}, map[int64]int64{}, nil, nil,
		types.ImportOptions{}, result, &mockTracker{}, nil)

	if result.PostsImported != 1 {
		t.Fatalf("a title-less story was dropped; errors: %v", result.Errors)
	}
	if _, err := queries.GetPageBySlug(ctx, "story-42"); err != nil {
		t.Errorf("expected fallback slug story-42: %v", err)
	}
}

// TestImportedUsersCannotSignIn is the security property the operator asked
// for: imported accounts are blocked with minimum permissions.
func TestImportedUsersCannotSignIn(t *testing.T) {
	queries, _ := setupDB(t)
	ctx := context.Background()
	result := &types.ImportResult{}
	userMap := make(map[string]int64)

	reader := &fakeReader{authors: []User{
		{ID: 2, Username: ns("Olegiv"), Name: ns("Oleg"), Email: ns("olegiv@tunisie.ru")},
		{ID: 5, Username: ns("sveta"), Name: ns(""), Email: ns("sveta@example.com")},
	}}
	if err := NewSource().importUsers(ctx, queries, reader, userMap,
		types.ImportOptions{}, result, &mockTracker{}); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}
	if result.UsersImported != 2 {
		t.Fatalf("UsersImported = %d, want 2; errors: %v", result.UsersImported, result.Errors)
	}

	for _, email := range []string{"olegiv@tunisie.ru", "sveta@example.com"} {
		user, err := queries.GetUserByEmail(ctx, email)
		if err != nil {
			t.Fatalf("imported user %q missing: %v", email, err)
		}
		if user.Role != model.RolePublic {
			t.Errorf("user %q has role %q, want %q", email, user.Role, model.RolePublic)
		}
		// The plaintext behind the hash is a discarded random secret, so every
		// guess an attacker could make from the source tree must fail.
		for _, guess := range []string{"", "imported-user-must-reset", "password", email} {
			if ok, _ := auth.CheckPassword(guess, user.PasswordHash); ok {
				t.Errorf("user %q can be signed into with %q", email, guess)
			}
		}
	}

	// A user whose profile name is blank still gets a usable display name.
	sveta, err := queries.GetUserByEmail(ctx, "sveta@example.com")
	if err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if sveta.Name != "sveta" {
		t.Errorf("name = %q, want the username as fallback", sveta.Name)
	}
	if len(userMap) != 2 {
		t.Errorf("userMap = %v, want both usernames mapped for story attribution", userMap)
	}
}

func TestImportUsersReusesExistingAccountByEmail(t *testing.T) {
	queries, _ := setupDB(t)
	ctx := context.Background()
	result := &types.ImportResult{}
	userMap := make(map[string]int64)

	reader := &fakeReader{authors: []User{
		{ID: 1, Username: ns("admin"), Name: ns("Admin"), Email: ns("admin@example.com")},
	}}
	if err := NewSource().importUsers(ctx, queries, reader, userMap,
		types.ImportOptions{}, result, &mockTracker{}); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}

	if result.UsersImported != 0 || result.UsersSkipped != 1 {
		t.Errorf("imported = %d, skipped = %d; want 0 and 1", result.UsersImported, result.UsersSkipped)
	}
	if _, ok := userMap["admin"]; !ok {
		t.Error("an existing account must still be mapped, or its stories lose attribution")
	}
}

func TestImportUsersSkipsAccountsWithoutEmail(t *testing.T) {
	queries, _ := setupDB(t)
	result := &types.ImportResult{}

	reader := &fakeReader{authors: []User{{ID: 3, Username: ns("ghost"), Email: ns("  ")}}}
	if err := NewSource().importUsers(context.Background(), queries, reader,
		map[string]int64{}, types.ImportOptions{}, result, &mockTracker{}); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}

	if result.UsersImported != 0 {
		t.Errorf("UsersImported = %d, want 0", result.UsersImported)
	}
	if len(result.Notices) != 1 {
		t.Errorf("a missing email is expected, so it belongs in notices: %v", result)
	}
	if len(result.Errors) != 0 {
		t.Errorf("a missing email must not be reported as an error: %v", result.Errors)
	}
}

func TestResolveAuthorIDPrefersInformantThenAid(t *testing.T) {
	userMap := map[string]int64{"submitter": 10, "publisher": 20}
	for _, tc := range []struct {
		name  string
		story Story
		want  int64
	}{
		{"informant wins", Story{Informant: ns("submitter"), AuthorID: ns("publisher")}, 10},
		{"falls back to aid", Story{Informant: ns(""), AuthorID: ns("publisher")}, 20},
		{"unknown informant falls back to aid", Story{Informant: ns("nobody"), AuthorID: ns("publisher")}, 20},
		{"neither known", Story{Informant: ns("nobody"), AuthorID: ns("nobody")}, 99},
		{"both blank", Story{}, 99},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, resolved := resolveAuthorID(&tc.story, userMap, 99)
			if got != tc.want {
				t.Errorf("resolveAuthorID() = %d, want %d", got, tc.want)
			}
			if resolved != (tc.want != 99) {
				t.Errorf("resolved = %v, want %v", resolved, tc.want != 99)
			}
		})
	}
}

func TestImportStoriesAttributesToImportedAuthor(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)
	result := &types.ImportResult{}
	userMap := make(map[string]int64)

	reader := &fakeReader{authors: []User{{ID: 2, Username: ns("sveta"), Name: ns("Sveta"), Email: ns("sveta@example.com")}}}
	source := NewSource()
	if err := source.importUsers(ctx, queries, reader, userMap, types.ImportOptions{}, result, &mockTracker{}); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}

	stories := []Story{
		{ID: 1, Title: ns("By Sveta"), Informant: ns("sveta")},
		{ID: 2, Title: ns("By Nobody"), Informant: ns("stranger")},
	}
	source.importStories(ctx, queries, stories, userMap, adminID, lang,
		map[int64]int64{}, map[int64]int64{}, nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)

	authored, err := queries.GetPageBySlug(ctx, "by-sveta")
	if err != nil {
		t.Fatalf("failed to load page: %v", err)
	}
	if authored.AuthorID != userMap["sveta"] {
		t.Errorf("author_id = %d, want the imported user %d", authored.AuthorID, userMap["sveta"])
	}

	orphan, err := queries.GetPageBySlug(ctx, "by-nobody")
	if err != nil {
		t.Fatalf("failed to load page: %v", err)
	}
	if orphan.AuthorID != adminID {
		t.Errorf("author_id = %d, want the fallback author %d", orphan.AuthorID, adminID)
	}
}

func TestImportStaticPagesHonorsActiveFlag(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	result := &types.ImportResult{}

	pages := []StaticPage{
		{ID: 1, Title: ns("Live Page"), Text: ns("<p>live</p>"), Active: ni(1)},
		{ID: 2, Title: ns("Hidden Page"), Text: ns("<p>hidden</p>"), Active: ni(0)},
	}
	NewSource().importStaticPages(ctx, queries, pages, adminID, defaultLang(t, queries),
		map[int64]int64{}, nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)

	if result.PagesImported != 2 {
		t.Fatalf("PagesImported = %d, want 2; errors: %v", result.PagesImported, result.Errors)
	}
	live, err := queries.GetPageBySlug(ctx, "live-page")
	if err != nil {
		t.Fatalf("failed to load page: %v", err)
	}
	if live.Status != model.PageStatusPublished || !live.PublishedAt.Valid {
		t.Errorf("active page should be published, got status %q published_at %v", live.Status, live.PublishedAt)
	}
	if live.PageType != pageTypePage {
		t.Errorf("page_type = %q, want %q", live.PageType, pageTypePage)
	}

	hidden, err := queries.GetPageBySlug(ctx, "hidden-page")
	if err != nil {
		t.Fatalf("failed to load page: %v", err)
	}
	if hidden.Status != model.PageStatusDraft || hidden.PublishedAt.Valid {
		t.Errorf("inactive page should be a draft, got status %q published_at %v", hidden.Status, hidden.PublishedAt)
	}
}

// TestImportStaticPagesStripsMarkupFromMetaDescription covers audit finding
// F-03: the PHP-Nuke subtitle is source-controlled and was stored verbatim as
// the meta description, which is plain text by definition.
func TestImportStaticPagesStripsMarkupFromMetaDescription(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	result := &types.ImportResult{}

	pages := []StaticPage{{
		ID:       1,
		Title:    ns("Subtitle Test"),
		Subtitle: ns(`<img src=x onerror=alert(1)> Гид по Тунису`),
		Text:     ns("<p>body</p>"),
		Active:   ni(1),
	}}
	NewSource().importStaticPages(ctx, queries, pages, adminID, defaultLang(t, queries),
		map[int64]int64{}, nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)

	page, err := queries.GetPageBySlug(ctx, "subtitle-test")
	if err != nil {
		t.Fatalf("failed to load imported page: %v", err)
	}
	if strings.ContainsAny(page.MetaDescription, "<>") {
		t.Errorf("meta description retained markup: %q", page.MetaDescription)
	}
	if !strings.Contains(page.MetaDescription, "Гид по Тунису") {
		t.Errorf("meta description lost its real text: %q", page.MetaDescription)
	}
}

func TestImportEncyclopediaCreatesOnePagePerEntry(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	result := &types.ImportResult{}

	content := &importContent{
		encEntries: []EncyclopediaEntry{
			{ID: 1, Title: ns("Русско-арабский разговорник"), Active: ni(1)},
		},
		encTerms: map[int64][]EncyclopediaTerm{
			1: {
				{ID: 1, EntryID: ni(1), Title: ns("Привет!"), Text: ns("<p>marhaba</p>")},
				{ID: 2, EntryID: ni(1), Title: ns("Как дела?"), Text: ns("<p>kayf halak</p>")},
			},
		},
	}
	NewSource().importEncyclopedia(ctx, queries, content, adminID, defaultLang(t, queries),
		nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)

	if result.PagesImported != 1 {
		t.Fatalf("PagesImported = %d, want 1; errors: %v", result.PagesImported, result.Errors)
	}
	pages, err := queries.ListPages(ctx, store.ListPagesParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("failed to list pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1 page holding every term", len(pages))
	}
	for _, want := range []string{"Привет!", "marhaba", "Как дела?", "kayf halak"} {
		if !strings.Contains(pages[0].Body, want) {
			t.Errorf("encyclopedia body is missing %q", want)
		}
	}
}

func TestImportCategoriesMapsTopicsAndReusesExisting(t *testing.T) {
	queries, _ := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)
	result := &types.ImportResult{}
	topicMap := make(map[int64]int64)
	pageCategoryMap := make(map[int64]int64)

	reader := &fakeReader{
		topics: []Topic{
			{ID: 8, Text: ns("Отели")},
			{ID: 9, Name: ns("rTourism")},
			{ID: 10},
		},
		pageCats: []Category{{ID: 1, Title: ns("Информация")}},
	}
	if err := NewSource().importCategories(ctx, queries, reader, lang, topicMap, pageCategoryMap,
		types.ImportOptions{}, result, &mockTracker{}); err != nil {
		t.Fatalf("importCategories() error = %v", err)
	}

	if len(topicMap) != 2 {
		t.Errorf("topicMap = %v, want the two named topics", topicMap)
	}
	if _, ok := topicMap[10]; ok {
		t.Error("a nameless topic should be skipped, not imported")
	}
	if len(pageCategoryMap) != 1 {
		t.Errorf("pageCategoryMap = %v, want one entry", pageCategoryMap)
	}
	if result.CategoriesImported != 3 {
		t.Errorf("CategoriesImported = %d, want 3; errors %v", result.CategoriesImported, result.Errors)
	}

	// A second run must reuse the rows rather than create "oteli-2".
	rerun := &types.ImportResult{}
	if err := NewSource().importCategories(ctx, queries, reader, lang,
		make(map[int64]int64), make(map[int64]int64), types.ImportOptions{}, rerun, &mockTracker{}); err != nil {
		t.Fatalf("importCategories() rerun error = %v", err)
	}
	if rerun.CategoriesImported != 0 || rerun.CategoriesSkipped != 3 {
		t.Errorf("rerun imported = %d skipped = %d; want 0 and 3",
			rerun.CategoriesImported, rerun.CategoriesSkipped)
	}
}

// TestImportCategoriesDistinguishesMissingTableFromReadFailure covers a PR
// review finding. A site that never enabled the static pages module genuinely
// has no such table, and that is routine. Every other cause — a dropped
// connection, a missing SELECT grant — silently imports every static page with
// no category, so downgrading all of them to a notice let a broken run report
// "Completed".
func TestImportCategoriesDistinguishesMissingTableFromReadFailure(t *testing.T) {
	topics := []Topic{{ID: 1, Text: ns("News")}}

	t.Run("absent table is a notice", func(t *testing.T) {
		queries, _ := setupDB(t)
		result := &types.ImportResult{}
		reader := &fakeReader{
			topics:      topics,
			pageCatsErr: &mysql.MySQLError{Number: mysqlErrNoSuchTable, Message: "Table 'tr_pages_categories' doesn't exist"},
		}
		if err := NewSource().importCategories(context.Background(), queries, reader,
			defaultLang(t, queries), make(map[int64]int64), make(map[int64]int64),
			types.ImportOptions{}, result, &mockTracker{}); err != nil {
			t.Fatalf("a missing optional table must not abort the stage: %v", err)
		}
		if len(result.Errors) != 0 {
			t.Errorf("optional table absence should not be an error: %v", result.Errors)
		}
		if len(result.Notices) != 1 {
			t.Fatalf("expected one notice, got %v", result.Notices)
		}
		// Prefix() was added to sourceReader solely so this names the real
		// table; without the assertion a regression would pass unnoticed.
		if !strings.Contains(result.Notices[0], "tr_pages_categories") {
			t.Errorf("the notice should name the missing table: %q", result.Notices[0])
		}
		if result.CategoriesImported != 1 {
			t.Errorf("topics should still import: %d", result.CategoriesImported)
		}
	})

	t.Run("any other read failure is an error", func(t *testing.T) {
		queries, _ := setupDB(t)
		result := &types.ImportResult{}
		reader := &fakeReader{topics: topics, pageCatsErr: errors.New("Access denied for user")}
		if err := NewSource().importCategories(context.Background(), queries, reader,
			defaultLang(t, queries), make(map[int64]int64), make(map[int64]int64),
			types.ImportOptions{}, result, &mockTracker{}); err != nil {
			t.Fatalf("the stage should continue with topics: %v", err)
		}
		if len(result.Errors) != 1 {
			t.Fatalf("a read failure must surface as an error, got errors=%v notices=%v",
				result.Errors, result.Notices)
		}
		if !strings.Contains(result.Errors[0], "no category assigned") {
			t.Errorf("the error should state the consequence: %q", result.Errors[0])
		}
	})
}
func TestImportStoryCategoryTagsCreatesTags(t *testing.T) {
	queries, _ := setupDB(t)
	ctx := context.Background()
	result := &types.ImportResult{}
	storyCategoryMap := make(map[int64]int64)

	reader := &fakeReader{storyCats: []Category{
		{ID: 2, Title: ns("Новости")},
		{ID: 3, Title: ns("Information")},
		{ID: 4, Title: ns("   ")},
	}}
	if err := NewSource().importStoryCategoryTags(ctx, queries, reader, defaultLang(t, queries),
		storyCategoryMap, types.ImportOptions{}, result, &mockTracker{}); err != nil {
		t.Fatalf("importStoryCategoryTags() error = %v", err)
	}

	if result.TagsImported != 2 {
		t.Errorf("TagsImported = %d, want 2; errors %v", result.TagsImported, result.Errors)
	}
	if len(storyCategoryMap) != 2 {
		t.Errorf("storyCategoryMap = %v, want two entries", storyCategoryMap)
	}
	if _, err := queries.GetTagBySlug(ctx, "novosti"); err != nil {
		t.Errorf("expected a transliterated tag slug: %v", err)
	}
}

func TestResolveLanguageCode(t *testing.T) {
	queries, _ := setupDB(t)
	ctx := context.Background()
	source := NewSource()
	siteDefault := defaultLang(t, queries)

	t.Run("empty falls back to site default", func(t *testing.T) {
		got, err := source.resolveLanguageCode(ctx, queries, map[string]string{})
		if err != nil {
			t.Fatalf("resolveLanguageCode() error = %v", err)
		}
		if got != siteDefault {
			t.Errorf("got %q, want %q", got, siteDefault)
		}
	})

	t.Run("explicit existing language", func(t *testing.T) {
		got, err := source.resolveLanguageCode(ctx, queries,
			map[string]string{"language_code": strings.ToUpper(siteDefault)})
		if err != nil {
			t.Fatalf("resolveLanguageCode() error = %v", err)
		}
		if got != siteDefault {
			t.Errorf("got %q, want %q", got, siteDefault)
		}
	})

	// A named-but-absent language must fail loudly. Silently writing 669 Russian
	// stories into the English namespace is work the operator has to unpick by
	// hand, one page at a time.
	t.Run("unknown language is a hard error", func(t *testing.T) {
		_, err := source.resolveLanguageCode(ctx, queries, map[string]string{"language_code": "ru"})
		if err == nil {
			t.Fatal("expected an error for a language that does not exist")
		}
		if !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("error should name the problem, got: %v", err)
		}
	})

	t.Run("reserved code is rejected", func(t *testing.T) {
		if _, err := source.resolveLanguageCode(ctx, queries,
			map[string]string{"language_code": "admin"}); err == nil {
			t.Fatal("expected a reserved language code to be rejected")
		}
	})
}

func TestUniqueTaxonomySlugProbesUntilFree(t *testing.T) {
	ctx := context.Background()
	taken := map[string]bool{"news": true, "news-2": true}

	got, err := uniqueTaxonomySlug(ctx, "news", func(candidate string) (bool, error) {
		return taken[candidate], nil
	})
	if err != nil {
		t.Fatalf("uniqueTaxonomySlug() error = %v", err)
	}
	if got != "news-3" {
		t.Errorf("uniqueTaxonomySlug() = %q, want %q", got, "news-3")
	}
}

// TestUniqueTaxonomySlugTreatsErrorsAsTaken guards the fail-safe direction: a
// transient database error must never be read as "the slug is free".
// TestUniqueTaxonomySlugSurfacesProbeErrors guards both halves of the contract.
// A probe error must never be read as "free", and it must abort the search
// rather than issuing a hundred more doomed probes and reporting a database
// outage as a slug collision.
func TestUniqueTaxonomySlugSurfacesProbeErrors(t *testing.T) {
	probes := 0
	got, err := uniqueTaxonomySlug(context.Background(), "news", func(string) (bool, error) {
		probes++
		return false, errors.New("database is locked")
	})
	if err == nil {
		t.Fatal("expected the probe error to surface, not be swallowed")
	}
	if got != "" {
		t.Errorf("uniqueTaxonomySlug() = %q, want \"\" when the check cannot be trusted", got)
	}
	if probes != 1 {
		t.Errorf("probed %d times after an error; the search must stop at the first failure", probes)
	}
}

func TestUniqueTaxonomySlugStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := uniqueTaxonomySlug(ctx, "news", func(string) (bool, error) { return false, nil })
	if err == nil {
		t.Fatal("expected the cancellation to surface as an error")
	}
	if got != "" {
		t.Errorf("uniqueTaxonomySlug() = %q, want \"\" once the context is canceled", got)
	}
}

func TestCorePathReserved(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"", true},
		{"/", true},
		{"sitemap.xml", true},
		{"robots.txt", true},
		{"favicon.ico", true},
		{".well-known", true},
		{".well-known/security.txt", true},
		{"admin", true},
		{"api/v2", true},
		{"hotels", false},
		{"otel-royal-azur", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := corePathReserved(tc.path); got != tc.want {
				t.Errorf("corePathReserved(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestPrepareBodySanitizesAndRewritesMedia(t *testing.T) {
	source := NewSource()
	body := `<p onclick="steal()">Text</p><script>evil()</script><img src="a/b.jpg">`
	got := source.prepareBody(body, map[string]string{"a/b.jpg": "/uploads/originals/uuid/b.jpg"}, nil)

	if strings.Contains(got, "<script") {
		t.Errorf("script survived sanitizing: %s", got)
	}
	if strings.Contains(got, "onclick") {
		t.Errorf("event handler survived sanitizing: %s", got)
	}
	if !strings.Contains(got, "/uploads/originals/uuid/b.jpg") {
		t.Errorf("media reference was not rewritten: %s", got)
	}
}

// TestPrepareBodySanitizesWithoutMediaMap proves sanitizing is unconditional
// rather than a side effect of media rewriting.
func TestPrepareBodySanitizesWithoutMediaMap(t *testing.T) {
	got := NewSource().prepareBody(`<script>evil()</script><p>ok</p>`, nil, nil)
	if strings.Contains(got, "<script") {
		t.Errorf("body was not sanitized when no media map was supplied: %s", got)
	}
}

func TestReadContentHonorsOptions(t *testing.T) {
	reader := &fakeReader{
		stories:     []Story{{ID: 1}},
		staticPages: []StaticPage{{ID: 1}},
		encEntries:  []EncyclopediaEntry{{ID: 1}},
		encTerms:    map[int64][]EncyclopediaTerm{1: {{ID: 1}}},
	}
	source := NewSource()
	ctx := context.Background()

	postsOnly, err := source.readContent(ctx, reader, types.ImportOptions{ImportPosts: true})
	if err != nil {
		t.Fatalf("readContent() error = %v", err)
	}
	if len(postsOnly.stories) != 1 || len(postsOnly.staticPages) != 0 || len(postsOnly.encEntries) != 0 {
		t.Errorf("posts-only read pulled page content: %+v", postsOnly)
	}

	pagesOnly, err := source.readContent(ctx, reader, types.ImportOptions{ImportPages: true})
	if err != nil {
		t.Fatalf("readContent() error = %v", err)
	}
	if len(pagesOnly.stories) != 0 || len(pagesOnly.staticPages) != 1 || len(pagesOnly.encEntries) != 1 {
		t.Errorf("pages-only read pulled stories: %+v", pagesOnly)
	}
	// Without this, dropping the GetEncyclopediaTerms call entirely goes
	// unnoticed and every encyclopedia imports with its terms missing.
	if len(pagesOnly.encTerms) != 1 {
		t.Errorf("encyclopedia terms were not read: %+v", pagesOnly.encTerms)
	}
}

// TestImportContentBodiesCoverEveryImportedKind matters because media
// discovery works from this list: a body missing here means its images are
// never imported and its references silently keep pointing at the old site.
func TestImportContentBodiesCoverEveryImportedKind(t *testing.T) {
	content := &importContent{
		stories: []Story{{
			ID:       1,
			HomeText: sql.NullString{String: `<img src="story-home.jpg">`, Valid: true},
			BodyText: ns(`<img src="story-body.jpg">`),
		}},
		staticPages: []StaticPage{{ID: 1, Text: ns(`<img src="page.jpg">`)}},
		encEntries:  []EncyclopediaEntry{{ID: 1, Description: ns(`<img src="enc.jpg">`)}},
		encTerms:    map[int64][]EncyclopediaTerm{1: {{Text: ns(`<img src="term.jpg">`)}}},
	}

	joined := strings.Join(content.bodies(nil), "\n")
	for _, want := range []string{"story-home.jpg", "story-body.jpg", "page.jpg", "enc.jpg", "term.jpg"} {
		if !strings.Contains(joined, want) {
			t.Errorf("bodies() omitted %q, so its media would never be imported", want)
		}
	}
}

// TestMediaOpenFailuresAreClassified covers a PR review finding (P1). Every
// mediaRoot.Open failure was reported as "was not found", so an unreadable
// source tree produced only notices — and a run that imported zero media still
// finished as "Completed", with every <img> still pointing at the old site.
func TestMediaOpenFailuresAreClassified(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("mode 0o000 is not enforced for root, so the unreadable case cannot be exercised")
	}
	queries, adminID := setupDB(t)
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv(shared.EnvAllowedFileRoots, root)
	t.Setenv("OCMS_UPLOADS_DIR", t.TempDir())

	// One file that is genuinely absent, one that exists but cannot be read.
	if err := os.WriteFile(filepath.Join(root, "locked.jpg"), []byte("\xff\xd8\xff\xe0"), 0o000); err != nil {
		t.Fatal(err)
	}
	content := &importContent{
		stories: []Story{{
			ID:       1,
			BodyText: ns(`<img src="gone.jpg"><img src="locked.jpg">`),
		}},
		encTerms: map[int64][]EncyclopediaTerm{},
	}

	result := &types.ImportResult{}
	if _, err := NewSource().importMedia(ctx, queries, content, nil, root, os.Getenv("OCMS_UPLOADS_DIR"),
		adminID, defaultLang(t, queries), result, &mockTracker{}); err != nil {
		t.Fatalf("importMedia() error = %v", err)
	}

	if len(result.Notices) != 1 {
		t.Errorf("the genuinely absent file should be one notice, got %v", result.Notices)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("the unreadable file must be an error, not a notice; errors=%v notices=%v",
			result.Errors, result.Notices)
	}
	if !strings.Contains(result.Errors[0], "locked.jpg") {
		t.Errorf("the error should name the file: %q", result.Errors[0])
	}
	if result.MediaSkipped != 2 {
		t.Errorf("MediaSkipped = %d, want 2 so the job record shows the loss", result.MediaSkipped)
	}
	// The two causes must be summarised separately AND correctly: one sends the
	// operator to the old server, the other to their own filesystem. Checking
	// only the count let the two messages be swapped.
	if len(result.Summaries) != 2 {
		t.Fatalf("expected separate summaries for absent vs failed, got %v", result.Summaries)
	}
	var absentMsg, failedMsg string
	for _, sum := range result.Summaries {
		switch {
		case strings.Contains(sum, "missing from the source tree"):
			absentMsg = sum
		case strings.Contains(sum, "could not be imported"):
			failedMsg = sum
		}
	}
	if !strings.HasPrefix(absentMsg, "1 of 2") {
		t.Errorf("the absent summary should count exactly the missing file: %q", absentMsg)
	}
	if !strings.HasPrefix(failedMsg, "1 of 2") {
		t.Errorf("the failed summary should count exactly the unreadable file: %q", failedMsg)
	}
}

// TestDefaultAuthorIsTheOldestAccount covers a PR review finding. ListUsers
// orders by created_at DESC, so taking the first row picked the most recently
// created account — which, on any run after the first, is one of the inert
// role-"public" accounts this importer itself created. The fallback author then
// changed between runs and attributed content to an account nobody can sign
// into.
func TestDefaultAuthorIsTheOldestAccount(t *testing.T) {
	queries, oldest := setupDB(t)
	ctx := context.Background()

	for i, email := range []string{"later-1@example.com", "later-2@example.com"} {
		if _, err := queries.CreateUser(ctx, store.CreateUserParams{
			Email: email, PasswordHash: "x", Role: model.RolePublic, Name: "Imported",
			CreatedAt: time.Now().Add(time.Duration(i+1) * time.Hour), UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("failed to seed user: %v", err)
		}
	}

	got, err := NewSource().getDefaultAuthorID(ctx, queries)
	if err != nil {
		t.Fatalf("getDefaultAuthorID() error = %v", err)
	}
	if got != oldest {
		t.Errorf("getDefaultAuthorID() = %d, want the oldest account %d", got, oldest)
	}
}

// TestDefaultAuthorHandlesSingleUser pins the offset arithmetic at its boundary:
// total-1 is 0 when there is exactly one account.
func TestDefaultAuthorHandlesSingleUser(t *testing.T) {
	queries, only := setupDB(t)
	got, err := NewSource().getDefaultAuthorID(context.Background(), queries)
	if err != nil {
		t.Fatalf("getDefaultAuthorID() error = %v", err)
	}
	if got != only {
		t.Errorf("getDefaultAuthorID() = %d, want %d", got, only)
	}
}

// TestAuthorTallyIgnoresResolvedFallbackMatches covers a second-pass review
// finding. The tally used "authorID == fallbackAuthorID" as a proxy for "did
// not resolve", but a source author mapped onto an existing oCMS account is
// very often that same oldest-admin row — so correctly-attributed stories were
// reported as having lost their author.
func TestAuthorTallyIgnoresResolvedFallbackMatches(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	result := &types.ImportResult{}

	// The source author resolves to the very account used as the fallback.
	// Keyed the way importUsers builds it: case-folded.
	userMap := map[string]int64{authorKey("Olegiv"): adminID}
	stories := []Story{
		{ID: 1, Title: ns("Resolved"), Informant: ns("Olegiv")},
		{ID: 2, Title: ns("Orphaned"), Informant: ns("stranger")},
	}
	NewSource().importStories(ctx, queries, stories, userMap, adminID, defaultLang(t, queries),
		map[int64]int64{}, map[int64]int64{}, nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)

	if result.PostsImported != 2 {
		t.Fatalf("PostsImported = %d; errors %v", result.PostsImported, result.Errors)
	}
	joined := strings.Join(result.Summaries, " ")
	if strings.Contains(joined, "2 of 2") {
		t.Errorf("the resolved story was counted as unattributed: %q", joined)
	}
	if !strings.Contains(joined, "1 of 2") {
		t.Errorf("expected exactly one unattributed post to be reported, got %q", joined)
	}
}

// TestSkipExistingIsIdempotentAcrossRuns covers a review finding found by
// mutation testing. The SkipExisting probe derived its slug differently from
// the allocator, so a title that transliterates to nothing was never skipped
// and every re-run created another copy.
func TestSkipExistingIsIdempotentAcrossRuns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title string
	}{
		{"ordinary title", "Отель Royal Azur"},
		{"title that slugifies to nothing", "???"},
		{"punctuation only", "!!! ... ---"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries, adminID := setupDB(t)
			ctx := context.Background()
			lang := defaultLang(t, queries)
			stories := []Story{{ID: 42, Title: ns(tc.title), BodyText: ns("<p>body</p>")}}
			opts := types.ImportOptions{SkipExisting: true}

			content := &importContent{stories: stories}

			first := &types.ImportResult{}
			NewSource().importStories(ctx, queries, stories, map[string]int64{}, adminID, lang,
				map[int64]int64{}, map[int64]int64{},
				nil, NewSource().planSkips(ctx, queries, content, opts, first),
				opts, first, &mockTracker{}, nil)
			if first.PostsImported != 1 {
				t.Fatalf("first run imported %d, want 1; errors %v", first.PostsImported, first.Errors)
			}

			second := &types.ImportResult{}
			NewSource().importStories(ctx, queries, stories, map[string]int64{}, adminID, lang,
				map[int64]int64{}, map[int64]int64{},
				nil, NewSource().planSkips(ctx, queries, content, opts, second),
				opts, second, &mockTracker{}, nil)
			if second.PostsImported != 0 {
				t.Errorf("second run imported %d more copies, want 0", second.PostsImported)
			}
			if second.PostsSkipped != 1 {
				t.Errorf("PostsSkipped = %d, want 1", second.PostsSkipped)
			}

			pages, err := queries.ListPages(ctx, store.ListPagesParams{Limit: 10, Offset: 0})
			if err != nil {
				t.Fatalf("failed to list pages: %v", err)
			}
			if len(pages) != 1 {
				slugs := make([]string, len(pages))
				for i, p := range pages {
					slugs[i] = p.Slug
				}
				t.Errorf("got %d pages after two runs, want 1: %v", len(pages), slugs)
			}
		})
	}
}

func TestBaseSlugFallsBackWhenTitleTransliteratesToNothing(t *testing.T) {
	if got := baseSlug("Отель", "story-1"); got != "otel" {
		t.Errorf("baseSlug() = %q, want the transliterated title", got)
	}
	if got := baseSlug("???", "story-42"); got != "story-42" {
		t.Errorf("baseSlug() = %q, want the fallback", got)
	}
}

// setupImportDB gives a migrated database plus a tracker, for orchestration
// tests that go through importWithReader rather than a single stage.
func setupImportDB(t *testing.T) (*sql.DB, *store.Queries) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	queries := store.New(db)
	now := time.Now()
	if _, err := queries.CreateUser(context.Background(), store.CreateUserParams{
		Email: "admin@example.com", PasswordHash: "x", Role: "admin", Name: "Admin",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to seed admin: %v", err)
	}
	return db, queries
}

func fullOptions() types.ImportOptions {
	return types.ImportOptions{
		ImportTags: true, ImportCategories: true, ImportMedia: true,
		ImportPosts: true, ImportPages: true, ImportUsers: true,
	}
}

// TestImportReportsPartialResultWhenContentReadFails is the drift test commit
// 9144a8be owes. readContent is the only post-write hard abort: users and
// taxonomy have already been committed, so returning a nil result would discard
// every message describing what is now in the operator's database.
func TestImportReportsPartialResultWhenContentReadFails(t *testing.T) {
	db, queries := setupImportDB(t)
	reader := &fakeReader{
		authors:  []User{{ID: 1, Username: ns("a"), Email: ns("a@example.com")}},
		topics:   []Topic{{ID: 1, Text: ns("News")}},
		encTerms: map[int64][]EncyclopediaTerm{},
		errs:     map[string]error{"GetStories": errors.New("read timeout")},
	}

	result, err := NewSource().importWithReader(context.Background(), db, reader,
		map[string]string{}, fullOptions(), &mockTracker{})

	if err == nil {
		t.Fatal("expected the read failure to be returned")
	}
	if result == nil {
		t.Fatal("result was discarded, losing every message from the stages that already wrote rows")
	}
	if result.UsersImported == 0 || result.CategoriesImported == 0 {
		t.Errorf("rows written before the failure were not reported: users=%d categories=%d",
			result.UsersImported, result.CategoriesImported)
	}
	if len(result.Errors) == 0 {
		t.Error("the read failure should also be recorded on the result")
	}
	// The rows really are in the database, which is why reporting them matters.
	users, _ := queries.ListUsers(context.Background(), store.ListUsersParams{Limit: 10})
	if len(users) < 2 {
		t.Errorf("expected the imported user to be committed, got %d users", len(users))
	}
}

// TestImportRejectsMediaWithNothingToScan covers a review finding: media is
// discovered from imported bodies, so requesting it without posts or pages
// imported nothing at all and reported Completed with no message.
func TestImportRejectsMediaWithNothingToScan(t *testing.T) {
	db, _ := setupImportDB(t)
	reader := &fakeReader{encTerms: map[int64][]EncyclopediaTerm{}}

	result, err := NewSource().importWithReader(context.Background(), db, reader,
		map[string]string{"files_path": t.TempDir()},
		types.ImportOptions{ImportMedia: true}, &mockTracker{})
	if err != nil {
		t.Fatalf("importWithReader() error = %v", err)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("a requested option that cannot run must be an error, got errors=%v notices=%v",
			result.Errors, result.Notices)
	}
	if !strings.Contains(result.Errors[0], "neither posts nor pages") {
		t.Errorf("the error should name the cause: %q", result.Errors[0])
	}
}

func TestImportRejectsMediaWithoutFilesPath(t *testing.T) {
	db, _ := setupImportDB(t)
	reader := &fakeReader{
		stories:  []Story{{ID: 1, Title: ns("A"), BodyText: ns("<p>x</p>")}},
		encTerms: map[int64][]EncyclopediaTerm{},
	}
	result, err := NewSource().importWithReader(context.Background(), db, reader,
		map[string]string{}, types.ImportOptions{ImportMedia: true, ImportPosts: true}, &mockTracker{})
	if err != nil {
		t.Fatalf("importWithReader() error = %v", err)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "no files path") {
		t.Errorf("expected the missing-files-path error, got %v", result.Errors)
	}
}

// TestImportHonorsOptionCombinations pins that each option drives the work it
// claims, and only that work.
func TestImportHonorsOptionCombinations(t *testing.T) {
	newReader := func() *fakeReader {
		return &fakeReader{
			authors:     []User{{ID: 1, Username: ns("olegiv"), Email: ns("o@example.com")}},
			topics:      []Topic{{ID: 3, Text: ns("Hotels")}},
			storyCats:   []Category{{ID: 9, Title: ns("News")}},
			stories:     []Story{{ID: 1, Title: ns("Story"), BodyText: ns("<p>x</p>"), TopicID: ni(3), CategoryID: ni(9), Informant: ns("olegiv")}},
			staticPages: []StaticPage{{ID: 1, Title: ns("Page"), Text: ns("<p>y</p>"), Active: ni(1)}},
			encEntries:  []EncyclopediaEntry{{ID: 1, Title: ns("Enc"), Active: ni(1)}},
			encTerms:    map[int64][]EncyclopediaTerm{1: {{ID: 1, EntryID: ni(1), Title: ns("t"), Text: ns("d")}}},
		}
	}
	for _, tc := range []struct {
		name                     string
		opts                     types.ImportOptions
		posts, pages, cats, tags int
		users                    int
	}{
		{"posts only", types.ImportOptions{ImportPosts: true}, 1, 0, 0, 0, 0},
		{"pages only brings encyclopedia", types.ImportOptions{ImportPages: true}, 0, 2, 0, 0, 0},
		{"users only", types.ImportOptions{ImportUsers: true}, 0, 0, 0, 0, 1},
		{"taxonomy only", types.ImportOptions{ImportCategories: true, ImportTags: true}, 0, 0, 1, 1, 0},
		{"everything but media", types.ImportOptions{
			ImportPosts: true, ImportPages: true, ImportCategories: true,
			ImportTags: true, ImportUsers: true}, 1, 2, 1, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := setupImportDB(t)
			result, err := NewSource().importWithReader(context.Background(), db, newReader(),
				map[string]string{}, tc.opts, &mockTracker{})
			if err != nil {
				t.Fatalf("importWithReader() error = %v", err)
			}
			if result.PostsImported != tc.posts {
				t.Errorf("posts = %d, want %d", result.PostsImported, tc.posts)
			}
			if result.PagesImported != tc.pages {
				t.Errorf("pages = %d, want %d", result.PagesImported, tc.pages)
			}
			if result.CategoriesImported != tc.cats {
				t.Errorf("categories = %d, want %d", result.CategoriesImported, tc.cats)
			}
			if result.TagsImported != tc.tags {
				t.Errorf("tags = %d, want %d", result.TagsImported, tc.tags)
			}
			if result.UsersImported != tc.users {
				t.Errorf("users = %d, want %d", result.UsersImported, tc.users)
			}
		})
	}
}

// failingTracker exercises the compensating-rollback arm of track(), which no
// test reached: mockTracker always succeeds, so the rollback closure every call
// site passes had never executed.
type failingTracker struct{ err error }

func (f failingTracker) TrackImportedItem(context.Context, string, string, int64) error {
	return f.err
}

// TestTrackingFailureRollsBackTheCreatedRow pins the mechanism that keeps an
// untracked row from being left behind as an orphan invisible to
// "delete imported content".
func TestTrackingFailureRollsBackTheCreatedRow(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	result := &types.ImportResult{}

	stories := []Story{{ID: 1, Title: ns("Rolled Back"), BodyText: ns("<p>x</p>")}}
	NewSource().importStories(ctx, queries, stories, map[string]int64{}, adminID,
		defaultLang(t, queries), map[int64]int64{}, map[int64]int64{}, nil, nil,
		types.ImportOptions{}, result, failingTracker{err: errors.New("tracker table is locked")}, nil)

	if result.PostsImported != 0 {
		t.Errorf("PostsImported = %d; an untracked row must not be counted", result.PostsImported)
	}
	if _, err := queries.GetPageBySlug(ctx, "rolled-back"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("the page was not rolled back: err = %v", err)
	}
	if len(result.Errors) == 0 {
		t.Error("a tracking failure must be reported, not silently swallowed")
	}
}

// TestSlugAllocationRefusesRoutesOwnedElsewhere covers two guards that mutation
// testing showed were unexercised: an imported page must not shadow a module
// route, nor sit under a path an enabled redirect already answers.
func TestSlugAllocationRefusesRoutesOwnedElsewhere(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	now := time.Now()

	if _, err := queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: "/news", TargetUrl: "/elsewhere", StatusCode: 301,
		TargetType: model.TargetSelf, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to seed redirect: %v", err)
	}

	source := NewSource()
	source.SetPublicRouteChecker(pathOwner{"/hotels": true})
	result := &types.ImportResult{}
	stories := []Story{
		{ID: 1, Title: ns("News"), BodyText: ns("<p>a</p>")},
		{ID: 2, Title: ns("Hotels"), BodyText: ns("<p>b</p>")},
	}
	source.importStories(ctx, queries, stories, map[string]int64{}, adminID,
		defaultLang(t, queries), map[int64]int64{}, map[int64]int64{}, nil, nil,
		types.ImportOptions{}, result, &mockTracker{}, nil)

	for _, taken := range []string{"news", "hotels"} {
		if page, err := queries.GetPageBySlug(ctx, taken); err == nil {
			t.Errorf("page %d claimed %q, which is already owned elsewhere", page.ID, taken)
		}
	}
	if result.PostsImported+len(result.Errors) != 2 {
		t.Errorf("each story should be imported under another slug or refused: %+v", result)
	}
}

// pathOwner stands in for the module-route checker the module wires in
// production.
type pathOwner map[string]bool

func (p pathOwner) OwnsPublicPath(path string) bool { return p[path] }

func TestResolveLanguageCodeRejectsInactiveLanguage(t *testing.T) {
	queries, _ := setupDB(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "ru", Name: "Russian", NativeName: "Русский", IsDefault: false,
		IsActive: false, Direction: "ltr", Position: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to seed language: %v", err)
	}
	_, err := NewSource().resolveLanguageCode(ctx, queries, map[string]string{"language_code": "ru"})
	if err == nil {
		t.Fatal("an inactive target language must be refused")
	}
	if !strings.Contains(err.Error(), "inactive") {
		t.Errorf("the error should name the cause: %v", err)
	}
}

// TestStoryWithoutTimestampIsPublishedNow pins storyTimestamp's fallback: a
// NULL or zero source date would otherwise store 0001-01-01 and sort the story
// to the bottom of the archive forever.
func TestStoryWithoutTimestampIsPublishedNow(t *testing.T) {
	for _, tc := range []struct {
		name string
		when sql.NullTime
	}{
		{"null time", sql.NullTime{}},
		{"zero time marked valid", sql.NullTime{Time: time.Time{}, Valid: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries, adminID := setupDB(t)
			ctx := context.Background()
			result := &types.ImportResult{}
			stories := []Story{{ID: 7, Title: ns("Undated"), BodyText: ns("<p>x</p>"), Time: tc.when}}
			NewSource().importStories(ctx, queries, stories, map[string]int64{}, adminID,
				defaultLang(t, queries), map[int64]int64{}, map[int64]int64{}, nil, nil,
				types.ImportOptions{}, result, &mockTracker{}, nil)

			page, err := queries.GetPageBySlug(ctx, "undated")
			if err != nil {
				t.Fatalf("failed to load page: %v", err)
			}
			if page.CreatedAt.Year() < 2000 {
				t.Errorf("created_at = %v; an undated story must fall back to now", page.CreatedAt)
			}
			if !page.PublishedAt.Valid || page.PublishedAt.Time.Year() < 2000 {
				t.Errorf("published_at = %v; want a real timestamp", page.PublishedAt)
			}
		})
	}
}

// writeTestPNG puts a real, decodable image on disk so the media success path
// runs for real rather than against a stub.
func writeTestPNG(t *testing.T, dir, name string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 32), G: uint8(y * 32), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
}

// TestImportMediaRewritesEveryRawSpelling is the reason assetRef carries both
// Raw and Path. A PHP-Nuke body references the same file as "a/b.png" and
// "/a/b.png"; the file must be imported once, and BOTH spellings must be
// rewritten. Nothing pinned that, so discarding the raw spellings and keying
// the map by normalized path alone passed the whole suite — which on a real
// site leaves half the <img> tags pointing at the dead server.
func TestImportMediaRewritesEveryRawSpelling(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	root := t.TempDir()
	uploads := t.TempDir()
	t.Setenv(shared.EnvAllowedFileRoots, root)
	writeTestPNG(t, root, "tourism/hotels/photo.png")

	content := &importContent{
		stories: []Story{{
			ID: 1,
			BodyText: ns(`<p><img src="tourism/hotels/photo.png">` +
				`<img src="/tourism/hotels/photo.png"></p>`),
		}},
		encTerms: map[int64][]EncyclopediaTerm{},
	}
	result := &types.ImportResult{}
	mediaMap, err := NewSource().importMedia(ctx, queries, content, nil, root, uploads,
		adminID, defaultLang(t, queries), result, &mockTracker{})
	if err != nil {
		t.Fatalf("importMedia() error = %v", err)
	}

	if result.MediaImported != 1 {
		t.Fatalf("MediaImported = %d, want 1; errors %v notices %v",
			result.MediaImported, result.Errors, result.Notices)
	}
	// One file, two spellings, one URL.
	if len(mediaMap) != 2 {
		t.Fatalf("mediaMap has %d entries, want both raw spellings: %v", len(mediaMap), mediaMap)
	}
	bare, hasBare := mediaMap["tourism/hotels/photo.png"]
	rooted, hasRooted := mediaMap["/tourism/hotels/photo.png"]
	if !hasBare || !hasRooted {
		t.Fatalf("both spellings must be rewritten, got %v", mediaMap)
	}
	if bare != rooted {
		t.Errorf("the same file produced two URLs: %q and %q", bare, rooted)
	}
	if !strings.HasPrefix(bare, "/uploads/") {
		t.Errorf("rewritten URL = %q, want an /uploads/ path", bare)
	}

	// The media row describes a real decoded image, not a placeholder.
	media, err := queries.ListMedia(ctx, store.ListMediaParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("failed to list media: %v", err)
	}
	if len(media) != 1 {
		t.Fatalf("got %d media rows, want 1", len(media))
	}
	if media[0].MimeType != "image/png" {
		t.Errorf("mime = %q, want image/png", media[0].MimeType)
	}
	if media[0].Size <= 0 {
		t.Errorf("size = %d, want the real byte count", media[0].Size)
	}
	if !media[0].Width.Valid || media[0].Width.Int64 != 8 || media[0].Height.Int64 != 8 {
		t.Errorf("dimensions = %v x %v, want 8 x 8", media[0].Width, media[0].Height)
	}

	// End to end: no reference to the old site survives in the written body.
	body := NewSource().prepareBody(assembleStoryBody(&content.stories[0]), mediaMap, nil)
	if strings.Contains(body, `"tourism/hotels/photo.png"`) ||
		strings.Contains(body, `"/tourism/hotels/photo.png"`) {
		t.Errorf("a source path survived into the imported body: %s", body)
	}
	if strings.Count(body, bare) != 2 {
		t.Errorf("expected both img tags rewritten to %q, got: %s", bare, body)
	}
}

// TestImportMediaHandlesNonImageFile covers the other branch of importOneFile,
// where no decoding happens and no variants are created.
func TestImportMediaHandlesNonImageFile(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	root := t.TempDir()
	uploads := t.TempDir()
	t.Setenv(shared.EnvAllowedFileRoots, root)
	if err := os.WriteFile(filepath.Join(root, "brochure.pdf"),
		[]byte("%PDF-1.4\n% test\n"), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	content := &importContent{
		stories:  []Story{{ID: 1, BodyText: ns(`<a href="brochure.pdf">Brochure</a>`)}},
		encTerms: map[int64][]EncyclopediaTerm{},
	}
	result := &types.ImportResult{}
	mediaMap, err := NewSource().importMedia(ctx, queries, content, nil, root, uploads,
		adminID, defaultLang(t, queries), result, &mockTracker{})
	if err != nil {
		t.Fatalf("importMedia() error = %v", err)
	}
	if result.MediaImported != 1 {
		t.Fatalf("MediaImported = %d; errors %v", result.MediaImported, result.Errors)
	}
	if len(mediaMap) != 1 {
		t.Errorf("mediaMap = %v, want the single reference", mediaMap)
	}

	media, err := queries.ListMedia(ctx, store.ListMediaParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("failed to list media: %v", err)
	}
	if len(media) != 1 || media[0].MimeType != "application/pdf" {
		t.Fatalf("got %+v, want one application/pdf row", media)
	}
	if media[0].Width.Valid || media[0].Height.Valid {
		t.Errorf("a non-image must have no dimensions, got %v x %v", media[0].Width, media[0].Height)
	}
	if media[0].Size <= 0 {
		t.Errorf("size = %d, want the real byte count", media[0].Size)
	}
}

// TestStagesReportTheProgressPhaseTheyWork pins that each stage announces the
// phase it actually imports, with a total matching the work in front of it.
// A wrong phase or a total that disagrees with the rows drives a misleading
// admin progress bar during exactly the long-running job it exists for.
func TestStagesReportTheProgressPhaseTheyWork(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)
	source := NewSource()

	t.Run("posts", func(t *testing.T) {
		tracker := &mockTracker{}
		stories := []Story{{ID: 1, Title: ns("A")}, {ID: 2, Title: ns("B")}}
		source.importStories(ctx, queries, stories, map[string]int64{}, adminID, lang,
			map[int64]int64{}, map[int64]int64{}, nil, nil, types.ImportOptions{},
			&types.ImportResult{}, tracker, nil)
		total, ok := tracker.totalFor(types.EntityPost)
		if !ok {
			t.Fatal("importStories reported no post phase")
		}
		if total != 2 {
			t.Errorf("post total = %d, want 2", total)
		}
	})

	// The page phase spans two stages, so its total is reported by the
	// orchestration. Reported from importStaticPages alone it counted only the
	// static pages, and the encyclopedia pages that followed pushed progress
	// past the stated total.
	t.Run("pages and encyclopedia share one page total", func(t *testing.T) {
		db, _ := setupImportDB(t)
		tracker := &mockTracker{}
		reader := &fakeReader{
			staticPages: []StaticPage{
				{ID: 1, Title: ns("P1"), Active: ni(1)},
				{ID: 2, Title: ns("P2"), Active: ni(1)},
			},
			encEntries: []EncyclopediaEntry{
				{ID: 1, Title: ns("E1"), Active: ni(1)},
				{ID: 2, Title: ns("E2"), Active: ni(1)},
				{ID: 3, Title: ns("E3"), Active: ni(1)},
			},
			encTerms: map[int64][]EncyclopediaTerm{},
		}
		result, err := NewSource().importWithReader(ctx, db, reader, map[string]string{},
			types.ImportOptions{ImportPages: true}, tracker)
		if err != nil {
			t.Fatalf("importWithReader() error = %v", err)
		}
		total, ok := tracker.totalFor(types.EntityPage)
		if !ok {
			t.Fatal("no page phase was reported")
		}
		if total != 5 {
			t.Errorf("page total = %d, want 5 (2 static pages + 3 encyclopedias)", total)
		}
		if result.PagesImported > total {
			t.Errorf("imported %d pages against a stated total of %d; progress would "+
				"count past its own maximum", result.PagesImported, total)
		}
	})

	t.Run("users", func(t *testing.T) {
		tracker := &mockTracker{}
		reader := &fakeReader{authors: []User{
			{ID: 1, Username: ns("a"), Email: ns("a@example.com")},
			{ID: 2, Username: ns("b"), Email: ns("b@example.com")},
		}}
		if err := source.importUsers(ctx, queries, reader, map[string]int64{},
			types.ImportOptions{}, &types.ImportResult{}, tracker); err != nil {
			t.Fatalf("importUsers() error = %v", err)
		}
		total, ok := tracker.totalFor(types.EntityUser)
		if !ok {
			t.Fatal("importUsers reported no user phase")
		}
		if total != 2 {
			t.Errorf("user total = %d, want 2", total)
		}
	})

	// The category total must survive the page-category read failing, since
	// that path nils the slice after the count is taken.
	t.Run("categories with the optional table absent", func(t *testing.T) {
		tracker := &mockTracker{}
		reader := &fakeReader{
			topics:      []Topic{{ID: 1, Text: ns("One")}, {ID: 2, Text: ns("Two")}},
			pageCatsErr: &mysql.MySQLError{Number: mysqlErrNoSuchTable},
		}
		if err := source.importCategories(ctx, queries, reader, lang,
			map[int64]int64{}, map[int64]int64{}, types.ImportOptions{},
			&types.ImportResult{}, tracker); err != nil {
			t.Fatalf("importCategories() error = %v", err)
		}
		total, ok := tracker.totalFor(types.EntityCategory)
		if !ok {
			t.Fatal("importCategories reported no category phase")
		}
		if total != 2 {
			t.Errorf("category total = %d, want 2 (topics only)", total)
		}
	})
}

// TestAssociationFailureStillCountsThePage pins the contract the whole
// continue-on-error design rests on: a page that was genuinely written counts as
// imported even when attaching its taxonomy fails, and the failure is still
// reported. No test made a destination write fail, so every
// AddError("Failed to create ...") branch in the package was unreachable.
func TestAssociationFailureStillCountsThePage(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()
	admin, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "admin@example.com", PasswordHash: "x", Role: "admin", Name: "A",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	lang := defaultLang(t, queries)
	category, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name: "Hotels", Slug: "hotels", LanguageCode: lang,
		Description: sql.NullString{String: "", Valid: true},
		CreatedAt:   now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed category: %v", err)
	}

	// Remove the join table so the association fails while the page write
	// succeeds — the precise split the contract is about.
	if _, err := db.Exec(`DROP TABLE page_categories`); err != nil {
		t.Fatalf("drop join table: %v", err)
	}

	result := &types.ImportResult{}
	stories := []Story{{ID: 1, Title: ns("Hotel Review"), BodyText: ns("<p>x</p>"), TopicID: ni(3)}}
	NewSource().importStories(ctx, queries, stories, map[string]int64{}, admin.ID, lang,
		map[int64]int64{3: category.ID}, map[int64]int64{}, nil, nil,
		types.ImportOptions{}, result, &mockTracker{}, nil)

	if result.PostsImported != 1 {
		t.Errorf("PostsImported = %d; the page was written, so it must be counted", result.PostsImported)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("the association failure must be reported, got %v", result.Errors)
	}
	if !strings.Contains(result.Errors[0], "category") {
		t.Errorf("the error should name what failed: %q", result.Errors[0])
	}
	if _, err := queries.GetPageBySlug(ctx, "hotel-review"); err != nil {
		t.Errorf("the page itself should still exist: %v", err)
	}
}

// TestPageWriteFailureIsReportedAndNotCounted covers the other side: when the
// write itself fails, nothing is counted and the run continues rather than
// aborting or panicking.
func TestPageWriteFailureIsReportedAndNotCounted(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()
	admin, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "admin@example.com", PasswordHash: "x", Role: "admin", Name: "A",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	lang := defaultLang(t, queries)

	if _, err := db.Exec(`DROP TABLE pages`); err != nil {
		t.Fatalf("drop pages: %v", err)
	}

	result := &types.ImportResult{}
	stories := []Story{
		{ID: 1, Title: ns("First"), BodyText: ns("<p>a</p>")},
		{ID: 2, Title: ns("Second"), BodyText: ns("<p>b</p>")},
	}
	NewSource().importStories(ctx, queries, stories, map[string]int64{}, admin.ID, lang,
		map[int64]int64{}, map[int64]int64{}, nil, nil,
		types.ImportOptions{}, result, &mockTracker{}, nil)

	if result.PostsImported != 0 {
		t.Errorf("PostsImported = %d, want 0 when every write fails", result.PostsImported)
	}
	// Both rows are attempted: one bad row must not abort the remaining work.
	if len(result.Errors) != 2 {
		t.Errorf("expected one error per story, got %d: %v", len(result.Errors), result.Errors)
	}
}

// TestAuthorLookupIsCaseInsensitive covers a Codex review finding. The source
// query matches usernames under the MySQL column collation, which is
// case-insensitive on a PHP-Nuke database, but a Go map is not — so a story
// crediting "Olegiv" against a user row stored as "olegiv" imported the user
// and then attributed every one of their stories to the fallback account.
func TestAuthorLookupIsCaseInsensitive(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	result := &types.ImportResult{}
	userMap := make(map[string]int64)

	// The destination row is lower-case; the stories credit mixed case.
	reader := &fakeReader{authors: []User{
		{ID: 2, Username: ns("olegiv"), Name: ns("Oleg"), Email: ns("o@example.com")},
	}}
	if err := NewSource().importUsers(ctx, queries, reader, userMap,
		types.ImportOptions{}, result, &mockTracker{}); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}
	imported := userMap[authorKey("olegiv")]
	if imported == 0 {
		t.Fatalf("user was not mapped: %v", userMap)
	}

	for _, spelling := range []string{"Olegiv", "OLEGIV", "olegiv", " Olegiv "} {
		story := &Story{Informant: ns(spelling)}
		got, resolved := resolveAuthorID(story, userMap, adminID)
		if !resolved || got != imported {
			t.Errorf("informant %q resolved to %d (resolved=%v), want the imported user %d",
				spelling, got, resolved, imported)
		}
	}
}

func TestAuthorKeyFolds(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Olegiv", "olegiv"},
		{" Olegiv ", "olegiv"},
		{"OLEGIV", "olegiv"},
		{"", ""},
		{"   ", ""},
	} {
		if got := authorKey(tc.in); got != tc.want {
			t.Errorf("authorKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSkipExistingRerunImportsNoMedia covers a Codex review finding.
//
// Media is discovered from bodies and imported before any page stage runs, so
// the skip decision used to arrive too late to stop it. A rerun re-imported
// every referenced file under a fresh UUID, then skipped the page that would
// have used it — leaving a complete set of unattached duplicates in the media
// library on every single run.
func TestSkipExistingRerunImportsNoMedia(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)
	root := t.TempDir()
	t.Setenv(shared.EnvAllowedFileRoots, root)
	writeTestPNG(t, root, "photo.png")

	stories := []Story{{ID: 1, Title: ns("Hotel Royal"), BodyText: ns(`<img src="photo.png">`)}}
	opts := types.ImportOptions{ImportMedia: true, ImportPosts: true, SkipExisting: true}
	source := NewSource()

	for run := 1; run <= 2; run++ {
		content := &importContent{stories: stories, encTerms: map[int64][]EncyclopediaTerm{}}
		result := &types.ImportResult{}
		skips := source.planSkips(ctx, queries, content, opts, result)
		mediaMap, err := source.importMedia(ctx, queries, content, skips, root, t.TempDir(),
			adminID, lang, result, &mockTracker{})
		if err != nil {
			t.Fatalf("run %d: importMedia() error = %v", run, err)
		}
		source.importStories(ctx, queries, stories, map[string]int64{}, adminID, lang,
			map[int64]int64{}, map[int64]int64{}, mediaMap, skips, opts, result, &mockTracker{}, nil)

		wantMedia, wantPosts := 1, 1
		if run == 2 {
			wantMedia, wantPosts = 0, 0
		}
		if result.MediaImported != wantMedia {
			t.Errorf("run %d imported %d media, want %d", run, result.MediaImported, wantMedia)
		}
		if result.PostsImported != wantPosts {
			t.Errorf("run %d imported %d posts, want %d", run, result.PostsImported, wantPosts)
		}
	}

	media, err := queries.ListMedia(ctx, store.ListMediaParams{Limit: 50, Offset: 0})
	if err != nil {
		t.Fatalf("failed to list media: %v", err)
	}
	if len(media) != 1 {
		names := make([]string, len(media))
		for i, m := range media {
			names[i] = m.Filename
		}
		t.Errorf("two runs left %d media rows, want 1: %v", len(media), names)
	}
}

// TestPlanSkipsKeepsDistinctRowsSharingATitle covers the second half of the
// same defect. Deciding skips inside the stages meant a run could skip its own
// work: the first of two same-titled stories claimed the slug, and the second —
// a genuinely different article — was then read as "already imported" and
// dropped. Settling every decision before the first write removes the race.
func TestPlanSkipsKeepsDistinctRowsSharingATitle(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)

	stories := []Story{
		{ID: 1, Title: ns("News"), BodyText: ns("<p>first</p>")},
		{ID: 2, Title: ns("News"), BodyText: ns("<p>second, a different article</p>")},
	}
	opts := types.ImportOptions{ImportPosts: true, SkipExisting: true}
	source := NewSource()
	content := &importContent{stories: stories, encTerms: map[int64][]EncyclopediaTerm{}}
	result := &types.ImportResult{}

	skips := source.planSkips(ctx, queries, content, opts, result)
	source.importStories(ctx, queries, stories, map[string]int64{}, adminID, lang,
		map[int64]int64{}, map[int64]int64{}, nil, skips, opts, result, &mockTracker{}, nil)

	if result.PostsImported != 2 {
		t.Errorf("imported %d of 2 stories; a distinct article was dropped as a "+
			"duplicate of its namesake (skipped %d)", result.PostsImported, result.PostsSkipped)
	}

	// And a rerun must still skip both, which is what SkipExisting is for.
	rerun := &types.ImportResult{}
	source.importStories(ctx, queries, stories, map[string]int64{}, adminID, lang,
		map[int64]int64{}, map[int64]int64{}, nil,
		source.planSkips(ctx, queries, content, opts, rerun), opts, rerun, &mockTracker{}, nil)
	if rerun.PostsImported != 0 {
		t.Errorf("rerun imported %d more copies, want 0", rerun.PostsImported)
	}
}

// TestPageExistsIsCalledOnlyWhilePlanning is the mechanical half of the fix.
//
// The bug was not a wrong probe, it was a probe in the wrong place: any skip
// decision made after planSkips runs is a decision media discovery could not
// see. Rather than trust that nobody reintroduces one, this walks the package
// and fails if pageExists is called from anywhere else.
func TestPageExistsIsCalledOnlyWhilePlanning(t *testing.T) {
	const allowed = "planSkips"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("failed to read package directory: %v", err)
	}
	fset := token.NewFileSet()
	callers := make(map[string][]string)

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("failed to parse %s: %v", name, parseErr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "pageExists" {
					callers[fn.Name.Name] = append(callers[fn.Name.Name],
						fmt.Sprintf("%s:%d", name, fset.Position(call.Pos()).Line))
				}
				return true
			})
		}
	}

	if len(callers[allowed]) == 0 {
		t.Errorf("%s no longer calls pageExists; either the probe moved or this "+
			"test is now guarding nothing", allowed)
	}
	for caller, sites := range callers {
		if caller != allowed {
			t.Errorf("%s calls pageExists at %v; skip decisions made outside %s "+
				"run after media has already been imported, which is the bug this "+
				"guards", caller, sites, allowed)
		}
	}
}
