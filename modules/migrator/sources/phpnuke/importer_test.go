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
	"strconv"
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
	admins      []User

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
func (f *fakeReader) GetPublishingAdmins(context.Context) ([]User, error) {
	return f.admins, f.err("GetPublishingAdmins")
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
		map[int64]int64{}, nil, nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)

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
		map[int64]int64{}, nil, nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)

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
		nil, nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)

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

// takenTerms builds a lookup over a fixed set of existing taxonomy rows.
func takenTerms(rows map[string]taxonomyTerm) func(string) (taxonomyTerm, bool, error) {
	return func(slug string) (taxonomyTerm, bool, error) {
		term, ok := rows[slug]
		return term, ok, nil
	}
}

func TestResolveTaxonomySlugProbesUntilFree(t *testing.T) {
	ctx := context.Background()
	rows := map[string]taxonomyTerm{
		"news":   {ID: 1, Name: "Different", Language: "en"},
		"news-2": {ID: 2, Name: "Also different", Language: "en"},
	}

	id, slug, err := resolveTaxonomySlug(ctx, "news", "News", "en", takenTerms(rows))
	if err != nil {
		t.Fatalf("resolveTaxonomySlug() error = %v", err)
	}
	if id != 0 {
		t.Errorf("reused row %d; no existing row names this term", id)
	}
	if slug != "news-3" {
		t.Errorf("slug = %q, want %q", slug, "news-3")
	}
}

// TestResolveTaxonomySlugSurfacesProbeErrors guards both halves of the
// contract. A probe error must never be read as "free", and it must abort the
// search rather than issuing a hundred more doomed probes and reporting a
// database outage as a slug collision.
func TestResolveTaxonomySlugSurfacesProbeErrors(t *testing.T) {
	probes := 0
	id, slug, err := resolveTaxonomySlug(context.Background(), "news", "News", "en",
		func(string) (taxonomyTerm, bool, error) {
			probes++
			return taxonomyTerm{}, false, errors.New("database is locked")
		})
	if err == nil {
		t.Fatal("expected the probe error to surface, not be swallowed")
	}
	if id != 0 || slug != "" {
		t.Errorf("resolveTaxonomySlug() = %d, %q; want zero values when the check cannot be trusted", id, slug)
	}
	if probes != 1 {
		t.Errorf("probed %d times after an error; the search must stop at the first failure", probes)
	}
}

func TestResolveTaxonomySlugStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id, slug, err := resolveTaxonomySlug(ctx, "news", "News", "en",
		func(string) (taxonomyTerm, bool, error) { return taxonomyTerm{}, false, nil })
	if err == nil {
		t.Fatal("expected the cancellation to surface as an error")
	}
	if id != 0 || slug != "" {
		t.Errorf("resolveTaxonomySlug() = %d, %q; want zero values once the context is canceled", id, slug)
	}
}

// TestResolveTaxonomySlugRecoversItsOwnSuffixedRow covers a Codex review
// finding. Reuse used to turn on the slug alone, so two distinct source labels
// that slugify the same way — "Hello World" and "Hello, World!" — silently
// became one term and the posts of the second were refiled under the first.
//
// Distinguishing them is only half the fix. The second term is stored under a
// suffixed slug, so a probe that gives up after the base slug would allocate a
// fresh suffix on every re-run; the walk has to recognise its own earlier row.
func TestResolveTaxonomySlugRecoversItsOwnSuffixedRow(t *testing.T) {
	rows := map[string]taxonomyTerm{
		"hello-world":   {ID: 1, Name: "Hello World", Language: "en"},
		"hello-world-2": {ID: 2, Name: "Hello, World!", Language: "en"},
	}

	for _, tc := range []struct {
		name, term string
		wantID     int64
		wantSlug   string
	}{
		{"base row is the same term", "Hello World", 1, ""},
		{"suffixed row is the same term", "Hello, World!", 2, ""},
		{"case and padding are ignored", "  hello world  ", 1, ""},
		{"a third distinct term allocates", "Hello - World", 0, "hello-world-3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, slug, err := resolveTaxonomySlug(context.Background(), "hello-world",
				tc.term, "en", takenTerms(rows))
			if err != nil {
				t.Fatalf("resolveTaxonomySlug() error = %v", err)
			}
			if id != tc.wantID || slug != tc.wantSlug {
				t.Errorf("resolveTaxonomySlug() = %d, %q; want %d, %q",
					id, slug, tc.wantID, tc.wantSlug)
			}
		})
	}
}

// TestResolveTaxonomySlugKeepsLanguagesApart pins the other half of "same
// term": two languages legitimately hold the same label, and merging them would
// file translated content under one term.
func TestResolveTaxonomySlugKeepsLanguagesApart(t *testing.T) {
	rows := map[string]taxonomyTerm{"hotels": {ID: 1, Name: "Hotels", Language: "en"}}

	id, slug, err := resolveTaxonomySlug(context.Background(), "hotels", "Hotels", "ru",
		takenTerms(rows))
	if err != nil {
		t.Fatalf("resolveTaxonomySlug() error = %v", err)
	}
	if id != 0 {
		t.Errorf("reused the English row %d for a Russian term", id)
	}
	if slug != "hotels-2" {
		t.Errorf("slug = %q, want %q", slug, "hotels-2")
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

	postsOnly, err := source.readContent(ctx, reader, types.ImportOptions{ImportPosts: true}, &types.ImportResult{})
	if err != nil {
		t.Fatalf("readContent() error = %v", err)
	}
	if len(postsOnly.stories) != 1 || len(postsOnly.staticPages) != 0 || len(postsOnly.encEntries) != 0 {
		t.Errorf("posts-only read pulled page content: %+v", postsOnly)
	}

	pagesOnly, err := source.readContent(ctx, reader, types.ImportOptions{ImportPages: true}, &types.ImportResult{})
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

// TestEveryPhaseReportsItsProgressToCompletion covers a Codex review finding.
//
// Each stage announced its total once and then never published another sample,
// so `Processed` stayed at zero for the whole phase and the admin job view read
// "0 / 669" from the first row to the last — indistinguishable from a stalled
// import, on a detached job where that view is all an operator has.
//
// The assertion is per phase rather than per stage so it keeps holding for the
// page phase, whose total is shared by two stages that each fill part of it.
func TestEveryPhaseReportsItsProgressToCompletion(t *testing.T) {
	db, _ := setupImportDB(t)
	root := t.TempDir()
	t.Setenv(shared.EnvAllowedFileRoots, root)
	t.Setenv("OCMS_UPLOADS_DIR", t.TempDir())
	writeTestPNG(t, root, "photo.png")

	reader := &fakeReader{
		authors: []User{
			{ID: 1, Username: ns("sveta"), Email: ns("sveta@example.com")},
			{ID: 2, Username: ns("oleg"), Email: ns("oleg@example.com")},
		},
		admins:    []User{{Username: ns("God"), Name: ns("Admin"), Email: ns("god@example.com")}},
		topics:    []Topic{{ID: 1, Text: ns("News")}, {ID: 2, Text: ns("Hotels")}},
		storyCats: []Category{{ID: 1, Title: ns("Travel")}},
		pageCats:  []Category{{ID: 1, Title: ns("Info")}},
		stories: []Story{
			{ID: 1, Title: ns("One"), BodyText: ns(`<img src="photo.png">`)},
			{ID: 2, Title: ns("Two"), BodyText: ns("<p>two</p>")},
			{ID: 3, Title: ns("Three"), BodyText: ns("<p>three</p>")},
		},
		staticPages: []StaticPage{{ID: 1, Title: ns("About"), Text: ns("<p>about</p>"), Active: ni(1)}},
		encEntries:  []EncyclopediaEntry{{ID: 1, Title: ns("Phrasebook"), Active: ni(1)}},
		encTerms:    map[int64][]EncyclopediaTerm{1: {{ID: 1, EntryID: ni(1), Title: ns("Hi")}}},
	}

	tracker := &mockTracker{}
	if _, err := NewSource().importWithReader(context.Background(), db, reader,
		map[string]string{"files_path": root}, fullOptions(), tracker); err != nil {
		t.Fatalf("importWithReader() error = %v", err)
	}

	// Last sample wins: the tracker keeps every one, but the job view only ever
	// shows the most recent for a phase.
	final := make(map[types.EntityType]types.Progress)
	samples := make(map[types.EntityType]int)
	for _, p := range tracker.progress {
		final[p.Phase] = p
		samples[p.Phase]++
	}
	if len(final) == 0 {
		t.Fatal("no progress was reported at all")
	}

	for _, phase := range []types.EntityType{
		types.EntityUser, types.EntityCategory, types.EntityTag,
		types.EntityMedia, types.EntityPost, types.EntityPage,
	} {
		last, ok := final[phase]
		if !ok {
			t.Errorf("phase %q published no progress", phase)
			continue
		}
		if last.Total == 0 {
			t.Errorf("phase %q announced a total of 0; this test proves nothing for it", phase)
			continue
		}
		if last.Processed != last.Total {
			t.Errorf("phase %q finished at %d / %d; the job view would show it "+
				"permanently short of done", phase, last.Processed, last.Total)
		}
		if samples[phase] < 2 {
			t.Errorf("phase %q published %d sample(s); a phase that reports only its "+
				"total leaves the progress bar pinned at zero", phase, samples[phase])
		}
	}
}

// TestEmptySelectedTablesAreNotAnOperatorError covers a Codex review finding.
// The media stage asked whether any content had been *read*, which conflated
// "you did not select the content media is discovered from" with "the tables
// you selected are empty" — reporting the second, a perfectly valid import of a
// site with no stories yet, as an error that finished the job as Partial.
func TestEmptySelectedTablesAreNotAnOperatorError(t *testing.T) {
	db, _ := setupImportDB(t)
	root := t.TempDir()
	t.Setenv(shared.EnvAllowedFileRoots, root)
	t.Setenv("OCMS_UPLOADS_DIR", t.TempDir())

	reader := &fakeReader{encTerms: map[int64][]EncyclopediaTerm{}}
	result, err := NewSource().importWithReader(context.Background(), db, reader,
		map[string]string{"files_path": root},
		types.ImportOptions{ImportPosts: true, ImportPages: true, ImportMedia: true},
		&mockTracker{})
	if err != nil {
		t.Fatalf("importWithReader() error = %v", err)
	}
	if len(result.Errors) != 0 {
		t.Errorf("an empty but correctly configured import reported errors: %v", result.Errors)
	}
	if result.MediaImported != 0 {
		t.Errorf("MediaImported = %d, want 0", result.MediaImported)
	}
}

// TestMediaWithoutContentOptionsIsStillAnError keeps the other half of that
// check alive: selecting media without posts or pages really is a mistake,
// because media is only ever discovered from imported bodies.
func TestMediaWithoutContentOptionsIsStillAnError(t *testing.T) {
	db, _ := setupImportDB(t)
	root := t.TempDir()
	t.Setenv(shared.EnvAllowedFileRoots, root)

	reader := &fakeReader{encTerms: map[int64][]EncyclopediaTerm{}}
	result, err := NewSource().importWithReader(context.Background(), db, reader,
		map[string]string{"files_path": root},
		types.ImportOptions{ImportMedia: true}, &mockTracker{})
	if err != nil {
		t.Fatalf("importWithReader() error = %v", err)
	}
	if len(result.Errors) == 0 {
		t.Error("selecting media with neither posts nor pages passed without comment")
	}
}

// TestPublishingAdminReadFailuresAreClassified covers a Codex review finding.
//
// The reader used to decide for itself whether an unreadable `authors` table
// was absent, by probing it — and a probe that fails for a missing SELECT grant
// or a canceled context is indistinguishable from one that fails because the
// table is gone. Only ER_NO_SUCH_TABLE is routine; everything else has to
// surface, or the import silently attributes an administrator's whole archive
// to the fallback account.
func TestPublishingAdminReadFailuresAreClassified(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantError bool
	}{
		{
			name: "absent table is a notice",
			err:  &mysql.MySQLError{Number: mysqlErrNoSuchTable, Message: "Table 'tr_authors' doesn't exist"},
		},
		{
			name:      "permission denied is an error",
			err:       &mysql.MySQLError{Number: 1142, Message: "SELECT command denied"},
			wantError: true,
		},
		{
			name:      "transport failure is an error",
			err:       errors.New("invalid connection"),
			wantError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries, _ := setupDB(t)
			result := &types.ImportResult{}
			reader := &fakeReader{
				authors: []User{{ID: 1, Username: ns("sveta"), Email: ns("sveta@example.com")}},
				errs:    map[string]error{"GetPublishingAdmins": tc.err},
			}

			if err := NewSource().importUsers(context.Background(), queries, reader,
				map[string]int64{}, types.ImportOptions{}, result, &mockTracker{}); err != nil {
				t.Fatalf("importUsers() error = %v", err)
			}

			if got := len(result.Errors) > 0; got != tc.wantError {
				t.Errorf("reported error = %v, want %v (errors %v, notices %v)",
					got, tc.wantError, result.Errors, result.Notices)
			}
			if !tc.wantError && len(result.Notices) == 0 {
				t.Error("an absent authors table passed with no notice at all")
			}
			// Either way the registered half must still import: losing bylines
			// must not cost the accounts that were readable.
			if result.UsersImported != 1 {
				t.Errorf("UsersImported = %d, want 1", result.UsersImported)
			}
		})
	}
}

// TestCollidingTopicLabelsStayDistinct is the stage-level companion to
// TestResolveTaxonomySlugRecoversItsOwnSuffixedRow: two topics whose labels
// slugify identically must become two categories, and a re-run must recover
// both rather than allocate a third.
func TestCollidingTopicLabelsStayDistinct(t *testing.T) {
	queries, _ := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)
	reader := &fakeReader{topics: []Topic{
		{ID: 1, Text: ns("Hello World")},
		{ID: 2, Text: ns("Hello, World!")},
	}}
	opts := types.ImportOptions{ImportCategories: true}

	first := &types.ImportResult{}
	topicMap := map[int64]int64{}
	if err := NewSource().importCategories(ctx, queries, reader, lang, topicMap,
		map[int64]int64{}, opts, first, &mockTracker{}); err != nil {
		t.Fatalf("importCategories() error = %v", err)
	}
	if first.CategoriesImported != 2 {
		t.Fatalf("imported %d of 2 topics; distinct terms were merged (skipped %d)",
			first.CategoriesImported, first.CategoriesSkipped)
	}
	if topicMap[1] == topicMap[2] {
		t.Fatalf("both topics map to category %d, so every post from the second "+
			"would be refiled under the first", topicMap[1])
	}

	second := &types.ImportResult{}
	rerunMap := map[int64]int64{}
	if err := NewSource().importCategories(ctx, queries, reader, lang, rerunMap,
		map[int64]int64{}, opts, second, &mockTracker{}); err != nil {
		t.Fatalf("importCategories() error = %v", err)
	}
	if second.CategoriesImported != 0 {
		t.Errorf("re-run created %d more categories; the suffixed row was not recovered",
			second.CategoriesImported)
	}
	for id := range topicMap {
		if rerunMap[id] != topicMap[id] {
			t.Errorf("topic %d mapped to category %d on the first run and %d on the second",
				id, topicMap[id], rerunMap[id])
		}
	}
}

// TestAdminsSharingAnEmailKeepDistinctBylines covers a Codex review finding.
//
// oCMS grants one account per email, and `authors.email` on a PHP-Nuke site is
// very often the shared webmaster address — a fact this importer's own
// documentation calls out. Reusing an account by email therefore merged several
// administrators into one and handed all of their stories to whichever was read
// first. That merge is not something an operator can undo; two accounts they
// can.
func TestAdminsSharingAnEmailKeepDistinctBylines(t *testing.T) {
	queries, _ := setupDB(t)
	ctx := context.Background()
	reader := &fakeReader{admins: []User{
		{Username: ns("God"), Name: ns("Oleg"), Email: ns("webmaster@site.ru")},
		{Username: ns("Sveta"), Name: ns("Sveta"), Email: ns("webmaster@site.ru")},
	}}

	var firstRun map[string]int64
	for run := 1; run <= 2; run++ {
		userMap := map[string]int64{}
		result := &types.ImportResult{}
		if err := NewSource().importUsers(ctx, queries, reader, userMap,
			types.ImportOptions{}, result, &mockTracker{}); err != nil {
			t.Fatalf("run %d: importUsers() error = %v", run, err)
		}

		god, sveta := userMap[authorKey("God")], userMap[authorKey("Sveta")]
		if god == 0 || sveta == 0 {
			t.Fatalf("run %d: an admin was not mapped at all: %v", run, userMap)
		}
		if god == sveta {
			t.Fatalf("run %d: both admins map to account %d, so one loses every byline",
				run, god)
		}
		if len(result.Notices) == 0 {
			t.Errorf("run %d: the address clash was resolved silently", run)
		}
		if run == 1 {
			if result.UsersImported != 2 {
				t.Errorf("first run imported %d accounts, want 2", result.UsersImported)
			}
			firstRun = userMap
			continue
		}
		// A re-run must find both accounts again rather than mint more.
		if result.UsersImported != 0 {
			t.Errorf("re-run created %d more accounts; the inert address is not stable",
				result.UsersImported)
		}
		for name, id := range firstRun {
			if userMap[name] != id {
				t.Errorf("%q mapped to %d on the first run and %d on the second",
					name, id, userMap[name])
			}
		}
	}

	users, err := queries.ListUsers(ctx, store.ListUsersParams{Limit: 20, Offset: 0})
	if err != nil {
		t.Fatalf("failed to list users: %v", err)
	}
	for _, user := range users {
		if user.Role == model.RoleAdmin {
			continue // the seeded operator
		}
		if user.Email == "webmaster@site.ru" {
			continue // the first claimant keeps the real address
		}
		if !strings.HasSuffix(user.Email, ".invalid") {
			t.Errorf("substitute address %q is deliverable; it must sit under the "+
				"reserved .invalid domain so no real mailbox can receive it", user.Email)
		}
	}
}

// TestDistinctInertEmailIsStableAndUndeliverable pins the properties the fix
// above rests on. Determinism is what makes a re-run idempotent, distinctness
// is the whole point, and the reserved .invalid domain (RFC 2606) is what
// guarantees the substitute address can never reach a real mailbox.
func TestDistinctInertEmailIsStableAndUndeliverable(t *testing.T) {
	t.Run("shape", func(t *testing.T) {
		for _, tc := range []struct{ name, username, email, wantPrefix, wantDomain string }{
			{"ordinary", "Sveta", "webmaster@site.ru", "sveta-", "@site.ru.invalid"},
			{"case folds", "SVETA", "webmaster@SITE.RU", "sveta-", "@site.ru.invalid"},
			{"no domain to borrow", "Sveta", "not-an-address", "sveta-", "@phpnuke.invalid"},
			// The illegal characters go, and so does the empty label they leave
			// behind: "evil..site" would not name a host.
			{"hostile domain", "Sveta", "x@ev'il/../site", "sveta-", "@evil.site.invalid"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := distinctInertEmail(tc.username, tc.email)
				if !strings.HasPrefix(got, tc.wantPrefix) {
					t.Errorf("distinctInertEmail() = %q, want it to start %q so an "+
						"operator can still tell whose account it is", got, tc.wantPrefix)
				}
				if !strings.HasSuffix(got, tc.wantDomain) {
					t.Errorf("distinctInertEmail() = %q, want it to end %q", got, tc.wantDomain)
				}
				local, _, _ := strings.Cut(got, "@")
				if len(local) > 64 {
					t.Errorf("local part is %d octets, over the 64 RFC 5321 allows", len(local))
				}
			})
		}
	})

	// Every address must end up somewhere no mail can follow.
	t.Run("undeliverable", func(t *testing.T) {
		for _, username := range []string{"Sveta", "Олег", "", "  ", "x@y"} {
			if got := distinctInertEmail(username, "a@b.ru"); !strings.HasSuffix(got, ".invalid") {
				t.Errorf("distinctInertEmail(%q) = %q, which is deliverable", username, got)
			}
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		for _, username := range []string{"Sveta", "Олег", "john.doe"} {
			first := distinctInertEmail(username, "a@b.ru")
			if again := distinctInertEmail(username, "a@b.ru"); again != first {
				t.Errorf("%q produced %q then %q; a re-run would create a second account",
					username, first, again)
			}
		}
	})

	// The collision that motivated the second round of this fix. util.Slugify
	// is lossy, so a local part built from the slug alone maps several distinct
	// administrators onto one address — collapsing them back into the single
	// shared account the whole mechanism exists to avoid.
	t.Run("distinct usernames never collide", func(t *testing.T) {
		usernames := []string{
			"john.doe", "john-doe", "john_doe", "John Doe", "JOHN.DOE",
			"jóhn.doe", "Олег", "Олёг", "olegiv", "oleg.iv", "oleg-iv",
			strings.Repeat("a", 60) + "-one", strings.Repeat("a", 60) + "-two",
		}
		seen := make(map[string]string, len(usernames))
		for _, username := range usernames {
			address := distinctInertEmail(username, "webmaster@site.ru")
			if owner, clash := seen[address]; clash && authorKey(owner) != authorKey(username) {
				t.Errorf("%q and %q both map to %s, so one loses every byline",
					owner, username, address)
			}
			seen[address] = username
		}
	})
}

// TestSlugGuardChecksTheLanguagePrefixedPath covers a Codex review finding.
//
// A page imported into a non-default language is served under that language's
// prefix, but the guard checked only the bare "/news". The redirects middleware
// is mounted on the root router, ahead of the language-aware frontend router,
// so it sees "/ru/news" whole and answers it before the prefix is ever
// stripped — leaving the imported page at a URL nothing can reach.
func TestSlugGuardChecksTheLanguagePrefixedPath(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "ru", Name: "Russian", IsDefault: false, IsActive: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to create the Russian language: %v", err)
	}
	if _, err := queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: "/ru/news", TargetUrl: "/elsewhere", StatusCode: 301,
		TargetType: "url", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to create the redirect: %v", err)
	}

	for _, tc := range []struct {
		name, langCode string
		wantSlug       string
	}{
		// "/ru/news" is claimed, so the Russian page has to move.
		{"non-default language sees the prefixed redirect", "ru", "news-2"},
		// The default language is served unprefixed, and "/news" is free.
		{"default language keeps the base slug", "en", "news"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &types.ImportResult{}
			NewSource().importStories(ctx, queries,
				[]Story{{ID: 1, Title: ns("News"), BodyText: ns("<p>x</p>")}},
				map[string]int64{}, adminID, tc.langCode, map[int64]int64{},
				map[int64]int64{}, nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)
			if result.PostsImported != 1 {
				t.Fatalf("imported %d posts, want 1; errors %v", result.PostsImported, result.Errors)
			}

			pages, err := queries.ListPages(ctx, store.ListPagesParams{Limit: 20, Offset: 0})
			if err != nil {
				t.Fatalf("failed to list pages: %v", err)
			}
			var got string
			for _, page := range pages {
				if page.LanguageCode == tc.langCode {
					got = page.Slug
				}
			}
			if got != tc.wantSlug {
				t.Errorf("slug = %q, want %q; the page would be answered by the "+
					"redirect rather than served", got, tc.wantSlug)
			}
		})
	}
}

// TestImportedPagePathPrefixFollowsTheDefaultLanguage pins which URL the guard
// above has to check. Only a non-default language is prefixed, so guarding the
// prefixed path for the default one would refuse slugs no redirect can reach.
func TestImportedPagePathPrefixFollowsTheDefaultLanguage(t *testing.T) {
	queries, _ := setupDB(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "ru", Name: "Russian", IsDefault: false, IsActive: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to create the Russian language: %v", err)
	}

	for _, tc := range []struct{ langCode, want string }{
		{"en", ""},
		{"EN", ""},
		{"ru", "/ru"},
		{"", ""},
	} {
		if got := importedPagePathPrefix(ctx, queries, tc.langCode); got != tc.want {
			t.Errorf("importedPagePathPrefix(%q) = %q, want %q", tc.langCode, got, tc.want)
		}
	}
}

// TestThreeAdminsSharingAnEmailStayDistinct covers a Codex review finding, and
// is the case the two-administrator test above cannot reach: the first claimant
// keeps the real address, so a substitute only ever collides with another
// substitute. util.Slugify is lossy, and "john.doe" and "john_doe" both reduce
// to "johndoe" — so the fix for the shared address reintroduced the very merge
// it was written to prevent, one level down.
func TestThreeAdminsSharingAnEmailStayDistinct(t *testing.T) {
	queries, _ := setupDB(t)
	ctx := context.Background()
	names := []string{"alice", "john.doe", "john_doe"}
	admins := make([]User, len(names))
	for i, name := range names {
		admins[i] = User{Username: ns(name), Name: ns(name), Email: ns("webmaster@site.ru")}
	}
	reader := &fakeReader{admins: admins}

	userMap := map[string]int64{}
	result := &types.ImportResult{}
	if err := NewSource().importUsers(ctx, queries, reader, userMap,
		types.ImportOptions{}, result, &mockTracker{}); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}

	seen := make(map[int64]string, len(names))
	for _, name := range names {
		id := userMap[authorKey(name)]
		if id == 0 {
			t.Fatalf("%q was not mapped to an account at all: %v", name, userMap)
		}
		if owner, clash := seen[id]; clash {
			t.Errorf("%q and %q both map to account %d, so one loses every byline",
				owner, name, id)
		}
		seen[id] = name
	}
	if result.UsersImported != len(names) {
		t.Errorf("imported %d accounts, want %d", result.UsersImported, len(names))
	}
}

// TestPlanSkipsStopsOnCanceledContext covers a Codex review finding.
//
// pageExists fails closed — an unreadable destination counts as occupied — so
// a canceled preflight kept probing to the end of the archive and recorded a
// "failed to check" error for every remaining row. The operator got their own
// cancellation back as a hundred import failures.
func TestPlanSkipsStopsOnCanceledContext(t *testing.T) {
	queries, _ := setupDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stories := make([]Story, 300)
	for i := range stories {
		stories[i] = Story{ID: int64(i + 1), Title: ns(fmt.Sprintf("Story %d", i))}
	}
	content := &importContent{stories: stories, encTerms: map[int64][]EncyclopediaTerm{}}
	result := &types.ImportResult{}

	NewSource().planSkips(ctx, queries, content, types.ImportOptions{SkipExisting: true}, result)

	if len(result.Errors) != 0 {
		t.Errorf("a canceled preflight recorded %d errors (first: %q); cancellation "+
			"is not a failure to report", len(result.Errors), result.Errors[0])
	}
	if result.ErrorsOmitted != 0 {
		t.Errorf("%d further errors were omitted, so the probe ran on past cancellation",
			result.ErrorsOmitted)
	}
}

// TestPlanSkipsStillProbesOnALiveContext keeps the guard from turning into a
// blanket early return.
func TestPlanSkipsStillProbesOnALiveContext(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)
	stories := []Story{{ID: 1, Title: ns("News"), BodyText: ns("<p>x</p>")}}
	opts := types.ImportOptions{ImportPosts: true, SkipExisting: true}
	content := &importContent{stories: stories, encTerms: map[int64][]EncyclopediaTerm{}}

	first := &types.ImportResult{}
	NewSource().importStories(ctx, queries, stories, map[string]int64{}, adminID, lang,
		map[int64]int64{}, map[int64]int64{}, nil,
		NewSource().planSkips(ctx, queries, content, opts, first), opts, first, &mockTracker{}, nil)
	if first.PostsImported != 1 {
		t.Fatalf("first run imported %d, want 1", first.PostsImported)
	}

	second := &types.ImportResult{}
	skips := NewSource().planSkips(ctx, queries, content, opts, second)
	if !skips.skipped(types.EntityPost, 1) {
		t.Error("the existing page was not detected, so SkipExisting would duplicate it")
	}
}

// TestTaxonomyOverflowSlugIsReproducible covers a Codex review finding. The
// last-resort slug used a timestamp, so once a base slug's whole suffix family
// was occupied the term could never be found again: the next run probed the
// same hundred candidates, missed, minted a different timestamp and duplicated
// the term on every import.
func TestTaxonomyOverflowSlugIsReproducible(t *testing.T) {
	ctx := context.Background()
	rows := map[string]taxonomyTerm{"news": {ID: 1, Name: "Other 1", Language: "en"}}
	for i := 2; i <= maxTaxonomySlugSuffix; i++ {
		rows["news-"+strconv.Itoa(i)] = taxonomyTerm{
			ID: int64(i), Name: "Other " + strconv.Itoa(i), Language: "en"}
	}
	lookup := func(slug string) (taxonomyTerm, bool, error) {
		term, ok := rows[slug]
		return term, ok, nil
	}

	_, first, err := resolveTaxonomySlug(ctx, "news", "Mine", "en", lookup)
	if err != nil {
		t.Fatalf("resolveTaxonomySlug() error = %v", err)
	}
	if first == "" {
		t.Fatal("no overflow slug was allocated")
	}
	_, again, err := resolveTaxonomySlug(ctx, "news", "Mine", "en", lookup)
	if err != nil {
		t.Fatalf("resolveTaxonomySlug() error = %v", err)
	}
	if again != first {
		t.Errorf("overflow slug was %q then %q; a re-run cannot find its own row",
			first, again)
	}

	// With that row now present, a re-run must recover it rather than allocate.
	rows[first] = taxonomyTerm{ID: 999, Name: "Mine", Language: "en"}
	id, slug, err := resolveTaxonomySlug(ctx, "news", "Mine", "en", lookup)
	if err != nil {
		t.Fatalf("resolveTaxonomySlug() error = %v", err)
	}
	if id != 999 || slug != "" {
		t.Errorf("re-run returned (%d, %q), want the existing row 999", id, slug)
	}

	// Distinct terms must still land on distinct overflow slugs.
	_, mine, _ := resolveTaxonomySlug(ctx, "news", "Mine", "en", lookup)
	_, other, _ := resolveTaxonomySlug(ctx, "news", "Theirs", "en", lookup)
	_, russian, _ := resolveTaxonomySlug(ctx, "news", "Mine", "ru", lookup)
	if other == "" || russian == "" || other == russian {
		t.Errorf("distinct terms shared an overflow slug: %q %q", other, russian)
	}
	_ = mine
}

// TestAbsentOptionalContentTablesDoNotAbortTheImport covers a Codex review
// finding.
//
// Static pages and the encyclopedia were optional PHP-Nuke modules and plenty
// of installs never enabled either. Failing the read was the worst possible
// response: users and taxonomy are already committed by then, so a site that
// simply lacks the table got a half-finished migration out of a configuration
// TestConnection had reported as good.
func TestAbsentOptionalContentTablesDoNotAbortTheImport(t *testing.T) {
	absent := func(table string) error {
		return &mysql.MySQLError{Number: mysqlErrNoSuchTable,
			Message: "Table 'tr_" + table + "' doesn't exist"}
	}

	for _, tc := range []struct {
		name string
		errs map[string]error
	}{
		{"no pages table", map[string]error{"GetStaticPages": absent("pages")}},
		{"no encyclopedia table", map[string]error{"GetEncyclopediaEntries": absent("encyclopedia")}},
		{"no encyclopedia_text table", map[string]error{"GetEncyclopediaTerms": absent("encyclopedia_text")}},
		{"neither module was ever enabled", map[string]error{
			"GetStaticPages":         absent("pages"),
			"GetEncyclopediaEntries": absent("encyclopedia"),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := setupImportDB(t)
			reader := &fakeReader{
				authors:  []User{{ID: 1, Username: ns("a"), Email: ns("a@example.com")}},
				topics:   []Topic{{ID: 1, Text: ns("News")}},
				stories:  []Story{{ID: 1, Title: ns("A story"), BodyText: ns("<p>x</p>")}},
				encTerms: map[int64][]EncyclopediaTerm{},
				errs:     tc.errs,
			}

			result, err := NewSource().importWithReader(context.Background(), db, reader,
				map[string]string{}, types.ImportOptions{
					ImportUsers: true, ImportCategories: true,
					ImportPosts: true, ImportPages: true,
				}, &mockTracker{})
			if err != nil {
				t.Fatalf("an absent optional table aborted the import: %v", err)
			}
			if result.PostsImported != 1 {
				t.Errorf("PostsImported = %d, want 1; the content that does exist "+
					"must still arrive", result.PostsImported)
			}
			if len(result.Errors) != 0 {
				t.Errorf("an absent optional table was reported as an error: %v", result.Errors)
			}
			if len(result.Notices) == 0 {
				t.Error("the absent table was passed over with no notice at all")
			}
		})
	}
}

// TestUnreadableContentTablesStillAbort is the other half: only absence is
// survivable. A dropped connection or a missing SELECT grant produces the same
// empty read as a missing module, so treating every failure as absence would
// silently migrate a fraction of the site.
func TestUnreadableContentTablesStillAbort(t *testing.T) {
	for _, method := range []string{"GetStaticPages", "GetEncyclopediaEntries", "GetEncyclopediaTerms"} {
		t.Run(method, func(t *testing.T) {
			db, _ := setupImportDB(t)
			reader := &fakeReader{
				topics:   []Topic{{ID: 1, Text: ns("News")}},
				stories:  []Story{{ID: 1, Title: ns("A story")}},
				encTerms: map[int64][]EncyclopediaTerm{},
				errs: map[string]error{method: &mysql.MySQLError{
					Number: 1142, Message: "SELECT command denied"}},
			}
			result, err := NewSource().importWithReader(context.Background(), db, reader,
				map[string]string{}, types.ImportOptions{
					ImportCategories: true, ImportPosts: true, ImportPages: true,
				}, &mockTracker{})
			if err == nil {
				t.Error("a permission failure passed as an absent module")
			}
			if result == nil {
				t.Error("the partial result was discarded, losing the record of what was written")
			}
		})
	}
}

// TestPrepareBodyRewritesOnlyAttributes guards the call site, not just the
// helper.
//
// The first version of this test exercised rewriteAssetRefs directly and passed
// happily with prepareBody still calling the global string replacement — which
// is the bug. What has to hold is the property of the body an import actually
// stores, so this goes through the production path.
func TestPrepareBodyRewritesOnlyAttributes(t *testing.T) {
	mediaMap := map[string]string{"images/a.jpg": "/uploads/originals/uuid.jpg"}
	body := `<p><img src="images/a.jpg"> download images/a.jpg.bak from ` +
		`<a href="http://old.example/images/a.jpg">the mirror</a></p>`

	altered := 0
	got := NewSource().prepareBody(body, mediaMap, &altered)

	if !strings.Contains(got, `src="/uploads/originals/uuid.jpg"`) {
		t.Errorf("the imported image was not rewritten: %s", got)
	}
	for _, survivor := range []string{
		"images/a.jpg.bak",                // prose that merely contains the path
		"http://old.example/images/a.jpg", // an off-site URL that contains it too
	} {
		if !strings.Contains(got, survivor) {
			t.Errorf("%q was rewritten; only attribute values naming an imported "+
				"file may change:\n%s", survivor, got)
		}
	}
	if strings.Contains(got, "old.example//uploads") {
		t.Errorf("an external URL was mangled into a double-slash path: %s", got)
	}
}

// TestEveryStageLoopChecksForCancellation is the test that should have existed
// two rounds ago.
//
// A cancellation guard was added to planSkips when a canceled job was found
// reporting a hundred "failed to check" errors instead of stopping. That fix
// was applied to the one loop that had been reported and not to the others, so
// the next review found the same defect in the taxonomy stages: a canceled run
// there produced an error per remaining row, each one a formatting of the same
// cancellation.
//
// Fixing an instance of a pattern is half the work. This walks the package and
// fails on any row loop that talks to the database or to the source without a
// way out, so the class cannot come back through a stage nobody has written yet.
func TestEveryStageLoopChecksForCancellation(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("failed to read package directory: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0

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
			if !ok || fn.Body == nil || fn.Recv == nil {
				continue // only methods on the source drive an import
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				loop, ok := n.(*ast.RangeStmt)
				if !ok || !loopReachesOutward(loop.Body) {
					return true
				}
				checked++
				if !checksCancellation(loop.Body) {
					t.Errorf("%s:%d: %s ranges over rows and calls out to the database "+
						"or the source without checking for cancellation; a canceled job "+
						"runs to the end of the archive, reporting one failure per row",
						name, fset.Position(loop.Pos()).Line, fn.Name.Name)
				}
				return true
			})
		}
	}

	if checked == 0 {
		t.Error("no stage loop was examined; this test is guarding nothing")
	}
	t.Logf("%d stage loops examined", checked)
}

// loopReachesOutward reports whether a loop body calls the destination database
// or the source reader, which is what makes running past a cancellation costly
// rather than merely pointless.
func loopReachesOutward(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if receiver, ok := selector.X.(*ast.Ident); ok {
			switch receiver.Name {
			case "queries", "reader", "s":
				found = true
			}
		}
		return !found
	})
	return found
}

// checksCancellation reports whether a loop body can observe a canceled
// context, by either idiom this package uses.
func checksCancellation(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		selector, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok || receiver.Name != "ctx" {
			return true
		}
		if selector.Sel.Name == "Done" || selector.Sel.Name == "Err" {
			found = true
		}
		return !found
	})
	return found
}

// TestAdminProfileFillsGapsInTheRegisteredRow covers a Codex review finding.
//
// "The users row wins" was applied wholesale, so a matching admin row was
// discarded entirely. A users row with a blank user_email then reached
// importUsers with no address at all, was rejected, and every story that person
// published fell back to the default author — the byline loss that reading the
// authors table was added to prevent.
func TestAdminProfileFillsGapsInTheRegisteredRow(t *testing.T) {
	for _, tc := range []struct {
		name                string
		registered, admin   User
		wantEmail, wantName string
	}{
		{
			name:       "the blank email comes from the admin row",
			registered: User{ID: 1, Username: ns("sveta"), Name: ns("Sveta"), Email: ns("")},
			admin:      User{Username: ns("sveta"), Name: ns("S. Admin"), Email: ns("sveta@site.ru")},
			wantEmail:  "sveta@site.ru",
			wantName:   "Sveta",
		},
		{
			name:       "a whitespace-only email is still blank",
			registered: User{ID: 1, Username: ns("sveta"), Name: ns("Sveta"), Email: ns("   ")},
			admin:      User{Username: ns("sveta"), Email: ns("sveta@site.ru")},
			wantEmail:  "sveta@site.ru",
			wantName:   "Sveta",
		},
		{
			name:       "the registered row still wins where it has a value",
			registered: User{ID: 1, Username: ns("sveta"), Name: ns("Sveta"), Email: ns("sveta@personal.ru")},
			admin:      User{Username: ns("sveta"), Name: ns("Webmaster"), Email: ns("webmaster@site.ru")},
			wantEmail:  "sveta@personal.ru",
			wantName:   "Sveta",
		},
		{
			name:       "a blank display name falls back to the admin row",
			registered: User{ID: 1, Username: ns("sveta"), Name: ns(" "), Email: ns("sveta@personal.ru")},
			admin:      User{Username: ns("SVETA"), Name: ns("Sveta Admin"), Email: ns("webmaster@site.ru")},
			wantEmail:  "sveta@personal.ru",
			wantName:   "Sveta Admin",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries, _ := setupDB(t)
			reader := &fakeReader{
				authors: []User{tc.registered},
				admins:  []User{tc.admin},
			}
			userMap := map[string]int64{}
			result := &types.ImportResult{}
			if err := NewSource().importUsers(context.Background(), queries, reader,
				userMap, types.ImportOptions{}, result, &mockTracker{}); err != nil {
				t.Fatalf("importUsers() error = %v", err)
			}

			id := userMap[authorKey("sveta")]
			if id == 0 {
				t.Fatalf("the credited account was not imported at all; its stories "+
					"would fall back to the default author (notices %v)", result.Notices)
			}
			if result.UsersImported != 1 {
				t.Errorf("UsersImported = %d, want 1", result.UsersImported)
			}
			user, err := queries.GetUserByID(context.Background(), id)
			if err != nil {
				t.Fatalf("failed to read the imported account: %v", err)
			}
			if user.Email != tc.wantEmail {
				t.Errorf("email = %q, want %q", user.Email, tc.wantEmail)
			}
			if user.Name != tc.wantName {
				t.Errorf("name = %q, want %q", user.Name, tc.wantName)
			}
		})
	}
}

// TestTaxonomyStagesStopOnCancellation is the behavioural half of the loop
// guard: a canceled job must report the cancellation once, not once per row.
func TestTaxonomyStagesStopOnCancellation(t *testing.T) {
	topics := make([]Topic, 200)
	storyCats := make([]Category, 200)
	for i := range topics {
		topics[i] = Topic{ID: int64(i + 1), Text: ns("Topic")}
		storyCats[i] = Category{ID: int64(i + 1), Title: ns("Category")}
	}
	pageCats := append([]Category(nil), storyCats...)

	for _, tc := range []struct {
		name string
		run  func(context.Context, *store.Queries, *types.ImportResult) error
	}{
		{"categories", func(ctx context.Context, q *store.Queries, r *types.ImportResult) error {
			return NewSource().importCategories(ctx, q, &fakeReader{topics: topics, pageCats: pageCats},
				"en", map[int64]int64{}, map[int64]int64{}, types.ImportOptions{}, r, &mockTracker{})
		}},
		{"tags", func(ctx context.Context, q *store.Queries, r *types.ImportResult) error {
			return NewSource().importStoryCategoryTags(ctx, q, &fakeReader{storyCats: storyCats},
				"en", map[int64]int64{}, types.ImportOptions{}, r, &mockTracker{})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries, _ := setupDB(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			result := &types.ImportResult{}
			if err := tc.run(ctx, queries, result); !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled returned once by the stage", err)
			}
			if len(result.Errors) != 0 {
				t.Errorf("the canceled stage recorded %d per-row errors (first: %q); "+
					"cancellation is one event, not one per row",
					len(result.Errors), result.Errors[0])
			}
			if result.ErrorsOmitted != 0 {
				t.Errorf("%d further errors were omitted, so the loop ran on past cancellation",
					result.ErrorsOmitted)
			}
		})
	}
}

// recordingRouteChecker owns a fixed set of module paths and remembers every
// path it was asked about, so a test can assert on both.
type recordingRouteChecker struct {
	owned map[string]bool
	asked []string
}

func (c *recordingRouteChecker) OwnsPublicPath(path string) bool {
	c.asked = append(c.asked, path)
	return c.owned[path]
}

// TestSlugGuardChecksModuleRoutesOnBothPaths covers a Codex review finding, and
// finishes a fix from an earlier round.
//
// Redirects and module routes are both mounted on the root router, ahead of the
// language-aware frontend router, so both see "/ru/news" whole. The redirect
// check was widened to cover the prefixed path; the module check two lines above
// it was not, so a module owning the prefixed route still produced an imported
// page at a URL nothing could reach.
func TestSlugGuardChecksModuleRoutesOnBothPaths(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "ru", Name: "Russian", IsDefault: false, IsActive: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to create the Russian language: %v", err)
	}

	checker := &recordingRouteChecker{owned: map[string]bool{"/ru/news": true}}
	source := NewSource()
	source.SetPublicRouteChecker(checker)

	result := &types.ImportResult{}
	source.importStories(ctx, queries,
		[]Story{{ID: 1, Title: ns("News"), BodyText: ns("<p>x</p>")}},
		map[string]int64{}, adminID, "ru", map[int64]int64{}, map[int64]int64{},
		nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)
	if result.PostsImported != 1 {
		t.Fatalf("imported %d posts, want 1; errors %v", result.PostsImported, result.Errors)
	}

	pages, err := queries.ListPages(ctx, store.ListPagesParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("failed to list pages: %v", err)
	}
	for _, page := range pages {
		if page.Slug == "news" {
			t.Errorf("the page took %q while a module owns /ru/news, its only public "+
				"URL; requests would reach the module instead", page.Slug)
		}
	}

	askedPrefixed := false
	for _, path := range checker.asked {
		if path == "/ru/news" {
			askedPrefixed = true
		}
	}
	if !askedPrefixed {
		t.Errorf("the module checker was never asked about the prefixed path; it saw %v",
			checker.asked)
	}
}

// TestDefaultLanguageChecksOnlyTheBarePath keeps the widened guard from turning
// into a blanket prefix check: the default language is served unprefixed, so
// asking about "/en/news" would refuse slugs no route can reach.
func TestDefaultLanguageChecksOnlyTheBarePath(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)

	checker := &recordingRouteChecker{owned: map[string]bool{}}
	source := NewSource()
	source.SetPublicRouteChecker(checker)

	result := &types.ImportResult{}
	source.importStories(ctx, queries,
		[]Story{{ID: 1, Title: ns("News"), BodyText: ns("<p>x</p>")}},
		map[string]int64{}, adminID, lang, map[int64]int64{}, map[int64]int64{},
		nil, nil, types.ImportOptions{}, result, &mockTracker{}, nil)

	for _, path := range checker.asked {
		if strings.HasPrefix(path, "/"+lang+"/") {
			t.Errorf("the default language was checked at the prefixed path %q; it is "+
				"served unprefixed, so that only refuses reachable slugs", path)
		}
	}
}

// TestEncyclopediaSummaryIsAggregated covers a Codex review finding. Summaries
// are deliberately uncapped and are copied into the persisted job and the admin
// response, so one line per encyclopedia grew both with the size of the source.
func TestEncyclopediaSummaryIsAggregated(t *testing.T) {
	queries, adminID := setupDB(t)
	ctx := context.Background()
	lang := defaultLang(t, queries)

	const containers = 250
	content := &importContent{encTerms: map[int64][]EncyclopediaTerm{}}
	for i := 1; i <= containers; i++ {
		content.encEntries = append(content.encEntries, EncyclopediaEntry{
			ID: int64(i), Title: ns("Book " + strconv.Itoa(i)), Active: ni(1),
		})
		content.encTerms[int64(i)] = []EncyclopediaTerm{
			{ID: int64(i), EntryID: ni(int64(i)), Title: ns("Term"), Text: ns("<p>t</p>")},
		}
	}

	result := &types.ImportResult{}
	NewSource().importEncyclopedia(ctx, queries, content, adminID, lang, nil, nil, nil,
		types.ImportOptions{}, result, &mockTracker{}, nil)

	if result.PagesImported != containers {
		t.Fatalf("PagesImported = %d, want %d", result.PagesImported, containers)
	}
	if len(result.Summaries) > 2 {
		t.Errorf("%d summaries for %d encyclopedias; summaries are uncapped and are "+
			"persisted with the job, so they must not scale with the source",
			len(result.Summaries), containers)
	}
	if len(result.Notices) > types.MaxTrackedMessages {
		t.Errorf("%d notices retained, above the %d cap", len(result.Notices),
			types.MaxTrackedMessages)
	}
	joined := strings.Join(result.Summaries, " ")
	if !strings.Contains(joined, strconv.Itoa(containers)) {
		t.Errorf("the aggregate summary does not report the count: %q", joined)
	}
}

// TestNoStageLoopAppendsAnUncappedSummary is the mechanical half.
//
// AddError and AddNotice go through appendCapped; AddSummary does not, by
// design — a summary is an end-of-stage aggregate. That distinction is invisible
// at the call site, so this walks the package and fails on any AddSummary inside
// a row loop, which is what turns an aggregate into an unbounded list.
func TestNoStageLoopAppendsAnUncappedSummary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("failed to read package directory: %v", err)
	}
	fset := token.NewFileSet()

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("failed to parse %s: %v", name, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			loop, ok := n.(*ast.RangeStmt)
			if !ok {
				return true
			}
			ast.Inspect(loop.Body, func(inner ast.Node) bool {
				call, ok := inner.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "AddSummary" {
					return true
				}
				t.Errorf("%s:%d: AddSummary inside a loop; summaries are uncapped and "+
					"are persisted with the job, so a per-row one grows without bound. "+
					"Use AddNotice for per-item detail and one AddSummary after the loop",
					name, fset.Position(call.Pos()).Line)
				return true
			})
			return true
		})
	}
}
