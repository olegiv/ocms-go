// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package phpnuke imports content from a PHP-Nuke MySQL database.
//
// PHP-Nuke shipped no canonical URL scheme — content is served from query
// strings such as modules.php?name=News&file=article&sid=12 — so no page
// aliases are created. Preserving those URLs is a reverse-proxy concern; an
// alias would invent a path the source site never served.
package phpnuke

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// PublicRouteChecker reports whether a path belongs to a registered module
// route. Core routes are handled separately by corePathReserved in stages.go.
type PublicRouteChecker interface {
	OwnsPublicPath(path string) bool
}

// Source implements the migrator Source interface for PHP-Nuke.
type Source struct {
	publicRouteChecker PublicRouteChecker
}

// NewSource creates a new PHP-Nuke source.
func NewSource() *Source {
	return &Source{}
}

// SetPublicRouteChecker supplies destination module-route ownership checks for
// imported page slugs.
func (s *Source) SetPublicRouteChecker(checker PublicRouteChecker) {
	s.publicRouteChecker = checker
}

// Name returns the unique identifier for this source.
func (s *Source) Name() string {
	return "phpnuke"
}

// DisplayName returns the human-readable name.
func (s *Source) DisplayName() string {
	return "PHP-Nuke"
}

// Description returns a brief description of the source.
func (s *Source) Description() string {
	return "phpnuke.description"
}

// Connection bounds for the source database. A migration is a long sequence of
// modest reads, so the pool stays small and every statement is bounded.
const (
	connectTimeout          = 10 * time.Second
	readTimeout             = 60 * time.Second
	trackingRollbackTimeout = 30 * time.Second
	maxOpenConns            = 4
	connMaxLifetime         = 30 * time.Minute
)

// envOrDefault returns the environment variable value or the default if unset.
var envOrDefault = shared.EnvOrDefault

// getUploadDir returns the oCMS uploads directory from env or default.
var getUploadDir = shared.UploadDir

// ConfigFields returns the configuration fields needed for this source.
//
// The table prefix is configuration rather than a constant: PHP-Nuke's
// installer defaults to "nuke_" but lets the operator choose, and long-lived
// installs frequently use a site-specific prefix.
func (s *Source) ConfigFields() []types.ConfigField {
	return []types.ConfigField{
		{Name: "mysql_host", Label: "phpnuke.field_mysql_host", Type: "text", Required: true, Default: envOrDefault("PHPNUKE_HOST", "localhost")},
		{Name: "mysql_port", Label: "phpnuke.field_mysql_port", Type: "number", Required: true, Default: envOrDefault("PHPNUKE_PORT", "3306")},
		{Name: "mysql_user", Label: "phpnuke.field_mysql_user", Type: "text", Required: true, Default: os.Getenv("PHPNUKE_USER")},
		{Name: "mysql_password", Label: "phpnuke.field_mysql_password", Type: "password", Required: true, Default: os.Getenv("PHPNUKE_PASSWORD")},
		{Name: "mysql_database", Label: "phpnuke.field_mysql_database", Type: "text", Required: true, Default: os.Getenv("PHPNUKE_DB")},
		{Name: "table_prefix", Label: "phpnuke.field_table_prefix", Type: "text", Required: false, Default: envOrDefault("PHPNUKE_PREFIX", "nuke_"), Placeholder: "phpnuke.placeholder_table_prefix"},
		{Name: "files_path", Label: "phpnuke.field_files_path", Type: "text", Required: false, Default: os.Getenv("PHPNUKE_FILES"), Placeholder: "phpnuke.placeholder_files_path"},
		{Name: "language_code", Label: "phpnuke.field_language_code", Type: "text", Required: false, Default: os.Getenv("PHPNUKE_LANGUAGE"), Placeholder: "phpnuke.placeholder_language_code"},
	}
}

// SupportedImportOptions declares the import options this source acts on.
//
// PHP-Nuke has no menu table — site navigation lives in `blocks` as PHP
// snippets — so ImportMenus is absent rather than offered and ignored.
//
// TestSourcesDeclareTheOptionsTheyRead keeps this in step with the code.
func (s *Source) SupportedImportOptions() []string {
	return []string{
		"import_tags",
		"import_categories",
		"import_media",
		"import_posts",
		"import_pages",
		"import_users",
		"skip_existing",
	}
}

// buildDSN builds a MySQL DSN from the config.
//
// It delegates to shared.BuildMySQLDSN rather than formatting a string: raw
// interpolation lets a database name such as "db?allowAllFiles=true" inject
// driver parameters and turn on LOCAL INFILE handling, and it bypasses the
// OCMS_MIGRATOR_ALLOWED_DB_HOSTS allowlist entirely.
//
// The shared builder also pins the connection charset to utf8mb4, which is
// what makes a legacy single-byte database readable: PHP-Nuke installs from
// the cp1251/latin1 era store text in the table's own charset, and MySQL
// transcodes it on read only because the connection asks for utf8mb4.
func (s *Source) buildDSN(cfg map[string]string) (string, error) {
	return shared.BuildMySQLDSN(cfg, shared.MySQLDSNOptions{
		ConnectTimeout: connectTimeout,
		ReadTimeout:    readTimeout,
	})
}

// openReader builds the DSN and opens a bounded connection to the source.
func (s *Source) openReader(ctx context.Context, cfg map[string]string) (*Reader, error) {
	dsn, err := s.buildDSN(cfg)
	if err != nil {
		return nil, err
	}
	return NewReader(ctx, dsn, cfg["table_prefix"])
}

// closeReader closes a reader, logging rather than dropping a close failure.
func closeReader(reader *Reader) {
	if err := reader.Close(); err != nil {
		slog.Error("failed to close phpnuke reader", "error", err)
	}
}

// TestConnection tests the connection to the PHP-Nuke database.
func (s *Source) TestConnection(cfg map[string]string) error {
	return s.TestConnectionContext(context.Background(), cfg)
}

// TestConnectionContext tests the connection while honoring cancellation from
// the HTTP request that initiated it.
func (s *Source) TestConnectionContext(parent context.Context, cfg map[string]string) error {
	ctx, cancel := context.WithTimeout(parent, connectTimeout+readTimeout)
	defer cancel()

	prefix := cfg["table_prefix"]
	reader, err := s.openReader(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeReader(reader)

	// Query the core content tables so a wrong prefix fails here, with the
	// table name in the message, rather than midway through an import.
	if _, err := reader.StoryCount(ctx); err != nil {
		return fmt.Errorf("failed to query %sstories table: %w", prefix, err)
	}
	if _, err := reader.TopicCount(ctx); err != nil {
		return fmt.Errorf("failed to query %stopics table: %w", prefix, err)
	}
	return nil
}

// track records a created entity so the module can undo the import later.
//
// A tracking failure does not abort the import, but it must not be silent: an
// untracked item is invisible to "delete imported content" and is left behind
// as an orphan with no record of where it came from.
func (s *Source) track(ctx context.Context, tracker types.ImportTracker, result *types.ImportResult,
	entityType types.EntityType, id int64, rollback func(context.Context) error) bool {
	if tracker == nil {
		return true
	}
	if err := tracker.TrackImportedItem(ctx, s.Name(), string(entityType), id); err != nil {
		result.AddError("Failed to track imported %s %d: %v", entityType, id, err)
		if rollback != nil {
			// Tracking commonly fails because the import context was canceled.
			// Compensation must still get a short independent window or the
			// database driver will reject it immediately with the same error.
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trackingRollbackTimeout)
			rollbackErr := rollback(rollbackCtx)
			cancel()
			if rollbackErr != nil {
				result.AddError("Failed to roll back untracked %s %d: %v", entityType, id, rollbackErr)
			}
		}
		return false
	}
	return true
}

// sourceReader is the read side of a PHP-Nuke database, as the import stages
// use it.
//
// The stages take this interface rather than *Reader so they can be driven by
// an in-memory fake in tests. Without it the only way to exercise importUsers
// or importCategories would be a live MySQL server.
type sourceReader interface {
	GetStories(ctx context.Context) ([]Story, error)
	GetStaticPages(ctx context.Context) ([]StaticPage, error)
	GetTopics(ctx context.Context) ([]Topic, error)
	GetStoryCategories(ctx context.Context) ([]Category, error)
	GetPageCategories(ctx context.Context) ([]Category, error)
	GetEncyclopediaEntries(ctx context.Context) ([]EncyclopediaEntry, error)
	GetEncyclopediaTerms(ctx context.Context) (map[int64][]EncyclopediaTerm, error)
	GetStoryAuthors(ctx context.Context) ([]User, error)
	GetPublishingAdmins(ctx context.Context) ([]User, error)
	// Prefix lets a stage name the exact table it could not read, which is the
	// difference between an actionable message and "table doesn't exist".
	Prefix() string
}

// Compile-time proof that the live reader satisfies the stage interface.
var _ sourceReader = (*Reader)(nil)

// importContent holds the source rows an import reads before it writes
// anything, so media discovery can see every body that will be imported.
type importContent struct {
	stories     []Story
	staticPages []StaticPage
	encEntries  []EncyclopediaEntry
	encTerms    map[int64][]EncyclopediaTerm
}

// Import imports content from PHP-Nuke into oCMS.
func (s *Source) Import(ctx context.Context, db *sql.DB, cfg map[string]string,
	opts types.ImportOptions, tracker types.ImportTracker) (*types.ImportResult, error) {
	reader, err := s.openReader(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PHP-Nuke database: %w", err)
	}
	defer closeReader(reader)

	return s.importWithReader(ctx, db, reader, cfg, opts, tracker)
}

// importWithReader is Import with the source connection already open.
//
// Splitting it here is what makes the orchestration testable: every stage
// already takes sourceReader, and this was the last function reaching for the
// concrete *Reader. Ordering, option handling and the partial-result contract
// on a failed read all live in this function and are otherwise reachable only
// with a live MySQL server.
func (s *Source) importWithReader(ctx context.Context, db *sql.DB, reader sourceReader,
	cfg map[string]string, opts types.ImportOptions, tracker types.ImportTracker) (*types.ImportResult, error) {
	result := &types.ImportResult{}
	queries := store.New(db)

	fallbackAuthorID, err := s.getDefaultAuthorID(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("failed to get default author: %w", err)
	}

	langCode, err := s.resolveLanguageCode(ctx, queries, cfg)
	if err != nil {
		return nil, err
	}

	// Users first: pages.author_id is ON DELETE RESTRICT, so an imported page
	// cannot reference an author row that does not exist yet.
	userMap := make(map[string]int64)
	if opts.ImportUsers {
		if err := s.importUsers(ctx, queries, reader, userMap, opts, result, tracker); err != nil {
			result.AddError("Users import error: %v", err)
		}
	}

	// Taxonomy before content, so a story can be attached as it is created.
	topicMap := make(map[int64]int64)
	pageCategoryMap := make(map[int64]int64)
	if opts.ImportCategories {
		if err := s.importCategories(ctx, queries, reader, langCode, topicMap, pageCategoryMap,
			opts, result, tracker); err != nil {
			result.AddError("Categories import error: %v", err)
		}
	}
	storyCategoryMap := make(map[int64]int64)
	if opts.ImportTags {
		if err := s.importStoryCategoryTags(ctx, queries, reader, langCode, storyCategoryMap,
			opts, result, tracker); err != nil {
			result.AddError("Tags import error: %v", err)
		}
	}

	content, err := s.readContent(ctx, reader, opts)
	if err != nil {
		// Users, categories and tags have already written rows by this point.
		// Returning a nil result would discard every error and notice they
		// recorded, leaving the operator with a bare read failure and no
		// account of what is already in their database.
		result.AddError("Failed to read source content: %v", err)
		return result, err
	}

	// Which rows this run will write has to be settled before media, not inside
	// the page stages that run after it. See plannedSkips.
	skips := s.planSkips(ctx, queries, content, opts, result)

	// Media before bodies: every body is rewritten against the resulting map,
	// so the files have to exist in oCMS first.
	var mediaMap map[string]string
	if opts.ImportMedia {
		switch filesPath := strings.TrimSpace(cfg["files_path"]); {
		case !opts.ImportPosts && !opts.ImportPages:
			// The test is on the options, not on the rows they returned. Asking
			// whether any content was *read* conflated "you did not select the
			// content media is discovered from" with "the tables you selected are
			// empty" — and reported the second, a perfectly valid import, as an
			// operator error that finished the job as Partial.
			result.AddError("Media import was requested but neither posts nor pages were " +
				"selected; media is discovered from imported bodies, so nothing was imported.")
		case filesPath != "":
			mediaMap, err = s.importMedia(ctx, queries, content, skips, filesPath, getUploadDir(),
				fallbackAuthorID, langCode, result, tracker)
			if err != nil {
				result.AddError("Media import error: %v", err)
			}
		default:
			result.AddError("Media import was requested but no files path was configured; " +
				"inline image references were left pointing at the old site.")
		}
	}

	bodiesAltered := 0
	if opts.ImportPosts {
		s.importStories(ctx, queries, content.stories, userMap, fallbackAuthorID, langCode,
			topicMap, storyCategoryMap, mediaMap, skips, opts, result, tracker, &bodiesAltered)
	}
	if opts.ImportPages {
		// Both stages write pages, so the phase total is reported here rather
		// than inside either one. importStaticPages used to announce only its
		// own count, and the encyclopedia pages that followed then pushed
		// progress past the stated total.
		pageProgress := newPhaseProgress(ctx, tracker, s.Name(), types.EntityPage,
			len(content.staticPages)+len(content.encEntries))
		s.importStaticPages(ctx, queries, content.staticPages, fallbackAuthorID, langCode,
			pageCategoryMap, mediaMap, skips, pageProgress, opts, result, tracker, &bodiesAltered)
		s.importEncyclopedia(ctx, queries, content, fallbackAuthorID, langCode, mediaMap,
			skips, pageProgress, opts, result, tracker, &bodiesAltered)
	}
	if bodiesAltered > 0 {
		result.AddSummary("%d imported bodies contained markup the oCMS page HTML policy does "+
			"not allow (inline styles, <font>, <iframe> and similar) and it was removed. "+
			"Review those pages if the old site relied on it for layout.", bodiesAltered)
	}

	// Rebuild the FTS index so imported pages are searchable.
	if opts.ImportPosts || opts.ImportPages {
		if err := service.NewSearchService(db).RebuildIndex(ctx); err != nil {
			result.AddError("FTS index rebuild error: %v", err)
		}
	}

	return result, nil
}

// readContent loads every source row the selected options will import.
//
// Reading happens up front, before any write, because media discovery works
// from the bodies: only files a body actually references are imported, rather
// than every image in the PHP-Nuke document root.
func (s *Source) readContent(ctx context.Context, reader sourceReader, opts types.ImportOptions) (*importContent, error) {
	content := &importContent{encTerms: make(map[int64][]EncyclopediaTerm)}

	if opts.ImportPosts {
		stories, err := reader.GetStories(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read stories: %w", err)
		}
		content.stories = stories
	}
	if opts.ImportPages {
		pages, err := reader.GetStaticPages(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read pages: %w", err)
		}
		content.staticPages = pages

		entries, err := reader.GetEncyclopediaEntries(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read encyclopedia: %w", err)
		}
		content.encEntries = entries

		terms, err := reader.GetEncyclopediaTerms(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read encyclopedia terms: %w", err)
		}
		content.encTerms = terms
	}
	return content, nil
}

// bodies returns every HTML body this import will write, for media discovery.
func (c *importContent) bodies(skips *plannedSkips) []string {
	bodies := make([]string, 0, len(c.stories)+len(c.staticPages)+len(c.encEntries))
	for i := range c.stories {
		if !skips.skipped(types.EntityPost, c.stories[i].ID) {
			bodies = append(bodies, assembleStoryBody(&c.stories[i]))
		}
	}
	for i := range c.staticPages {
		if !skips.skipped(entityStaticPage, c.staticPages[i].ID) {
			bodies = append(bodies, assembleStaticPageBody(&c.staticPages[i]))
		}
	}
	for i := range c.encEntries {
		if !skips.skipped(entityEncyclopedia, c.encEntries[i].ID) {
			bodies = append(bodies, buildEncyclopediaBody(&c.encEntries[i], c.encTerms[c.encEntries[i].ID]))
		}
	}
	return bodies
}

// entityStaticPage and entityEncyclopedia separate the two kinds of source row
// that both become oCMS pages, so their ids cannot collide in a skip set.
const (
	entityStaticPage   = types.EntityType("phpnuke-static-page")
	entityEncyclopedia = types.EntityType("phpnuke-encyclopedia")
)

// plannedSkips records the source rows a SkipExisting run has already decided
// not to write. A nil value skips nothing, which is what a run without
// SkipExisting wants.
//
// The decision is made once, up front, rather than inside each page stage,
// because media is discovered from bodies and imported *before* any page stage
// runs. Deciding later meant every rerun imported a fresh copy of every
// referenced file — new UUID, new media row, new derivative — and then skipped
// the page that would have used it, so each rerun left another complete set of
// unattached duplicates in the media library.
//
// Settling it before the stages also stops one run from skipping its own work:
// two source rows sharing a title used to have the second one skipped because
// the first had just claimed the slug, silently dropping a distinct story.
type plannedSkips struct {
	byEntity map[types.EntityType]map[int64]bool
}

// skipped reports whether a source row was ruled out before the import began.
func (p *plannedSkips) skipped(entity types.EntityType, id int64) bool {
	if p == nil {
		return false
	}
	return p.byEntity[entity][id]
}

func (p *plannedSkips) mark(entity types.EntityType, id int64) {
	if p.byEntity[entity] == nil {
		p.byEntity[entity] = make(map[int64]bool)
	}
	p.byEntity[entity][id] = true
}

// planSkips probes the destination for every source row a run would write and
// records the ones whose slug is already taken.
func (s *Source) planSkips(ctx context.Context, queries *store.Queries, content *importContent,
	opts types.ImportOptions, result *types.ImportResult) *plannedSkips {
	if !opts.SkipExisting {
		return nil
	}
	skips := &plannedSkips{byEntity: make(map[types.EntityType]map[int64]bool)}

	// Collected first so the probing runs in one loop with one cancellation
	// check. Without it a canceled job kept probing to the end of the archive,
	// and because pageExists fails closed it recorded a "failed to check"
	// error for every remaining row — handing the operator a cancellation
	// dressed up as a hundred import failures.
	type candidate struct {
		entity   types.EntityType
		id       int64
		title    string
		fallback string
	}
	candidates := make([]candidate, 0,
		len(content.stories)+len(content.staticPages)+len(content.encEntries))
	for i := range content.stories {
		title, fallback := storyIdentity(&content.stories[i])
		candidates = append(candidates,
			candidate{types.EntityPost, content.stories[i].ID, title, fallback})
	}
	for i := range content.staticPages {
		title, fallback := staticPageIdentity(&content.staticPages[i])
		candidates = append(candidates,
			candidate{entityStaticPage, content.staticPages[i].ID, title, fallback})
	}
	for i := range content.encEntries {
		title, fallback := encyclopediaIdentity(&content.encEntries[i])
		candidates = append(candidates,
			candidate{entityEncyclopedia, content.encEntries[i].ID, title, fallback})
	}

	for _, c := range candidates {
		if ctx.Err() != nil {
			return skips
		}
		if s.pageExists(ctx, queries, baseSlug(c.title, c.fallback), c.title, result) {
			skips.mark(c.entity, c.id)
		}
	}
	return skips
}

// getDefaultAuthorID returns the oldest oCMS account, used when a story's own
// author cannot be resolved to an imported one.
//
// Oldest, not newest. ListUsers orders by created_at DESC, so taking the first
// row picked whichever account was created most recently — which, on any run
// after the first, is one of the inert role-"public" accounts this importer
// itself created. That made the fallback author change between runs and
// attributed content to an account nobody can sign into. The oldest account is
// stable and, on any real install, the original administrator.
func (s *Source) getDefaultAuthorID(ctx context.Context, queries *store.Queries) (int64, error) {
	total, err := queries.CountUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("count destination users: %w", err)
	}
	if total == 0 {
		return 0, errors.New("no users found in oCMS database")
	}
	users, err := queries.ListUsers(ctx, store.ListUsersParams{Limit: 1, Offset: total - 1})
	if err != nil {
		return 0, err
	}
	if len(users) == 0 {
		return 0, errors.New("no users found in oCMS database")
	}
	return users[0].ID, nil
}

// resolveLanguageCode determines which oCMS language receives the import.
//
// A PHP-Nuke database carries no usable language metadata — the per-row
// language columns are almost always blank — so the operator names the target
// explicitly. An unset value falls back to the site default. A named language
// that does not exist is a hard error rather than a silent write into the
// default language, which is content the operator would then have to unpick by
// hand.
func (s *Source) resolveLanguageCode(ctx context.Context, queries *store.Queries, cfg map[string]string) (string, error) {
	requested := strings.ToLower(strings.TrimSpace(cfg["language_code"]))
	if requested == "" {
		defaultLang, err := shared.RoutableDefaultLanguage(ctx, queries)
		if err != nil {
			return "", fmt.Errorf("failed to get default language: %w", err)
		}
		return defaultLang.Code, nil
	}
	if !util.IsValidLangCode(requested) || util.IsReservedLanguageCode(requested) {
		return "", fmt.Errorf("language code %q is not a valid oCMS language code", requested)
	}
	language, err := queries.GetLanguageByCode(ctx, requested)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("language %q does not exist in oCMS; create it before importing", requested)
	}
	if err != nil {
		return "", fmt.Errorf("failed to look up language %q: %w", requested, err)
	}
	if !language.IsActive {
		return "", fmt.Errorf("language %q is inactive; activate it before importing", requested)
	}
	return language.Code, nil
}

// Slug allocation lives in stages.go, alongside the writes it protects.
