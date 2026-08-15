// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package drupal imports content from a Drupal 8/9/10/11 site into oCMS by
// reading its MySQL database and its public files directory.
//
// Only each entity's source translation is imported: Drupal marks that row with
// default_langcode = 1, but source translations can belong to several site
// languages. Additional translation rows are counted and reported as skipped.
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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/security"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// defaultTypeMap maps stock Drupal bundles onto oCMS page types.
const defaultTypeMap = "article:post,page:page"

// oCMS page types. These mirror internal/handler's constants, redeclared here
// because a module must not import the admin handler package.
const (
	pageTypePost            = "post"
	pageTypePage            = "page"
	trackingRollbackTimeout = 30 * time.Second
)

// PublicRouteChecker reports whether a concrete URL path belongs to a
// registered module. It is optional so the source remains usable by embedders
// that do not run oCMS's module registry.
type PublicRouteChecker interface {
	OwnsPublicPath(path string) bool
}

// Source implements types.Source for Drupal.
type Source struct {
	publicRouteChecker PublicRouteChecker
}

// NewSource creates a new Drupal source.
func NewSource() *Source { return &Source{} }

// SetPublicRouteChecker supplies the destination route ownership check used
// before creating taxonomy redirects.
func (s *Source) SetPublicRouteChecker(checker PublicRouteChecker) {
	s.publicRouteChecker = checker
}

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
		{Name: "public_files_url", Label: "drupal.field_public_files_url", Type: "text", Required: false, Default: os.Getenv("DRUPAL_PUBLIC_FILES_URL"), Placeholder: "drupal.placeholder_public_files_url"},
		{Name: "type_map", Label: "drupal.field_type_map", Type: "text", Required: false, Default: shared.EnvOrDefault("DRUPAL_TYPE_MAP", defaultTypeMap), Placeholder: "drupal.placeholder_type_map"},
		{Name: "tag_vocabularies", Label: "drupal.field_tag_vocabularies", Type: "text", Required: false, Default: shared.EnvOrDefault("DRUPAL_TAG_VOCABULARIES", "tags"), Placeholder: "drupal.placeholder_tag_vocabularies"},
	}
}

// TestConnection connects to the Drupal database and reports what was found.
// The returned error doubles as the admin-facing summary, so a successful test
// still tells the operator which bundles exist and which tables are missing.
func (s *Source) TestConnection(cfg map[string]string) error {
	return s.TestConnectionContext(context.Background(), cfg)
}

// TestConnectionContext performs the test with the request context as its
// parent, while retaining the source's own upper bound.
func (s *Source) TestConnectionContext(parent context.Context, cfg map[string]string) error {
	ctx, cancel := context.WithTimeout(parent, connectTimeout+readTimeout)
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
	Warnings() []string
	MediaUUIDsByFile(ctx context.Context) (map[int64][]string, error)
	GetNodeLanguages(ctx context.Context) (map[int64]string, error)
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
	users       map[int64]int64 // Drupal uid -> oCMS user id
	tags        map[int64]int64 // Drupal tid -> oCMS tag id
	categories  map[int64]int64 // Drupal tid -> oCMS category id
	// createdNodes holds only the pages this run created. Nodes skipped by
	// SkipExisting are still mapped in `nodes` so menu links resolve to the
	// page that already exists, but nothing may be *written* to those pages:
	// aliases attached to a page the import does not own survive the delete,
	// because aliases are removed only by cascade from a deleted page.
	createdNodes map[int64]bool
	// nodeLang records the langcode of each node this run imported, so the
	// redirect-alias pass can accept only that node's own aliases. Drupal
	// stores one alias row per language; attaching them all to the single
	// default-language page made a translated URL redirect to unrelated
	// content and could consume an alias another imported page needed.
	nodeLang map[int64]string
	// availableLangs are the oCMS language codes an imported node may be
	// assigned to, lowercased. Drupal's default_langcode = 1 marks each
	// entity's *source* translation, not the site's one default language, so a
	// multilingual site returns English, French and other originals together —
	// filing them all under defaultLang put content under the wrong locale.
	availableLangs map[string]bool
	// unmappedLangs counts nodes whose source language has no oCMS language, so
	// the fallback is reported once rather than per node.
	unmappedLangs map[string]int
	// unmappedTermLangs is separate because taxonomy can be imported without
	// pages; term fallbacks still need an operator-visible summary.
	unmappedTermLangs map[string]int
	neutralTermLangs  map[string]int
	unmappedMenuLangs map[string]int
	neutralMenuLangs  map[string]int
	// unknownFormats counts nodes whose Drupal text format is neither core HTML
	// nor plain text — a contrib format such as Markdown, which arrives
	// unrendered. Counted so it is reported once per format.
	unknownFormats map[string]int
	// claimed term slugs include rows reused from before this import as well as
	// rows it created. Each source term must map distinctly even when two names
	// normalize alike; tag/category slugs are globally unique in oCMS.
	claimedTagSlugs      map[string]bool
	claimedCategorySlugs map[string]bool
	// claimedMenuSlugs has the same per-source identity rule, but menu slugs are
	// unique only within the destination language.
	claimedMenuSlugs map[string]bool
	// createdCategories holds only the categories this run created. Parent
	// links are applied to those alone: a pre-existing category matched by slug
	// is mapped so content can reference it, but reparenting it would rewrite a
	// hierarchy the import does not own and cannot restore on delete.
	createdCategories   map[int64]bool
	mediaByFID          map[int64]int64  // Drupal fid -> oCMS media id
	nodes               map[int64]int64  // Drupal nid -> oCMS page id
	termURLs            map[int64]string // Drupal tid -> oCMS public tag/category URL
	termLang            map[int64]string // Drupal tid -> source langcode
	termDestinationLang map[int64]string // Drupal tid -> resolved oCMS language code
	// aliasByNode maps a Drupal nid to its aliases keyed by langcode. Aliases
	// are per-language, and only default-language nodes are imported, so
	// collapsing them to one entry let a translation's alias overwrite the
	// canonical slug of the page actually being imported.
	aliasByNode map[int64]map[string]string
	// pathAliases is loaded once and shared by the canonical-slug, page-alias,
	// and taxonomy-redirect passes.
	pathAliases        []PathAlias
	aliasesLoaded      bool
	aliasLoadErr       error
	aliasErrorReported bool
	// Every concrete legacy URL has one deterministic owner. Keys include the
	// public language prefix for non-default content (for example /fr/about),
	// so aliases that are identical in Drupal but belong to different
	// languages do not incorrectly shadow one another.
	// nodeID is non-zero for a node owner and zero for a taxonomy owner.
	aliasReservations map[string]aliasReservation
	// Page slugs are globally unique in storage even though public routes are
	// language-scoped. A single-segment source alias therefore chooses one
	// deterministic node that may retain the unsuffixed stored slug. A
	// default-language owner wins regardless of source-row/import order; other
	// language owners keep their concrete legacy URLs through tracked redirects.
	aliasSlugOwners map[string]int64
	refs            *MediaRefs
}

type aliasReservation struct {
	nodeID int64
}

// mediaSource is the capability needed by importOneFile. The live
// shared.MediaRoot returns *os.File; the interface also lets tests inject a
// reader whose Close fails and prove partial output is compensated.
type mediaSource interface {
	Open(relativePath string) (sourceFile, error)
}

type sourceFile interface {
	Read([]byte) (int, error)
	Seek(int64, int) (int64, error)
	Close() error
}

type mediaRootSource struct {
	root *shared.MediaRoot
}

func (s mediaRootSource) Open(relativePath string) (sourceFile, error) {
	return s.root.Open(relativePath)
}

// newImportState builds an import state with every lookup allocated.
//
// It exists so the maps are initialized in exactly one place. There were two
// hand-maintained literals — one here, one in the test helper — and adding a
// field to the struct silently left the test's copy nil until an import
// panicked on the first write to it.
func newImportState(queries *store.Queries, reader sourceReader, result *types.ImportResult,
	tracker types.ImportTracker, opts types.ImportOptions, defaultLang string, authorID int64) *importState {

	st := &importState{
		queries:              queries,
		reader:               reader,
		result:               result,
		tracker:              tracker,
		opts:                 opts,
		defaultLang:          defaultLang,
		authorID:             authorID,
		typeMap:              ParseTypeMap(""),
		tagVocabs:            parseVocabularyList("tags"),
		users:                make(map[int64]int64),
		tags:                 make(map[int64]int64),
		categories:           make(map[int64]int64),
		createdNodes:         make(map[int64]bool),
		createdCategories:    make(map[int64]bool),
		claimedTagSlugs:      make(map[string]bool),
		claimedCategorySlugs: make(map[string]bool),
		claimedMenuSlugs:     make(map[string]bool),
		mediaByFID:           make(map[int64]int64),
		nodes:                make(map[int64]int64),
		termURLs:             make(map[int64]string),
		termLang:             make(map[int64]string),
		termDestinationLang:  make(map[int64]string),
		aliasByNode:          make(map[int64]map[string]string),
		aliasReservations:    make(map[string]aliasReservation),
		aliasSlugOwners:      make(map[string]int64),
		nodeLang:             make(map[int64]string),
		availableLangs:       make(map[string]bool),
		unmappedLangs:        make(map[string]int),
		unmappedTermLangs:    make(map[string]int),
		neutralTermLangs:     make(map[string]int),
		unmappedMenuLangs:    make(map[string]int),
		neutralMenuLangs:     make(map[string]int),
		unknownFormats:       make(map[string]int),
		refs:                 NewMediaRefs(),
	}
	st.addAvailableLanguage(defaultLang)
	return st
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
	defaultLang, err := shared.RoutableDefaultLanguage(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("failed to get default language: %w", err)
	}

	st := newImportState(queries, reader, result, tracker, opts, defaultLang.Code, authorID)
	if langs, err := queries.ListActiveLanguages(ctx); err != nil {
		result.AddError("failed to read configured languages; all content will use %q: %v",
			defaultLang.Code, err)
	} else {
		for _, l := range langs {
			st.addAvailableLanguage(l.Code)
		}
	}
	st.typeMap = ParseTypeMap(cfg["type_map"])
	st.tagVocabs = parseVocabularyList(cfg["tag_vocabularies"])
	st.uploadDir = shared.UploadDir()
	st.filesPath = strings.TrimSpace(cfg["files_path"])
	st.refs.ExtraPrefix = NormalizeFilePrefix(cfg["public_files_url"])

	for _, missing := range reader.Schema().MissingOptional() {
		result.AddNotice("optional table %q not found in source database; related content skipped", missing)
	}

	if err := s.runImportStages(ctx, st); err != nil {
		return result, err
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

func (s *Source) runImportStages(ctx context.Context, st *importState) error {
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
			return err
		}
		types.Report(ctx, st.tracker, types.Progress{Source: s.Name(), Phase: stage.phase})
		stageErr := stage.run(ctx, st)
		st.flushReaderWarnings()
		if stageErr != nil {
			if errors.Is(stageErr, context.Canceled) || errors.Is(stageErr, context.DeadlineExceeded) {
				return stageErr
			}
			// A stage failure is reported and the import continues: a missing
			// taxonomy table should not cost the admin their pages.
			st.result.AddError("%s import failed: %v", stage.phase, stageErr)
		}
	}
	st.flushReaderWarnings()
	return nil
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

	// One unguessable hash shared by every user in this run: hashing per user
	// would add Argon2id's cost (19 MB, 2 passes) per row for no benefit, but
	// the secret must be random rather than a constant — see
	// shared.UnguessablePlaceholderHash.
	passwordHash, err := shared.UnguessablePlaceholderHash()
	if err != nil {
		return err
	}

	now := time.Now()
	for i, u := range users {
		if err := ctx.Err(); err != nil {
			return err
		}

		existing, lookupErr := st.queries.GetUserByEmail(ctx, u.Mail)
		switch {
		case lookupErr == nil:
			// Always remember the mapping so nodes authored by this Drupal user
			// still resolve, whether or not the row was created by this import.
			st.users[u.UID] = existing.ID
			st.result.UsersSkipped++
			continue
		case !errors.Is(lookupErr, sql.ErrNoRows):
			st.result.AddError("could not check for existing user %q: %v", u.Mail, lookupErr)
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

		if !st.track(ctx, types.EntityUser, created.ID, func(rollbackCtx context.Context) error {
			return st.queries.DeleteUser(rollbackCtx, created.ID)
		}) {
			continue
		}
		st.users[u.UID] = created.ID
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
	st.reportUnmappedTermLanguages()
	s.importTaxonomyAliases(ctx, st, now)
	return nil
}

// importTag creates or reuses a tag for a Drupal term.
func (s *Source) importTag(ctx context.Context, st *importState, t Term, now time.Time) {
	slug := util.Slugify(t.Name)
	if slug == "" {
		return
	}
	language := st.languageForTerm(t)
	st.termLang[t.TID] = t.Langcode

	existing, err := st.queries.GetTagBySlug(ctx, slug)
	switch {
	case err == nil:
		if !st.claimedTagSlugs[slug] && existing.LanguageCode == language {
			st.tags[t.TID] = existing.ID
			st.termURLs[t.TID] = taxonomyURL(language, st.defaultLang, "tag", existing.Slug)
			st.termDestinationLang[t.TID] = language
			st.claimedTagSlugs[slug] = true
			st.result.TagsSkipped++
			return
		}
		slug = uniqueTagSlug(ctx, st, slug)
	case !errors.Is(err, sql.ErrNoRows):
		// A failed lookup is not "the row is absent". Treating it as such made
		// the importer proceed to create, so a transient database error
		// surfaced later as a confusing unique-constraint failure.
		st.result.AddError("could not check for existing tag %q: %v", slug, err)
		return
	}

	tag, err := st.queries.CreateTag(ctx, store.CreateTagParams{
		Name:         t.Name,
		Slug:         slug,
		LanguageCode: language,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		st.result.AddError("failed to create tag %q: %v", t.Name, err)
		return
	}

	if !st.track(ctx, types.EntityTag, tag.ID, func(rollbackCtx context.Context) error {
		return st.queries.DeleteTag(rollbackCtx, tag.ID)
	}) {
		return
	}
	st.tags[t.TID] = tag.ID
	st.termURLs[t.TID] = taxonomyURL(language, st.defaultLang, "tag", tag.Slug)
	st.termDestinationLang[t.TID] = language
	st.claimedTagSlugs[tag.Slug] = true
	st.result.TagsImported++
}

// importCategory creates or reuses a category for a Drupal term. The parent is
// linked separately by linkCategoryParents.
func (s *Source) importCategory(ctx context.Context, st *importState, t Term, now time.Time) {
	slug := util.Slugify(t.Name)
	if slug == "" {
		return
	}
	language := st.languageForTerm(t)
	st.termLang[t.TID] = t.Langcode

	existing, err := st.queries.GetCategoryBySlug(ctx, slug)
	switch {
	case err == nil:
		if !st.claimedCategorySlugs[slug] && existing.LanguageCode == language {
			st.categories[t.TID] = existing.ID
			st.termURLs[t.TID] = taxonomyURL(language, st.defaultLang, "category", existing.Slug)
			st.termDestinationLang[t.TID] = language
			st.claimedCategorySlugs[slug] = true
			st.result.CategoriesSkipped++
			return
		}
		slug = uniqueCategorySlug(ctx, st, slug)
	case !errors.Is(err, sql.ErrNoRows):
		st.result.AddError("could not check for existing category %q: %v", slug, err)
		return
	}

	category, err := st.queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name:         t.Name,
		Slug:         slug,
		Description:  t.Description,
		ParentID:     sql.NullInt64{},
		Position:     t.Weight,
		LanguageCode: language,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		st.result.AddError("failed to create category %q: %v", t.Name, err)
		return
	}

	if !st.track(ctx, types.EntityCategory, category.ID, func(rollbackCtx context.Context) error {
		return st.queries.DeleteCategory(rollbackCtx, category.ID)
	}) {
		return
	}
	st.categories[t.TID] = category.ID
	st.termURLs[t.TID] = taxonomyURL(language, st.defaultLang, "category", category.Slug)
	st.termDestinationLang[t.TID] = language
	st.createdCategories[t.TID] = true
	st.claimedCategorySlugs[category.Slug] = true
	st.result.CategoriesImported++
}

// linkCategoryParents applies Drupal's term hierarchy once every category row
// exists, so a child created before its parent still gets linked.
func (s *Source) linkCategoryParents(ctx context.Context, st *importState, terms []Term, now time.Time) {
	for _, t := range terms {
		if t.ParentTID == 0 {
			continue
		}
		// Only reparent categories this import created.
		if !st.createdCategories[t.TID] {
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

// track records a created entity so the module can undo the import later. An
// untracked row is immediately compensated: otherwise deleting the import can
// never discover it. Callers must not publish maps/counters until this returns
// true.
func (st *importState) track(ctx context.Context, entityType types.EntityType, id int64,
	rollback func(context.Context) error) bool {
	if st.tracker == nil {
		return true
	}
	if err := st.tracker.TrackImportedItem(ctx, "drupal", string(entityType), id); err != nil {
		st.result.AddError("failed to track imported %s %d: %v", entityType, id, err)
		if rollback != nil {
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trackingRollbackTimeout)
			rollbackErr := rollback(rollbackCtx)
			cancel()
			if rollbackErr != nil {
				st.result.AddError("failed to roll back untracked %s %d: %v", entityType, id, rollbackErr)
			}
		}
		return false
	}
	return true
}

func (st *importState) cleanupMediaFiles(ctx context.Context, canonicalUploadRoot, mediaUUID string) error {
	err := imaging.DeleteMediaFilesFromCanonicalRoot(canonicalUploadRoot, mediaUUID)
	if err == nil {
		return nil
	}
	queuer, ok := st.tracker.(types.MediaCleanupQueuer)
	if !ok {
		return err
	}
	queueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trackingRollbackTimeout)
	defer cancel()
	if queueErr := queuer.QueueMediaCleanup(queueCtx, "drupal", canonicalUploadRoot, mediaUUID); queueErr != nil {
		return errors.Join(err, fmt.Errorf("queue media cleanup: %w", queueErr))
	}
	return fmt.Errorf("%w (durable cleanup retry queued)", err)
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

func (st *importState) flushReaderWarnings() {
	for _, warning := range st.reader.Warnings() {
		st.result.AddSummary("%s", warning)
	}
}

// unixOrNow converts a Drupal Unix timestamp, falling back to now when unset.
func unixOrNow(ts int64, now time.Time) time.Time {
	if ts <= 0 {
		return now
	}
	return time.Unix(ts, 0).UTC()
}

// hasTraversalSegment reports whether any path segment is exactly "..".
func hasTraversalSegment(rel string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(rel), "/") {
		if segment == ".." {
			return true
		}
	}
	return false
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

	mediaRoot, err := shared.OpenMediaRoot(st.filesPath)
	if err != nil {
		return fmt.Errorf("failed to open media root: %w", err)
	}
	defer func() {
		if err := mediaRoot.Close(); err != nil {
			slog.Error("failed to close drupal media root", "error", err)
		}
	}()
	scanned, err := mediaRoot.Scan()
	if err != nil {
		return fmt.Errorf("failed to scan media files: %w", err)
	}
	scannedPaths := make(map[string]string, len(scanned))
	for _, file := range scanned {
		scannedPaths[filepath.ToSlash(file.Path)] = file.Path
	}

	// A media-library site addresses embeds by media UUID, not file UUID, so
	// both have to land in ByUUID. A classic image-field site has no media
	// table and this is simply empty.
	mediaUUIDs, err := st.reader.MediaUUIDsByFile(ctx)
	if err != nil {
		st.result.AddError("failed to read media entity references: %v", err)
		mediaUUIDs = map[int64][]string{}
	}

	canonicalUploadRoot, err := imaging.CanonicalUploadRoot(st.uploadDir)
	if err != nil {
		return fmt.Errorf("failed to open uploads root: %w", err)
	}
	processor := imaging.NewProcessor(canonicalUploadRoot)
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

		relPath, err := importableFilePath(f)
		if err != nil {
			st.result.AddNotice("%s: %v", f.Filename, err)
			st.result.MediaSkipped++
			continue
		}
		rootPath, ok := scannedPaths[filepath.ToSlash(relPath)]
		if !ok {
			st.result.AddNotice("%s: source file is missing, unsafe, or outside the trusted media root", f.Filename)
			st.result.MediaSkipped++
			continue
		}

		mediaID, publicURL, err := s.importOneFile(ctx, st, mediaRootSource{root: mediaRoot},
			processor, canonicalUploadRoot, f, rootPath, mimeType, now)
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

func importableFilePath(f File) (string, error) {
	if f.Scheme() != "public" {
		return "", fmt.Errorf("unsupported Drupal stream wrapper %q", f.Scheme())
	}
	rel := filepath.ToSlash(filepath.Clean(f.RelPath()))
	if rel == "." || rel == "" || filepath.IsAbs(rel) || hasTraversalSegment(f.RelPath()) {
		return "", fmt.Errorf("invalid public file path %q", f.RelPath())
	}
	return rel, nil
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
		st.result.AddSummary("%d file(s) of type %q were skipped; the type is not in the media allowlist",
			skipped[mimeType], mimeType)
	}
}

// importOneFile copies a single file onto disk and creates its media row.
func (s *Source) importOneFile(ctx context.Context, st *importState, source mediaSource,
	processor *imaging.Processor, canonicalUploadRoot string,
	f File, relPath, mimeType string, now time.Time) (int64, string, error) {

	// Sanitize once, here, and use the result for both the database row and the
	// disk write. The writers already apply filepath.Base themselves, so
	// storing f.Filename raw let the two diverge: a source row naming
	// "../../evil.jpg" wrote to <uuid>/evil.jpg on disk while the database
	// recorded the traversal string, which then went into every rendered URL.
	safeFilename, err := util.SanitizeFilename(f.Filename)
	if err != nil {
		return 0, "", fmt.Errorf("invalid source filename %q: %w", f.Filename, err)
	}
	f.Filename = safeFilename

	src, err := source.Open(relPath)
	if err != nil {
		return 0, "", fmt.Errorf("failed to open file: %w", err)
	}

	fileUUID := uuid.New().String()
	params := store.CreateMediaParams{
		Uuid:         fileUUID,
		Filename:     safeFilename,
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
		processed, processErr := processor.ProcessImageWithOptions(src, fileUUID, f.Filename,
			imaging.ProcessOptions{DownscaleOversized: true})
		closeErr := src.Close()
		if processErr != nil {
			// ProcessImage compensates partial writes through its open capability;
			// retry against the captured canonical root so a failed internal cleanup
			// becomes durable instead of re-resolving the configured path.
			cleanupErr := st.cleanupMediaFiles(ctx, canonicalUploadRoot, fileUUID)
			return 0, "", fmt.Errorf("failed to process image: %w", errors.Join(processErr, closeErr, cleanupErr))
		}
		if closeErr != nil {
			cleanupErr := st.cleanupMediaFiles(ctx, canonicalUploadRoot, fileUUID)
			return 0, "", fmt.Errorf("failed to close source image: %w", errors.Join(closeErr, cleanupErr))
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
		var copyErr error
		canonicalUploadRoot, copyErr = shared.SaveNonImageFileWithCanonicalRoot(src, canonicalUploadRoot, fileUUID, f.Filename)
		closeErr := src.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			var cleanupErr error
			if canonicalUploadRoot != "" {
				cleanupErr = st.cleanupMediaFiles(ctx, canonicalUploadRoot, fileUUID)
			}
			return 0, "", fmt.Errorf("failed to copy file: %w", errors.Join(err, cleanupErr))
		}
	}

	media, err := st.queries.CreateMedia(ctx, params)
	if err != nil {
		// The file is already on disk under fileUUID. Without this cleanup no
		// media row and no tracking row exist, so nothing — including "delete
		// imported content" — can ever find it again: a database outage would
		// leave one orphaned upload per source file processed.
		cleanupErr := st.cleanupMediaFiles(ctx, canonicalUploadRoot, fileUUID)
		return 0, "", fmt.Errorf("failed to create media record: %w", errors.Join(err, cleanupErr))
	}
	if !st.track(ctx, types.EntityMedia, media.ID, func(rollbackCtx context.Context) error {
		if err := st.queries.DeleteMedia(rollbackCtx, media.ID); err != nil {
			return err
		}
		return st.cleanupMediaFiles(rollbackCtx, canonicalUploadRoot, fileUUID)
	}) {
		return 0, "", fmt.Errorf("media record could not be tracked and was rolled back")
	}

	if variantSource != "" {
		// Variants are best-effort: a partial failure costs a thumbnail, not the
		// file. But CreateAllVariants returns an error ONLY when every variant
		// failed, so discarding it hid the one case that matters — a full disk
		// or an unwritable variant directory leaving the entire library with no
		// thumbnails and the job still reporting a clean import.
		variants, varErr := processor.CreateAllVariants(variantSource, fileUUID, f.Filename)
		if varErr != nil {
			st.result.AddError("%s: no resized variants could be created: %v", f.Filename, varErr)
		}
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

// importNodes copies Drupal nodes into oCMS pages and posts, then writes their
// path aliases so the site's existing URLs keep working.
func (s *Source) importNodes(ctx context.Context, st *importState) error {
	if !st.opts.ImportPosts && !st.opts.ImportPages {
		return nil
	}

	if err := s.loadNodeAliases(ctx, st); err != nil {
		st.reportAliasLoadError(err)
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
	st.reportUnmappedLanguages()
	st.reportUnknownFormats()
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
	for _, mediaUUID := range st.refs.Unresolved {
		unique[mediaUUID] = true
	}
	st.result.AddSummary("%d media embed(s) referencing %d unknown media item(s) were removed "+
		"from page bodies; the referenced files were not imported",
		len(st.refs.Unresolved), len(unique))
}

// loadPathAliases reads aliases exactly once. Taxonomy redirects may run before
// pages, while page slugs and aliases consume the same rows later.
func (s *Source) loadPathAliases(ctx context.Context, st *importState) error {
	if st.aliasesLoaded {
		return st.aliasLoadErr
	}
	st.aliasesLoaded = true
	st.pathAliases, st.aliasLoadErr = st.reader.GetPathAliases(ctx)
	return st.aliasLoadErr
}

// loadNodeAliases records the best path alias per node and resolves source
// alias ownership in two passes. Node aliases take precedence over taxonomy
// redirects regardless of source row order; otherwise a lower-ID term alias
// could create a redirect that permanently shadows the page URL written later.
// Concrete ownership is language-aware, while the separate single-segment
// slug owner remains global because pages.slug is globally unique in storage.
func (s *Source) loadNodeAliases(ctx context.Context, st *importState) error {
	if err := s.loadPathAliases(ctx, st); err != nil {
		return err
	}
	nodeLanguages, err := st.reader.GetNodeLanguages(ctx)
	if err != nil {
		return fmt.Errorf("read node languages for path aliases: %w", err)
	}
	for nid, language := range nodeLanguages {
		st.nodeLang[nid] = language
	}

	// First establish every node-owned path. Highest ID still wins as the
	// canonical alias per node/language because pathAliases is ID-ordered.
	for _, a := range st.pathAliases {
		nid, ok := a.NodeID()
		if !ok {
			continue
		}
		if st.aliasByNode[nid] == nil {
			st.aliasByNode[nid] = make(map[string]string)
		}
		st.aliasByNode[nid][a.Langcode] = strings.TrimPrefix(a.Alias, "/")

		alias := strings.Trim(a.Alias, "/")
		if !isSafeAliasPath(alias) {
			continue
		}
		if !langcodeApplies(a.Langcode, st.nodeLang[nid]) {
			continue
		}
		sourcePath := localizedAliasSourcePath(
			st.aliasDestinationLanguage(st.nodeLang[nid]), st.defaultLang, "/"+alias,
		)
		if _, claimed := st.aliasReservations[sourcePath]; !claimed {
			st.aliasReservations[sourcePath] = aliasReservation{nodeID: nid}
		}
	}

	// Prefer the default-language node for the one globally unsuffixed stored
	// slug, independent of path_alias row order. A second pass gives an alias
	// with no default-language owner to the first deterministic source row.
	for pass := 0; pass < 2; pass++ {
		for _, a := range st.pathAliases {
			nid, ok := a.NodeID()
			if !ok {
				continue
			}
			alias := strings.Trim(a.Alias, "/")
			if !isSafeAliasPath(alias) || strings.Contains(alias, "/") {
				continue
			}
			if !langcodeApplies(a.Langcode, st.nodeLang[nid]) {
				continue
			}
			isDefault := st.aliasDestinationLanguage(st.nodeLang[nid]) == st.defaultLang
			if (pass == 0) != isDefault {
				continue
			}
			if _, claimed := st.aliasSlugOwners[alias]; !claimed {
				st.aliasSlugOwners[alias] = nid
			}
		}
	}

	// Then reserve single-segment taxonomy paths that did not lose to a node.
	// A term must have been imported to own a usable destination URL.
	for _, a := range st.pathAliases {
		tid, ok := a.TermID()
		if !ok || st.termURLs[tid] == "" {
			continue
		}
		alias := strings.Trim(a.Alias, "/")
		if !isSafeAliasPath(alias) || strings.Contains(alias, "/") {
			continue
		}
		sourcePath := localizedAliasSourcePath(
			st.termDestinationLang[tid], st.defaultLang, "/"+alias,
		)
		if _, claimed := st.aliasReservations[sourcePath]; !claimed {
			st.aliasReservations[sourcePath] = aliasReservation{}
		}
	}
	return nil
}

// aliasDestinationLanguage resolves path_alias.langcode without changing the
// operator-facing fallback counters. The node/term import stages report those
// fallbacks once from the entity rows themselves.
func (st *importState) aliasDestinationLanguage(raw string) string {
	code := strings.ToLower(strings.TrimSpace(raw))
	if isNeutralDrupalLangcode(code) || !st.availableLangs[code] {
		return st.defaultLang
	}
	return code
}

// aliasFor returns the canonical alias for a node in its own language.
//
// Drupal's language-neutral values (und, zxx) and an empty langcode all apply
// to any node, so they are accepted as a fallback when no alias exists for the
// node's own language.
func (st *importState) aliasFor(n Node) string {
	byLang := st.aliasByNode[n.NID]
	if byLang == nil {
		return ""
	}
	if alias := byLang[n.Langcode]; alias != "" {
		return alias
	}
	for _, neutral := range neutralLangcodes {
		if alias := byLang[neutral]; alias != "" {
			return alias
		}
	}
	return ""
}

// neutralLangcodes are Drupal's "applies to any language" values. "und" is
// LANGCODE_NOT_SPECIFIED, "zxx" is LANGCODE_NOT_APPLICABLE, and an empty value
// appears on rows written before a site was made multilingual.
var neutralLangcodes = []string{"und", "zxx", ""}

// langcodeApplies reports whether a row in aliasLang applies to a node in
// nodeLang. Both aliasFor and the redirect-alias pass use it, so the canonical
// slug and the redirects that point at it cannot disagree about language.
func langcodeApplies(aliasLang, nodeLang string) bool {
	if aliasLang == nodeLang {
		return true
	}
	for _, neutral := range neutralLangcodes {
		if aliasLang == neutral {
			return true
		}
	}
	return false
}

// languageFor maps a Drupal node's source language onto an oCMS language code.
//
// Drupal marks each entity's source translation with default_langcode = 1, so a
// multilingual site's nodes arrive in several languages and are all "default"
// in Drupal's sense. Assigning st.defaultLang to every one of them filed
// French originals as English, which language-filtered listings and URLs then
// served under the wrong locale.
//
// A language oCMS does not have falls back to the default and is counted, so
// the operator is told once rather than silently getting mislabelled content.
func (st *importState) languageFor(n Node) string {
	return st.languageForCode(n.Langcode, st.unmappedLangs)
}

func (st *importState) languageForTerm(t Term) string {
	code := strings.ToLower(strings.TrimSpace(t.Langcode))
	if code == "" || code == "und" || code == "zxx" {
		label := code
		if label == "" {
			label = "empty"
		}
		st.neutralTermLangs[label]++
		return st.defaultLang
	}
	return st.languageForCode(code, st.unmappedTermLangs)
}

func (st *importState) languageForMenuLink(link MenuLink) string {
	code := strings.ToLower(strings.TrimSpace(link.Langcode))
	if isNeutralDrupalLangcode(code) {
		label := code
		if label == "" {
			label = "empty"
		}
		st.neutralMenuLangs[label]++
		return st.defaultLang
	}
	return st.languageForCode(code, st.unmappedMenuLangs)
}

// addAvailableLanguage mirrors the public language router's eligibility
// policy. Legacy active rows with malformed or reserved codes remain
// manageable in the admin UI, but imported entities must not be assigned to a
// URL prefix that the router deliberately ignores.
func (st *importState) addAvailableLanguage(raw string) {
	code := strings.TrimSpace(raw)
	if !util.IsValidLangCode(code) || util.IsReservedLanguageCode(code) {
		return
	}
	st.availableLangs[code] = true
}

func (st *importState) languageForCode(raw string, unmapped map[string]int) string {
	code := strings.ToLower(strings.TrimSpace(raw))
	// "und" and "zxx" mean the content is not in any particular language.
	if code == "" || code == "und" || code == "zxx" {
		return st.defaultLang
	}
	if st.availableLangs[code] {
		return code
	}
	unmapped[code]++
	return st.defaultLang
}

// reportUnmappedLanguages tells the operator which source languages had no oCMS
// equivalent, once per language rather than once per node.
func (st *importState) reportUnmappedLanguages() {
	codes := make([]string, 0, len(st.unmappedLangs))
	for code := range st.unmappedLangs {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		st.result.AddSummary(
			"%d node(s) are in Drupal language %q, which is not configured in oCMS; "+
				"they were imported as %q", st.unmappedLangs[code], code, st.defaultLang)
	}
}

// reportUnmappedTermLanguages is deliberately independent of the node report:
// taxonomy-only imports must still disclose language fallback.
func (st *importState) reportUnmappedTermLanguages() {
	codes := make([]string, 0, len(st.unmappedTermLangs))
	for code := range st.unmappedTermLangs {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		st.result.AddSummary(
			"%d taxonomy term(s) are in Drupal language %q, which is not configured in oCMS; "+
				"they were imported as %q", st.unmappedTermLangs[code], code, st.defaultLang)
	}
	neutral := make([]string, 0, len(st.neutralTermLangs))
	for code := range st.neutralTermLangs {
		neutral = append(neutral, code)
	}
	sort.Strings(neutral)
	for _, code := range neutral {
		st.result.AddSummary(
			"%d taxonomy term(s) use Drupal's language-neutral code %q; they were imported as %q",
			st.neutralTermLangs[code], code, st.defaultLang)
	}
}

func (st *importState) reportUnmappedMenuLanguages() {
	codes := make([]string, 0, len(st.unmappedMenuLangs))
	for code := range st.unmappedMenuLangs {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		st.result.AddSummary(
			"%d menu link(s) are in Drupal language %q, which is not configured in oCMS; "+
				"they were imported into the %q menu", st.unmappedMenuLangs[code], code, st.defaultLang)
	}
	neutral := make([]string, 0, len(st.neutralMenuLangs))
	for code := range st.neutralMenuLangs {
		neutral = append(neutral, code)
	}
	sort.Strings(neutral)
	for _, code := range neutral {
		st.result.AddSummary(
			"%d menu link(s) use Drupal's language-neutral code %q; they were imported into the %q menu",
			st.neutralMenuLangs[code], code, st.defaultLang)
	}
}

func taxonomyURL(language, defaultLanguage, entity, slug string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if !util.IsValidLangCode(language) || util.IsReservedLanguageCode(language) {
		return ""
	}
	path := "/" + entity + "/" + slug
	if language != "" && !strings.EqualFold(language, defaultLanguage) {
		return "/" + strings.ToLower(language) + path
	}
	return path
}

// reportUnknownFormats tells the operator which text formats arrived unrendered.
func (st *importState) reportUnknownFormats() {
	formats := make([]string, 0, len(st.unknownFormats))
	for format := range st.unknownFormats {
		formats = append(formats, format)
	}
	sort.Strings(formats)
	for _, format := range formats {
		st.result.AddSummary(
			"%d node(s) use the Drupal text format %q, which oCMS cannot render; "+
				"their bodies were imported as-is and may need review",
			st.unknownFormats[format], format)
	}
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
	nodeLanguage := st.languageFor(n)

	if st.opts.SkipExisting {
		existing, err := st.queries.GetPageBySlug(ctx, baseSlug)
		switch {
		case err == nil && existing.LanguageCode == nodeLanguage:
			// Map the node to the page that is already there. Discarding the ID
			// made resolveMenuTarget treat every link to a skipped node as
			// pointing at uncreated content, so the menu item was dropped even
			// though its destination existed.
			st.nodes[n.NID] = existing.ID
			st.nodeLang[n.NID] = n.Langcode
			countSkipped(st.result, pageType)
			return
		case err == nil:
			// Slugs are globally unique in storage, but public routes are scoped
			// by language. A same-slug page in another language is not the
			// source entity and must never become its node/menu mapping.
		case !errors.Is(err, sql.ErrNoRows):
			st.result.AddError("could not check for existing page %q: %v", baseSlug, err)
			return
		}
	}
	slug := s.uniqueNodeSlug(ctx, st, baseSlug, n.NID, nodeLanguage)
	if slug == "" {
		st.result.AddError("failed to allocate an unshadowed page slug for node %d (%q)", n.NID, n.Title)
		return
	}

	// The source format decides whether the body is HTML at all. Plain text is
	// escaped and paragraphed, and skips the media rewriter: a path inside it is
	// text the author typed, not a link, so turning it into one would invent
	// markup the source never had.
	body, handling := RenderSourceBody(shared.NullString(n.Body), shared.NullString(n.Format))
	if handling == BodyFormatUnknown {
		st.unknownFormats[strings.ToLower(strings.TrimSpace(shared.NullString(n.Format)))]++
	}
	if handling != BodyFormatEscaped {
		body = RewriteBody(body, st.refs)
	}
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
		LanguageCode:    nodeLanguage,
		PageType:        pageType,
		PublishedAt:     publishedAt,
		CreatedAt:       unixOrNow(n.Created, now),
		UpdatedAt:       unixOrNow(n.Changed, now),
	})
	if err != nil {
		st.result.AddError("failed to create page for node %d (%q): %v", n.NID, n.Title, err)
		return
	}

	if !st.track(ctx, entityTypeFor(pageType), page.ID, func(rollbackCtx context.Context) error {
		return st.queries.DeletePage(rollbackCtx, page.ID)
	}) {
		return
	}
	st.nodes[n.NID] = page.ID
	st.createdNodes[n.NID] = true
	st.nodeLang[n.NID] = n.Langcode
	countImported(st.result, pageType)

	s.linkNodeTerms(ctx, st, n, page.ID)
}

// uniqueNodeSlug respects aliases that exist only in the source cache as well
// as slugs and aliases already persisted in oCMS. The alias's owning node may
// claim its own single-segment route; every other node must suffix.
func (s *Source) uniqueNodeSlug(ctx context.Context, st *importState, base string, nodeID int64, language string) string {
	if s.nodeSlugIsFree(ctx, st, base, nodeID, language) {
		return base
	}
	for i := 2; i <= 100; i++ {
		candidate := base + "-" + strconv.Itoa(i)
		if s.nodeSlugIsFree(ctx, st, candidate, nodeID, language) {
			return candidate
		}
	}
	fallback := "imported-" + strconv.FormatInt(nodeID, 10)
	if s.nodeSlugIsFree(ctx, st, fallback, nodeID, language) {
		return fallback
	}
	for i := 2; i <= 100; i++ {
		candidate := fallback + "-" + strconv.Itoa(i)
		if s.nodeSlugIsFree(ctx, st, candidate, nodeID, language) {
			return candidate
		}
	}
	return ""
}

func (s *Source) nodeSlugIsFree(ctx context.Context, st *importState, slug string, nodeID int64, language string) bool {
	sourcePath := localizedAliasSourcePath(language, st.defaultLang, "/"+slug)
	if concreteAliasRouteReserved(st, sourcePath, "/"+slug) {
		return false
	}
	if s.publicRouteChecker != nil && s.publicRouteChecker.OwnsPublicPath(sourcePath) {
		return false
	}
	if owner, ok := st.aliasSlugOwners[slug]; ok && owner != nodeID {
		return false
	}
	if reservation, ok := st.aliasReservations[sourcePath]; ok && reservation.nodeID != nodeID {
		return false
	}
	redirectOccupied, err := enabledRedirectMatchesPath(ctx, st.queries, sourcePath)
	if err != nil {
		st.result.AddError("could not check redirect ownership for page path %q: %v", sourcePath, err)
		return false
	}
	if redirectOccupied {
		return false
	}
	exists, err := st.queries.SlugOrAliasExists(ctx, store.SlugOrAliasExistsParams{
		Slug: slug, Alias: slug,
	})
	return err == nil && exists == 0
}

// slugForNode picks a node's slug: its Drupal alias when it has one, so URLs
// carry over, otherwise a slug derived from the title.
func (s *Source) slugForNode(st *importState, n Node) string {
	if alias := st.aliasFor(n); alias != "" {
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

// uniqueCategorySlug appends -2, -3, … until the slug is free.
func uniqueCategorySlug(ctx context.Context, st *importState, base string) string {
	for i := 2; i < 100; i++ {
		candidate := base + "-" + strconv.Itoa(i)
		if st.claimedCategorySlugs[candidate] {
			continue
		}
		if _, err := st.queries.GetCategoryBySlug(ctx, candidate); errors.Is(err, sql.ErrNoRows) {
			return candidate
		}
	}
	return base + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// uniqueTagSlug appends -2, -3, … until the globally unique tag slug is free.
func uniqueTagSlug(ctx context.Context, st *importState, base string) string {
	for i := 2; i < 100; i++ {
		candidate := base + "-" + strconv.Itoa(i)
		if st.claimedTagSlugs[candidate] {
			continue
		}
		if _, err := st.queries.GetTagBySlug(ctx, candidate); errors.Is(err, sql.ErrNoRows) {
			return candidate
		}
	}
	return base + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// linkNodeTerms attaches a node's taxonomy references to its oCMS page.
func (s *Source) linkNodeTerms(ctx context.Context, st *importState, n Node, pageID int64) {
	for _, tid := range n.TermIDs {
		if tagID, ok := st.tags[tid]; ok {
			if err := st.queries.AddTagToPage(ctx, store.AddTagToPageParams{PageID: pageID, TagID: tagID}); err != nil {
				slog.Warn("failed to link tag to page", "page_id", pageID, "tag_id", tagID, "error", err)
				st.result.AddError("page %d kept none of its Drupal tag %d: %v", pageID, tagID, err)
			}
			continue
		}
		if categoryID, ok := st.categories[tid]; ok {
			if err := st.queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{PageID: pageID, CategoryID: categoryID}); err != nil {
				slog.Warn("failed to link category to page", "page_id", pageID, "category_id", categoryID, "error", err)
				st.result.AddError("page %d was not linked to category %d: %v", pageID, categoryID, err)
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
	if err := s.loadNodeAliases(ctx, st); err != nil {
		// Reported, not fatal: the canonical node paths below are worth writing
		// even when the alias table could not be read.
		st.reportAliasLoadError(err)
	}

	for _, a := range st.pathAliases {
		nid, ok := a.NodeID()
		if !ok {
			continue
		}
		pageID, ok := st.nodes[nid]
		if !ok {
			continue
		}
		// Never write aliases onto a page this import did not create.
		if !st.createdNodes[nid] {
			continue
		}
		// Only the node's own language. Drupal keeps one alias row per
		// language and only default-language nodes are imported, so taking
		// them all would point a translated URL at unrelated content.
		if !langcodeApplies(a.Langcode, st.nodeLang[nid]) {
			continue
		}
		s.importNodeAlias(ctx, st, nid, pageID, strings.Trim(a.Alias, "/"), now)
	}

	s.importNodePathAliases(ctx, st, now)
}

// importNodeAlias preserves a node alias in the namespace where Drupal served
// it. The legacy page_aliases table is global, so only default-language aliases
// are stored there. Non-default aliases use a concrete tracked redirect such
// as /fr/about -> /fr/about-2; this permits identical source aliases in two
// languages without one becoming unreachable or pointing at the other page.
func (s *Source) importNodeAlias(ctx context.Context, st *importState, nodeID, pageID int64, alias string, now time.Time) {
	page, err := st.queries.GetPageByID(ctx, pageID)
	if err != nil {
		st.result.AddError("could not load page %d for node %d alias %q: %v", pageID, nodeID, alias, err)
		return
	}
	if page.LanguageCode == st.defaultLang {
		if alias != page.Slug {
			pageBySlug, lookupErr := st.queries.GetPageBySlug(ctx, alias)
			switch {
			case lookupErr == nil && pageBySlug.LanguageCode != page.LanguageCode:
				// pages.slug is globally unique, but the foreign-language row is
				// not routable at this unprefixed/default URL. A concrete redirect
				// preserves the source URL without violating page_aliases' global
				// cross-table policy.
				s.createAliasRedirect(ctx, st, "/"+alias, "/"+alias, "/"+page.Slug, nodeID, now)
				return
			case lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows):
				st.result.AddError("could not check page slug for node %d alias %q: %v", nodeID, alias, lookupErr)
				return
			}
		}
		s.createPageAlias(ctx, st, pageID, alias, now)
		return
	}

	aliasPath := "/" + alias
	sourcePath := localizedAliasSourcePath(page.LanguageCode, st.defaultLang, aliasPath)
	targetPath := localizedAliasSourcePath(page.LanguageCode, st.defaultLang, "/"+page.Slug)
	if sourcePath == targetPath {
		return
	}
	s.createAliasRedirect(ctx, st, sourcePath, aliasPath, targetPath, nodeID, now)
}

func (st *importState) reportAliasLoadError(err error) {
	if err == nil || st.aliasErrorReported {
		return
	}
	st.aliasErrorReported = true
	st.result.AddError("failed to read path aliases: %v", err)
}

// importTaxonomyAliases preserves Drupal term aliases using the generic
// redirect subsystem. Page aliases cannot target tags/categories, and storing
// these as page aliases would either lose the URL or point it at unrelated
// content.
func (s *Source) importTaxonomyAliases(ctx context.Context, st *importState, now time.Time) {
	if err := s.loadNodeAliases(ctx, st); err != nil {
		st.reportAliasLoadError(err)
		return
	}
	for _, a := range st.pathAliases {
		tid, ok := a.TermID()
		if !ok {
			continue
		}
		target := st.termURLs[tid]
		if target == "" || !langcodeApplies(a.Langcode, st.termLang[tid]) {
			continue
		}
		alias := strings.Trim(a.Alias, "/")
		if !isSafeAliasPath(alias) {
			st.result.AddNotice("taxonomy path alias %q was not imported: it is not a usable URL path", a.Alias)
			continue
		}
		aliasPath := "/" + alias
		sourcePath := localizedAliasSourcePath(
			st.termDestinationLang[tid], st.defaultLang, aliasPath,
		)
		s.createAliasRedirect(ctx, st, sourcePath, aliasPath, target, 0, now)
	}
}

// localizedAliasSourcePath mirrors the public language router: the default
// language owns the unprefixed path, while non-default legacy URLs live below
// their language prefix. Redirect matching happens before language routing,
// so the stored source itself must include that prefix.
func localizedAliasSourcePath(language, defaultLanguage, aliasPath string) string {
	if language == "" || language == defaultLanguage {
		return aliasPath
	}
	return "/" + language + aliasPath
}

func (s *Source) createTaxonomyRedirect(ctx context.Context, st *importState,
	sourcePath, targetURL string, now time.Time) {
	s.createAliasRedirect(ctx, st, sourcePath, sourcePath, targetURL, 0, now)
}

// createAliasRedirect separates the concrete source stored in the global
// redirect table from the underlying Drupal alias. ownerNodeID is non-zero
// when the redirect preserves that node's own language-scoped legacy URL;
// taxonomy redirects use zero and must yield to a node owning the same
// concrete path.
func (s *Source) createAliasRedirect(ctx context.Context, st *importState,
	sourcePath, aliasPath, targetURL string, ownerNodeID int64, now time.Time) {
	alias := strings.TrimPrefix(aliasPath, "/")
	if reservation, ok := st.aliasReservations[sourcePath]; ok &&
		reservation.nodeID != 0 && reservation.nodeID != ownerNodeID {
		st.result.AddNotice("redirect %q was not imported because Drupal node %d owns that path alias", sourcePath, reservation.nodeID)
		return
	}
	if concreteAliasRouteReserved(st, sourcePath, aliasPath) {
		st.result.AddNotice("redirect %q was not imported because it conflicts with a reserved oCMS route", sourcePath)
		return
	}
	if s.publicRouteChecker != nil && s.publicRouteChecker.OwnsPublicPath(sourcePath) {
		st.result.AddNotice("redirect %q was not imported because a registered oCMS module route owns that path", sourcePath)
		return
	}
	occupied, err := aliasPathOccupied(ctx, st, sourcePath, alias)
	if err != nil {
		st.result.AddError("could not check page/alias conflict for redirect %q: %v", sourcePath, err)
		return
	}
	if occupied {
		st.result.AddNotice("redirect %q was not imported because an oCMS page slug or alias already owns that path", sourcePath)
		return
	}

	enabledRedirects, err := st.queries.ListEnabledRedirects(ctx)
	if err != nil {
		st.result.AddError("could not check wildcard conflicts for redirect %q: %v", sourcePath, err)
		return
	}
	for _, existing := range enabledRedirects {
		if existing.IsWildcard && wildcardRedirectMatchesPath(existing.SourcePath, sourcePath) {
			st.result.AddNotice("redirect %q was not imported because enabled wildcard redirect %q already matches that path",
				sourcePath, existing.SourcePath)
			return
		}
	}

	existing, err := st.queries.GetRedirectBySourcePath(ctx, sourcePath)
	switch {
	case err == nil:
		if existing.TargetUrl == targetURL && existing.Enabled {
			st.result.RedirectsSkipped++
		} else {
			st.result.AddNotice("redirect %q was not replaced: it already targets %q", sourcePath, existing.TargetUrl)
		}
		return
	case !errors.Is(err, sql.ErrNoRows):
		st.result.AddError("could not check for existing redirect %q: %v", sourcePath, err)
		return
	}

	redirect, err := st.queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: sourcePath,
		TargetUrl:  targetURL,
		StatusCode: 301,
		IsWildcard: false,
		TargetType: model.TargetSelf,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		st.result.AddError("failed to create redirect %q to %q: %v", sourcePath, targetURL, err)
		return
	}
	if !st.track(ctx, types.EntityRedirect, redirect.ID, func(rollbackCtx context.Context) error {
		return st.queries.DeleteRedirect(rollbackCtx, redirect.ID)
	}) {
		return
	}
	st.result.RedirectsImported++
}

// aliasPathOccupied checks the namespace the request will actually use. The
// unprefixed/default namespace is global in the legacy tables. A non-default
// concrete redirect such as /fr/about is only shadowed by a page or alias that
// belongs to fr; an English bare slug must not suppress that safe route.
func aliasPathOccupied(ctx context.Context, st *importState, sourcePath, alias string) (bool, error) {
	language := concreteAliasLanguage(sourcePath, "/"+alias, st.defaultLang)
	page, err := st.queries.GetPageBySlug(ctx, alias)
	switch {
	case err == nil && page.LanguageCode == language:
		return true, nil
	case err == nil, errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, err
	}

	page, err = st.queries.GetPageByAlias(ctx, alias)
	switch {
	case err == nil:
		// An unprefixed alias is globally routable and may intentionally
		// redirect to a non-default page. Explicit prefixes, by contrast, only
		// resolve aliases belonging to that same language.
		return language == st.defaultLang || page.LanguageCode == language, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

func enabledRedirectMatchesPath(ctx context.Context, queries *store.Queries, sourcePath string) (bool, error) {
	redirects, err := queries.ListEnabledRedirects(ctx)
	if err != nil {
		return false, err
	}
	for _, redirect := range redirects {
		if redirect.SourcePath == sourcePath ||
			(redirect.IsWildcard && wildcardRedirectMatchesPath(redirect.SourcePath, sourcePath)) {
			return true, nil
		}
	}
	return false, nil
}

func concreteAliasLanguage(sourcePath, aliasPath, defaultLanguage string) string {
	if sourcePath == aliasPath {
		return defaultLanguage
	}
	trimmed := strings.TrimPrefix(sourcePath, "/")
	language, _, found := strings.Cut(trimmed, "/")
	if !found {
		return defaultLanguage
	}
	return strings.ToLower(language)
}

// wildcardRedirectMatchesPath mirrors the redirect middleware's wildcard
// semantics for conflict detection. The importer only needs the match result,
// not the captured values used to build a redirect target.
func wildcardRedirectMatchesPath(pattern, requestPath string) bool {
	if strings.HasSuffix(pattern, "*") && !strings.HasSuffix(pattern, "**") {
		prefix := strings.TrimSuffix(pattern, "*")
		if !strings.HasSuffix(prefix, "/") {
			requestPath = strings.TrimSuffix(requestPath, "/")
			prefixWithoutSlash := strings.TrimSuffix(prefix, "/")
			return requestPath == prefixWithoutSlash || strings.HasPrefix(requestPath, prefix)
		}
	}

	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	requestParts := strings.Split(strings.Trim(requestPath, "/"), "/")
	return wildcardRedirectPartsMatch(patternParts, requestParts, 0, 0)
}

func wildcardRedirectPartsMatch(pattern, request []string, patternIndex, requestIndex int) bool {
	if patternIndex >= len(pattern) {
		return requestIndex >= len(request)
	}
	if requestIndex >= len(request) {
		for ; patternIndex < len(pattern); patternIndex++ {
			if pattern[patternIndex] != "**" {
				return false
			}
		}
		return true
	}

	switch pattern[patternIndex] {
	case "*":
		return wildcardRedirectPartsMatch(pattern, request, patternIndex+1, requestIndex+1)
	case "**":
		if wildcardRedirectPartsMatch(pattern, request, patternIndex+1, requestIndex) {
			return true
		}
		for end := requestIndex + 1; end <= len(request); end++ {
			if wildcardRedirectPartsMatch(pattern, request, patternIndex+1, end) {
				return true
			}
		}
		return false
	default:
		return pattern[patternIndex] == request[requestIndex] &&
			wildcardRedirectPartsMatch(pattern, request, patternIndex+1, requestIndex+1)
	}
}

// reservedPublicPath protects fixed routes that are selected before the page
// catch-all. The set includes every built-in public/admin/API/static prefix;
// module routes remain protected by the destination page/alias checks where
// they are represented in storage.
func reservedPublicPath(st *importState, path string) bool {
	path = strings.Trim(path, "/")
	if path == "" {
		return true
	}
	first, _, _ := strings.Cut(path, "/")
	if fixedPublicPaths[path] || util.IsReservedLanguageCode(first) {
		return true
	}
	// Active language prefixes are stripped before public route matching, and
	// the remainder may be either a fixed endpoint or a page slug/alias. Reject
	// the whole prefix namespace here: the generic redirect table cannot safely
	// prove that it is unowned without duplicating the language router.
	if st != nil && st.availableLangs[strings.ToLower(first)] {
		return true
	}
	return strings.HasPrefix(path, ".well-known/") || path == ".well-known"
}

// concreteAliasRouteReserved distinguishes top-level routes from routes that
// are actually mounted inside the language-aware frontend child. A default
// alias /admin conflicts with the admin router, while /fr/admin is a valid page
// path because only the child router sees "admin" after stripping "fr".
func concreteAliasRouteReserved(st *importState, sourcePath, aliasPath string) bool {
	if sourcePath == aliasPath {
		return reservedPublicPath(st, aliasPath)
	}
	parts := strings.Split(strings.Trim(aliasPath, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return true
	}
	switch parts[0] {
	case "blog", "search":
		return len(parts) == 1 || (parts[0] == "blog" && len(parts) == 3 && parts[1] == "tag")
	case "category", "tag", "page", "forms":
		return len(parts) == 2
	default:
		return false
	}
}

var fixedPublicPaths = map[string]bool{
	"sitemap.xml": true,
	"robots.txt":  true,
	"favicon.ico": true,
}

// importNodePathAliases gives every page this run created an alias for its
// Drupal canonical path, "node/<nid>".
//
// Drupal bodies routinely link to /node/42 rather than to an alias, and oCMS has
// no /node/{nid} route, so those links became 404s on every migrated site.
// Registering the canonical path as an alias fixes them wherever they appear —
// page bodies, menus, other sites' inbound links and bookmarks — which
// rewriting body HTML would not. renderNotFound resolves an arbitrary
// multi-segment path through GetPublishedPageByAlias and 301s to the real slug.
func (s *Source) importNodePathAliases(ctx context.Context, st *importState, now time.Time) {
	nids := make([]int64, 0, len(st.createdNodes))
	for nid := range st.createdNodes {
		nids = append(nids, nid)
	}
	// Map order is random; sorting keeps the alias-creation order, and so the
	// reported counts on a partial failure, reproducible.
	sort.Slice(nids, func(i, j int) bool { return nids[i] < nids[j] })

	for _, nid := range nids {
		pageID, ok := st.nodes[nid]
		if !ok {
			continue
		}
		s.importNodeAlias(ctx, st, nid, pageID, fmt.Sprintf("node/%d", nid), now)
	}
}

// createPageAlias writes one alias row, skipping the cases that are expected
// rather than failures.
func (s *Source) createPageAlias(ctx context.Context, st *importState, pageID int64, alias string, now time.Time) {
	if alias == "" {
		return
	}
	if !isSafeAliasPath(alias) {
		// Reported rather than dropped in silence: this is an established URL
		// that will 404 after the migration, and the operator can add a
		// redirect for it if they know it existed.
		st.result.AddNotice("path alias %q was not imported: it is not a usable URL path", alias)
		return
	}
	if reservedPublicPath(st, alias) {
		st.result.AddNotice("path alias %q was not imported because it conflicts with a reserved oCMS route", alias)
		return
	}
	if s.publicRouteChecker != nil && s.publicRouteChecker.OwnsPublicPath("/"+alias) {
		st.result.AddNotice("path alias %q was not imported because a registered oCMS module route owns that path", alias)
		return
	}

	// Redirect middleware runs before frontend page-alias lookup. Preserve
	// exact redirect ownership even when it is currently disabled (it may be
	// re-enabled later), and reject every enabled wildcard that would make the
	// new alias unreachable while still being counted as imported.
	sourcePath := "/" + alias
	existingRedirect, err := st.queries.GetRedirectBySourcePath(ctx, sourcePath)
	switch {
	case err == nil:
		st.result.AddNotice("path alias %q was not imported because redirect %q already owns that path",
			alias, existingRedirect.SourcePath)
		return
	case !errors.Is(err, sql.ErrNoRows):
		st.result.AddError("could not check whether alias %q conflicts with a redirect: %v", alias, err)
		return
	}
	redirectOccupied, err := enabledRedirectMatchesPath(ctx, st.queries, sourcePath)
	if err != nil {
		st.result.AddError("could not check wildcard redirects for alias %q: %v", alias, err)
		return
	}
	if redirectOccupied {
		st.result.AddNotice("path alias %q was not imported because an enabled wildcard redirect already owns that path", alias)
		return
	}

	// Page slugs are resolved before aliases. Refuse to create a legacy alias
	// that would be shadowed by another page even if a future schema change
	// removes cross-table validation at write time.
	pageBySlug, err := st.queries.GetPageBySlug(ctx, alias)
	switch {
	case err == nil && pageBySlug.ID == pageID:
		return // redundant alias equal to this page's own slug
	case err == nil:
		st.result.AddNotice("path alias %q was not imported because page %d already owns that slug", alias, pageBySlug.ID)
		return
	case !errors.Is(err, sql.ErrNoRows):
		st.result.AddError("could not check whether alias %q shadows a page slug: %v", alias, err)
		return
	}

	created, err := st.queries.CreatePageAlias(ctx, store.CreatePageAliasParams{
		PageID:    pageID,
		Alias:     alias,
		CreatedAt: now,
	})
	if err != nil {
		// A duplicate alias is expected when two Drupal nodes shared one path
		// over time, and is not worth failing the import over. Any other
		// failure is: with menus disabled this is the last step, so a database
		// problem here could otherwise lose every legacy URL while the job
		// still reported "completed".
		if isUniqueConstraintErr(err) {
			slog.Warn("duplicate page alias skipped", "page_id", pageID, "alias", alias)
		} else {
			st.result.AddError("failed to create alias %q: %v", alias, err)
		}
		return
	}
	if !st.track(ctx, types.EntityAlias, created.ID, func(rollbackCtx context.Context) error {
		return st.queries.DeletePageAlias(rollbackCtx, created.ID)
	}) {
		return
	}
	st.result.AliasesImported++
}

// isSafeAliasPath reports whether a Drupal alias can be stored and served as-is.
//
// util.IsValidAlias is deliberately not used here. It requires every segment to
// be a lowercase oCMS slug, which discards aliases Drupal produces routinely —
// "About_Us", "News/Archive", anything with non-ASCII — even though page_aliases
// stores arbitrary text and the frontend looks it up by exact match. Applying
// the admin form's grammar to imported data turned established URLs into 404s
// for no benefit.
//
// What is rejected is what would misroute or is not a path: a scheme or host, a
// query or fragment, traversal, empty or duplicated segments, control
// characters and whitespace.
const maxAliasLength = shared.MaxImportedAliasLength

func isSafeAliasPath(alias string) bool {
	return shared.IsSafeImportedAliasPath(alias)
}

// isUniqueConstraintErr reports whether err is SQLite's uniqueness violation,
// which for aliases is an expected collision rather than a failure.
func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "constraint failed: unique")
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
	// Menu links may point at a taxonomy term through its human-readable path
	// alias (internal:/topics/go), not only its canonical entity URI. Reuse the
	// one cached alias read so those links can resolve to the imported,
	// language-aware tag/category URL.
	if err := s.loadNodeAliases(ctx, st); err != nil {
		return fmt.Errorf("failed to load path aliases for menu targets: %w", err)
	}

	links, err := st.reader.GetMenuLinks(ctx)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		return nil
	}

	type menuPartition struct {
		name     string
		language string
	}
	byMenu := make(map[menuPartition][]MenuLink)
	var order []menuPartition
	for _, l := range links {
		if l.MenuName == "" {
			continue
		}
		partition := menuPartition{name: l.MenuName, language: st.languageForMenuLink(l)}
		if _, seen := byMenu[partition]; !seen {
			order = append(order, partition)
		}
		byMenu[partition] = append(byMenu[partition], l)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].language == order[j].language {
			return order[i].name < order[j].name
		}
		return order[i].language < order[j].language
	})

	now := time.Now()
	for i, partition := range order {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.importMenu(ctx, st, partition.name, partition.language, byMenu[partition], now)
		st.report(ctx, types.EntityMenu, i+1, len(order))
	}
	st.reportUnmappedMenuLanguages()
	return nil
}

// menuItemKey identifies a menu item by what it shows and where it points.
func menuItemKey(title string, pageID sql.NullInt64, url sql.NullString) string {
	target := ""
	switch {
	case pageID.Valid:
		target = "page:" + strconv.FormatInt(pageID.Int64, 10)
	case url.Valid:
		target = "url:" + url.String
	}
	return title + "\x00" + target
}

// uniqueMenuSlug appends -2, -3, … until the slug is free.
func uniqueMenuSlug(ctx context.Context, st *importState, base, language string) string {
	for i := 2; i < 100; i++ {
		candidate := base + "-" + strconv.Itoa(i)
		if st.claimedMenuSlugs[menuSlugClaim(language, candidate)] {
			continue
		}
		if _, err := st.queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
			Slug: candidate, LanguageCode: language,
		}); errors.Is(err, sql.ErrNoRows) {
			return candidate
		}
	}
	return base + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func menuSlugClaim(language, slug string) string { return language + "\x00" + slug }

// importMenu creates or reuses one menu and imports its links.
func (s *Source) importMenu(ctx context.Context, st *importState, menuName, language string, links []MenuLink, now time.Time) {
	slug := util.Slugify(menuName)
	if slug == "" {
		return
	}

	// Two distinct Drupal menu names can normalize to the same slug ("foo_bar"
	// and "foobar"). Without this the second reuses the first's menu and both
	// sets of links land in one navigation.
	if st.claimedMenuSlugs[menuSlugClaim(language, slug)] {
		slug = uniqueMenuSlug(ctx, st, slug, language)
	}

	menuID, created, err := s.ensureMenu(ctx, st, menuName, slug, language, now)
	if err != nil {
		st.result.AddError("failed to create menu %q: %v", menuName, err)
		return
	}
	if created {
		if !st.track(ctx, types.EntityMenu, menuID, func(rollbackCtx context.Context) error {
			return st.queries.DeleteMenu(rollbackCtx, menuID)
		}) {
			return
		}
		st.result.MenusImported++
	} else {
		st.result.MenusSkipped++
	}
	// Reused rows count as claimed too. Otherwise the next distinct source menu
	// with the same normalized slug is merged into that same destination menu.
	st.claimedMenuSlugs[menuSlugClaim(language, slug)] = true

	itemByUUID := make(map[string]int64, len(links))
	// Items this run created. itemByUUID also holds pre-existing rows matched
	// during dedup — needed so a child can resolve them as a *parent* — but
	// their own parent_id must never be rewritten: that row belongs to the
	// administrator, is not tracked, and deleting the import could not restore
	// a hierarchy the import had changed.
	createdItems := make(map[int64]bool, len(links))

	// Titles already present in a reused menu. Menu items carry no uniqueness
	// constraint, so re-running an import — or importing into a menu an admin
	// had already created — appended a second copy of every link.
	// Keyed on title AND target: Drupal menus legitimately repeat labels like
	// "Home" or "Learn more" pointing at different pages, so matching on the
	// title alone silently dropped distinct entries.
	//
	// The value is the existing item's ID, not just presence: a skipped link
	// still has to enter itemByUUID, or linkMenuParents cannot resolve any
	// child pointing at it and the whole subtree is flattened to the menu root.
	existingItems := make(map[string]int64)
	if !created {
		items, err := st.queries.ListMenuItems(ctx, menuID)
		if err != nil {
			st.result.AddError("could not read existing items of menu %q, skipping it to "+
				"avoid duplicating links: %v", menuName, err)
			return
		}
		for _, item := range items {
			existingItems[menuItemKey(item.Title, item.PageID, item.Url)] = item.ID
		}
	}

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
		if existingID, ok := existingItems[menuItemKey(l.Title, pageID, url)]; ok {
			// Map the source UUID onto the item already there, so a child of
			// this link still finds its parent in the second pass.
			if l.UUID != "" {
				itemByUUID[l.UUID] = existingID
			}
			st.result.MenuItemsSkipped++
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

		if !st.track(ctx, types.EntityMenuItem, item.ID, func(rollbackCtx context.Context) error {
			return st.queries.DeleteMenuItem(rollbackCtx, item.ID)
		}) {
			continue
		}
		if l.UUID != "" {
			itemByUUID[l.UUID] = item.ID
		}
		createdItems[item.ID] = true
		st.result.MenuItemsImported++
	}

	// Second pass: apply the hierarchy now that every item exists.
	s.linkMenuParents(ctx, st, links, itemByUUID, createdItems, now)
}

// ensureMenu returns the ID of the oCMS menu for a Drupal menu name, creating
// it when absent. created reports whether this import made the row.
func (s *Source) ensureMenu(ctx context.Context, st *importState, menuName, slug, language string, now time.Time) (int64, bool, error) {
	existing, err := st.queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug: slug, LanguageCode: language,
	})
	switch {
	case err == nil:
		return existing.ID, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, fmt.Errorf("failed to check for existing menu %q: %w", slug, err)
	}

	menu, err := st.queries.CreateMenu(ctx, store.CreateMenuParams{
		Name:         menuName,
		Slug:         slug,
		LanguageCode: language,
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
	target, err := ResolveLinkURI(l.LinkURI)
	if err != nil {
		return sql.NullInt64{}, sql.NullString{}, fmt.Errorf("link %q: %w", l.Title, err)
	}

	if target.NodeID != 0 {
		pageID, ok := st.nodes[target.NodeID]
		if !ok {
			return sql.NullInt64{}, sql.NullString{}, fmt.Errorf("link %q points at node %d, which was not imported", l.Title, target.NodeID)
		}
		return sql.NullInt64{Int64: pageID, Valid: true}, sql.NullString{String: "", Valid: true}, nil
	}
	if target.TermID != 0 {
		url := st.termURLs[target.TermID]
		if url == "" {
			return sql.NullInt64{}, sql.NullString{}, fmt.Errorf("link %q points at taxonomy term %d, which was not imported", l.Title, target.TermID)
		}
		return sql.NullInt64{}, sql.NullString{String: url, Valid: true}, nil
	}
	if pageID, nodeID, known := st.nodePageForAlias(target.URL, l.Langcode); known {
		if pageID == 0 {
			return sql.NullInt64{}, sql.NullString{}, fmt.Errorf(
				"link %q points at node alias %q for node %d, which was not imported in language %q",
				l.Title, target.URL, nodeID, st.aliasDestinationLanguage(l.Langcode))
		}
		return sql.NullInt64{Int64: pageID, Valid: true}, sql.NullString{}, nil
	}
	if url, termID, known := st.taxonomyURLForAlias(target.URL, l.Langcode); known {
		if url == "" {
			return sql.NullInt64{}, sql.NullString{}, fmt.Errorf(
				"link %q points at taxonomy alias %q for term %d, which was not imported",
				l.Title, target.URL, termID)
		}
		return sql.NullInt64{}, sql.NullString{String: url, Valid: true}, nil
	}

	return sql.NullInt64{}, sql.NullString{String: target.URL, Valid: true}, nil
}

// nodePageForAlias resolves internal:/legacy-path menu links to the exact
// imported node in the link's source language. Returning a PageID lets the
// menu service generate the page's canonical language-aware URL; preserving
// the raw path would send a French link to an English page when both languages
// used the same Drupal alias.
func (st *importState) nodePageForAlias(path, menuLang string) (pageID, nodeID int64, known bool) {
	alias := strings.Trim(path, "/")
	if alias == "" || !isSafeAliasPath(alias) {
		return 0, 0, false
	}
	preferredLang := st.aliasDestinationLanguage(menuLang)

	var neutralPageID, neutralNodeID int64
	var neutralKnown bool
	for _, candidate := range st.pathAliases {
		if strings.Trim(candidate.Alias, "/") != alias {
			continue
		}
		nid, ok := candidate.NodeID()
		if !ok {
			continue
		}
		candidateLang := strings.ToLower(strings.TrimSpace(candidate.Langcode))
		nodeDestinationLang := st.aliasDestinationLanguage(st.nodeLang[nid])
		switch {
		case candidateLang == preferredLang:
			if nodeDestinationLang == preferredLang && langcodeApplies(candidate.Langcode, st.nodeLang[nid]) {
				return st.nodes[nid], nid, true
			}
			return 0, nid, true
		case isNeutralDrupalLangcode(candidateLang) && !neutralKnown && nodeDestinationLang == preferredLang:
			neutralKnown = true
			neutralNodeID = nid
			neutralPageID = st.nodes[nid]
		}
	}
	return neutralPageID, neutralNodeID, neutralKnown
}

// taxonomyURLForAlias maps an internal menu URL such as /topics/go back to the
// imported term that owned that Drupal path alias in the menu link's source
// language. Drupal permits the same alias text in different languages, so row
// order is not ownership: an exact language match wins, followed by a neutral
// alias. Node aliases still take precedence just as they do for redirects.
func (st *importState) taxonomyURLForAlias(path, menuLang string) (target string, termID int64, known bool) {
	alias := strings.Trim(path, "/")
	if alias == "" || !isSafeAliasPath(alias) {
		return "", 0, false
	}
	preferredLang := st.aliasDestinationLanguage(menuLang)
	sourcePath := localizedAliasSourcePath(preferredLang, st.defaultLang, "/"+alias)
	if reservation, ok := st.aliasReservations[sourcePath]; ok && reservation.nodeID != 0 {
		return "", 0, false
	}

	var exactTarget, neutralTarget string
	var exactTermID, neutralTermID int64
	var exactSeen, neutralSeen bool

	for _, candidate := range st.pathAliases {
		if strings.Trim(candidate.Alias, "/") != alias {
			continue
		}
		tid, ok := candidate.TermID()
		if !ok {
			continue
		}
		if !known {
			termID = tid
		}
		known = true
		if st.termDestinationLang[tid] != preferredLang {
			continue
		}
		if !langcodeApplies(candidate.Langcode, st.termLang[tid]) {
			continue
		}

		candidateLang := strings.ToLower(strings.TrimSpace(candidate.Langcode))
		switch {
		case candidateLang == preferredLang:
			if !exactSeen {
				exactSeen = true
				exactTermID = tid
			}
			if candidateTarget := st.termURLs[tid]; exactTarget == "" && candidateTarget != "" {
				exactTarget = candidateTarget
				exactTermID = tid
			}
		case isNeutralDrupalLangcode(candidateLang):
			if !neutralSeen {
				neutralSeen = true
				neutralTermID = tid
			}
			if candidateTarget := st.termURLs[tid]; neutralTarget == "" && candidateTarget != "" {
				neutralTarget = candidateTarget
				neutralTermID = tid
			}
		}
	}
	if exactSeen {
		return exactTarget, exactTermID, true
	}
	if neutralSeen {
		return neutralTarget, neutralTermID, true
	}
	return "", termID, known
}

func isNeutralDrupalLangcode(code string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	return code == "" || code == "und" || code == "zxx"
}

// linkMenuParents applies the Drupal menu hierarchy to the created items.
func (s *Source) linkMenuParents(ctx context.Context, st *importState, links []MenuLink,
	itemByUUID map[string]int64, createdItems map[int64]bool, now time.Time) {

	for _, l := range links {
		parentUUID := l.ParentUUID()
		if parentUUID == "" || l.UUID == "" {
			continue
		}
		childID, ok := itemByUUID[l.UUID]
		if !ok {
			continue
		}
		// Only items this run created. A pre-existing item matched during
		// dedup is a valid *parent* but must keep its own place in the menu.
		if !createdItems[childID] {
			continue
		}
		parentID, ok := itemByUUID[parentUUID]
		if !ok {
			st.result.AddNotice("menu %q item %q references parent %q outside its language partition; left at root",
				l.MenuName, l.Title, parentUUID)
			continue
		}
		if parentID == childID {
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
