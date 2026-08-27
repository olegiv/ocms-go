// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package phpnuke

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
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

	pageCatsErr error
}

func (f *fakeReader) GetStories(context.Context) ([]Story, error) { return f.stories, nil }
func (f *fakeReader) GetStaticPages(context.Context) ([]StaticPage, error) {
	return f.staticPages, nil
}
func (f *fakeReader) GetTopics(context.Context) ([]Topic, error) { return f.topics, nil }
func (f *fakeReader) GetStoryCategories(context.Context) ([]Category, error) {
	return f.storyCats, nil
}
func (f *fakeReader) GetPageCategories(context.Context) ([]Category, error) {
	return f.pageCats, f.pageCatsErr
}
func (f *fakeReader) GetEncyclopediaEntries(context.Context) ([]EncyclopediaEntry, error) {
	return f.encEntries, nil
}
func (f *fakeReader) GetEncyclopediaTerms(context.Context) (map[int64][]EncyclopediaTerm, error) {
	return f.encTerms, nil
}
func (f *fakeReader) GetStoryAuthors(context.Context) ([]User, error) { return f.authors, nil }

// mockTracker records what an import claimed so the undo path can find it.
type mockTracker struct {
	items []trackedItem
}

type trackedItem struct {
	entityType string
	entityID   int64
}

func (m *mockTracker) TrackImportedItem(_ context.Context, _, entityType string, entityID int64) error {
	m.items = append(m.items, trackedItem{entityType, entityID})
	return nil
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
		map[int64]int64{}, map[int64]int64{}, nil, types.ImportOptions{}, result, tracker)

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
		map[int64]int64{3: category.ID}, map[int64]int64{9: tag.ID}, nil,
		types.ImportOptions{}, result, tracker)

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
		map[int64]int64{}, map[int64]int64{}, nil, types.ImportOptions{}, result, &mockTracker{})

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
		defaultLang(t, queries), map[int64]int64{}, map[int64]int64{}, nil,
		types.ImportOptions{}, result, &mockTracker{})

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
			if got := resolveAuthorID(&tc.story, userMap, 99); got != tc.want {
				t.Errorf("resolveAuthorID() = %d, want %d", got, tc.want)
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
		map[int64]int64{}, map[int64]int64{}, nil, types.ImportOptions{}, result, &mockTracker{})

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
		map[int64]int64{}, nil, types.ImportOptions{}, result, &mockTracker{})

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
		map[int64]int64{}, nil, types.ImportOptions{}, result, &mockTracker{})

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
		nil, types.ImportOptions{}, result, &mockTracker{})

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

func TestImportCategoriesTreatsMissingPageTableAsNotice(t *testing.T) {
	queries, _ := setupDB(t)
	result := &types.ImportResult{}

	reader := &fakeReader{
		topics:      []Topic{{ID: 1, Text: ns("News")}},
		pageCatsErr: errors.New("Table 'nuke_pages_categories' doesn't exist"),
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
		t.Errorf("expected one notice, got %v", result.Notices)
	}
	if result.CategoriesImported != 1 {
		t.Errorf("topics should still import: %d", result.CategoriesImported)
	}
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

	got := uniqueTaxonomySlug(ctx, "news", func(candidate string) (bool, error) {
		return taken[candidate], nil
	})
	if got != "news-3" {
		t.Errorf("uniqueTaxonomySlug() = %q, want %q", got, "news-3")
	}
}

// TestUniqueTaxonomySlugTreatsErrorsAsTaken guards the fail-safe direction: a
// transient database error must never be read as "the slug is free".
func TestUniqueTaxonomySlugTreatsErrorsAsTaken(t *testing.T) {
	got := uniqueTaxonomySlug(context.Background(), "news", func(string) (bool, error) {
		return false, errors.New("database is locked")
	})
	if got != "" {
		t.Errorf("uniqueTaxonomySlug() = %q, want \"\" when the check cannot be trusted", got)
	}
}

func TestUniqueTaxonomySlugStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := uniqueTaxonomySlug(ctx, "news", func(string) (bool, error) { return false, nil }); got != "" {
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
	got := source.prepareBody(body, map[string]string{"a/b.jpg": "/uploads/originals/uuid/b.jpg"})

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
	got := NewSource().prepareBody(`<script>evil()</script><p>ok</p>`, nil)
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

	joined := strings.Join(content.bodies(), "\n")
	for _, want := range []string{"story-home.jpg", "story-body.jpg", "page.jpg", "enc.jpg", "term.jpg"} {
		if !strings.Contains(joined, want) {
			t.Errorf("bodies() omitted %q, so its media would never be imported", want)
		}
	}
}
