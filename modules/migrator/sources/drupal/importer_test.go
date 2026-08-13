// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// fakeReader is an in-memory sourceReader. It exists so the import stages can
// be driven end to end without a live MySQL server — the stages under test are
// the real ones, only the source database is substituted.
type fakeReader struct {
	schema       Schema
	warnings     []string
	users        []User
	terms        []Term
	files        []File
	mediaUUIDs   map[int64][]string
	nodes        []Node
	nodeImages   map[int64]int64
	nodeTerms    map[int64][]int64
	aliases      []PathAlias
	menuLinks    []MenuLink
	translations int
	err          error
}

func (f *fakeReader) Schema() Schema { return f.schema }

func (f *fakeReader) NodeCount(context.Context) (int, error) { return len(f.nodes), f.err }

func (f *fakeReader) TranslationCount(context.Context) (int, error) { return f.translations, nil }

func (f *fakeReader) GetUsers(context.Context) ([]User, error) { return f.users, f.err }

func (f *fakeReader) GetTerms(context.Context) ([]Term, error) { return f.terms, f.err }

func (f *fakeReader) GetFiles(context.Context) ([]File, error) { return f.files, f.err }

// Warnings satisfies sourceReader; the fake replays whatever was seeded.
func (f *fakeReader) Warnings() []string { return f.warnings }

func (f *fakeReader) MediaUUIDsByFile(context.Context) (map[int64][]string, error) {
	if f.mediaUUIDs == nil {
		return map[int64][]string{}, nil
	}
	return f.mediaUUIDs, nil
}

func (f *fakeReader) GetNodes(_ context.Context, offset int) ([]Node, error) {
	if f.err != nil {
		return nil, f.err
	}
	if offset >= len(f.nodes) {
		return nil, nil
	}
	end := offset + batchSize
	if end > len(f.nodes) {
		end = len(f.nodes)
	}
	return f.nodes[offset:end], nil
}

func (f *fakeReader) NodeImages(context.Context) (map[int64]int64, error) {
	images := f.nodeImages
	if images == nil {
		images = map[int64]int64{}
	}
	return images, nil
}

func (f *fakeReader) NodeTerms(context.Context) (map[int64][]int64, error) {
	if f.nodeTerms == nil {
		return map[int64][]int64{}, nil
	}
	return f.nodeTerms, nil
}

func (f *fakeReader) GetPathAliases(context.Context) ([]PathAlias, error) { return f.aliases, nil }

func (f *fakeReader) GetMenuLinks(context.Context) ([]MenuLink, error) { return f.menuLinks, nil }

// recordingTracker captures tracked items and progress reports.
type recordingTracker struct {
	items    []trackedItem
	progress []types.Progress
}

type trackedItem struct {
	source     string
	entityType string
	entityID   int64
}

func (r *recordingTracker) TrackImportedItem(_ context.Context, source, entityType string, entityID int64) error {
	r.items = append(r.items, trackedItem{source, entityType, entityID})
	return nil
}

func (r *recordingTracker) ReportProgress(_ context.Context, p types.Progress) {
	r.progress = append(r.progress, p)
}

func (r *recordingTracker) countOf(entityType types.EntityType) int {
	n := 0
	for _, item := range r.items {
		if item.entityType == string(entityType) {
			n++
		}
	}
	return n
}

// newTestState builds an import state backed by a real migrated SQLite database
// and a fake source reader.
func newTestState(t *testing.T, reader *fakeReader, opts types.ImportOptions) (*importState, *recordingTracker, *store.Queries) {
	t.Helper()

	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)

	queries := store.New(db)
	ctx := context.Background()

	hash, err := auth.HashPassword("admin-password-123")
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	owner, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "owner@example.com",
		PasswordHash: hash,
		Role:         model.RoleAdmin,
		Name:         "Owner",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create owner: %v", err)
	}

	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	tracker := &recordingTracker{}
	return &importState{
		queries:           queries,
		reader:            reader,
		result:            &types.ImportResult{},
		tracker:           tracker,
		opts:              opts,
		defaultLang:       lang.Code,
		authorID:          owner.ID,
		typeMap:           ParseTypeMap(""),
		tagVocabs:         parseVocabularyList("tags"),
		uploadDir:         t.TempDir(),
		users:             make(map[int64]int64),
		tags:              make(map[int64]int64),
		categories:        make(map[int64]int64),
		createdCategories: make(map[int64]bool),
		mediaByFID:        make(map[int64]int64),
		nodes:             make(map[int64]int64),
		aliasByNode:       make(map[int64]map[string]string),
		refs:              NewMediaRefs(),
	}, tracker, queries
}

func TestImportUsersCreatesPublicAccounts(t *testing.T) {
	reader := &fakeReader{users: []User{
		{UID: 2, Name: "Ada", Mail: "ada@example.com", Created: 1700000000},
		{UID: 3, Name: "", Mail: "grace@example.com"},
	}}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportUsers: true})

	if err := (&Source{}).importUsers(context.Background(), st); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}

	if st.result.UsersImported != 2 {
		t.Errorf("UsersImported = %d, want 2", st.result.UsersImported)
	}
	if tracker.countOf(types.EntityUser) != 2 {
		t.Errorf("tracked %d users, want 2", tracker.countOf(types.EntityUser))
	}

	ada, err := queries.GetUserByEmail(context.Background(), "ada@example.com")
	if err != nil {
		t.Fatalf("imported user not found: %v", err)
	}
	// Drupal roles are deliberately not carried over — importing a foreign
	// system's administrator straight into oCMS admin would be an escalation.
	if ada.Role != model.RolePublic {
		t.Errorf("Role = %q, want %q", ada.Role, model.RolePublic)
	}
	// Drupal's phpass hashes cannot be verified by oCMS's Argon2id verifier, so
	// imported accounts carry a placeholder they can never log in with.
	//
	// This assertion used to be the exact opposite — it required the constant
	// "imported-user-must-reset" to verify against the stored hash, which meant
	// the plaintext was in the source tree and anyone knowing an imported email
	// could authenticate as that user. The login handler applies no
	// forced-reset gate, so that was a live account-takeover path.
	if ok, _ := auth.CheckPassword("imported-user-must-reset", ada.PasswordHash); ok {
		t.Error("the documented placeholder string authenticates against an imported " +
			"account; the placeholder secret must be random and never exposed")
	}
	if ada.PasswordHash == "" {
		t.Error("imported user has no password hash at all")
	}

	// Two users imported in the same run share one hash (hashing per user would
	// add Argon2id's cost per row for no benefit) — but it must be unguessable.
	if grace, err := queries.GetUserByEmail(context.Background(), "grace@example.com"); err == nil {
		if grace.PasswordHash != ada.PasswordHash {
			t.Error("expected one shared placeholder hash per import run")
		}
	}

	grace, err := queries.GetUserByEmail(context.Background(), "grace@example.com")
	if err != nil {
		t.Fatalf("imported user not found: %v", err)
	}
	if grace.Name != "grace@example.com" {
		t.Errorf("a nameless Drupal user should fall back to its email, got %q", grace.Name)
	}

	if st.users[2] != ada.ID {
		t.Errorf("uid 2 mapped to %d, want %d", st.users[2], ada.ID)
	}
}

func TestImportUsersSkipsExistingButStillMaps(t *testing.T) {
	reader := &fakeReader{users: []User{{UID: 5, Name: "Owner", Mail: "owner@example.com"}}}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportUsers: true, SkipExisting: true})

	if err := (&Source{}).importUsers(context.Background(), st); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}

	if st.result.UsersSkipped != 1 {
		t.Errorf("UsersSkipped = %d, want 1", st.result.UsersSkipped)
	}
	if st.result.UsersImported != 0 {
		t.Errorf("UsersImported = %d, want 0", st.result.UsersImported)
	}

	// The mapping must still be recorded, or nodes authored by this Drupal user
	// would silently fall back to the default author.
	owner, err := queries.GetUserByEmail(context.Background(), "owner@example.com")
	if err != nil {
		t.Fatalf("existing user lookup failed: %v", err)
	}
	if st.users[5] != owner.ID {
		t.Errorf("skipped user was not mapped: got %d, want %d", st.users[5], owner.ID)
	}
}

func TestImportUsersDisabledByOption(t *testing.T) {
	reader := &fakeReader{users: []User{{UID: 2, Mail: "ada@example.com"}}}
	st, _, _ := newTestState(t, reader, types.ImportOptions{})

	if err := (&Source{}).importUsers(context.Background(), st); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}
	if st.result.UsersImported != 0 {
		t.Errorf("UsersImported = %d, want 0 when the option is off", st.result.UsersImported)
	}
}

func TestImportTaxonomySplitsTagsAndCategories(t *testing.T) {
	reader := &fakeReader{
		schema: Schema{HasTermData: true, HasTermPar: true},
		terms: []Term{
			{TID: 1, Vocabulary: "tags", Name: "Go"},
			{TID: 2, Vocabulary: "topics", Name: "Engineering", Weight: 5},
			{TID: 3, Vocabulary: "topics", Name: "Backend", ParentTID: 2},
			{TID: 4, Vocabulary: "tags", Name: ""},
		},
	}
	st, tracker, queries := newTestState(t, reader,
		types.ImportOptions{ImportTags: true, ImportCategories: true})

	if err := (&Source{}).importTaxonomy(context.Background(), st); err != nil {
		t.Fatalf("importTaxonomy() error = %v", err)
	}

	if st.result.TagsImported != 1 {
		t.Errorf("TagsImported = %d, want 1", st.result.TagsImported)
	}
	if st.result.CategoriesImported != 2 {
		t.Errorf("CategoriesImported = %d, want 2", st.result.CategoriesImported)
	}
	if tracker.countOf(types.EntityTag) != 1 || tracker.countOf(types.EntityCategory) != 2 {
		t.Errorf("tracked %d tags and %d categories, want 1 and 2",
			tracker.countOf(types.EntityTag), tracker.countOf(types.EntityCategory))
	}

	// The child term is created before its parent link is applied, so this also
	// proves the second pass runs.
	child, err := queries.GetCategoryByID(context.Background(), st.categories[3])
	if err != nil {
		t.Fatalf("child category not found: %v", err)
	}
	if !child.ParentID.Valid || child.ParentID.Int64 != st.categories[2] {
		t.Errorf("child ParentID = %v, want %d", child.ParentID, st.categories[2])
	}

	parent, err := queries.GetCategoryByID(context.Background(), st.categories[2])
	if err != nil {
		t.Fatalf("parent category not found: %v", err)
	}
	if parent.Position != 5 {
		t.Errorf("Position = %d, want the Drupal weight 5", parent.Position)
	}
	if parent.ParentID.Valid {
		t.Error("a root term should have no parent")
	}
}

func TestImportTaxonomyRespectsOptions(t *testing.T) {
	reader := &fakeReader{
		schema: Schema{HasTermData: true},
		terms: []Term{
			{TID: 1, Vocabulary: "tags", Name: "Go"},
			{TID: 2, Vocabulary: "topics", Name: "Engineering"},
		},
	}

	st, _, _ := newTestState(t, reader, types.ImportOptions{ImportTags: true})
	if err := (&Source{}).importTaxonomy(context.Background(), st); err != nil {
		t.Fatalf("importTaxonomy() error = %v", err)
	}
	if st.result.TagsImported != 1 || st.result.CategoriesImported != 0 {
		t.Errorf("with categories off: tags=%d categories=%d, want 1 and 0",
			st.result.TagsImported, st.result.CategoriesImported)
	}
}

func TestImportNodesMapsBundlesStatusAndAuthor(t *testing.T) {
	reader := &fakeReader{
		schema: Schema{HasAliases: true, HasNodeTags: true},
		users:  []User{{UID: 9, Name: "Ada", Mail: "ada@example.com"}},
		nodes: []Node{
			{NID: 1, Type: "article", Title: "Hello World", Status: 1, UID: 9,
				Created: 1700000000, Changed: 1700000500,
				Body:    sql.NullString{String: "<p>Body</p>", Valid: true},
				Summary: sql.NullString{String: "Teaser", Valid: true}},
			{NID: 2, Type: "page", Title: "About Us", Status: 0, UID: 42},
			{NID: 3, Type: "recipe", Title: "Soup", Status: 1},
		},
		aliases: []PathAlias{
			{ID: 1, Path: "/node/1", Alias: "/blog/hello-world"},
			{ID: 2, Path: "/node/2", Alias: "/about-us"},
			{ID: 3, Path: "/taxonomy/term/1", Alias: "/tags/go"},
		},
		nodeTerms: map[int64][]int64{1: {7}},
	}

	st, tracker, queries := newTestState(t, reader,
		types.ImportOptions{ImportUsers: true, ImportPosts: true, ImportPages: true})
	ctx := context.Background()

	if err := (&Source{}).importUsers(ctx, st); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}
	// Pretend taxonomy ran and produced a tag for term 7.
	tag, err := queries.CreateTag(ctx, store.CreateTagParams{
		Name: "Go", Slug: "go", LanguageCode: st.defaultLang,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create tag fixture: %v", err)
	}
	st.tags[7] = tag.ID

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}

	if st.result.PostsImported != 1 {
		t.Errorf("PostsImported = %d, want 1 (article maps to post)", st.result.PostsImported)
	}
	// "page" plus the unmapped "recipe" bundle both land as pages.
	if st.result.PagesImported != 2 {
		t.Errorf("PagesImported = %d, want 2", st.result.PagesImported)
	}

	post, err := queries.GetPageByID(ctx, st.nodes[1])
	if err != nil {
		t.Fatalf("imported post not found: %v", err)
	}
	if post.PageType != pageTypePost {
		t.Errorf("PageType = %q, want %q", post.PageType, pageTypePost)
	}
	if post.Status != model.PageStatusPublished {
		t.Errorf("Status = %q, want %q", post.Status, model.PageStatusPublished)
	}
	// The Drupal alias supplies the slug so existing URLs carry over.
	if post.Slug != "hello-world" {
		t.Errorf("Slug = %q, want %q (derived from the path alias)", post.Slug, "hello-world")
	}
	if post.Summary != "Teaser" {
		t.Errorf("Summary = %q, want %q", post.Summary, "Teaser")
	}
	if post.CreatedAt.Unix() != 1700000000 {
		t.Errorf("CreatedAt = %d, want the Drupal created timestamp", post.CreatedAt.Unix())
	}
	if !post.PublishedAt.Valid {
		t.Error("a published node should carry PublishedAt")
	}
	ada, _ := queries.GetUserByEmail(ctx, "ada@example.com")
	if post.AuthorID != ada.ID {
		t.Errorf("AuthorID = %d, want the mapped Drupal author %d", post.AuthorID, ada.ID)
	}

	unpublished, err := queries.GetPageByID(ctx, st.nodes[2])
	if err != nil {
		t.Fatalf("imported page not found: %v", err)
	}
	if unpublished.Status != model.PageStatusDraft {
		t.Errorf("Status = %q, want %q for an unpublished node", unpublished.Status, model.PageStatusDraft)
	}
	// uid 42 has no oCMS counterpart, so the default author owns it.
	if unpublished.AuthorID != st.authorID {
		t.Errorf("AuthorID = %d, want the default author %d", unpublished.AuthorID, st.authorID)
	}

	// A node without an alias falls back to a slug derived from its title.
	recipe, err := queries.GetPageByID(ctx, st.nodes[3])
	if err != nil {
		t.Fatalf("imported page not found: %v", err)
	}
	if recipe.Slug != "soup" {
		t.Errorf("Slug = %q, want %q (derived from the title)", recipe.Slug, "soup")
	}

	tags, err := queries.GetTagsForPage(ctx, post.ID)
	if err != nil {
		t.Fatalf("failed to read page tags: %v", err)
	}
	if len(tags) != 1 || tags[0].ID != tag.ID {
		t.Errorf("post tags = %v, want the single mapped tag", tags)
	}

	if tracker.countOf(types.EntityPost) != 1 || tracker.countOf(types.EntityPage) != 2 {
		t.Errorf("tracked %d posts and %d pages, want 1 and 2",
			tracker.countOf(types.EntityPost), tracker.countOf(types.EntityPage))
	}
}

func TestImportNodesWritesAliasesForOldURLs(t *testing.T) {
	reader := &fakeReader{
		schema:  Schema{HasAliases: true},
		nodes:   []Node{{NID: 1, Type: "page", Title: "About", Status: 1}},
		aliases: []PathAlias{{ID: 1, Path: "/node/1", Alias: "/company/about"}},
	}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	ctx := context.Background()

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}

	if st.result.AliasesImported != 1 {
		t.Errorf("AliasesImported = %d, want 1", st.result.AliasesImported)
	}
	if tracker.countOf(types.EntityAlias) != 1 {
		t.Errorf("tracked %d aliases, want 1", tracker.countOf(types.EntityAlias))
	}

	aliases, err := queries.GetAliasesForPage(ctx, st.nodes[1])
	if err != nil {
		t.Fatalf("failed to read page aliases: %v", err)
	}
	if len(aliases) != 1 || aliases[0].Alias != "company/about" {
		t.Errorf("aliases = %v, want the multi-segment Drupal path", aliases)
	}
}

// TestImportNodesSkipsRedundantAlias covers the case where the Drupal alias is
// a single segment and therefore already became the page slug — writing it
// again would collide with the globally unique alias index.
func TestImportNodesSkipsRedundantAlias(t *testing.T) {
	reader := &fakeReader{
		schema:  Schema{HasAliases: true},
		nodes:   []Node{{NID: 1, Type: "page", Title: "About", Status: 1}},
		aliases: []PathAlias{{ID: 1, Path: "/node/1", Alias: "/about"}},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	ctx := context.Background()

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}

	page, err := queries.GetPageByID(ctx, st.nodes[1])
	if err != nil {
		t.Fatalf("imported page not found: %v", err)
	}
	if page.Slug != "about" {
		t.Fatalf("Slug = %q, want %q", page.Slug, "about")
	}
	if st.result.AliasesImported != 0 {
		t.Errorf("AliasesImported = %d, want 0 — the alias equals the slug", st.result.AliasesImported)
	}
}

func TestImportNodesSkipExisting(t *testing.T) {
	reader := &fakeReader{nodes: []Node{{NID: 1, Type: "page", Title: "Duplicate", Status: 1}}}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true, SkipExisting: true})
	ctx := context.Background()

	if _, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Duplicate", Slug: "duplicate", Status: model.PageStatusPublished,
		AuthorID: st.authorID, LanguageCode: st.defaultLang, PageType: pageTypePage,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("failed to create existing page: %v", err)
	}

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}
	if st.result.PagesSkipped != 1 || st.result.PagesImported != 0 {
		t.Errorf("skipped=%d imported=%d, want 1 and 0", st.result.PagesSkipped, st.result.PagesImported)
	}
}

func TestImportNodesDeduplicatesSlugs(t *testing.T) {
	reader := &fakeReader{nodes: []Node{
		{NID: 1, Type: "page", Title: "Same Title", Status: 1},
		{NID: 2, Type: "page", Title: "Same Title", Status: 1},
	}}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	ctx := context.Background()

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}
	if st.result.PagesImported != 2 {
		t.Fatalf("PagesImported = %d, want 2", st.result.PagesImported)
	}

	first, _ := queries.GetPageByID(ctx, st.nodes[1])
	second, _ := queries.GetPageByID(ctx, st.nodes[2])
	if first.Slug == second.Slug {
		t.Errorf("both pages got slug %q; the second should have been suffixed", first.Slug)
	}
	if second.Slug != "same-title-2" {
		t.Errorf("second slug = %q, want %q", second.Slug, "same-title-2")
	}
}

// TestImportNodesSanitizesBody proves imported markup goes through the same
// sanitizer as admin-authored content. A Drupal body is arbitrary HTML from a
// foreign system, so this is the boundary that keeps a script tag in a migrated
// article from becoming stored XSS.
func TestImportNodesSanitizesBody(t *testing.T) {
	reader := &fakeReader{nodes: []Node{{
		NID: 1, Type: "page", Title: "Nasty", Status: 1,
		Body: sql.NullString{
			String: `<p>ok</p><script>alert(1)</script><img src=x onerror="alert(2)">`,
			Valid:  true,
		},
	}}}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	ctx := context.Background()

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}

	page, err := queries.GetPageByID(ctx, st.nodes[1])
	if err != nil {
		t.Fatalf("imported page not found: %v", err)
	}
	if strings.Contains(page.Body, "<script") {
		t.Errorf("body retained a script tag: %q", page.Body)
	}
	if strings.Contains(page.Body, "onerror") {
		t.Errorf("body retained an event handler: %q", page.Body)
	}
	if !strings.Contains(page.Body, "<p>ok</p>") {
		t.Errorf("body lost its legitimate markup: %q", page.Body)
	}
}

func TestImportMenusResolvesTargetsAndHierarchy(t *testing.T) {
	reader := &fakeReader{
		schema: Schema{HasMenuLinks: true, HasAliases: false},
		nodes:  []Node{{NID: 1, Type: "page", Title: "About", Status: 1}},
		menuLinks: []MenuLink{
			{ID: 1, UUID: "u1", Title: "Company", MenuName: "main", LinkURI: "internal:/company", Weight: 1, Enabled: 1},
			{ID: 2, UUID: "u2", Title: "About", MenuName: "main", LinkURI: "entity:node/1", Weight: 2, Enabled: 1,
				Parent: sql.NullString{String: "menu_link_content:u1", Valid: true}},
			{ID: 3, UUID: "u3", Title: "External", MenuName: "main", LinkURI: "https://example.com", Weight: 3, Enabled: 0},
			{ID: 4, UUID: "u4", Title: "Broken", MenuName: "main", LinkURI: "route:<front>", Weight: 4, Enabled: 1},
		},
	}
	st, tracker, queries := newTestState(t, reader,
		types.ImportOptions{ImportPages: true, ImportMenus: true})
	ctx := context.Background()

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}
	if err := (&Source{}).importMenus(ctx, st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}

	if st.result.MenusImported != 1 {
		t.Errorf("MenusImported = %d, want 1", st.result.MenusImported)
	}
	// The route: link has no oCMS equivalent and is reported, not guessed at.
	if st.result.MenuItemsImported != 3 {
		t.Errorf("MenuItemsImported = %d, want 3", st.result.MenuItemsImported)
	}
	// A route: link has no oCMS equivalent — that is an expected limitation of
	// the mapping, not a failure, so it must not be reported as an error.
	if st.result.HasErrors() {
		t.Errorf("unsupported link forms must not be errors, got: %v", st.result.Errors)
	}
	if !st.result.HasNotices() {
		t.Error("the unsupported route: link should have been reported as a notice")
	}

	menu, err := queries.GetMenuBySlug(ctx, "main")
	if err != nil {
		t.Fatalf("imported menu not found: %v", err)
	}
	items, err := queries.ListMenuItems(ctx, menu.ID)
	if err != nil {
		t.Fatalf("failed to read menu items: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("menu has %d items, want 3", len(items))
	}

	byTitle := make(map[string]store.MenuItem, len(items))
	for _, item := range items {
		byTitle[item.Title] = item
	}

	about := byTitle["About"]
	if !about.PageID.Valid || about.PageID.Int64 != st.nodes[1] {
		t.Errorf("About PageID = %v, want the imported page %d", about.PageID, st.nodes[1])
	}
	if !about.ParentID.Valid || about.ParentID.Int64 != byTitle["Company"].ID {
		t.Errorf("About ParentID = %v, want the Company item", about.ParentID)
	}

	company := byTitle["Company"]
	if !company.Url.Valid || company.Url.String != "/company" {
		t.Errorf("Company Url = %v, want /company", company.Url)
	}

	external := byTitle["External"]
	if external.IsActive {
		t.Error("a disabled Drupal link should import as inactive")
	}
	if external.Position != 3 {
		t.Errorf("Position = %d, want the Drupal weight 3", external.Position)
	}

	if tracker.countOf(types.EntityMenu) != 1 || tracker.countOf(types.EntityMenuItem) != 3 {
		t.Errorf("tracked %d menus and %d items, want 1 and 3",
			tracker.countOf(types.EntityMenu), tracker.countOf(types.EntityMenuItem))
	}
}

// TestImportMenusReusesExistingMenuWithoutTrackingIt is the guard against
// "delete all imported content" destroying a menu the operator built by hand:
// when the import adds links to a pre-existing menu, only the items it created
// may be tracked.
func TestImportMenusReusesExistingMenuWithoutTrackingIt(t *testing.T) {
	reader := &fakeReader{
		schema: Schema{HasMenuLinks: true},
		menuLinks: []MenuLink{
			{ID: 1, UUID: "u1", Title: "Docs", MenuName: "main", LinkURI: "internal:/docs", Enabled: 1},
		},
	}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	ctx := context.Background()

	if _, err := queries.CreateMenu(ctx, store.CreateMenuParams{
		Name: "Main", Slug: "main", LanguageCode: st.defaultLang,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("failed to create existing menu: %v", err)
	}

	if err := (&Source{}).importMenus(ctx, st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}

	if st.result.MenusImported != 0 || st.result.MenusSkipped != 1 {
		t.Errorf("imported=%d skipped=%d, want 0 and 1",
			st.result.MenusImported, st.result.MenusSkipped)
	}
	if tracker.countOf(types.EntityMenu) != 0 {
		t.Error("a pre-existing menu must never be tracked; deleting the import would destroy it")
	}
	if tracker.countOf(types.EntityMenuItem) != 1 {
		t.Errorf("tracked %d menu items, want 1", tracker.countOf(types.EntityMenuItem))
	}
}

func TestImportMenusDisabledByOption(t *testing.T) {
	reader := &fakeReader{
		schema:    Schema{HasMenuLinks: true},
		menuLinks: []MenuLink{{ID: 1, UUID: "u1", Title: "Docs", MenuName: "main", LinkURI: "internal:/docs", Enabled: 1}},
	}
	st, _, _ := newTestState(t, reader, types.ImportOptions{})

	if err := (&Source{}).importMenus(context.Background(), st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}
	if st.result.MenusImported != 0 {
		t.Errorf("MenusImported = %d, want 0 when the option is off", st.result.MenusImported)
	}
}

func TestImportMediaWithoutFilesPathIsReported(t *testing.T) {
	reader := &fakeReader{schema: Schema{HasFiles: true}, files: []File{{FID: 1, Filename: "a.jpg", URI: "public://a.jpg"}}}
	st, _, _ := newTestState(t, reader, types.ImportOptions{ImportMedia: true})

	if err := (&Source{}).importMedia(context.Background(), st); err != nil {
		t.Fatalf("importMedia() error = %v", err)
	}
	// Not having configured a files path is a choice, not a failure.
	if st.result.HasErrors() {
		t.Errorf("a missing files path must not be an error, got: %v", st.result.Errors)
	}
	if !st.result.HasNotices() {
		t.Error("importing media with no files path configured should be reported as a notice")
	}
	if st.result.MediaImported != 0 {
		t.Errorf("MediaImported = %d, want 0", st.result.MediaImported)
	}
}

// writeTestPNG writes a small real PNG at relPath under root.
func writeTestPNG(t *testing.T, root, relPath string) {
	t.Helper()

	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 200, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	if err := os.WriteFile(full, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestImportMediaCreatesMediaAndRefs is the successful-path coverage importMedia
// never had — the whole stage was only exercised through its "no files path"
// early return, which is why several defects in it went unnoticed at once.
//
// It pins three of them together:
//   - alt text reaches the media row (it was read and then discarded);
//   - ByUUID carries the MEDIA entity uuid as well as the file uuid, without
//     which every <drupal-media> embed on a Drupal 9+ site is deleted;
//   - the emitted URL is percent-encoded, without which the page sanitizer
//     removes the <img> that was just built.
func TestImportMediaCreatesMediaAndRefs(t *testing.T) {
	filesRoot := t.TempDir()
	const relPath = "2026-01/фото с пробелом.png"
	writeTestPNG(t, filesRoot, relPath)

	reader := &fakeReader{
		schema: Schema{HasFiles: true, HasMedia: true},
		files: []File{{
			FID:      7,
			UUID:     "file-uuid-7",
			Filename: "фото с пробелом.png",
			URI:      "public://" + relPath,
			MimeType: "image/png",
			Alt:      sql.NullString{String: "Самолёт Tunisair", Valid: true},
		}},
		mediaUUIDs: map[int64][]string{7: {"media-uuid-42"}},
	}

	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportMedia: true})
	st.filesPath = filesRoot

	if err := (&Source{}).importMedia(context.Background(), st); err != nil {
		t.Fatalf("importMedia() error = %v", err)
	}
	if st.result.HasErrors() {
		t.Fatalf("importMedia reported errors: %v", st.result.Errors)
	}
	if st.result.MediaImported != 1 {
		t.Fatalf("MediaImported = %d, want 1", st.result.MediaImported)
	}

	mediaID, ok := st.mediaByFID[7]
	if !ok {
		t.Fatal("file 7 was not recorded in mediaByFID")
	}
	media, err := queries.GetMediaByID(context.Background(), mediaID)
	if err != nil {
		t.Fatalf("GetMedia: %v", err)
	}
	if media.Alt.String != "Самолёт Tunisair" {
		t.Errorf("media.Alt = %q, want the source alt text", media.Alt.String)
	}
	if media.Width.Int64 == 0 || media.Height.Int64 == 0 {
		t.Errorf("media dimensions = %dx%d, want them recorded",
			media.Width.Int64, media.Height.Int64)
	}

	newURL, ok := st.refs.ByPath[relPath]
	if !ok {
		t.Fatalf("ByPath is missing the raw relative path %q; keys = %v", relPath, st.refs.ByPath)
	}
	if strings.Contains(newURL, " ") {
		t.Errorf("the emitted URL %q contains a literal space; the sanitizer will drop it", newURL)
	}

	if got := st.refs.ByUUID["file-uuid-7"]; got != newURL {
		t.Errorf("ByUUID[file uuid] = %q, want %q", got, newURL)
	}
	if got := st.refs.ByUUID["media-uuid-42"]; got != newURL {
		t.Errorf("ByUUID[media uuid] = %q, want %q — <drupal-media> embeds "+
			"reference the media entity uuid, not the file uuid", got, newURL)
	}

	if !st.refs.IsImg[newURL] {
		t.Error("IsImg was not set for the imported image")
	}
	if st.refs.AltMap[newURL] != "Самолёт Tunisair" {
		t.Errorf("AltMap[%q] = %q, want the source alt text", newURL, st.refs.AltMap[newURL])
	}
}

// TestImportMediaReportsSkippedTypes checks that declining a file leaves a
// trace. A file skipped for its type used to produce no error, no notice, and a
// counter that was never persisted or rendered — so an SVG logo simply failed
// to appear with nothing anywhere to explain why.
func TestImportMediaReportsSkippedTypes(t *testing.T) {
	filesRoot := t.TempDir()

	reader := &fakeReader{
		schema: Schema{HasFiles: true},
		files: []File{
			{FID: 1, Filename: "logo.svg", URI: "public://logo.svg", MimeType: "image/svg+xml"},
			{FID: 2, Filename: "icon.svg", URI: "public://icon.svg", MimeType: "image/svg+xml"},
		},
	}

	st, _, _ := newTestState(t, reader, types.ImportOptions{ImportMedia: true})
	st.filesPath = filesRoot

	if err := (&Source{}).importMedia(context.Background(), st); err != nil {
		t.Fatalf("importMedia() error = %v", err)
	}

	if st.result.MediaSkipped != 2 {
		t.Errorf("MediaSkipped = %d, want 2", st.result.MediaSkipped)
	}
	if !st.result.HasNotices() {
		t.Fatal("skipping files for their type must be reported, got no notices")
	}
	// Aggregated, not one per file: a site with many SVGs would otherwise fill
	// the tracked-message budget and bury the per-file notices.
	//
	// It is recorded as a Summary rather than a Notice because aggregates are
	// emitted after the per-item loops — on a systematically failing import the
	// capped list is already full by then, and the aggregate was the message
	// being dropped.
	if len(st.result.Summaries) != 1 {
		t.Errorf("summaries = %v, want a single aggregated summary", st.result.Summaries)
	}
	if !strings.Contains(st.result.Summaries[0], "image/svg+xml") {
		t.Errorf("summary %q does not name the skipped type", st.result.Summaries[0])
	}
}

func TestImportRespectsContextCancellation(t *testing.T) {
	reader := &fakeReader{users: []User{
		{UID: 2, Mail: "a@example.com"}, {UID: 3, Mail: "b@example.com"},
	}}
	st, _, _ := newTestState(t, reader, types.ImportOptions{ImportUsers: true})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := (&Source{}).importUsers(ctx, st); err == nil {
		t.Error("importUsers() should return the context error when cancelled")
	}
}

func TestReportProgressReachesTracker(t *testing.T) {
	reader := &fakeReader{users: []User{{UID: 2, Mail: "a@example.com"}}}
	st, tracker, _ := newTestState(t, reader, types.ImportOptions{ImportUsers: true})

	if err := (&Source{}).importUsers(context.Background(), st); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}
	if len(tracker.progress) == 0 {
		t.Fatal("the import published no progress samples")
	}
	last := tracker.progress[len(tracker.progress)-1]
	if last.Source != "drupal" {
		t.Errorf("progress source = %q, want %q", last.Source, "drupal")
	}
	if last.Phase != types.EntityUser {
		t.Errorf("progress phase = %q, want %q", last.Phase, types.EntityUser)
	}
	if last.Total != 1 {
		t.Errorf("progress total = %d, want 1", last.Total)
	}
}

// TestExpectedOutcomesAreNoticesNotErrors pins the classification of the
// messages a healthy Drupal import routinely produces.
//
// A real migration reported "3 errors" while having done exactly what it was
// asked to: the site had no body field, no image field, and 19 translations
// that are deliberately out of scope. None of those need the operator to act,
// and calling them errors makes a clean import look broken.
func TestExpectedOutcomesAreNoticesNotErrors(t *testing.T) {
	result := &types.ImportResult{}

	// The two sites that produced the misleading report, driven exactly as
	// Import drives them for an install with no optional tables.
	missing := (Schema{}).MissingOptional()
	for _, table := range missing {
		result.AddNotice("optional table %q not found in source database; related content skipped", table)
	}
	result.AddNotice("%d non-default-language node translations were not imported", 19)

	if result.HasErrors() {
		t.Errorf("expected outcomes were classified as errors: %v", result.Errors)
	}
	if !result.HasNotices() {
		t.Fatal("expected outcomes must still be surfaced to the operator, as notices")
	}
	if len(result.Notices) != len(missing)+1 {
		t.Errorf("got %d notices, want one per missing table (%d) plus the translation count",
			len(result.Notices), len(missing))
	}

	// node__body is the table whose absence originally aborted the node stage;
	// it must now be reported, and reported as a notice.
	var namesBody bool
	for _, msg := range result.Notices {
		if strings.Contains(msg, tableNodeBody) {
			namesBody = true
		}
		if strings.Contains(strings.ToLower(msg), "failed") {
			t.Errorf("notice reads like a failure: %q", msg)
		}
	}
	if !namesBody {
		t.Errorf("notices should name %s: %v", tableNodeBody, result.Notices)
	}
}
