// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
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
	mediaUUIDErr error
	nodes        []Node
	nodeImages   map[int64]int64
	nodeTerms    map[int64][]int64
	nodeWarning  string
	aliases      []PathAlias
	aliasCalls   int
	aliasErr     error
	menuLinks    []MenuLink
	translations int
	err          error
}

type closeFailSource struct {
	data []byte
}

type pathOwnership map[string]bool

func (p pathOwnership) OwnsPublicPath(path string) bool { return p[path] }

func (s closeFailSource) Open(string) (sourceFile, error) {
	return &closeFailFile{Reader: bytes.NewReader(s.data)}, nil
}

type closeFailFile struct {
	*bytes.Reader
}

func (*closeFailFile) Close() error { return errors.New("source close failed") }

func (f *fakeReader) Schema() Schema { return f.schema }

func (f *fakeReader) NodeCount(context.Context) (int, error) { return len(f.nodes), f.err }

func (f *fakeReader) TranslationCount(context.Context) (int, error) { return f.translations, nil }

func (f *fakeReader) GetUsers(context.Context) ([]User, error) { return f.users, f.err }

func (f *fakeReader) GetTerms(context.Context) ([]Term, error) { return f.terms, f.err }

func (f *fakeReader) GetFiles(context.Context) ([]File, error) { return f.files, f.err }

func (f *fakeReader) Warnings() []string {
	warnings := f.warnings
	f.warnings = nil
	return warnings
}

func (f *fakeReader) MediaUUIDsByFile(context.Context) (map[int64][]string, error) {
	if f.mediaUUIDErr != nil {
		return nil, f.mediaUUIDErr
	}
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

func (f *fakeReader) GetNodeLanguages(context.Context) (map[int64]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	languages := make(map[int64]string, len(f.nodes))
	for _, node := range f.nodes {
		languages[node.NID] = node.Langcode
	}
	return languages, nil
}

func (f *fakeReader) NodeImages(context.Context) (map[int64]int64, error) {
	images := f.nodeImages
	if images == nil {
		images = map[int64]int64{}
	}
	return images, nil
}

func (f *fakeReader) NodeTerms(context.Context) (map[int64][]int64, error) {
	if f.nodeWarning != "" {
		f.warnings = append(f.warnings, f.nodeWarning)
		f.nodeWarning = ""
	}
	if f.nodeTerms == nil {
		return map[int64][]int64{}, nil
	}
	return f.nodeTerms, nil
}

func (f *fakeReader) GetPathAliases(context.Context) ([]PathAlias, error) {
	f.aliasCalls++
	return f.aliases, f.aliasErr
}

func (f *fakeReader) GetMenuLinks(context.Context) ([]MenuLink, error) { return f.menuLinks, nil }

// recordingTracker captures tracked items and progress reports.
type recordingTracker struct {
	items         []trackedItem
	progress      []types.Progress
	failType      types.EntityType
	trackErr      error
	cancel        context.CancelFunc
	beforeFailure func()
}

type cleanupQueueTracker struct {
	recordingTracker
	queueCtxErr  error
	queueBounded bool
	queuedRoot   string
	queuedUUID   string
}

func (r *cleanupQueueTracker) QueueMediaCleanup(ctx context.Context, _, uploadRoot, mediaUUID string) error {
	r.queueCtxErr = ctx.Err()
	_, r.queueBounded = ctx.Deadline()
	r.queuedRoot = uploadRoot
	r.queuedUUID = mediaUUID
	return nil
}

type trackedItem struct {
	source     string
	entityType string
	entityID   int64
}

func (r *recordingTracker) TrackImportedItem(_ context.Context, source, entityType string, entityID int64) error {
	r.items = append(r.items, trackedItem{source, entityType, entityID})
	if r.trackErr != nil && (r.failType == "" || string(r.failType) == entityType) {
		if r.beforeFailure != nil {
			r.beforeFailure()
		}
		if r.cancel != nil {
			r.cancel()
		}
		return r.trackErr
	}
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
	st, tracker, queries, _ := newTestStateWithDB(t, reader, opts)
	return st, tracker, queries
}

func newTestStateWithDB(t *testing.T, reader *fakeReader, opts types.ImportOptions) (*importState, *recordingTracker, *store.Queries, *sql.DB) {
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
	st := newImportState(queries, reader, &types.ImportResult{}, tracker, opts, lang.Code, owner.ID)
	st.uploadDir = t.TempDir()
	return st, tracker, queries, db
}

func TestImportStagesFlushReaderWarningsWithMediaDisabledAndAfterNodes(t *testing.T) {
	reader := &fakeReader{
		warnings:    []string{"warning before media stage"},
		nodeWarning: "warning discovered while reading node taxonomy fields",
		nodes: []Node{{
			NID: 1, Type: "page", Langcode: "en", Title: "Warning page", Status: 1,
		}},
	}
	st, _, _ := newTestState(t, reader, types.ImportOptions{
		ImportMedia: false,
		ImportPages: true,
	})
	if err := (&Source{}).runImportStages(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"warning before media stage":                            1,
		"warning discovered while reading node taxonomy fields": 1,
	}
	for _, summary := range st.result.Summaries {
		want[summary]--
	}
	for summary, remaining := range want {
		if remaining != 0 {
			t.Fatalf("summary %q count mismatch (%d); all summaries: %v",
				summary, remaining, st.result.Summaries)
		}
	}
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

func TestImportUsersDoesNotTreatLookupFailureAsMissing(t *testing.T) {
	reader := &fakeReader{users: []User{{UID: 2, Name: "Ada", Mail: "ada@example.com"}}}
	st, _, _, db := newTestStateWithDB(t, reader, types.ImportOptions{ImportUsers: true})
	if _, err := db.Exec(`DROP TABLE users`); err != nil {
		t.Fatalf("failed to break the users lookup fixture: %v", err)
	}

	if err := (&Source{}).importUsers(context.Background(), st); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}
	if st.result.UsersImported != 0 || st.users[2] != 0 {
		t.Fatalf("lookup failure created/mapped a user: imported=%d map=%v", st.result.UsersImported, st.users)
	}
	joined := strings.Join(st.result.Errors, "\n")
	if !strings.Contains(joined, "could not check for existing user") {
		t.Errorf("errors = %v, want the operational lookup failure", st.result.Errors)
	}
	if strings.Contains(joined, "failed to create user") {
		t.Errorf("lookup failure was misclassified as a missing row: %v", st.result.Errors)
	}
}

func TestImportUsersRollsBackTrackingFailure(t *testing.T) {
	reader := &fakeReader{users: []User{{UID: 2, Name: "Ada", Mail: "ada@example.com"}}}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportUsers: true})
	tracker.failType = types.EntityUser
	tracker.trackErr = errors.New("tracking database unavailable")

	if err := (&Source{}).importUsers(context.Background(), st); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}
	if _, err := queries.GetUserByEmail(context.Background(), "ada@example.com"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("untracked user survived rollback: %v", err)
	}
	if st.result.UsersImported != 0 || st.users[2] != 0 {
		t.Errorf("tracking failure published state: imported=%d map=%v", st.result.UsersImported, st.users)
	}
}

func TestTrackingRollbackSurvivesCanceledImportContext(t *testing.T) {
	reader := &fakeReader{users: []User{{UID: 2, Name: "Ada", Mail: "ada@example.com"}}}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportUsers: true})
	ctx, cancel := context.WithCancel(context.Background())
	tracker.failType = types.EntityUser
	tracker.trackErr = errors.New("tracking canceled")
	tracker.cancel = cancel

	if err := (&Source{}).importUsers(ctx, st); err != nil {
		t.Fatalf("importUsers() error = %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("tracker did not cancel the import context")
	}
	if _, err := queries.GetUserByEmail(context.Background(), "ada@example.com"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("rollback reused the canceled context; untracked user survived: %v", err)
	}
	if got := strings.Join(st.result.Errors, "\n"); strings.Contains(got, "failed to roll back") {
		t.Errorf("independent rollback failed after import cancellation: %v", st.result.Errors)
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

func TestImportTaxonomyMapsLanguagesAndKeepsTermsDistinct(t *testing.T) {
	reader := &fakeReader{terms: []Term{
		{TID: 1, Vocabulary: "tags", Langcode: "en", Name: "Go"},
		{TID: 2, Vocabulary: "tags", Langcode: "en", Name: "Go"},
		{TID: 3, Vocabulary: "tags", Langcode: "fr", Name: "Bonjour"},
		{TID: 4, Vocabulary: "tags", Langcode: "en", Name: "Only FR"},
		{TID: 5, Vocabulary: "topics", Langcode: "tlh", Name: "Engineering"},
		{TID: 6, Vocabulary: "topics", Langcode: "tlh", Name: "Engineering"},
		{TID: 7, Vocabulary: "tags", Langcode: "und", Name: "Neutral"},
		{TID: 8, Vocabulary: "tags", Langcode: "fr", Name: "Existing French"},
	}}
	st, _, queries := newTestState(t, reader,
		types.ImportOptions{ImportTags: true, ImportCategories: true})
	ctx := context.Background()
	now := time.Now()
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr",
		IsActive: true, Position: 2, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateLanguage(fr): %v", err)
	}
	st.availableLangs["fr"] = true
	preexisting, err := queries.CreateTag(ctx, store.CreateTagParams{
		Name: "Go", Slug: "go", LanguageCode: st.defaultLang, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTag(go): %v", err)
	}
	if _, err := queries.CreateTag(ctx, store.CreateTagParams{
		Name: "Only FR", Slug: "only-fr", LanguageCode: "fr", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTag(only-fr): %v", err)
	}
	existingFrench, err := queries.CreateTag(ctx, store.CreateTagParams{
		Name: "Existing French", Slug: "existing-french", LanguageCode: "fr", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTag(existing-french): %v", err)
	}

	if err := (&Source{}).importTaxonomy(ctx, st); err != nil {
		t.Fatalf("importTaxonomy() error = %v", err)
	}
	if st.tags[1] != preexisting.ID {
		t.Errorf("first Go term did not reuse the same-language row: got %d want %d", st.tags[1], preexisting.ID)
	}
	go2, err := queries.GetTagBySlug(ctx, "go-2")
	if err != nil || st.tags[2] != go2.ID {
		t.Errorf("second Go term was merged instead of getting go-2: tag=%+v err=%v map=%v", go2, err, st.tags)
	}
	bonjour, err := queries.GetTagBySlug(ctx, "bonjour")
	if err != nil || bonjour.LanguageCode != "fr" {
		t.Errorf("French term = %+v, err=%v; want language fr", bonjour, err)
	}
	if st.termURLs[3] != "/fr/tag/bonjour" {
		t.Errorf("French term URL = %q, want /fr/tag/bonjour", st.termURLs[3])
	}
	if st.tags[8] != existingFrench.ID || st.termURLs[8] != "/fr/tag/existing-french" {
		t.Errorf("reused French term = id %d URL %q, want id %d URL /fr/tag/existing-french",
			st.tags[8], st.termURLs[8], existingFrench.ID)
	}
	onlyFR2, err := queries.GetTagBySlug(ctx, "only-fr-2")
	if err != nil || onlyFR2.LanguageCode != st.defaultLang {
		t.Errorf("cross-language global slug conflict = %+v, err=%v; want suffixed default-language tag", onlyFR2, err)
	}
	engineering, _ := queries.GetCategoryByID(ctx, st.categories[5])
	engineering2, _ := queries.GetCategoryByID(ctx, st.categories[6])
	if engineering.Slug != "engineering" || engineering2.Slug != "engineering-2" ||
		engineering.LanguageCode != st.defaultLang || engineering2.LanguageCode != st.defaultLang {
		t.Errorf("categories were merged or mislabelled: first=%+v second=%+v", engineering, engineering2)
	}
	if got := strings.Join(st.result.Summaries, "\n"); !strings.Contains(got, "2 taxonomy term(s)") || !strings.Contains(got, "tlh") {
		t.Errorf("summaries = %v, want aggregated tlh fallback report", st.result.Summaries)
	}
	if got := strings.Join(st.result.Summaries, "\n"); !strings.Contains(got, "language-neutral code \"und\"") {
		t.Errorf("summaries = %v, want a distinct neutral-language fallback report", st.result.Summaries)
	}
}

func TestLegacyActiveUnroutableLanguagesFallBack(t *testing.T) {
	reader := &fakeReader{
		terms: []Term{
			{TID: 1, Vocabulary: "tags", Langcode: "admin", Name: "Reserved term"},
			{TID: 2, Vocabulary: "tags", Langcode: "x", Name: "Invalid term"},
		},
		nodes: []Node{
			{NID: 1, Type: "page", Title: "Reserved node", Status: 1, Langcode: "admin"},
			{NID: 2, Type: "page", Title: "Invalid node", Status: 1, Langcode: "x"},
		},
	}
	st, _, queries := newTestState(t, reader,
		types.ImportOptions{ImportTags: true, ImportPages: true})
	ctx := context.Background()
	now := time.Now()
	for i, code := range []string{"admin", "x"} {
		if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
			Code: code, Name: code, NativeName: code, Direction: "ltr",
			IsActive: true, Position: int64(i + 2), CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("CreateLanguage(%q): %v", code, err)
		}
	}
	langs, err := queries.ListActiveLanguages(ctx)
	if err != nil {
		t.Fatalf("ListActiveLanguages: %v", err)
	}
	for _, lang := range langs {
		st.addAvailableLanguage(lang.Code)
	}
	if st.availableLangs["admin"] || st.availableLangs["x"] {
		t.Fatalf("unroutable legacy languages entered availableLangs: %v", st.availableLangs)
	}

	source := NewSource()
	if err := source.importTaxonomy(ctx, st); err != nil {
		t.Fatalf("importTaxonomy: %v", err)
	}
	if err := source.importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes: %v", err)
	}

	for _, tid := range []int64{1, 2} {
		tag, err := queries.GetTagByID(ctx, st.tags[tid])
		if err != nil {
			t.Fatalf("GetTagByID(term %d): %v", tid, err)
		}
		if tag.LanguageCode != st.defaultLang {
			t.Errorf("term %d language = %q, want fallback %q", tid, tag.LanguageCode, st.defaultLang)
		}
		wantURL := "/tag/" + tag.Slug
		if st.termURLs[tid] != wantURL {
			t.Errorf("term %d URL = %q, want routable fallback URL %q", tid, st.termURLs[tid], wantURL)
		}
	}
	for _, nid := range []int64{1, 2} {
		page, err := queries.GetPageByID(ctx, st.nodes[nid])
		if err != nil {
			t.Fatalf("GetPageByID(node %d): %v", nid, err)
		}
		if page.LanguageCode != st.defaultLang {
			t.Errorf("node %d language = %q, want fallback %q", nid, page.LanguageCode, st.defaultLang)
		}
		if strings.HasPrefix("/"+page.Slug, "/admin/") || strings.HasPrefix("/"+page.Slug, "/x/") {
			t.Errorf("node %d received unroutable prefixed URL /%s", nid, page.Slug)
		}
	}

	summaries := strings.Join(st.result.Summaries, "\n")
	for _, code := range []string{"admin", "x"} {
		if !strings.Contains(summaries, code) {
			t.Errorf("summaries = %v, want fallback disclosure for %q", st.result.Summaries, code)
		}
	}
}

func TestLegacyUnroutableDefaultHasNoPublicTaxonomyURL(t *testing.T) {
	for _, code := range []string{"admin", "x"} {
		t.Run(code, func(t *testing.T) {
			st := newImportState(nil, nil, &types.ImportResult{}, nil,
				types.ImportOptions{}, code, 0)
			if st.availableLangs[code] {
				t.Fatalf("legacy default %q was exposed as an available URL prefix", code)
			}
			if got := st.languageForCode("unknown", st.unmappedLangs); got != code {
				t.Errorf("fallback language = %q, want legacy configured default %q", got, code)
			}
			if got := taxonomyURL(code, code, "tag", "go"); got != "" {
				t.Errorf("taxonomyURL() = %q, want unsafe legacy default ignored", got)
			}
		})
	}
}

func TestImportTaxonomyAliasesBecomeTrackedRedirects(t *testing.T) {
	reader := &fakeReader{
		terms:   []Term{{TID: 7, Vocabulary: "tags", Langcode: "en", Name: "Go"}},
		aliases: []PathAlias{{ID: 1, Path: "/taxonomy/term/7", Alias: "/topics/go", Langcode: "en"}},
	}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportTags: true})
	if err := (&Source{}).importTaxonomy(context.Background(), st); err != nil {
		t.Fatalf("importTaxonomy() error = %v", err)
	}
	redirect, err := queries.GetRedirectBySourcePath(context.Background(), "/topics/go")
	if err != nil {
		t.Fatalf("taxonomy redirect not found: %v", err)
	}
	if redirect.TargetUrl != "/tag/go" || redirect.StatusCode != 301 || !redirect.Enabled {
		t.Errorf("redirect = %+v, want enabled 301 to /tag/go", redirect)
	}
	if st.result.RedirectsImported != 1 || tracker.countOf(types.EntityRedirect) != 1 {
		t.Errorf("redirect counters/tracking = %d/%d, want 1/1", st.result.RedirectsImported, tracker.countOf(types.EntityRedirect))
	}
}

func TestImportTaxonomyAliasesUseDestinationLanguagePrefix(t *testing.T) {
	reader := &fakeReader{
		terms: []Term{
			{TID: 7, Vocabulary: "tags", Langcode: "en", Name: "Current"},
			{TID: 8, Vocabulary: "tags", Langcode: "fr", Name: "Actuel"},
		},
		aliases: []PathAlias{
			{ID: 1, Path: "/taxonomy/term/7", Alias: "/topics/current", Langcode: "en"},
			{ID: 2, Path: "/taxonomy/term/8", Alias: "/topics/current", Langcode: "fr"},
		},
	}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportTags: true})
	ctx := context.Background()
	now := time.Now()
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr",
		IsActive: true, Position: 2, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateLanguage(fr): %v", err)
	}
	st.availableLangs["fr"] = true

	if err := (&Source{}).importTaxonomy(ctx, st); err != nil {
		t.Fatalf("importTaxonomy() error = %v", err)
	}

	for sourcePath, targetURL := range map[string]string{
		"/topics/current":    "/tag/current",
		"/fr/topics/current": "/fr/tag/actuel",
	} {
		redirect, err := queries.GetRedirectBySourcePath(ctx, sourcePath)
		if err != nil {
			t.Errorf("GetRedirectBySourcePath(%q): %v", sourcePath, err)
			continue
		}
		if redirect.TargetUrl != targetURL {
			t.Errorf("redirect %q target = %q, want %q", sourcePath, redirect.TargetUrl, targetURL)
		}
	}
	if st.result.RedirectsImported != 2 || tracker.countOf(types.EntityRedirect) != 2 {
		t.Errorf("redirect counters/tracking = %d/%d, want 2/2",
			st.result.RedirectsImported, tracker.countOf(types.EntityRedirect))
	}
}

func TestTaxonomyAliasReservationPrecedesPageSlug(t *testing.T) {
	reader := &fakeReader{
		terms: []Term{{TID: 7, Vocabulary: "tags", Langcode: "en", Name: "Updates"}},
		nodes: []Node{{NID: 1, Type: "page", Title: "News", Status: 1, Langcode: "en"}},
		aliases: []PathAlias{
			{ID: 1, Path: "/taxonomy/term/7", Alias: "/news", Langcode: "en"},
		},
	}
	st, _, queries := newTestState(t, reader,
		types.ImportOptions{ImportTags: true, ImportPages: true})
	ctx := context.Background()
	source := NewSource()

	if err := source.importTaxonomy(ctx, st); err != nil {
		t.Fatalf("importTaxonomy: %v", err)
	}
	if err := source.importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes: %v", err)
	}

	redirect, err := queries.GetRedirectBySourcePath(ctx, "/news")
	if err != nil || redirect.TargetUrl != "/tag/updates" {
		t.Fatalf("taxonomy redirect = %+v, err=%v; want /news -> /tag/updates", redirect, err)
	}
	page, err := queries.GetPageByID(ctx, st.nodes[1])
	if err != nil {
		t.Fatalf("GetPageByID: %v", err)
	}
	if page.Slug != "news-2" {
		t.Errorf("page slug = %q, want news-2 because taxonomy owns /news", page.Slug)
	}
}

func TestNodeAliasesPrecedeTaxonomyRedirectsRegardlessOfRowOrder(t *testing.T) {
	reader := &fakeReader{
		terms: []Term{
			{TID: 7, Vocabulary: "tags", Langcode: "en", Name: "Term One"},
			{TID: 8, Vocabulary: "tags", Langcode: "en", Name: "Term Two"},
		},
		nodes: []Node{
			{NID: 1, Type: "page", Title: "Foo", Status: 1, Langcode: "en"},
			{NID: 2, Type: "page", Title: "Archive", Status: 1, Langcode: "en"},
		},
		aliases: []PathAlias{
			// Taxonomy rows deliberately come first. Node ownership must not
			// depend on the source ID order.
			{ID: 1, Path: "/taxonomy/term/7", Alias: "/foo", Langcode: "en"},
			{ID: 2, Path: "/taxonomy/term/8", Alias: "/news/archive", Langcode: "en"},
			{ID: 3, Path: "/node/1", Alias: "/foo", Langcode: "en"},
			{ID: 4, Path: "/node/2", Alias: "/news/archive", Langcode: "en"},
		},
	}
	st, _, queries := newTestState(t, reader,
		types.ImportOptions{ImportTags: true, ImportPages: true})
	ctx := context.Background()
	source := NewSource()

	if err := source.importTaxonomy(ctx, st); err != nil {
		t.Fatalf("importTaxonomy: %v", err)
	}
	for _, path := range []string{"/foo", "/news/archive"} {
		if _, err := queries.GetRedirectBySourcePath(ctx, path); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("taxonomy redirect %q shadowed a source node alias: %v", path, err)
		}
	}

	if err := source.importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes: %v", err)
	}
	page, err := queries.GetPublishedPageByAlias(ctx, "news/archive")
	if err != nil || page.ID != st.nodes[2] {
		t.Fatalf("multi-segment node alias owner = %+v, err=%v", page, err)
	}
}

func TestImportTaxonomyRedirectRollsBackTrackingFailure(t *testing.T) {
	reader := &fakeReader{
		terms:   []Term{{TID: 7, Vocabulary: "tags", Langcode: "en", Name: "Go"}},
		aliases: []PathAlias{{ID: 1, Path: "/taxonomy/term/7", Alias: "/topics/go", Langcode: "en"}},
	}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportTags: true})
	tracker.failType = types.EntityRedirect
	tracker.trackErr = errors.New("tracking failed")
	if err := (&Source{}).importTaxonomy(context.Background(), st); err != nil {
		t.Fatalf("importTaxonomy() error = %v", err)
	}
	if _, err := queries.GetRedirectBySourcePath(context.Background(), "/topics/go"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("untracked redirect survived rollback: %v", err)
	}
	if st.result.RedirectsImported != 0 {
		t.Errorf("RedirectsImported = %d, want 0", st.result.RedirectsImported)
	}
}

func TestTaxonomyRedirectRefusesPagesAliasesAndReservedRoutes(t *testing.T) {
	st, _, queries := newTestState(t, &fakeReader{}, types.ImportOptions{})
	ctx := context.Background()
	now := time.Now()
	page, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Owned", Slug: "owned", Status: model.PageStatusPublished,
		AuthorID: st.authorID, LanguageCode: st.defaultLang, PageType: pageTypePage,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if _, err := queries.CreatePageAlias(ctx, store.CreatePageAliasParams{
		PageID: page.ID, Alias: "legacy", CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreatePageAlias: %v", err)
	}

	s := &Source{}
	for _, source := range []string{
		"/owned", "/legacy", "/admin/config", "/en", "/en/tag/go", "/en/existing-page",
		"/sitemap.xml", "/robots.txt", "/.well-known/security.txt",
	} {
		s.createTaxonomyRedirect(ctx, st, source, "/tag/go", now)
		if _, err := queries.GetRedirectBySourcePath(ctx, source); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("conflicting redirect %q was created: %v", source, err)
		}
	}
	if st.result.RedirectsImported != 0 || len(st.result.Notices) != 9 {
		t.Errorf("redirect result = %+v, want nine conflict notices and no imports", st.result)
	}
}

func TestTaxonomyRedirectRefusesRegisteredModuleRoutes(t *testing.T) {
	st, _, queries := newTestState(t, &fakeReader{}, types.ImportOptions{})
	ctx := context.Background()
	now := time.Now()
	source := &Source{publicRouteChecker: pathOwnership{
		"/bookmarks":                        true,
		"/example":                          true,
		"/analytics/read":                   true,
		"/embed/dify/messages/id/suggested": true,
	}}

	for _, path := range []string{
		"/bookmarks", "/example", "/analytics/read",
		"/embed/dify/messages/id/suggested",
	} {
		source.createTaxonomyRedirect(ctx, st, path, "/tag/go", now)
		if _, err := queries.GetRedirectBySourcePath(ctx, path); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("module route %q was shadowed by redirect: %v", path, err)
		}
	}

	source.createTaxonomyRedirect(ctx, st, "/topics/go", "/tag/go", now)
	if _, err := queries.GetRedirectBySourcePath(ctx, "/topics/go"); err != nil {
		t.Fatalf("ordinary taxonomy path was not imported: %v", err)
	}
	if st.result.RedirectsImported != 1 {
		t.Errorf("RedirectsImported = %d, want 1", st.result.RedirectsImported)
	}
}

func TestTaxonomyRedirectRefusesExistingWildcardMatch(t *testing.T) {
	st, _, queries := newTestState(t, &fakeReader{}, types.ImportOptions{})
	ctx := context.Background()
	now := time.Now()
	if _, err := queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: "/topics/**", TargetUrl: "/archive/**", StatusCode: 301,
		IsWildcard: true, TargetType: model.TargetSelf, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateRedirect(wildcard): %v", err)
	}

	source := NewSource()
	source.createTaxonomyRedirect(ctx, st, "/topics/news", "/tag/news", now)
	if _, err := queries.GetRedirectBySourcePath(ctx, "/topics/news"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("redirect overlapping /topics/** was created: %v", err)
	}
	source.createTaxonomyRedirect(ctx, st, "/archive/news", "/tag/news", now)
	if _, err := queries.GetRedirectBySourcePath(ctx, "/archive/news"); err != nil {
		t.Fatalf("non-overlapping redirect was not created: %v", err)
	}
}

func TestTaxonomyRedirectRefusesDraftPageAliasesInEachLanguageNamespace(t *testing.T) {
	st, _, queries := newTestState(t, &fakeReader{}, types.ImportOptions{})
	ctx := context.Background()
	now := time.Now()
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateLanguage(fr): %v", err)
	}
	st.availableLangs["fr"] = true

	for _, tc := range []struct {
		language, slug, alias, sourcePath string
	}{
		{language: "en", slug: "draft-en", alias: "topics-en", sourcePath: "/topics-en"},
		{language: "fr", slug: "draft-fr", alias: "topics-fr", sourcePath: "/fr/topics-fr"},
	} {
		page, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title: "Draft " + tc.language, Slug: tc.slug, Status: "draft", AuthorID: st.authorID,
			LanguageCode: tc.language, PageType: "page", CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreatePage(%s): %v", tc.language, err)
		}
		if _, err := queries.CreatePageAlias(ctx, store.CreatePageAliasParams{
			PageID: page.ID, Alias: tc.alias, CreatedAt: now,
		}); err != nil {
			t.Fatalf("CreatePageAlias(%s): %v", tc.language, err)
		}
		(&Source{}).createTaxonomyRedirect(ctx, st, tc.sourcePath, "/tag/imported", now)
		if _, err := queries.GetRedirectBySourcePath(ctx, tc.sourcePath); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("draft %s alias %q was shadowed by a redirect: %v", tc.language, tc.sourcePath, err)
		}
	}
	if len(st.result.Notices) != 2 {
		t.Fatalf("conflict notices = %v; want one for each draft alias", st.result.Notices)
	}
}

func TestNodeSlugAllocationRefusesExactAndWildcardRedirectOwnership(t *testing.T) {
	st, _, queries := newTestState(t, &fakeReader{}, types.ImportOptions{})
	ctx := context.Background()
	now := time.Now()
	source := &Source{}

	source.createTaxonomyRedirect(ctx, st, "/foo", "/tag/foo", now)
	if got := source.uniqueNodeSlug(ctx, st, "foo", 1, st.defaultLang); got != "foo-2" {
		t.Fatalf("node slug with taxonomy redirect = %q; want foo-2", got)
	}
	if _, err := queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: "/legacy*", TargetUrl: "/archive", StatusCode: 301,
		IsWildcard: true, TargetType: model.TargetSelf, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateRedirect(wildcard): %v", err)
	}
	if got := source.uniqueNodeSlug(ctx, st, "legacy", 2, st.defaultLang); got != "imported-2" {
		t.Fatalf("node slug with wildcard redirect = %q; want imported-2", got)
	}
}

func TestTaxonomyRedirectNeverOverwritesExistingRedirect(t *testing.T) {
	st, _, queries := newTestState(t, &fakeReader{}, types.ImportOptions{})
	ctx := context.Background()
	now := time.Now()
	existing, err := queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: "/topics/go", TargetUrl: "/old-target", StatusCode: 302,
		TargetType: model.TargetSelf, Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRedirect: %v", err)
	}
	(&Source{}).createTaxonomyRedirect(ctx, st, "/topics/go", "/tag/go", now)
	after, err := queries.GetRedirectBySourcePath(ctx, "/topics/go")
	if err != nil {
		t.Fatalf("GetRedirectBySourcePath: %v", err)
	}
	if after.ID != existing.ID || after.TargetUrl != "/old-target" || after.StatusCode != 302 {
		t.Errorf("existing redirect was overwritten: before=%+v after=%+v", existing, after)
	}
	if st.result.RedirectsImported != 0 || len(st.result.Notices) != 1 {
		t.Errorf("redirect conflict result = %+v, want one notice and no import", st.result)
	}

	identical, err := queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: "/topics/rust", TargetUrl: "/tag/rust", StatusCode: 301,
		TargetType: model.TargetSelf, Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRedirect(identical): %v", err)
	}
	(&Source{}).createTaxonomyRedirect(ctx, st, identical.SourcePath, identical.TargetUrl, now)
	if st.result.RedirectsSkipped != 1 {
		t.Errorf("RedirectsSkipped = %d, want 1 for an identical existing redirect", st.result.RedirectsSkipped)
	}
}

func TestImportTaxonomyRollsBackTrackingFailure(t *testing.T) {
	reader := &fakeReader{terms: []Term{{TID: 1, Vocabulary: "tags", Name: "Go"}}}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportTags: true})
	tracker.failType = types.EntityTag
	tracker.trackErr = errors.New("tracking failed")
	if err := (&Source{}).importTaxonomy(context.Background(), st); err != nil {
		t.Fatalf("importTaxonomy() error = %v", err)
	}
	if _, err := queries.GetTagBySlug(context.Background(), "go"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("untracked tag survived rollback: %v", err)
	}
	if st.result.TagsImported != 0 || st.tags[1] != 0 || st.termURLs[1] != "" {
		t.Errorf("tracking failure published taxonomy state: result=%+v tags=%v urls=%v", st.result, st.tags, st.termURLs)
	}
}

func TestImportCategoryRollsBackTrackingFailure(t *testing.T) {
	reader := &fakeReader{terms: []Term{{TID: 1, Vocabulary: "topics", Name: "Engineering"}}}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportCategories: true})
	tracker.failType = types.EntityCategory
	tracker.trackErr = errors.New("tracking failed")
	if err := (&Source{}).importTaxonomy(context.Background(), st); err != nil {
		t.Fatalf("importTaxonomy() error = %v", err)
	}
	if _, err := queries.GetCategoryBySlug(context.Background(), "engineering"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("untracked category survived rollback: %v", err)
	}
	if st.result.CategoriesImported != 0 || st.categories[1] != 0 || st.termURLs[1] != "" {
		t.Errorf("tracking failure published category state: result=%+v categories=%v urls=%v", st.result, st.categories, st.termURLs)
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

	// Two aliases: the Drupal path alias, and the canonical node path.
	if st.result.AliasesImported != 2 {
		t.Errorf("AliasesImported = %d, want 2", st.result.AliasesImported)
	}
	if tracker.countOf(types.EntityAlias) != 2 {
		t.Errorf("tracked %d aliases, want 2", tracker.countOf(types.EntityAlias))
	}

	aliases, err := queries.GetAliasesForPage(ctx, st.nodes[1])
	if err != nil {
		t.Fatalf("failed to read page aliases: %v", err)
	}
	got := make(map[string]bool, len(aliases))
	for _, a := range aliases {
		got[a.Alias] = true
	}
	if !got["company/about"] {
		t.Errorf("aliases = %v, want the multi-segment Drupal path", aliases)
	}
	if !got["node/1"] {
		t.Errorf("aliases = %v, want the canonical Drupal node path: bodies and menus "+
			"link to /node/N, and oCMS has no such route", aliases)
	}
}

// TestImportNodesAliasesCanonicalNodePath covers a site with no path aliases at
// all. Drupal bodies still link to /node/N, so those links 404 after migration
// unless the canonical path is registered as an alias in its own right.
func TestImportNodesAliasesCanonicalNodePath(t *testing.T) {
	reader := &fakeReader{
		nodes: []Node{{NID: 42, Type: "page", Title: "About", Status: 1}},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	ctx := context.Background()

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}

	aliases, err := queries.GetAliasesForPage(ctx, st.nodes[42])
	if err != nil {
		t.Fatalf("failed to read page aliases: %v", err)
	}
	if len(aliases) != 1 || aliases[0].Alias != "node/42" {
		t.Errorf("aliases = %v, want exactly node/42", aliases)
	}
}

// TestImportNodesDoesNotAliasSkippedPages keeps the canonical-path pass under
// the same rule as the Drupal alias pass: never write an alias onto a page this
// import did not create, because aliases are only removed by cascade from a
// deleted page and would outlive the import.
func TestImportNodesDoesNotAliasSkippedPages(t *testing.T) {
	reader := &fakeReader{
		nodes: []Node{{NID: 7, Type: "page", Title: "Duplicate", Status: 1}},
	}
	st, _, queries := newTestState(t, reader,
		types.ImportOptions{ImportPages: true, SkipExisting: true})
	ctx := context.Background()

	// Pre-create a page owning the slug the import would generate.
	if _, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Duplicate", Slug: "duplicate", Body: "", Status: "published",
		AuthorID: st.authorID, LanguageCode: st.defaultLang, PageType: "page",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("failed to pre-create page: %v", err)
	}

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}

	pageID, ok := st.nodes[7]
	if !ok {
		t.Fatal("the skipped node should still be mapped, for menu resolution")
	}
	aliases, err := queries.GetAliasesForPage(ctx, pageID)
	if err != nil {
		t.Fatalf("failed to read page aliases: %v", err)
	}
	if len(aliases) != 0 {
		t.Errorf("aliases = %v, want none on a page the import did not create", aliases)
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
	// Only the canonical node path: the Drupal alias equals the slug, so
	// writing it again would collide with the unique alias index.
	if st.result.AliasesImported != 1 {
		t.Errorf("AliasesImported = %d, want 1 — the redundant alias is skipped, "+
			"the canonical node path is not", st.result.AliasesImported)
	}
	aliases, err := queries.GetAliasesForPage(ctx, st.nodes[1])
	if err != nil {
		t.Fatalf("failed to read page aliases: %v", err)
	}
	if len(aliases) != 1 || aliases[0].Alias != "node/1" {
		t.Errorf("aliases = %v, want only node/1", aliases)
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

func TestImportNodesSkipExistingDoesNotMapAcrossLanguages(t *testing.T) {
	reader := &fakeReader{
		schema: Schema{HasMenuLinks: true},
		nodes:  []Node{{NID: 1, Type: "page", Title: "About", Status: 1, Langcode: "fr"}},
		menuLinks: []MenuLink{{
			ID: 1, UUID: "u1", Title: "À propos", MenuName: "main", Langcode: "fr",
			LinkURI: "entity:node/1", Enabled: 1,
		}},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{
		ImportPages: true, ImportMenus: true, SkipExisting: true,
	})
	st.addAvailableLanguage("fr")
	ctx := context.Background()
	existing, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "English About", Slug: "about", Status: model.PageStatusPublished,
		AuthorID: st.authorID, LanguageCode: st.defaultLang, PageType: pageTypePage,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatal(err)
	}
	importedID := st.nodes[1]
	if importedID == 0 || importedID == existing.ID {
		t.Fatalf("node mapping = %d, existing = %d", importedID, existing.ID)
	}
	imported, err := queries.GetPageByID(ctx, importedID)
	if err != nil {
		t.Fatal(err)
	}
	if imported.LanguageCode != "fr" || imported.Slug != "about-2" {
		t.Fatalf("imported page = %+v, want fr/about-2", imported)
	}
	if st.result.PagesImported != 1 || st.result.PagesSkipped != 0 {
		t.Fatalf("imported=%d skipped=%d", st.result.PagesImported, st.result.PagesSkipped)
	}
	if err := (&Source{}).importMenus(ctx, st); err != nil {
		t.Fatal(err)
	}
	menu, err := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: "fr",
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := queries.ListTopLevelMenuItems(ctx, menu.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].PageID.Valid || items[0].PageID.Int64 != importedID {
		t.Fatalf("menu items = %+v, want imported page %d", items, importedID)
	}
}

func TestMultilingualNodeAliasesKeepEveryConcreteURLAndMenuTarget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		aliases []PathAlias
	}{
		{
			name: "english alias row first",
			aliases: []PathAlias{
				{ID: 1, Path: "/node/1", Alias: "/about", Langcode: "en"},
				{ID: 2, Path: "/node/2", Alias: "/about", Langcode: "fr"},
			},
		},
		{
			name: "french alias row first",
			aliases: []PathAlias{
				{ID: 1, Path: "/node/2", Alias: "/about", Langcode: "fr"},
				{ID: 2, Path: "/node/1", Alias: "/about", Langcode: "en"},
			},
		},
		{
			name: "neutral French alias row cannot steal default route",
			aliases: []PathAlias{
				{ID: 1, Path: "/node/2", Alias: "/about", Langcode: "und"},
				{ID: 2, Path: "/node/1", Alias: "/about", Langcode: "en"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakeReader{
				schema: Schema{HasAliases: true, HasMenuLinks: true},
				// Import French first to prove the default-language alias owns the
				// globally unsuffixed stored slug independently of node order too.
				nodes: []Node{
					{NID: 2, Type: "page", Title: "À propos", Status: 1, Langcode: "fr"},
					{NID: 1, Type: "page", Title: "About", Status: 1, Langcode: "en"},
				},
				aliases: tc.aliases,
				menuLinks: []MenuLink{
					{ID: 1, UUID: "en-about", Title: "About", MenuName: "main", Langcode: "en", LinkURI: "internal:/about", Enabled: 1},
					{ID: 2, UUID: "fr-about", Title: "À propos", MenuName: "main", Langcode: "fr", LinkURI: "internal:/about", Enabled: 1},
				},
			}
			st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true, ImportMenus: true})
			st.addAvailableLanguage("fr")
			ctx := context.Background()
			source := NewSource()

			if err := source.importNodes(ctx, st); err != nil {
				t.Fatal(err)
			}
			english, err := queries.GetPageByID(ctx, st.nodes[1])
			if err != nil {
				t.Fatal(err)
			}
			french, err := queries.GetPageByID(ctx, st.nodes[2])
			if err != nil {
				t.Fatal(err)
			}
			if english.Slug != "about" || english.LanguageCode != "en" {
				t.Fatalf("English page = %+v, want en/about", english)
			}
			if french.Slug != "about-2" || french.LanguageCode != "fr" {
				t.Fatalf("French page = %+v, want fr/about-2", french)
			}
			redirect, err := queries.GetRedirectBySourcePath(ctx, "/fr/about")
			if err != nil {
				t.Fatalf("French legacy alias was not preserved: %v", err)
			}
			if redirect.TargetUrl != "/fr/about-2" {
				t.Fatalf("French redirect target = %q, want /fr/about-2", redirect.TargetUrl)
			}

			if err := source.importMenus(ctx, st); err != nil {
				t.Fatal(err)
			}
			for _, want := range []struct {
				language string
				pageID   int64
			}{{"en", english.ID}, {"fr", french.ID}} {
				menu, err := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
					Slug: "main", LanguageCode: want.language,
				})
				if err != nil {
					t.Fatalf("GetMenuBySlugAndLanguage(%s): %v", want.language, err)
				}
				items, err := queries.ListTopLevelMenuItems(ctx, menu.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(items) != 1 || !items[0].PageID.Valid || items[0].PageID.Int64 != want.pageID {
					t.Fatalf("%s menu items = %+v, want page %d", want.language, items, want.pageID)
				}
			}
		})
	}
}

func TestNonDefaultTaxonomyAliasIgnoresOtherLanguagePageSlug(t *testing.T) {
	reader := &fakeReader{
		terms: []Term{{TID: 7, Vocabulary: "tags", Langcode: "fr", Name: "Actualités"}},
		nodes: []Node{{NID: 1, Type: "page", Title: "Topics", Status: 1, Langcode: "en"}},
		aliases: []PathAlias{{
			ID: 1, Path: "/taxonomy/term/7", Alias: "/topics", Langcode: "fr",
		}},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportTags: true, ImportPages: true})
	st.addAvailableLanguage("fr")
	ctx := context.Background()
	source := NewSource()

	if err := source.importTaxonomy(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := source.importNodes(ctx, st); err != nil {
		t.Fatal(err)
	}
	page, err := queries.GetPageByID(ctx, st.nodes[1])
	if err != nil {
		t.Fatal(err)
	}
	if page.Slug != "topics" || page.LanguageCode != "en" {
		t.Fatalf("English page = %+v, want en/topics", page)
	}
	redirect, err := queries.GetRedirectBySourcePath(ctx, "/fr/topics")
	if err != nil {
		t.Fatalf("French taxonomy redirect was suppressed by the English slug: %v", err)
	}
	if redirect.TargetUrl != "/fr/tag/actualites" {
		t.Fatalf("redirect target = %q, want /fr/tag/actualites", redirect.TargetUrl)
	}
}

func TestDefaultAliasesIgnoreForeignLanguageStoredSlugs(t *testing.T) {
	t.Run("taxonomy redirect", func(t *testing.T) {
		reader := &fakeReader{
			terms: []Term{{TID: 7, Vocabulary: "tags", Langcode: "en", Name: "News"}},
			aliases: []PathAlias{{
				ID: 1, Path: "/taxonomy/term/7", Alias: "/topics", Langcode: "en",
			}},
		}
		st, _, queries := newTestState(t, reader, types.ImportOptions{ImportTags: true})
		ctx := context.Background()
		_, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title: "French Topics", Slug: "topics", Status: model.PageStatusPublished,
			AuthorID: st.authorID, LanguageCode: "fr", PageType: pageTypePage,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := NewSource().importTaxonomy(ctx, st); err != nil {
			t.Fatal(err)
		}
		redirect, err := queries.GetRedirectBySourcePath(ctx, "/topics")
		if err != nil || redirect.TargetUrl != "/tag/news" {
			t.Fatalf("default taxonomy alias = %+v, err=%v; want /topics -> /tag/news", redirect, err)
		}
	})

	t.Run("node redirect fallback", func(t *testing.T) {
		reader := &fakeReader{
			nodes:   []Node{{NID: 1, Type: "page", Title: "About", Status: 1, Langcode: "en"}},
			aliases: []PathAlias{{ID: 1, Path: "/node/1", Alias: "/about", Langcode: "en"}},
		}
		st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
		ctx := context.Background()
		_, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title: "French About", Slug: "about", Status: model.PageStatusPublished,
			AuthorID: st.authorID, LanguageCode: "fr", PageType: pageTypePage,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := NewSource().importNodes(ctx, st); err != nil {
			t.Fatal(err)
		}
		page, err := queries.GetPageByID(ctx, st.nodes[1])
		if err != nil || page.Slug != "about-2" {
			t.Fatalf("imported page = %+v, err=%v; want about-2", page, err)
		}
		redirect, err := queries.GetRedirectBySourcePath(ctx, "/about")
		if err != nil || redirect.TargetUrl != "/about-2" {
			t.Fatalf("default node alias = %+v, err=%v; want /about -> /about-2", redirect, err)
		}
	})
}

func TestNodeSlugsAndAliasesNeverShadowCoreOrModuleRoutes(t *testing.T) {
	reader := &fakeReader{
		nodes: []Node{
			{NID: 1, Type: "page", Title: "Admin", Status: 1, Langcode: "en"},
			{NID: 2, Type: "page", Title: "Bookmarks", Status: 1, Langcode: "en"},
		},
		aliases: []PathAlias{
			{ID: 1, Path: "/node/1", Alias: "/admin", Langcode: "en"},
			{ID: 2, Path: "/node/2", Alias: "/bookmarks", Langcode: "en"},
		},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	ctx := context.Background()
	source := &Source{publicRouteChecker: pathOwnership{"/bookmarks": true}}
	if err := source.importNodes(ctx, st); err != nil {
		t.Fatal(err)
	}
	for nid, wantSlug := range map[int64]string{1: "admin-2", 2: "bookmarks-2"} {
		page, err := queries.GetPageByID(ctx, st.nodes[nid])
		if err != nil {
			t.Fatal(err)
		}
		if page.Slug != wantSlug {
			t.Errorf("node %d slug = %q, want %q", nid, page.Slug, wantSlug)
		}
		aliases, err := queries.GetAliasesForPage(ctx, page.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, alias := range aliases {
			if alias.Alias == "admin" || alias.Alias == "bookmarks" {
				t.Errorf("shadowed route alias was created: %+v", alias)
			}
		}
	}
}

func TestNonDefaultNodeAliasMayReuseTopLevelOnlyRouteName(t *testing.T) {
	reader := &fakeReader{
		nodes:   []Node{{NID: 1, Type: "page", Title: "Administration", Status: 1, Langcode: "fr"}},
		aliases: []PathAlias{{ID: 1, Path: "/node/1", Alias: "/admin", Langcode: "fr"}},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	st.addAvailableLanguage("fr")
	if err := NewSource().importNodes(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	page, err := queries.GetPageByID(context.Background(), st.nodes[1])
	if err != nil {
		t.Fatal(err)
	}
	if page.Slug != "admin" || page.LanguageCode != "fr" {
		t.Fatalf("French page = %+v, want fr/admin", page)
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

func TestImportNodesReservesSingleSegmentAliasesForTheirOwners(t *testing.T) {
	reader := &fakeReader{
		nodes: []Node{
			{NID: 1, Type: "page", Title: "Reserved", Status: 1, Langcode: "en"},
			{NID: 2, Type: "page", Title: "Owner", Status: 1, Langcode: "en"},
		},
		aliases: []PathAlias{{ID: 1, Path: "/node/2", Alias: "/reserved", Langcode: "en"}},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	if err := (&Source{}).importNodes(context.Background(), st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}
	other, _ := queries.GetPageByID(context.Background(), st.nodes[1])
	owner, _ := queries.GetPageByID(context.Background(), st.nodes[2])
	if other.Slug != "reserved-2" || owner.Slug != "reserved" {
		t.Errorf("owner-aware reservation failed: other=%q owner=%q", other.Slug, owner.Slug)
	}
	if reader.aliasCalls != 1 {
		t.Errorf("GetPathAliases called %d times, want one cached read", reader.aliasCalls)
	}
}

func TestImportNodesRollsBackTrackingFailure(t *testing.T) {
	reader := &fakeReader{nodes: []Node{{NID: 1, Type: "page", Title: "Transient", Status: 1}}}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	tracker.failType = types.EntityPage
	tracker.trackErr = errors.New("tracking failed")
	if err := (&Source{}).importNodes(context.Background(), st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}
	if _, err := queries.GetPageBySlug(context.Background(), "transient"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("untracked page survived rollback: %v", err)
	}
	if st.result.PagesImported != 0 || st.nodes[1] != 0 || st.createdNodes[1] {
		t.Errorf("tracking failure published page state: result=%+v nodes=%v created=%v", st.result, st.nodes, st.createdNodes)
	}
}

func TestCreatePageAliasRefusesSlugShadow(t *testing.T) {
	st, _, queries := newTestState(t, &fakeReader{}, types.ImportOptions{})
	ctx := context.Background()
	now := time.Now()
	owner, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Owner", Slug: "claimed", Status: model.PageStatusPublished,
		AuthorID: st.authorID, LanguageCode: st.defaultLang, PageType: pageTypePage,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage(owner): %v", err)
	}
	target, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Target", Slug: "target", Status: model.PageStatusPublished,
		AuthorID: st.authorID, LanguageCode: st.defaultLang, PageType: pageTypePage,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage(target): %v", err)
	}
	(&Source{}).createPageAlias(ctx, st, target.ID, "claimed", now)
	aliases, err := queries.GetAliasesForPage(ctx, target.ID)
	if err != nil || len(aliases) != 0 {
		t.Errorf("shadowing alias was created: aliases=%v err=%v", aliases, err)
	}
	if owner.ID == target.ID || len(st.result.Notices) != 1 {
		t.Errorf("shadow refusal was not reported: %+v", st.result)
	}
}

func TestCreatePageAliasRefusesExactAndWildcardRedirectOwnership(t *testing.T) {
	st, _, queries := newTestState(t, &fakeReader{}, types.ImportOptions{})
	ctx := context.Background()
	now := time.Now()
	target, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Target", Slug: "target", Status: model.PageStatusPublished,
		AuthorID: st.authorID, LanguageCode: st.defaultLang, PageType: pageTypePage,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreatePage(target): %v", err)
	}
	if _, err := queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: "/legacy/about", TargetUrl: "/kept-exact", StatusCode: 302,
		TargetType: model.TargetSelf, Enabled: false, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateRedirect(exact): %v", err)
	}
	if _, err := queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: "/node/**", TargetUrl: "/kept-wildcard/**", StatusCode: 301,
		IsWildcard: true, TargetType: model.TargetSelf, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateRedirect(wildcard): %v", err)
	}

	source := &Source{}
	source.createPageAlias(ctx, st, target.ID, "legacy/about", now)
	source.createPageAlias(ctx, st, target.ID, "node/42", now)

	aliases, err := queries.GetAliasesForPage(ctx, target.ID)
	if err != nil {
		t.Fatalf("GetAliasesForPage: %v", err)
	}
	if len(aliases) != 0 || st.result.AliasesImported != 0 {
		t.Fatalf("redirect-shadowed aliases were imported: aliases=%v result=%+v", aliases, st.result)
	}
	if len(st.result.Notices) != 2 {
		t.Fatalf("redirect ownership notices = %v; want exact and wildcard notices", st.result.Notices)
	}
}

func TestImportAliasesRollsBackTrackingFailure(t *testing.T) {
	reader := &fakeReader{
		nodes:   []Node{{NID: 1, Type: "page", Title: "About", Status: 1, Langcode: "en"}},
		aliases: []PathAlias{{ID: 1, Path: "/node/1", Alias: "/company/about", Langcode: "en"}},
	}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	tracker.failType = types.EntityAlias
	tracker.trackErr = errors.New("tracking failed")
	if err := (&Source{}).importNodes(context.Background(), st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}
	aliases, err := queries.GetAliasesForPage(context.Background(), st.nodes[1])
	if err != nil || len(aliases) != 0 {
		t.Errorf("untracked aliases survived rollback: aliases=%v err=%v", aliases, err)
	}
	if st.result.AliasesImported != 0 {
		t.Errorf("AliasesImported = %d, want 0", st.result.AliasesImported)
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

func TestImportMenusClaimsReusedSlugAndScopesReuseToLanguage(t *testing.T) {
	reader := &fakeReader{menuLinks: []MenuLink{
		{ID: 1, UUID: "one", Title: "One", MenuName: "foo bar", LinkURI: "internal:/one", Enabled: 1},
		{ID: 2, UUID: "two", Title: "Two", MenuName: "foo-bar", LinkURI: "internal:/two", Enabled: 1},
		{ID: 3, UUID: "main", Title: "Home", MenuName: "main", LinkURI: "internal:/", Enabled: 1},
	}}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	ctx := context.Background()
	now := time.Now()
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr",
		IsActive: true, Position: 2, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateLanguage(fr): %v", err)
	}
	foo, err := queries.CreateMenu(ctx, store.CreateMenuParams{
		Name: "Foo", Slug: "foo-bar", LanguageCode: st.defaultLang, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMenu(foo-bar): %v", err)
	}
	if _, err := queries.CreateMenu(ctx, store.CreateMenuParams{
		Name: "Principal", Slug: "main", LanguageCode: "fr", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateMenu(fr/main): %v", err)
	}

	if err := (&Source{}).importMenus(ctx, st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}
	foo2, err := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "foo-bar-2", LanguageCode: st.defaultLang,
	})
	if err != nil {
		t.Fatalf("second normalized menu was not suffixed: %v", err)
	}
	firstItems, _ := queries.ListMenuItems(ctx, foo.ID)
	secondItems, _ := queries.ListMenuItems(ctx, foo2.ID)
	if len(firstItems) != 1 || firstItems[0].Title != "One" || len(secondItems) != 1 || secondItems[0].Title != "Two" {
		t.Errorf("source menus were merged: first=%v second=%v", firstItems, secondItems)
	}
	if _, err := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: st.defaultLang,
	}); err != nil {
		t.Errorf("foreign-language main menu incorrectly blocked destination-language creation: %v", err)
	}
}

func TestImportMenusPartitionsSourceLanguagesAndParents(t *testing.T) {
	reader := &fakeReader{menuLinks: []MenuLink{
		{ID: 1, UUID: "parent-en", Title: "English", MenuName: "main", Langcode: "en", LinkURI: "internal:/english", Enabled: 1},
		{ID: 2, UUID: "child-fr", Title: "Français", MenuName: "main", Langcode: "fr", LinkURI: "internal:/francais",
			Parent: sql.NullString{String: "menu_link_content:parent-en", Valid: true}, Enabled: 1},
		{ID: 3, UUID: "en-one", Title: "EN one", MenuName: "foo bar", Langcode: "en", LinkURI: "internal:/one", Enabled: 1},
		{ID: 4, UUID: "en-two", Title: "EN two", MenuName: "foo-bar", Langcode: "en", LinkURI: "internal:/two", Enabled: 1},
		{ID: 5, UUID: "fr-one", Title: "FR one", MenuName: "foo bar", Langcode: "fr", LinkURI: "internal:/un", Enabled: 1},
	}}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	ctx := context.Background()
	now := time.Now()
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr",
		IsActive: true, Position: 2, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	st.addAvailableLanguage("fr")

	if err := (&Source{}).importMenus(ctx, st); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		language string
		slug     string
		title    string
		root     bool
	}{
		{"en", "main", "English", true},
		{"fr", "main", "Français", true},
		{"en", "foo-bar", "EN one", true},
		{"en", "foo-bar-2", "EN two", true},
		{"fr", "foo-bar", "FR one", true},
	} {
		menu, err := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
			Slug: expected.slug, LanguageCode: expected.language,
		})
		if err != nil {
			t.Fatalf("menu %s/%s: %v", expected.language, expected.slug, err)
		}
		items, err := queries.ListMenuItems(ctx, menu.ID)
		if err != nil || len(items) != 1 || items[0].Title != expected.title {
			t.Fatalf("menu %s/%s items = %+v, err=%v", expected.language, expected.slug, items, err)
		}
		if expected.root && items[0].ParentID.Valid {
			t.Fatalf("menu %s/%s item crossed a language partition: %+v", expected.language, expected.slug, items[0])
		}
	}
	if st.result.MenusImported != 5 || st.result.MenuItemsImported != 5 {
		t.Fatalf("import counts menus=%d items=%d, want 5/5",
			st.result.MenusImported, st.result.MenuItemsImported)
	}
	foundNotice := false
	for _, notice := range st.result.Notices {
		if strings.Contains(notice, "outside its language partition") {
			foundNotice = true
		}
	}
	if !foundNotice {
		t.Fatalf("cross-language parent was not reported: %v", st.result.Notices)
	}
}

func TestImportMenusReportsUnknownAndNeutralLanguageFallbacks(t *testing.T) {
	reader := &fakeReader{menuLinks: []MenuLink{
		{ID: 1, UUID: "unknown", Title: "Unknown", MenuName: "main", Langcode: "es", LinkURI: "internal:/unknown", Enabled: 1},
		{ID: 2, UUID: "neutral", Title: "Neutral", MenuName: "main", Langcode: "und", LinkURI: "internal:/neutral", Enabled: 1},
		{ID: 3, UUID: "empty", Title: "Empty", MenuName: "main", Langcode: "", LinkURI: "internal:/empty", Enabled: 1},
	}}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	ctx := context.Background()
	if err := (&Source{}).importMenus(ctx, st); err != nil {
		t.Fatal(err)
	}
	menu, err := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: st.defaultLang,
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := queries.ListMenuItems(ctx, menu.ID)
	if err != nil || len(items) != 3 {
		t.Fatalf("default menu items = %v, err=%v; want all three fallbacks", items, err)
	}
	if st.unmappedMenuLangs["es"] != 1 || st.neutralMenuLangs["und"] != 1 || st.neutralMenuLangs["empty"] != 1 {
		t.Fatalf("menu fallback counters: unknown=%v neutral=%v", st.unmappedMenuLangs, st.neutralMenuLangs)
	}
	if len(st.unmappedLangs) != 0 || len(st.unmappedTermLangs) != 0 || len(st.neutralTermLangs) != 0 {
		t.Fatalf("menu fallbacks leaked into node/term counters: node=%v term=%v neutralTerm=%v",
			st.unmappedLangs, st.unmappedTermLangs, st.neutralTermLangs)
	}
	unknownSummaries, neutralSummaries := 0, 0
	for _, summary := range st.result.Summaries {
		switch {
		case strings.Contains(summary, `Drupal language "es"`) && strings.Contains(summary, "1 menu link(s)"):
			unknownSummaries++
		case strings.Contains(summary, "language-neutral code") && strings.Contains(summary, "1 menu link(s)"):
			neutralSummaries++
		}
	}
	if unknownSummaries != 1 || neutralSummaries != 2 {
		t.Fatalf("fallback summaries = %v; unknown=%d neutral=%d", st.result.Summaries,
			unknownSummaries, neutralSummaries)
	}
}

func TestImportMenusResolvesTaxonomyTermTarget(t *testing.T) {
	reader := &fakeReader{menuLinks: []MenuLink{{
		ID: 1, UUID: "term", Title: "Go", MenuName: "main",
		LinkURI: "entity:taxonomy_term/7", Enabled: 1,
	}}}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	st.termURLs[7] = "/fr/tag/go"
	if err := (&Source{}).importMenus(context.Background(), st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}
	menu, _ := queries.GetMenuBySlugAndLanguage(context.Background(), store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: st.defaultLang,
	})
	items, err := queries.ListMenuItems(context.Background(), menu.ID)
	if err != nil || len(items) != 1 || !items[0].Url.Valid || items[0].Url.String != "/fr/tag/go" {
		t.Errorf("taxonomy menu target = %v, err=%v; want /fr/tag/go", items, err)
	}
}

func TestImportMenusResolvesTaxonomyPathAliases(t *testing.T) {
	reader := &fakeReader{
		aliases: []PathAlias{
			{ID: 1, Path: "/taxonomy/term/7", Alias: "/topics/go", Langcode: "en"},
			{ID: 2, Path: "/taxonomy/term/8", Alias: "/sujets/rouille", Langcode: "fr"},
		},
		menuLinks: []MenuLink{
			{ID: 1, UUID: "default-term-alias", Title: "Go", MenuName: "main", Langcode: "en", LinkURI: "internal:/topics/go", Enabled: 1},
			{ID: 2, UUID: "french-term-alias", Title: "Rouille", MenuName: "main", Langcode: "fr", LinkURI: "internal:/sujets/rouille", Enabled: 1},
		},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	ctx := context.Background()
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr",
		IsActive: true, Position: 2, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	st.addAvailableLanguage("fr")
	st.termURLs[7] = "/tag/go"
	st.termURLs[8] = "/fr/category/rouille"
	st.termLang[7] = "en"
	st.termLang[8] = "fr"
	st.termDestinationLang[7] = "en"
	st.termDestinationLang[8] = "fr"

	if err := (&Source{}).importMenus(ctx, st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}
	menu, err := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: st.defaultLang,
	})
	if err != nil {
		t.Fatalf("GetMenuBySlugAndLanguage: %v", err)
	}
	items, err := queries.ListMenuItems(ctx, menu.ID)
	if err != nil {
		t.Fatalf("ListMenuItems: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Go" || !items[0].Url.Valid || items[0].Url.String != "/tag/go" {
		t.Fatalf("default-language menu items = %+v, want Go -> /tag/go", items)
	}
	frMenu, err := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: "fr",
	})
	if err != nil {
		t.Fatal(err)
	}
	frItems, err := queries.ListMenuItems(ctx, frMenu.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(frItems) != 1 || frItems[0].Title != "Rouille" ||
		!frItems[0].Url.Valid || frItems[0].Url.String != "/fr/category/rouille" {
		t.Fatalf("French menu items = %+v, want Rouille -> /fr/category/rouille", frItems)
	}
	if reader.aliasCalls != 1 {
		t.Errorf("GetPathAliases calls = %d, want one cached read", reader.aliasCalls)
	}
}

func TestImportMenusResolvesIdenticalTaxonomyAliasesByLinkLanguage(t *testing.T) {
	reader := &fakeReader{
		aliases: []PathAlias{
			// Put the non-default row first to prove source row order cannot choose
			// the destination for an English menu link.
			{ID: 1, Path: "/taxonomy/term/8", Alias: "/topics/current", Langcode: "fr"},
			{ID: 2, Path: "/taxonomy/term/7", Alias: "/topics/current", Langcode: "en"},
		},
		menuLinks: []MenuLink{
			{ID: 1, UUID: "current-en", Title: "Current EN", MenuName: "main", Langcode: "en", LinkURI: "internal:/topics/current", Enabled: 1},
			{ID: 2, UUID: "current-fr", Title: "Current FR", MenuName: "main", Langcode: "fr", LinkURI: "internal:/topics/current", Enabled: 1},
		},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	ctx := context.Background()
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr",
		IsActive: true, Position: 2, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	st.addAvailableLanguage("fr")
	st.termURLs[7] = "/tag/current"
	st.termURLs[8] = "/fr/tag/actuel"
	st.termLang[7] = "en"
	st.termLang[8] = "fr"
	st.termDestinationLang[7] = "en"
	st.termDestinationLang[8] = "fr"

	if err := (&Source{}).importMenus(ctx, st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}
	menu, err := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: st.defaultLang,
	})
	if err != nil {
		t.Fatalf("GetMenuBySlugAndLanguage: %v", err)
	}
	items, err := queries.ListMenuItems(ctx, menu.ID)
	if err != nil {
		t.Fatalf("ListMenuItems: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Current EN" ||
		!items[0].Url.Valid || items[0].Url.String != "/tag/current" {
		t.Fatalf("English menu items = %+v, want Current EN -> /tag/current", items)
	}
	frMenu, err := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: "fr",
	})
	if err != nil {
		t.Fatal(err)
	}
	frItems, err := queries.ListMenuItems(ctx, frMenu.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(frItems) != 1 || frItems[0].Title != "Current FR" ||
		!frItems[0].Url.Valid || frItems[0].Url.String != "/fr/tag/actuel" {
		t.Fatalf("French menu items = %+v, want Current FR -> /fr/tag/actuel", frItems)
	}
}

func TestImportMenusResolvesNeutralTaxonomyAliasesByTermLanguage(t *testing.T) {
	reader := &fakeReader{
		aliases: []PathAlias{
			{ID: 1, Path: "/taxonomy/term/7", Alias: "/topics/current", Langcode: "und"},
			{ID: 2, Path: "/taxonomy/term/8", Alias: "/topics/current", Langcode: "und"},
		},
		menuLinks: []MenuLink{
			{ID: 1, UUID: "current-en", Title: "Current EN", MenuName: "main", Langcode: "en", LinkURI: "internal:/topics/current", Enabled: 1},
			{ID: 2, UUID: "current-fr", Title: "Current FR", MenuName: "main", Langcode: "fr", LinkURI: "internal:/topics/current", Enabled: 1},
		},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	st.addAvailableLanguage("fr")
	st.termURLs[7], st.termLang[7], st.termDestinationLang[7] = "/tag/current", "en", "en"
	st.termURLs[8], st.termLang[8], st.termDestinationLang[8] = "/fr/tag/actuel", "fr", "fr"
	if err := NewSource().importMenus(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		language string
		url      string
	}{{"en", "/tag/current"}, {"fr", "/fr/tag/actuel"}} {
		menu, err := queries.GetMenuBySlugAndLanguage(context.Background(), store.GetMenuBySlugAndLanguageParams{
			Slug: "main", LanguageCode: want.language,
		})
		if err != nil {
			t.Fatal(err)
		}
		items, err := queries.ListMenuItems(context.Background(), menu.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || !items[0].Url.Valid || items[0].Url.String != want.url {
			t.Fatalf("%s neutral-alias menu items = %+v, want %q", want.language, items, want.url)
		}
	}
}

func TestImportMenusDropsUnresolvedTaxonomyPathAlias(t *testing.T) {
	reader := &fakeReader{
		aliases: []PathAlias{{
			ID: 1, Path: "/taxonomy/term/99", Alias: "/topics/missing", Langcode: "en",
		}},
		menuLinks: []MenuLink{{
			ID: 1, UUID: "missing-term-alias", Title: "Missing", MenuName: "main",
			LinkURI: "internal:/topics/missing", Enabled: 1,
		}},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	ctx := context.Background()
	if err := (&Source{}).importMenus(ctx, st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}
	menu, err := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: st.defaultLang,
	})
	if err != nil {
		t.Fatalf("GetMenuBySlugAndLanguage: %v", err)
	}
	items, err := queries.ListMenuItems(ctx, menu.ID)
	if err != nil {
		t.Fatalf("ListMenuItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("unresolved taxonomy alias was stored as a raw menu URL: %+v", items)
	}
	if got := strings.Join(st.result.Notices, "\n"); !strings.Contains(got, "term 99") ||
		!strings.Contains(got, "/topics/missing") {
		t.Errorf("notices = %v, want unresolved taxonomy alias and term", st.result.Notices)
	}
}

func TestImportMenusRollsBackTrackingFailure(t *testing.T) {
	reader := &fakeReader{menuLinks: []MenuLink{{
		ID: 1, UUID: "home", Title: "Home", MenuName: "main", LinkURI: "internal:/", Enabled: 1,
	}}}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	tracker.failType = types.EntityMenu
	tracker.trackErr = errors.New("tracking failed")
	if err := (&Source{}).importMenus(context.Background(), st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}
	if _, err := queries.GetMenuBySlugAndLanguage(context.Background(), store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: st.defaultLang,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("untracked menu survived rollback: %v", err)
	}
	if st.result.MenusImported != 0 || st.result.MenuItemsImported != 0 ||
		st.claimedMenuSlugs[menuSlugClaim(st.defaultLang, "main")] {
		t.Errorf("tracking failure published menu state: result=%+v claimed=%v", st.result, st.claimedMenuSlugs)
	}
}

func TestImportMenuItemRollsBackTrackingFailure(t *testing.T) {
	reader := &fakeReader{menuLinks: []MenuLink{{
		ID: 1, UUID: "home", Title: "Home", MenuName: "main", LinkURI: "internal:/", Enabled: 1,
	}}}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	tracker.failType = types.EntityMenuItem
	tracker.trackErr = errors.New("tracking failed")
	if err := (&Source{}).importMenus(context.Background(), st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}
	menu, err := queries.GetMenuBySlugAndLanguage(context.Background(), store.GetMenuBySlugAndLanguageParams{
		Slug: "main", LanguageCode: st.defaultLang,
	})
	if err != nil {
		t.Fatalf("the successfully tracked menu should remain: %v", err)
	}
	items, err := queries.ListMenuItems(context.Background(), menu.ID)
	if err != nil || len(items) != 0 {
		t.Errorf("untracked menu item survived rollback: items=%v err=%v", items, err)
	}
	if st.result.MenusImported != 1 || st.result.MenuItemsImported != 0 {
		t.Errorf("tracking failure counters = menu %d item %d, want 1/0",
			st.result.MenusImported, st.result.MenuItemsImported)
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
	t.Setenv(shared.EnvAllowedFileRoots, filesRoot)
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
	t.Setenv(shared.EnvAllowedFileRoots, filesRoot)

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

func TestImportMediaReportsMissingMediaFieldDataMapping(t *testing.T) {
	filesRoot := t.TempDir()
	t.Setenv(shared.EnvAllowedFileRoots, filesRoot)
	const rel = "document.pdf"
	if err := os.WriteFile(filepath.Join(filesRoot, rel), []byte("pdf fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reader := &fakeReader{
		files: []File{{FID: 1, Filename: rel, URI: "public://" + rel, MimeType: "application/pdf"}},
	}
	// Exercise the live Reader's required-media-data guard through the fake's
	// interface slot: importMedia must surface this as an ImportResult error and
	// still import the managed file by its own UUID.
	reader.mediaUUIDErr = errors.New("media_field_data is absent; media entity UUID references cannot be mapped")
	st, _, _ := newTestState(t, reader, types.ImportOptions{ImportMedia: true})
	st.filesPath = filesRoot

	if err := (&Source{}).importMedia(context.Background(), st); err != nil {
		t.Fatalf("importMedia() error = %v", err)
	}
	if st.result.MediaImported != 1 {
		t.Errorf("MediaImported = %d, want 1 despite missing media-entity mapping", st.result.MediaImported)
	}
	if got := strings.Join(st.result.Errors, "\n"); !strings.Contains(got, "failed to read media entity references") || !strings.Contains(got, "media_field_data") {
		t.Errorf("errors = %v, want descriptive importer-visible media mapping error", st.result.Errors)
	}
}

func TestImportMediaRollsBackDatabaseAndFilesOnTrackingFailure(t *testing.T) {
	filesRoot := t.TempDir()
	t.Setenv(shared.EnvAllowedFileRoots, filesRoot)
	const rel = "document.pdf"
	if err := os.WriteFile(filepath.Join(filesRoot, rel), []byte("pdf fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reader := &fakeReader{files: []File{{
		FID: 1, Filename: rel, URI: "public://" + rel, MimeType: "application/pdf",
	}}}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportMedia: true})
	st.filesPath = filesRoot
	tracker.failType = types.EntityMedia
	tracker.trackErr = errors.New("tracking failed")

	if err := (&Source{}).importMedia(context.Background(), st); err != nil {
		t.Fatalf("importMedia() error = %v", err)
	}
	count, err := queries.CountMedia(context.Background())
	if err != nil || count != 0 {
		t.Errorf("media count = %d, err=%v; want the untracked row removed", count, err)
	}
	if st.result.MediaImported != 0 || st.mediaByFID[1] != 0 || len(st.refs.ByPath) != 0 {
		t.Errorf("tracking failure published media state: result=%+v map=%v refs=%v", st.result, st.mediaByFID, st.refs.ByPath)
	}
	assertNoUploadedFiles(t, st.uploadDir)
}

func TestImportMediaTrackingRollbackUsesCapturedCanonicalUploadRoot(t *testing.T) {
	filesRoot := t.TempDir()
	t.Setenv(shared.EnvAllowedFileRoots, filesRoot)
	const rel = "document.pdf"
	if err := os.WriteFile(filepath.Join(filesRoot, rel), []byte("pdf fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := &fakeReader{files: []File{{
		FID: 1, Filename: rel, URI: "public://" + rel, MimeType: "application/pdf",
	}}}
	st, tracker, queries := newTestState(t, reader, types.ImportOptions{ImportMedia: true})
	st.filesPath = filesRoot

	destinationParent := t.TempDir()
	originalRoot := filepath.Join(destinationParent, "uploads-original")
	outsideRoot := filepath.Join(destinationParent, "uploads-outside")
	configuredRoot := filepath.Join(destinationParent, "uploads")
	for _, root := range []string{originalRoot, outsideRoot} {
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(originalRoot, configuredRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	st.uploadDir = configuredRoot
	tracker.failType = types.EntityMedia
	tracker.trackErr = errors.New("tracking failed")
	var outsideSentinel string
	tracker.beforeFailure = func() {
		entries, err := os.ReadDir(filepath.Join(originalRoot, model.OriginalsDir))
		if err != nil || len(entries) != 1 {
			t.Fatalf("read written UUID: entries=%v error=%v", entries, err)
		}
		outsideMediaDir := filepath.Join(outsideRoot, model.OriginalsDir, entries[0].Name())
		if err := os.MkdirAll(outsideMediaDir, 0o750); err != nil {
			t.Fatal(err)
		}
		outsideSentinel = filepath.Join(outsideMediaDir, "must-remain.pdf")
		if err := os.WriteFile(outsideSentinel, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(configuredRoot); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideRoot, configuredRoot); err != nil {
			t.Fatal(err)
		}
	}

	if err := (&Source{}).importMedia(context.Background(), st); err != nil {
		t.Fatalf("importMedia() error = %v", err)
	}
	if count, err := queries.CountMedia(context.Background()); err != nil || count != 0 {
		t.Fatalf("media count = %d, err=%v; want compensated row", count, err)
	}
	assertNoUploadedFiles(t, originalRoot)
	if data, err := os.ReadFile(outsideSentinel); err != nil || string(data) != "outside" {
		t.Fatalf("outside sentinel changed: data=%q error=%v", data, err)
	}
}

func TestImportMediaKeepsFilesWhenDatabaseRollbackFails(t *testing.T) {
	filesRoot := t.TempDir()
	t.Setenv(shared.EnvAllowedFileRoots, filesRoot)
	const rel = "document.pdf"
	if err := os.WriteFile(filepath.Join(filesRoot, rel), []byte("pdf fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reader := &fakeReader{files: []File{{
		FID: 1, Filename: rel, URI: "public://" + rel, MimeType: "application/pdf",
	}}}
	st, tracker, queries, db := newTestStateWithDB(t, reader, types.ImportOptions{ImportMedia: true})
	st.filesPath = filesRoot
	tracker.failType = types.EntityMedia
	tracker.trackErr = errors.New("tracking failed")
	if _, err := db.Exec(`CREATE TRIGGER block_media_delete BEFORE DELETE ON media BEGIN SELECT RAISE(FAIL, 'delete blocked'); END`); err != nil {
		t.Fatalf("failed to create rollback-failure trigger: %v", err)
	}

	if err := (&Source{}).importMedia(context.Background(), st); err != nil {
		t.Fatalf("importMedia() error = %v", err)
	}
	count, err := queries.CountMedia(context.Background())
	if err != nil || count != 1 {
		t.Errorf("media count = %d, err=%v; failed DB rollback must retain the row", count, err)
	}
	var files int
	if err := filepath.WalkDir(st.uploadDir, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			files++
		}
		return walkErr
	}); err != nil {
		t.Fatalf("WalkDir(uploadDir): %v", err)
	}
	if files == 0 {
		t.Error("failed DB rollback deleted the media files, leaving the retained row dangling")
	}
	if got := strings.Join(st.result.Errors, "\n"); !strings.Contains(got, "failed to roll back untracked media") || !strings.Contains(got, "delete blocked") {
		t.Errorf("errors = %v, want the rollback failure reported", st.result.Errors)
	}
}

func TestCleanupMediaFilesQueuesWithDetachedBoundedContext(t *testing.T) {
	st, _, _ := newTestState(t, &fakeReader{}, types.ImportOptions{ImportMedia: true})
	tracker := &cleanupQueueTracker{}
	st.tracker = tracker

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	parent := t.TempDir()
	originalRoot := filepath.Join(parent, "uploads-original")
	outsideRoot := filepath.Join(parent, "uploads-outside")
	configuredRoot := filepath.Join(parent, "uploads")
	for _, root := range []string{originalRoot, outsideRoot} {
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(originalRoot, configuredRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	canonicalUploadRoot, rootErr := imaging.CanonicalUploadRoot(configuredRoot)
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	if err := os.Remove(configuredRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideRoot, configuredRoot); err != nil {
		t.Fatal(err)
	}
	err := st.cleanupMediaFiles(ctx, canonicalUploadRoot, "not-a-media-uuid")
	if err == nil || !strings.Contains(err.Error(), "durable cleanup retry queued") {
		t.Fatalf("cleanupMediaFiles() error = %v, want queued cleanup result", err)
	}
	if tracker.queueCtxErr != nil {
		t.Errorf("cleanup queue received canceled context: %v", tracker.queueCtxErr)
	}
	if !tracker.queueBounded {
		t.Error("cleanup queue context has no deadline")
	}
	if tracker.queuedRoot != canonicalUploadRoot || tracker.queuedUUID != "not-a-media-uuid" {
		t.Fatalf("queued cleanup = (%q, %q), want captured root and UUID", tracker.queuedRoot, tracker.queuedUUID)
	}
}

func TestImportOneFileCleansOutputWhenSourceCloseFails(t *testing.T) {
	st, _, queries := newTestState(t, &fakeReader{}, types.ImportOptions{ImportMedia: true})
	canonicalUploadRoot, err := imaging.CanonicalUploadRoot(st.uploadDir)
	if err != nil {
		t.Fatal(err)
	}
	processor := imaging.NewProcessor(canonicalUploadRoot)
	pngData := func() []byte {
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatalf("png.Encode: %v", err)
		}
		return buf.Bytes()
	}()
	for _, tc := range []struct {
		name, filename, mime string
		data                 []byte
	}{
		{name: "non-image", filename: "document.pdf", mime: "application/pdf", data: []byte("pdf fixture")},
		{name: "image", filename: "photo.png", mime: "image/png", data: pngData},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := (&Source{}).importOneFile(context.Background(), st,
				closeFailSource{data: tc.data}, processor, canonicalUploadRoot,
				File{FID: 1, Filename: tc.filename, URI: "public://" + tc.filename, MimeType: tc.mime},
				tc.filename, tc.mime, time.Now())
			if err == nil || !strings.Contains(err.Error(), "source close failed") {
				t.Fatalf("importOneFile() error = %v, want the close failure", err)
			}
			assertNoUploadedFiles(t, st.uploadDir)
		})
	}
	count, countErr := queries.CountMedia(context.Background())
	if countErr != nil || count != 0 {
		t.Errorf("media count = %d, err=%v; close failure must precede DB creation", count, countErr)
	}
}

func assertNoUploadedFiles(t *testing.T, root string) {
	t.Helper()
	var files int
	if err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			files++
		}
		return walkErr
	}); err != nil {
		t.Fatalf("WalkDir(%s): %v", root, err)
	}
	if files != 0 {
		t.Errorf("cleanup left %d uploaded file(s)", files)
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

// TestImportMenusKeepsHierarchyThroughSkippedParents covers a rerun, or an
// import into a menu an administrator had already populated, where the parent
// link is already present but the child is not.
//
// Bug state: the dedup pass recorded only that a matching item existed, not its
// ID, so the skipped parent never entered itemByUUID. linkMenuParents then
// could not resolve the child's parentUUID and left it at the menu root,
// flattening the imported navigation.
func TestImportMenusKeepsHierarchyThroughSkippedParents(t *testing.T) {
	reader := &fakeReader{
		schema: Schema{HasMenuLinks: true},
		menuLinks: []MenuLink{
			{ID: 1, UUID: "parent", Title: "Docs", MenuName: "main", LinkURI: "internal:/docs", Enabled: 1},
			{ID: 2, UUID: "child", Title: "Guide", MenuName: "main", LinkURI: "internal:/docs/guide",
				Parent: sql.NullString{String: "menu_link_content:parent", Valid: true}, Enabled: 1},
		},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	ctx := context.Background()

	menu, err := queries.CreateMenu(ctx, store.CreateMenuParams{
		Name: "Main", Slug: "main", LanguageCode: st.defaultLang,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create existing menu: %v", err)
	}
	// The parent already exists, matching on title and target; the child does not.
	parent, err := queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
		MenuID: menu.ID, Title: "Docs", Url: sql.NullString{String: "/docs", Valid: true},
		Target:   sql.NullString{String: "_self", Valid: true},
		IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create existing menu item: %v", err)
	}

	if err := (&Source{}).importMenus(ctx, st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}

	if st.result.MenuItemsSkipped != 1 {
		t.Errorf("MenuItemsSkipped = %d, want 1 (the pre-existing parent)", st.result.MenuItemsSkipped)
	}

	items, err := queries.ListMenuItems(ctx, menu.ID)
	if err != nil {
		t.Fatalf("failed to list menu items: %v", err)
	}
	var child *store.MenuItem
	for i := range items {
		if items[i].Title == "Guide" {
			child = &items[i]
		}
	}
	if child == nil {
		t.Fatal("the child link was not imported at all")
	}
	if !child.ParentID.Valid || child.ParentID.Int64 != parent.ID {
		t.Errorf("child ParentID = %v, want %d — a link skipped as already present "+
			"must still be resolvable as a parent, or the hierarchy is flattened",
			child.ParentID, parent.ID)
	}
}

// TestImportMenusDoesNotReparentExistingItems is the other half of
// TestImportMenusKeepsHierarchyThroughSkippedParents.
//
// That fix put pre-existing items into itemByUUID so a child could resolve them
// as a parent — and thereby made the second pass eligible to rewrite their own
// parent_id. Those rows belong to the administrator and are not tracked, so a
// hierarchy the import changed could never be restored by deleting it.
//
// Bug state: drop the createdItems check in linkMenuParents and the existing
// item is moved under the imported one.
func TestImportMenusDoesNotReparentExistingItems(t *testing.T) {
	reader := &fakeReader{
		schema: Schema{HasMenuLinks: true},
		menuLinks: []MenuLink{
			{ID: 1, UUID: "top", Title: "Products", MenuName: "main", LinkURI: "internal:/products", Enabled: 1},
			// Already present in the menu, and Drupal says it belongs under "top".
			{ID: 2, UUID: "existing", Title: "Docs", MenuName: "main", LinkURI: "internal:/docs",
				Parent: sql.NullString{String: "menu_link_content:top", Valid: true}, Enabled: 1},
		},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportMenus: true})
	ctx := context.Background()

	menu, err := queries.CreateMenu(ctx, store.CreateMenuParams{
		Name: "Main", Slug: "main", LanguageCode: st.defaultLang,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create existing menu: %v", err)
	}
	existing, err := queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
		MenuID: menu.ID, Title: "Docs", Url: sql.NullString{String: "/docs", Valid: true},
		Target:   sql.NullString{String: "_self", Valid: true},
		IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create existing menu item: %v", err)
	}

	if err := (&Source{}).importMenus(ctx, st); err != nil {
		t.Fatalf("importMenus() error = %v", err)
	}

	after, err := queries.GetMenuItemByID(ctx, existing.ID)
	if err != nil {
		t.Fatalf("the pre-existing item disappeared: %v", err)
	}
	if after.ParentID.Valid {
		t.Errorf("the administrator's own menu item was reparented to %d; the import "+
			"must not rewrite the hierarchy of rows it does not own", after.ParentID.Int64)
	}
}

// TestImportAliasesFiltersByNodeLanguage covers a multilingual source.
//
// Drupal stores one alias row per language. Only default-language nodes are
// imported, so attaching every language's alias to that one page made a
// translated URL redirect to unrelated content — and could consume an alias
// another imported page needed.
func TestImportAliasesFiltersByNodeLanguage(t *testing.T) {
	reader := &fakeReader{
		schema: Schema{HasAliases: true},
		nodes:  []Node{{NID: 1, Type: "page", Title: "About", Status: 1, Langcode: "en"}},
		aliases: []PathAlias{
			{ID: 1, Path: "/node/1", Alias: "/company/about", Langcode: "en"},
			{ID: 2, Path: "/node/1", Alias: "/entreprise/a-propos", Langcode: "fr"},
			{ID: 3, Path: "/node/1", Alias: "/neutral/about", Langcode: "und"},
		},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	ctx := context.Background()

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}

	aliases, err := queries.GetAliasesForPage(ctx, st.nodes[1])
	if err != nil {
		t.Fatalf("failed to read page aliases: %v", err)
	}
	got := make(map[string]bool, len(aliases))
	for _, a := range aliases {
		got[a.Alias] = true
	}
	if !got["neutral/about"] {
		t.Errorf("aliases = %v, want the language-neutral alias kept", aliases)
	}
	if got["entreprise/a-propos"] {
		t.Errorf("aliases = %v, want the French alias rejected: it belongs to a "+
			"translation that was never imported", aliases)
	}
}

func TestHasTraversalSegment(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		// Legitimate names that the old substring check rejected outright.
		{"report..pdf", false},
		{"2024/annual..report.pdf", false},
		{"a..b/c..d.jpg", false},
		{"photo.jpg", false},

		{"../secret", true},
		{"a/../../etc/passwd", true},
		{"..", true},
		{"nested/..", true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := hasTraversalSegment(tc.path); got != tc.want {
				t.Errorf("hasTraversalSegment(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestImportNodesUsesEachNodesSourceLanguage covers a multilingual source.
//
// Drupal's default_langcode = 1 marks each entity's own source translation, not
// the site's one default language, so a multilingual site returns English,
// French and other originals together. Filing every one of them under the oCMS
// default language served French content under the English locale in
// language-filtered listings and URLs.
func TestImportNodesUsesEachNodesSourceLanguage(t *testing.T) {
	reader := &fakeReader{
		nodes: []Node{
			{NID: 1, Type: "page", Title: "About", Status: 1, Langcode: "en"},
			{NID: 2, Type: "page", Title: "A propos", Status: 1, Langcode: "fr"},
			{NID: 3, Type: "page", Title: "Neutral", Status: 1, Langcode: "und"},
			{NID: 4, Type: "page", Title: "Klingon", Status: 1, Langcode: "tlh"},
		},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	ctx := context.Background()

	// oCMS knows English and French, but not Klingon.
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr",
		IsActive: true, IsDefault: false, Position: 2,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("failed to create language: %v", err)
	}
	langs, err := queries.ListActiveLanguages(ctx)
	if err != nil {
		t.Fatalf("failed to list languages: %v", err)
	}
	for _, l := range langs {
		st.availableLangs[strings.ToLower(l.Code)] = true
	}

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}

	for _, tc := range []struct {
		nid  int64
		want string
	}{
		{1, "en"},
		{2, "fr"},
		{3, st.defaultLang}, // language-neutral
		{4, st.defaultLang}, // no oCMS language for it
	} {
		page, err := queries.GetPageByID(ctx, st.nodes[tc.nid])
		if err != nil {
			t.Fatalf("node %d was not imported: %v", tc.nid, err)
		}
		if page.LanguageCode != tc.want {
			t.Errorf("node %d language = %q, want %q", tc.nid, page.LanguageCode, tc.want)
		}
	}

	// The unmapped language must be reported, not silently absorbed.
	var reported bool
	for _, msg := range st.result.Summaries {
		if strings.Contains(msg, "tlh") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("summaries = %v, want one naming the unmapped language tlh", st.result.Summaries)
	}
}

// TestImportAliasesKeepsNonSlugPaths is the end-to-end half of
// TestIsSafeAliasPath: an established Drupal URL outside oCMS's slug grammar
// must still be stored, because page_aliases holds arbitrary text and the
// frontend matches it exactly.
func TestImportAliasesKeepsNonSlugPaths(t *testing.T) {
	reader := &fakeReader{
		schema: Schema{HasAliases: true},
		nodes:  []Node{{NID: 1, Type: "page", Title: "About", Status: 1, Langcode: "en"}},
		aliases: []PathAlias{
			{ID: 1, Path: "/node/1", Alias: "/About_Us", Langcode: "en"},
			{ID: 2, Path: "/node/1", Alias: "/News/Archive", Langcode: "en"},
			{ID: 3, Path: "/node/1", Alias: "/о-компании", Langcode: "en"},
			// Still rejected: not a usable path.
			{ID: 4, Path: "/node/1", Alias: "/has space", Langcode: "en"},
			{ID: 5, Path: "/node/1", Alias: "/a/../b", Langcode: "en"},
		},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	ctx := context.Background()

	if err := (&Source{}).importNodes(ctx, st); err != nil {
		t.Fatalf("importNodes() error = %v", err)
	}

	aliases, err := queries.GetAliasesForPage(ctx, st.nodes[1])
	if err != nil {
		t.Fatalf("failed to read page aliases: %v", err)
	}
	got := make(map[string]bool, len(aliases))
	for _, a := range aliases {
		got[a.Alias] = true
	}
	for _, want := range []string{"About_Us", "News/Archive", "о-компании"} {
		if !got[want] {
			t.Errorf("aliases = %v, want %q kept: it is an established URL that "+
				"oCMS can store and serve", aliases, want)
		}
	}
	for _, unwanted := range []string{"has space", "a/../b"} {
		if got[unwanted] {
			t.Errorf("aliases = %v, want %q rejected", aliases, unwanted)
		}
	}
	// A rejected alias is reported, not dropped in silence.
	var reported bool
	for _, msg := range st.result.Notices {
		if strings.Contains(msg, "has space") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("notices = %v, want one naming the alias that could not be imported",
			st.result.Notices)
	}
}

// TestNodeSlugIsNotFreeUnderActiveLanguagePrefix documents, and holds in
// place, the reason the Drupal source needs no separate language-prefix check.
//
// A page stored at a slug matching an active language code is answered by the
// language homepage, because the middleware strips that segment before the
// frontend router runs. The source's own route-ownership check already treats
// the prefix namespace as taken, so uniqueNodeSlug suffixes around it. The
// cross-package guard test in internal/handler relies on that, and this is
// what proves it rather than reading the call chain.
func TestNodeSlugIsNotFreeUnderActiveLanguagePrefix(t *testing.T) {
	st, _, _ := newTestState(t, &fakeReader{}, types.ImportOptions{})
	st.addAvailableLanguage("eng")

	source := &Source{}
	ctx := context.Background()
	if source.nodeSlugIsFree(ctx, st, "eng", 1, st.defaultLang) {
		t.Error("a slug matching an active language prefix was accepted; the page would be unreachable")
	}
	if !source.nodeSlugIsFree(ctx, st, "engineering", 1, st.defaultLang) {
		t.Error("an unrelated slug was rejected")
	}
	if got := source.uniqueNodeSlug(ctx, st, "eng", 1, st.defaultLang); got != "eng-2" {
		t.Errorf("uniqueNodeSlug(%q) = %q, want a suffix that clears the language prefix", "eng", got)
	}
}
