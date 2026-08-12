// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package drupal imports content from a Drupal 8/9/10/11 site into oCMS by
// reading its MySQL database and its public files directory.
//
// Only default-language content is imported: Drupal keeps translations as extra
// rows in the *_field_data tables keyed by langcode, and oCMS models a
// translation as a separate page with its own globally-unique slug, so mapping
// them automatically would produce slug collisions rather than useful content.
// Non-default rows are counted and reported as skipped.
package drupal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/security"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// placeholderPassword is hashed once and shared by every imported user. Drupal
// stores phpass/SHA-512 hashes that oCMS's Argon2id verifier cannot check, so
// imported accounts always need a password reset; hashing per user would only
// add Argon2id's cost (19 MB, 2 passes) to every row for no benefit.
const placeholderPassword = "imported-user-must-reset"

// defaultTypeMap maps stock Drupal bundles onto oCMS page types.
const defaultTypeMap = "article:post,page:page"

// oCMS page types. These mirror internal/handler's constants, redeclared here
// because a module must not import the admin handler package.
const (
	pageTypePost = "post"
	pageTypePage = "page"
)

// Source implements types.Source for Drupal.
type Source struct{}

// NewSource creates a new Drupal source.
func NewSource() *Source { return &Source{} }

// Name returns the unique identifier for this source.
func (s *Source) Name() string { return "drupal" }

// DisplayName returns the human-readable name.
func (s *Source) DisplayName() string { return "Drupal" }

// Description returns the i18n key for this source's description.
func (s *Source) Description() string { return "drupal.description" }

// ConfigFields returns the configuration fields needed for this source.
// Labels and placeholders are i18n keys, resolved by the admin view.
func (s *Source) ConfigFields() []types.ConfigField {
	return []types.ConfigField{
		{Name: "mysql_host", Label: "drupal.field_mysql_host", Type: "text", Required: true, Default: shared.EnvOrDefault("DRUPAL_HOST", "localhost")},
		{Name: "mysql_port", Label: "drupal.field_mysql_port", Type: "number", Required: true, Default: shared.EnvOrDefault("DRUPAL_PORT", "3306")},
		{Name: "mysql_user", Label: "drupal.field_mysql_user", Type: "text", Required: true, Default: os.Getenv("DRUPAL_USER")},
		{Name: "mysql_password", Label: "drupal.field_mysql_password", Type: "password", Required: true, Default: os.Getenv("DRUPAL_PASSWORD")},
		{Name: "mysql_database", Label: "drupal.field_mysql_database", Type: "text", Required: true, Default: os.Getenv("DRUPAL_DB")},
		{Name: "table_prefix", Label: "drupal.field_table_prefix", Type: "text", Required: false, Default: os.Getenv("DRUPAL_PREFIX"), Placeholder: "drupal.placeholder_table_prefix"},
		{Name: "files_path", Label: "drupal.field_files_path", Type: "text", Required: false, Default: os.Getenv("DRUPAL_FILES"), Placeholder: "drupal.placeholder_files_path"},
		{Name: "type_map", Label: "drupal.field_type_map", Type: "text", Required: false, Default: shared.EnvOrDefault("DRUPAL_TYPE_MAP", defaultTypeMap), Placeholder: "drupal.placeholder_type_map"},
		{Name: "tag_vocabularies", Label: "drupal.field_tag_vocabularies", Type: "text", Required: false, Default: shared.EnvOrDefault("DRUPAL_TAG_VOCABULARIES", "tags"), Placeholder: "drupal.placeholder_tag_vocabularies"},
	}
}

// TestConnection connects to the Drupal database and reports what was found.
// The returned error doubles as the admin-facing summary, so a successful test
// still tells the operator which bundles exist and which tables are missing.
func (s *Source) TestConnection(cfg map[string]string) error {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout+readTimeout)
	defer cancel()

	reader, err := s.openReader(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeReader(reader)

	if _, err := reader.NodeCount(ctx); err != nil {
		return fmt.Errorf("failed to query %s: %w", tableNodeData, err)
	}
	bundles, err := reader.Bundles(ctx)
	if err != nil {
		return fmt.Errorf("failed to read node bundles: %w", err)
	}

	slog.Info("drupal connection test succeeded",
		"bundles", len(bundles),
		"missing_optional_tables", reader.Schema().MissingOptional())
	return nil
}

// Summary describes what a connection test found. It is returned separately
// from TestConnection so the handler can render it without parsing an error.
type Summary struct {
	Bundles      map[string]int
	Nodes        int
	Translations int
	Missing      []string
}

// Inspect connects and reports what the source database contains.
func (s *Source) Inspect(ctx context.Context, cfg map[string]string) (*Summary, error) {
	reader, err := s.openReader(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer closeReader(reader)

	nodes, err := reader.NodeCount(ctx)
	if err != nil {
		return nil, err
	}
	translations, err := reader.TranslationCount(ctx)
	if err != nil {
		return nil, err
	}
	bundles, err := reader.Bundles(ctx)
	if err != nil {
		return nil, err
	}

	return &Summary{
		Bundles:      bundles,
		Nodes:        nodes,
		Translations: translations,
		Missing:      reader.Schema().MissingOptional(),
	}, nil
}

// openReader builds the DSN and opens a source reader.
func (s *Source) openReader(ctx context.Context, cfg map[string]string) (*Reader, error) {
	dsn, err := BuildDSN(cfg)
	if err != nil {
		return nil, err
	}
	return NewReader(ctx, dsn, cfg["table_prefix"])
}

// closeReader closes a reader, logging rather than propagating close errors.
func closeReader(r *Reader) {
	if r == nil {
		return
	}
	if err := r.Close(); err != nil {
		slog.Error("failed to close drupal reader", "error", err)
	}
}

// sourceReader is the read side of a Drupal database, as the import stages use
// it.
//
// The stages take this interface rather than *Reader so they can be driven by
// an in-memory fake in tests. Without it the only way to exercise importNodes
// or importMenus would be a live MySQL server, which is why the Elefant
// source's equivalent logic has no real coverage.
type sourceReader interface {
	Schema() Schema
	NodeCount(ctx context.Context) (int, error)
	TranslationCount(ctx context.Context) (int, error)
	GetUsers(ctx context.Context) ([]User, error)
	GetTerms(ctx context.Context) ([]Term, error)
	GetFiles(ctx context.Context) ([]File, error)
	MediaUUIDsByFile(ctx context.Context) (map[int64][]string, error)
	GetNodes(ctx context.Context, offset int) ([]Node, error)
	NodeImages(ctx context.Context) (map[int64]int64, error)
	NodeTerms(ctx context.Context) (map[int64][]int64, error)
	GetPathAliases(ctx context.Context) ([]PathAlias, error)
	GetMenuLinks(ctx context.Context) ([]MenuLink, error)
}

// Compile-time proof that the live reader satisfies the stage interface.
var _ sourceReader = (*Reader)(nil)

// importState carries the cross-stage lookups an import builds up.
type importState struct {
	queries     *store.Queries
	reader      sourceReader
	result      *types.ImportResult
	tracker     types.ImportTracker
	opts        types.ImportOptions
	defaultLang string
	authorID    int64
	typeMap     map[string]string
	tagVocabs   map[string]bool
	uploadDir   string
	filesPath   string
	users       map[int64]int64  // Drupal uid -> oCMS user id
	tags        map[int64]int64  // Drupal tid -> oCMS tag id
	categories  map[int64]int64  // Drupal tid -> oCMS category id
	mediaByFID  map[int64]int64  // Drupal fid -> oCMS media id
	nodes       map[int64]int64  // Drupal nid -> oCMS page id
	aliasByNode map[int64]string // Drupal nid -> canonical path alias
	refs        *MediaRefs
}

// Import copies content from Drupal into oCMS.
//
// Stage order is forced by referential integrity and by body rewriting: users
// before nodes (pages.author_id is ON DELETE RESTRICT), taxonomy before nodes
// (so terms can be linked), media before nodes (so bodies can be rewritten to
// the new URLs), and menus last (so they can point at imported pages).
//
// Writes are per entity and autocommitted. Wrapping the import in one
// transaction would hold SQLite's single write lock for its whole duration and
// starve every other writer; the tracking table provides the undo path instead.
func (s *Source) Import(ctx context.Context, db *sql.DB, cfg map[string]string, opts types.ImportOptions, tracker types.ImportTracker) (*types.ImportResult, error) {
	result := &types.ImportResult{}

	types.Report(ctx, tracker, types.Progress{Source: s.Name(), Phase: types.EntityUser})

	reader, err := s.openReader(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer closeReader(reader)

	queries := store.New(db)

	authorID, err := defaultAuthorID(ctx, queries)
	if err != nil {
		return nil, err
	}
	defaultLang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get default language: %w", err)
	}

	st := &importState{
		queries:     queries,
		reader:      reader,
		result:      result,
		tracker:     tracker,
		opts:        opts,
		defaultLang: defaultLang.Code,
		authorID:    authorID,
		typeMap:     ParseTypeMap(cfg["type_map"]),
		tagVocabs:   parseVocabularyList(cfg["tag_vocabularies"]),
		uploadDir:   shared.UploadDir(),
		filesPath:   strings.TrimSpace(cfg["files_path"]),
		users:       make(map[int64]int64),
		tags:        make(map[int64]int64),
		categories:  make(map[int64]int64),
		mediaByFID:  make(map[int64]int64),
		nodes:       make(map[int64]int64),
		aliasByNode: make(map[int64]string),
		refs:        NewMediaRefs(),
	}

	for _, missing := range reader.Schema().MissingOptional() {
		result.AddNotice("optional table %q not found in source database; related content skipped", missing)
	}

	stages := []struct {
		phase types.EntityType
		run   func(context.Context, *importState) error
	}{
		{types.EntityUser, s.importUsers},
		{types.EntityTag, s.importTaxonomy},
		{types.EntityMedia, s.importMedia},
		{types.EntityPage, s.importNodes},
		{types.EntityMenu, s.importMenus},
	}

	for _, stage := range stages {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		types.Report(ctx, tracker, types.Progress{Source: s.Name(), Phase: stage.phase})
		if err := stage.run(ctx, st); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return result, err
			}
			// A stage failure is reported and the import continues: a missing
			// taxonomy table should not cost the admin their pages.
			result.AddError("%s import failed: %v", stage.phase, err)
		}
	}

	if translations, err := reader.TranslationCount(ctx); err == nil && translations > 0 {
		result.AddNotice("%d non-default-language node translations were not imported", translations)
	}

	types.Report(ctx, tracker, types.Progress{Source: s.Name(), Phase: types.EntityPage})
	if result.PostsImported > 0 || result.PagesImported > 0 {
		if err := service.NewSearchService(db).RebuildIndex(ctx); err != nil {
			result.AddError("failed to rebuild search index: %v", err)
		}
	}

	return result, nil
}

// defaultAuthorID returns the user that owns imported content when the Drupal
// author cannot be mapped.
func defaultAuthorID(ctx context.Context, queries *store.Queries) (int64, error) {
	users, err := queries.ListUsers(ctx, store.ListUsersParams{Limit: 1, Offset: 0})
	if err != nil {
		return 0, fmt.Errorf("failed to find a default author: %w", err)
	}
	if len(users) == 0 {
		return 0, fmt.Errorf("no oCMS users exist to own imported content")
	}
	return users[0].ID, nil
}

// ParseTypeMap parses a "bundle:page_type,bundle:page_type" mapping.
//
// Unknown or malformed entries are ignored rather than failing the import, and
// any bundle not listed falls back to "page" at lookup time — an operator
// mistyping one bundle should not stop the other content from arriving.
func ParseTypeMap(raw string) map[string]string {
	result := make(map[string]string)
	if strings.TrimSpace(raw) == "" {
		raw = defaultTypeMap
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			continue
		}
		bundle := strings.ToLower(strings.TrimSpace(parts[0]))
		pageType := strings.ToLower(strings.TrimSpace(parts[1]))
		if bundle == "" {
			continue
		}
		if pageType != pageTypePost && pageType != pageTypePage {
			continue
		}
		result[bundle] = pageType
	}
	return result
}

// PageTypeFor resolves a Drupal bundle to an oCMS page type, defaulting to
// "page" for bundles the operator did not map.
func PageTypeFor(typeMap map[string]string, bundle string) string {
	if pageType, ok := typeMap[strings.ToLower(bundle)]; ok {
		return pageType
	}
	return pageTypePage
}

// parseVocabularyList parses a comma-separated vocabulary list.
func parseVocabularyList(raw string) map[string]bool {
	result := make(map[string]bool)
	for _, name := range strings.Split(raw, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			result[name] = true
		}
	}
	if len(result) == 0 {
		result["tags"] = true
	}
	return result
}

// importUsers copies Drupal accounts in as public users.
//
// Roles are deliberately not carried over: importing a foreign system's
// "administrator" straight into oCMS admin would be a privilege-escalation
// footgun, so every imported account lands as RolePublic and an operator
// promotes deliberately.
func (s *Source) importUsers(ctx context.Context, st *importState) error {
	if !st.opts.ImportUsers {
		return nil
	}

	users, err := st.reader.GetUsers(ctx)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}

	passwordHash, err := auth.HashPassword(placeholderPassword)
	if err != nil {
		return fmt.Errorf("failed to generate placeholder password hash: %w", err)
	}

	now := time.Now()
	for i, u := range users {
		if err := ctx.Err(); err != nil {
			return err
		}

		if existing, lookupErr := st.queries.GetUserByEmail(ctx, u.Mail); lookupErr == nil {
			// Always remember the mapping so nodes authored by this Drupal user
			// still resolve, whether or not the row was created by this import.
			st.users[u.UID] = existing.ID
			st.result.UsersSkipped++
			continue
		}

		name := u.Name
		if name == "" {
			name = u.Mail
		}

		created, err := st.queries.CreateUser(ctx, store.CreateUserParams{
			Email:        u.Mail,
			PasswordHash: passwordHash,
			Role:         model.RolePublic,
			Name:         name,
			CreatedAt:    unixOrNow(u.Created, now),
			UpdatedAt:    now,
		})
		if err != nil {
			st.result.AddError("failed to create user %q: %v", u.Mail, err)
			continue
		}

		st.users[u.UID] = created.ID
		st.track(ctx, types.EntityUser, created.ID)
		st.result.UsersImported++
		st.report(ctx, types.EntityUser, i+1, len(users))
	}
	return nil
}

// importTaxonomy copies Drupal taxonomy terms into oCMS tags and categories.
//
// Vocabularies listed in tag_vocabularies become flat tags; every other
// vocabulary becomes categories, which are hierarchical. Parents are linked in
// a second pass because a child term can appear before its parent.
func (s *Source) importTaxonomy(ctx context.Context, st *importState) error {
	if !st.opts.ImportTags && !st.opts.ImportCategories {
		return nil
	}

	terms, err := st.reader.GetTerms(ctx)
	if err != nil {
		return err
	}
	if len(terms) == 0 {
		return nil
	}

	now := time.Now()
	for i, t := range terms {
		if err := ctx.Err(); err != nil {
			return err
		}
		if t.Name == "" {
			continue
		}

		if st.tagVocabs[strings.ToLower(t.Vocabulary)] {
			if st.opts.ImportTags {
				s.importTag(ctx, st, t, now)
			}
			continue
		}
		if st.opts.ImportCategories {
			s.importCategory(ctx, st, t, now)
		}
		st.report(ctx, types.EntityTag, i+1, len(terms))
	}

	if st.opts.ImportCategories {
		s.linkCategoryParents(ctx, st, terms, now)
	}
	return nil
}

// importTag creates or reuses a tag for a Drupal term.
func (s *Source) importTag(ctx context.Context, st *importState, t Term, now time.Time) {
	slug := util.Slugify(t.Name)
	if slug == "" {
		return
	}

	if existing, err := st.queries.GetTagBySlug(ctx, slug); err == nil {
		st.tags[t.TID] = existing.ID
		st.result.TagsSkipped++
		return
	}

	tag, err := st.queries.CreateTag(ctx, store.CreateTagParams{
		Name:         t.Name,
		Slug:         slug,
		LanguageCode: st.defaultLang,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		st.result.AddError("failed to create tag %q: %v", t.Name, err)
		return
	}

	st.tags[t.TID] = tag.ID
	st.track(ctx, types.EntityTag, tag.ID)
	st.result.TagsImported++
}

// importCategory creates or reuses a category for a Drupal term. The parent is
// linked separately by linkCategoryParents.
func (s *Source) importCategory(ctx context.Context, st *importState, t Term, now time.Time) {
	slug := util.Slugify(t.Name)
	if slug == "" {
		return
	}

	if existing, err := st.queries.GetCategoryBySlug(ctx, slug); err == nil {
		st.categories[t.TID] = existing.ID
		st.result.CategoriesSkipped++
		return
	}

	category, err := st.queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name:         t.Name,
		Slug:         slug,
		Description:  t.Description,
		ParentID:     sql.NullInt64{},
		Position:     t.Weight,
		LanguageCode: st.defaultLang,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		st.result.AddError("failed to create category %q: %v", t.Name, err)
		return
	}

	st.categories[t.TID] = category.ID
	st.track(ctx, types.EntityCategory, category.ID)
	st.result.CategoriesImported++
}

// linkCategoryParents applies Drupal's term hierarchy once every category row
// exists, so a child created before its parent still gets linked.
func (s *Source) linkCategoryParents(ctx context.Context, st *importState, terms []Term, now time.Time) {
	for _, t := range terms {
		if t.ParentTID == 0 {
			continue
		}
		childID, ok := st.categories[t.TID]
		if !ok {
			continue
		}
		parentID, ok := st.categories[t.ParentTID]
		if !ok || parentID == childID {
			continue
		}

		category, err := st.queries.GetCategoryByID(ctx, childID)
		if err != nil {
			continue
		}
		if _, err := st.queries.UpdateCategory(ctx, store.UpdateCategoryParams{
			ID:           childID,
			Name:         category.Name,
			Slug:         category.Slug,
			Description:  category.Description,
			ParentID:     sql.NullInt64{Int64: parentID, Valid: true},
			Position:     category.Position,
			LanguageCode: category.LanguageCode,
			UpdatedAt:    now,
		}); err != nil {
			st.result.AddError("failed to link category %q to its parent: %v", category.Name, err)
		}
	}
}

// track records a created entity so the module can undo the import later.
func (st *importState) track(ctx context.Context, entityType types.EntityType, id int64) {
	if st.tracker == nil {
		return
	}
	if err := st.tracker.TrackImportedItem(ctx, "drupal", string(entityType), id); err != nil {
		slog.Warn("failed to track imported item", "type", entityType, "id", id, "error", err)
	}
}

// report publishes progress for the current stage.
func (st *importState) report(ctx context.Context, phase types.EntityType, processed, total int) {
	types.Report(ctx, st.tracker, types.Progress{
		Source:    "drupal",
		Phase:     phase,
		Processed: processed,
		Total:     total,
	})
}

// unixOrNow converts a Drupal Unix timestamp, falling back to now when unset.
func unixOrNow(ts int64, now time.Time) time.Time {
	if ts <= 0 {
		return now
	}
	return time.Unix(ts, 0).UTC()
}

// resolveFilePath maps a Drupal file URI onto a path inside the files root,
// confirming the result stays within that root.
//
// Only the public:// stream is importable — private:// and temporary:// live
// outside the public files directory and are reported rather than guessed at.
func resolveFilePath(f File, cleanRoot, realRoot string) (string, error) {
	switch f.Scheme() {
	case "public", "":
	case "private", "temporary":
		return "", fmt.Errorf("file uses the %s:// stream and was not imported", f.Scheme())
	default:
		return "", fmt.Errorf("file uses the unsupported %s:// stream", f.Scheme())
	}

	rel := f.RelPath()
	if rel == "" {
		return "", fmt.Errorf("file has an empty path")
	}
	if strings.Contains(rel, "..") {
		return "", fmt.Errorf("file path %q contains a traversal sequence", rel)
	}

	candidate := filepath.Join(cleanRoot, filepath.FromSlash(rel))
	resolved, ok := shared.ResolveWithinRoot(realRoot, candidate)
	if !ok {
		return "", fmt.Errorf("file %q is missing or resolves outside the files directory", rel)
	}
	return resolved, nil
}

// importMedia copies Drupal's managed files into the oCMS media library and
// builds the lookups used to rewrite body HTML.
//
// file_managed drives the import rather than a directory walk, so only files
// Drupal actually knows about are copied and each one keeps its UUID — which is
// what lets <drupal-media> embeds be resolved later.
func (s *Source) importMedia(ctx context.Context, st *importState) error {
	if !st.opts.ImportMedia {
		return nil
	}
	if st.filesPath == "" {
		st.result.AddNotice("no files path configured; media was not imported")
		return nil
	}

	files, err := st.reader.GetFiles(ctx)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}

	cleanRoot, realRoot, err := shared.ResolveMediaRoot(st.filesPath)
	if err != nil {
		return err
	}

	// A media-library site addresses embeds by media UUID, not file UUID, so
	// both have to land in ByUUID. A classic image-field site has no media
	// table and this is simply empty.
	mediaUUIDs, err := st.reader.MediaUUIDsByFile(ctx)
	if err != nil {
		st.result.AddError("failed to read media entity references: %v", err)
		mediaUUIDs = map[int64][]string{}
	}

	processor := imaging.NewProcessor(st.uploadDir)
	now := time.Now()
	skippedMimes := make(map[string]int)

	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}

		mimeType := f.MimeType
		if mimeType == "" {
			mimeType = shared.MimeTypeFromExt(f.Filename)
		}
		if !shared.IsAllowedMediaMime(mimeType) {
			// Counted and reported after the loop: one notice per file would
			// exhaust the tracked-message cap on a site with many SVGs and bury
			// the per-file messages that carry more information.
			skippedMimes[mimeType]++
			st.result.MediaSkipped++
			continue
		}

		fullPath, err := resolveFilePath(f, cleanRoot, realRoot)
		if err != nil {
			st.result.AddNotice("%s: %v", f.Filename, err)
			st.result.MediaSkipped++
			continue
		}

		mediaID, publicURL, err := s.importOneFile(ctx, st, processor, f, fullPath, mimeType, now)
		if err != nil {
			st.result.AddError("failed to import %q: %v", f.Filename, err)
			continue
		}

		st.mediaByFID[f.FID] = mediaID
		st.refs.ByPath[f.RelPath()] = publicURL
		if f.UUID != "" {
			st.refs.ByUUID[f.UUID] = publicURL
		}
		for _, mediaUUID := range mediaUUIDs[f.FID] {
			st.refs.ByUUID[mediaUUID] = publicURL
		}
		st.refs.IsImg[publicURL] = processor.IsImage(mimeType)
		if f.Alt.Valid && f.Alt.String != "" {
			st.refs.AltMap[publicURL] = f.Alt.String
		}

		st.result.MediaImported++
		st.report(ctx, types.EntityMedia, i+1, len(files))
	}

	reportSkippedMimes(st, skippedMimes)
	return nil
}

// reportSkippedMimes turns the per-type skip tally into one notice per type.
//
// Without this a file skipped for its type left no trace at all: not an error,
// not a notice, and MediaSkipped is not rendered anywhere, so an SVG logo
// simply failed to appear with nothing to explain why.
func reportSkippedMimes(st *importState, skipped map[string]int) {
	mimeTypes := make([]string, 0, len(skipped))
	for mimeType := range skipped {
		mimeTypes = append(mimeTypes, mimeType)
	}
	sort.Strings(mimeTypes)

	for _, mimeType := range mimeTypes {
		st.result.AddNotice("%d file(s) of type %q were skipped; the type is not in the media allowlist",
			skipped[mimeType], mimeType)
	}
}

// importOneFile copies a single file onto disk and creates its media row.
func (s *Source) importOneFile(ctx context.Context, st *importState, processor *imaging.Processor,
	f File, fullPath, mimeType string, now time.Time) (int64, string, error) {

	src, err := os.Open(fullPath) // #nosec G304 -- path validated by resolveFilePath
	if err != nil {
		return 0, "", fmt.Errorf("failed to open file: %w", err)
	}

	fileUUID := uuid.New().String()
	params := store.CreateMediaParams{
		Uuid:         fileUUID,
		Filename:     f.Filename,
		MimeType:     mimeType,
		Size:         f.Size,
		Alt:          sql.NullString{String: shared.NullString(f.Alt), Valid: true},
		Caption:      sql.NullString{String: "", Valid: true},
		FolderID:     sql.NullInt64{},
		UploadedBy:   st.authorID,
		LanguageCode: st.defaultLang,
		CreatedAt:    unixOrNow(f.Created, now),
		UpdatedAt:    now,
	}

	var variantSource string
	if processor.IsImage(mimeType) {
		// Migration files come from a trusted local directory, so an oversized
		// photo is downscaled rather than dropped; the upload path keeps the
		// strict reject.
		processed, err := processor.ProcessImageWithOptions(src, fileUUID, f.Filename,
			imaging.ProcessOptions{DownscaleOversized: true})
		closeFile(src, fullPath)
		if err != nil {
			return 0, "", fmt.Errorf("failed to process image: %w", err)
		}
		if processed.Downscaled {
			st.result.AddNotice("%s: downscaled from %dx%d to %dx%d to fit the %dx%d limit",
				f.Filename, processed.OriginalWidth, processed.OriginalHeight,
				processed.Width, processed.Height, imaging.MaxImageWidth, imaging.MaxImageHeight)
		}
		params.MimeType = processed.MimeType
		params.Size = processed.Size
		params.Width = sql.NullInt64{Int64: int64(processed.Width), Valid: true}
		params.Height = sql.NullInt64{Int64: int64(processed.Height), Valid: true}
		variantSource = processed.FilePath
	} else {
		err := shared.SaveNonImageFile(src, st.uploadDir, fileUUID, f.Filename)
		closeFile(src, fullPath)
		if err != nil {
			return 0, "", fmt.Errorf("failed to copy file: %w", err)
		}
	}

	media, err := st.queries.CreateMedia(ctx, params)
	if err != nil {
		return 0, "", fmt.Errorf("failed to create media record: %w", err)
	}
	st.track(ctx, types.EntityMedia, media.ID)

	if variantSource != "" {
		// Variants are best-effort: a failure here costs a thumbnail, not the file.
		variants, _ := processor.CreateAllVariants(variantSource, fileUUID, f.Filename)
		for _, v := range variants {
			if _, err := st.queries.CreateMediaVariant(ctx, store.CreateMediaVariantParams{
				MediaID:   media.ID,
				Type:      v.Type,
				Width:     int64(v.Width),
				Height:    int64(v.Height),
				Size:      v.Size,
				CreatedAt: now,
			}); err != nil {
				slog.Warn("failed to create media variant", "media_id", media.ID, "type", v.Type, "error", err)
			}
		}
	}

	return media.ID, publicMediaURL(fileUUID, f.Filename), nil
}

// closeFile closes a source file, logging failures.
func closeFile(f *os.File, path string) {
	if err := f.Close(); err != nil {
		slog.Error("failed to close source file", "path", path, "error", err)
	}
}

// importNodes copies Drupal nodes into oCMS pages and posts, then writes their
// path aliases so the site's existing URLs keep working.
func (s *Source) importNodes(ctx context.Context, st *importState) error {
	if !st.opts.ImportPosts && !st.opts.ImportPages {
		return nil
	}

	if err := s.loadNodeAliases(ctx, st); err != nil {
		st.result.AddError("failed to read path aliases: %v", err)
	}

	images, err := st.reader.NodeImages(ctx)
	if err != nil {
		st.result.AddError("failed to read node images: %v", err)
	}
	nodeTerms, err := st.reader.NodeTerms(ctx)
	if err != nil {
		st.result.AddError("failed to read node taxonomy references: %v", err)
	}

	total, err := st.reader.NodeCount(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	processed := 0
	for offset := 0; ; offset += batchSize {
		if err := ctx.Err(); err != nil {
			return err
		}

		nodes, err := st.reader.GetNodes(ctx, offset)
		if err != nil {
			return err
		}
		if len(nodes) == 0 {
			break
		}

		for _, n := range nodes {
			if err := ctx.Err(); err != nil {
				return err
			}
			n.ImageFID = nullInt64From(images[n.NID])
			n.TermIDs = nodeTerms[n.NID]
			s.importNode(ctx, st, n, now)
			processed++
		}
		st.report(ctx, types.EntityPage, processed, total)

		if len(nodes) < batchSize {
			break
		}
	}

	reportUnresolvedEmbeds(st)
	s.importAliases(ctx, st, now)
	return nil
}

// reportUnresolvedEmbeds tells the admin how many media embeds were dropped.
//
// The count is aggregated rather than listed per UUID: a body full of embeds
// from a media library the importer could not read would otherwise fill the
// tracked-message budget with opaque identifiers.
func reportUnresolvedEmbeds(st *importState) {
	if len(st.refs.Unresolved) == 0 {
		return
	}
	unique := make(map[string]bool, len(st.refs.Unresolved))
	for _, uuid := range st.refs.Unresolved {
		unique[uuid] = true
	}
	st.result.AddNotice("%d media embed(s) referencing %d unknown media item(s) were removed "+
		"from page bodies; the referenced files were not imported",
		len(st.refs.Unresolved), len(unique))
}

// loadNodeAliases records the best path alias per node. Drupal keeps every
// historical alias, so the highest id — the most recent — wins as the canonical
// slug and the rest are still written as redirect aliases.
func (s *Source) loadNodeAliases(ctx context.Context, st *importState) error {
	aliases, err := st.reader.GetPathAliases(ctx)
	if err != nil {
		return err
	}
	for _, a := range aliases {
		nid, ok := a.NodeID()
		if !ok {
			continue
		}
		st.aliasByNode[nid] = strings.TrimPrefix(a.Alias, "/")
	}
	return nil
}

// importNode converts one Drupal node into an oCMS page.
func (s *Source) importNode(ctx context.Context, st *importState, n Node, now time.Time) {
	pageType := PageTypeFor(st.typeMap, n.Type)
	if pageType == pageTypePost && !st.opts.ImportPosts {
		return
	}
	if pageType == pageTypePage && !st.opts.ImportPages {
		return
	}

	baseSlug := s.slugForNode(st, n)
	if baseSlug == "" {
		st.result.AddError("node %d has no usable title or alias; skipped", n.NID)
		return
	}

	if st.opts.SkipExisting {
		if _, err := st.queries.GetPageBySlug(ctx, baseSlug); err == nil {
			countSkipped(st.result, pageType)
			return
		}
	}
	slug := shared.MakeUniqueSlug(ctx, st.queries, baseSlug)

	body := RewriteBody(shared.NullString(n.Body), st.refs)
	body = security.SanitizePageHTML(body)

	status := model.PageStatusDraft
	var publishedAt sql.NullTime
	if n.IsPublished() {
		status = model.PageStatusPublished
		publishedAt = sql.NullTime{Time: unixOrNow(n.Created, now), Valid: true}
	}

	authorID := st.authorID
	if mapped, ok := st.users[n.UID]; ok {
		authorID = mapped
	}

	page, err := st.queries.CreatePage(ctx, store.CreatePageParams{
		Title:           n.Title,
		Slug:            slug,
		Body:            body,
		Summary:         summaryFor(n),
		Status:          status,
		AuthorID:        authorID,
		FeaturedImageID: st.featuredImageID(n),
		MetaTitle:       n.Title,
		MetaDescription: summaryFor(n),
		LanguageCode:    st.defaultLang,
		PageType:        pageType,
		PublishedAt:     publishedAt,
		CreatedAt:       unixOrNow(n.Created, now),
		UpdatedAt:       unixOrNow(n.Changed, now),
	})
	if err != nil {
		st.result.AddError("failed to create page for node %d (%q): %v", n.NID, n.Title, err)
		return
	}

	st.nodes[n.NID] = page.ID
	st.track(ctx, entityTypeFor(pageType), page.ID)
	countImported(st.result, pageType)

	s.linkNodeTerms(ctx, st, n, page.ID)
}

// slugForNode picks a node's slug: its Drupal alias when it has one, so URLs
// carry over, otherwise a slug derived from the title.
func (s *Source) slugForNode(st *importState, n Node) string {
	if alias, ok := st.aliasByNode[n.NID]; ok && alias != "" {
		segments := strings.Split(strings.Trim(alias, "/"), "/")
		candidate := util.Slugify(segments[len(segments)-1])
		if candidate != "" {
			return candidate
		}
	}
	return util.Slugify(n.Title)
}

// featuredImageID maps a node's Drupal image file onto an imported media row.
func (st *importState) featuredImageID(n Node) sql.NullInt64 {
	if !n.ImageFID.Valid {
		return sql.NullInt64{}
	}
	if mediaID, ok := st.mediaByFID[n.ImageFID.Int64]; ok {
		return sql.NullInt64{Int64: mediaID, Valid: true}
	}
	return sql.NullInt64{}
}

// linkNodeTerms attaches a node's taxonomy references to its oCMS page.
func (s *Source) linkNodeTerms(ctx context.Context, st *importState, n Node, pageID int64) {
	for _, tid := range n.TermIDs {
		if tagID, ok := st.tags[tid]; ok {
			if err := st.queries.AddTagToPage(ctx, store.AddTagToPageParams{PageID: pageID, TagID: tagID}); err != nil {
				slog.Warn("failed to link tag to page", "page_id", pageID, "tag_id", tagID, "error", err)
			}
			continue
		}
		if categoryID, ok := st.categories[tid]; ok {
			if err := st.queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{PageID: pageID, CategoryID: categoryID}); err != nil {
				slog.Warn("failed to link category to page", "page_id", pageID, "category_id", categoryID, "error", err)
			}
		}
	}
}

// importAliases writes Drupal path aliases against their imported pages so the
// site's old URLs keep resolving.
//
// An alias equal to the page's own slug is skipped — it would be a redundant
// row that also collides with the globally-unique alias index.
func (s *Source) importAliases(ctx context.Context, st *importState, now time.Time) {
	aliases, err := st.reader.GetPathAliases(ctx)
	if err != nil {
		st.result.AddError("failed to read path aliases: %v", err)
		return
	}

	for _, a := range aliases {
		nid, ok := a.NodeID()
		if !ok {
			continue
		}
		pageID, ok := st.nodes[nid]
		if !ok {
			continue
		}

		alias := strings.Trim(a.Alias, "/")
		if alias == "" || !util.IsValidAlias(alias) {
			continue
		}

		page, err := st.queries.GetPageByID(ctx, pageID)
		if err == nil && page.Slug == alias {
			continue
		}

		if _, err := st.queries.CreatePageAlias(ctx, store.CreatePageAliasParams{
			PageID:    pageID,
			Alias:     alias,
			CreatedAt: now,
		}); err != nil {
			// A duplicate alias is expected when two Drupal nodes shared one
			// path over time; it is not worth failing the import over.
			slog.Warn("failed to create page alias", "page_id", pageID, "alias", alias, "error", err)
			continue
		}
		st.track(ctx, types.EntityAlias, pageID)
		st.result.AliasesImported++
	}
}

// importMenus copies Drupal custom menu links into oCMS menus.
//
// An oCMS menu that already carries the Drupal menu's slug is reused rather
// than duplicated, and in that case only the menu items are tracked — tracking
// the menu would make "delete all imported content" destroy a menu the operator
// built by hand.
func (s *Source) importMenus(ctx context.Context, st *importState) error {
	if !st.opts.ImportMenus {
		return nil
	}

	links, err := st.reader.GetMenuLinks(ctx)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		return nil
	}

	byMenu := make(map[string][]MenuLink)
	var order []string
	for _, l := range links {
		if l.MenuName == "" {
			continue
		}
		if _, seen := byMenu[l.MenuName]; !seen {
			order = append(order, l.MenuName)
		}
		byMenu[l.MenuName] = append(byMenu[l.MenuName], l)
	}
	sort.Strings(order)

	now := time.Now()
	for i, menuName := range order {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.importMenu(ctx, st, menuName, byMenu[menuName], now)
		st.report(ctx, types.EntityMenu, i+1, len(order))
	}
	return nil
}

// importMenu creates or reuses one menu and imports its links.
func (s *Source) importMenu(ctx context.Context, st *importState, menuName string, links []MenuLink, now time.Time) {
	slug := util.Slugify(menuName)
	if slug == "" {
		return
	}

	menuID, created, err := s.ensureMenu(ctx, st, menuName, slug, now)
	if err != nil {
		st.result.AddError("failed to create menu %q: %v", menuName, err)
		return
	}
	if created {
		st.track(ctx, types.EntityMenu, menuID)
		st.result.MenusImported++
	} else {
		st.result.MenusSkipped++
	}

	itemByUUID := make(map[string]int64, len(links))

	// First pass: create every item at the root so a child whose parent appears
	// later in the list still gets created.
	for _, l := range links {
		if l.Title == "" {
			continue
		}
		pageID, url, err := s.resolveMenuTarget(st, l)
		if err != nil {
			st.result.AddNotice("menu %q: %v", menuName, err)
			continue
		}

		item, err := st.queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
			MenuID:    menuID,
			ParentID:  sql.NullInt64{},
			Title:     l.Title,
			Url:       url,
			Target:    sql.NullString{String: "_self", Valid: true},
			PageID:    pageID,
			Position:  l.Weight,
			CssClass:  sql.NullString{String: "", Valid: true},
			IsActive:  l.Enabled != 0,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			st.result.AddError("failed to create menu item %q: %v", l.Title, err)
			continue
		}

		if l.UUID != "" {
			itemByUUID[l.UUID] = item.ID
		}
		st.track(ctx, types.EntityMenuItem, item.ID)
		st.result.MenuItemsImported++
	}

	// Second pass: apply the hierarchy now that every item exists.
	s.linkMenuParents(ctx, st, links, itemByUUID, menuID, now)
}

// ensureMenu returns the ID of the oCMS menu for a Drupal menu name, creating
// it when absent. created reports whether this import made the row.
func (s *Source) ensureMenu(ctx context.Context, st *importState, menuName, slug string, now time.Time) (int64, bool, error) {
	if existing, err := st.queries.GetMenuBySlug(ctx, slug); err == nil {
		return existing.ID, false, nil
	}

	menu, err := st.queries.CreateMenu(ctx, store.CreateMenuParams{
		Name:         menuName,
		Slug:         slug,
		LanguageCode: st.defaultLang,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		return 0, false, err
	}
	return menu.ID, true, nil
}

// resolveMenuTarget maps a Drupal link URI onto an oCMS page reference or URL.
func (s *Source) resolveMenuTarget(st *importState, l MenuLink) (sql.NullInt64, sql.NullString, error) {
	nodeID, url, err := ResolveLinkURI(l.LinkURI)
	if err != nil {
		return sql.NullInt64{}, sql.NullString{}, fmt.Errorf("link %q: %w", l.Title, err)
	}

	if nodeID != 0 {
		pageID, ok := st.nodes[nodeID]
		if !ok {
			return sql.NullInt64{}, sql.NullString{}, fmt.Errorf("link %q points at node %d, which was not imported", l.Title, nodeID)
		}
		return sql.NullInt64{Int64: pageID, Valid: true}, sql.NullString{String: "", Valid: true}, nil
	}

	return sql.NullInt64{}, sql.NullString{String: url, Valid: true}, nil
}

// linkMenuParents applies the Drupal menu hierarchy to the created items.
func (s *Source) linkMenuParents(ctx context.Context, st *importState, links []MenuLink,
	itemByUUID map[string]int64, menuID int64, now time.Time) {

	for _, l := range links {
		parentUUID := l.ParentUUID()
		if parentUUID == "" || l.UUID == "" {
			continue
		}
		childID, ok := itemByUUID[l.UUID]
		if !ok {
			continue
		}
		parentID, ok := itemByUUID[parentUUID]
		if !ok || parentID == childID {
			continue
		}

		item, err := st.queries.GetMenuItemByID(ctx, childID)
		if err != nil {
			continue
		}
		if _, err := st.queries.UpdateMenuItem(ctx, store.UpdateMenuItemParams{
			ID:        childID,
			ParentID:  sql.NullInt64{Int64: parentID, Valid: true},
			Title:     item.Title,
			Url:       item.Url,
			Target:    item.Target,
			PageID:    item.PageID,
			Position:  item.Position,
			CssClass:  item.CssClass,
			IsActive:  item.IsActive,
			UpdatedAt: now,
		}); err != nil {
			st.result.AddError("failed to link menu item %q to its parent: %v", item.Title, err)
		}
	}
}

// summaryFor returns a node's teaser text.
func summaryFor(n Node) string {
	return strings.TrimSpace(shared.NullString(n.Summary))
}

// nullInt64From wraps a file ID, treating 0 as absent.
func nullInt64From(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

// entityTypeFor maps an oCMS page type onto its tracking entity type.
func entityTypeFor(pageType string) types.EntityType {
	if pageType == pageTypePost {
		return types.EntityPost
	}
	return types.EntityPage
}

// countImported increments the counter matching a page type.
func countImported(result *types.ImportResult, pageType string) {
	if pageType == pageTypePost {
		result.PostsImported++
		return
	}
	result.PagesImported++
}

// countSkipped increments the skip counter matching a page type.
func countSkipped(result *types.ImportResult, pageType string) {
	if pageType == pageTypePost {
		result.PostsSkipped++
		return
	}
	result.PagesSkipped++
}
