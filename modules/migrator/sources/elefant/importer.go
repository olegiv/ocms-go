// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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

// PublicRouteChecker reports whether a concrete URL belongs to a registered
// module route. Core routes are protected separately below.
type PublicRouteChecker interface {
	OwnsPublicPath(path string) bool
}

// Source implements the migrator.Source interface for Elefant CMS.
type Source struct {
	publicRouteChecker PublicRouteChecker
}

// NewSource creates a new Elefant CMS source.
func NewSource() *Source {
	return &Source{}
}

// SetPublicRouteChecker supplies destination module-route ownership checks for
// imported page slugs and aliases.
func (s *Source) SetPublicRouteChecker(checker PublicRouteChecker) {
	s.publicRouteChecker = checker
}

// Name returns the unique identifier for this source.
func (s *Source) Name() string {
	return "elefant"
}

// DisplayName returns the human-readable name.
func (s *Source) DisplayName() string {
	return "Elefant CMS"
}

// Description returns a brief description of the source.
func (s *Source) Description() string {
	return "elefant.description"
}

// ConfigFields returns the configuration fields needed for this source.
// Defaults are read from environment variables (ELEFANT_HOST, ELEFANT_PORT, etc.)
func (s *Source) ConfigFields() []types.ConfigField {
	return []types.ConfigField{
		{Name: "mysql_host", Label: "elefant.field_mysql_host", Type: "text", Required: true, Default: envOrDefault("ELEFANT_HOST", "localhost")},
		{Name: "mysql_port", Label: "elefant.field_mysql_port", Type: "number", Required: true, Default: envOrDefault("ELEFANT_PORT", "3306")},
		{Name: "mysql_user", Label: "elefant.field_mysql_user", Type: "text", Required: true, Default: os.Getenv("ELEFANT_USER")},
		{Name: "mysql_password", Label: "elefant.field_mysql_password", Type: "password", Required: true, Default: os.Getenv("ELEFANT_PASSWORD")},
		{Name: "mysql_database", Label: "elefant.field_mysql_database", Type: "text", Required: true, Default: os.Getenv("ELEFANT_DB")},
		{Name: "table_prefix", Label: "elefant.field_table_prefix", Type: "text", Required: false, Default: os.Getenv("ELEFANT_PREFIX"), Placeholder: "elefant.placeholder_table_prefix"},
		{Name: "files_path", Label: "elefant.field_files_path", Type: "text", Required: false, Default: os.Getenv("ELEFANT_FILES"), Placeholder: "elefant.placeholder_files_path"},
	}
}

// SupportedImportOptions declares the import options this source acts on.
//
// Elefant has no vocabulary or navigation tables to read, so ImportCategories
// and ImportMenus are absent — Import does not consult either. They used to be
// offered anyway, checked by default, which made every Elefant run promise
// categories and menus and silently deliver neither.
//
// TestSourcesDeclareTheOptionsTheyRead keeps this in step with the code.
func (s *Source) SupportedImportOptions() []string {
	return []string{
		"import_tags",
		"import_media",
		"import_posts",
		"import_pages",
		"import_users",
		"skip_existing",
	}
}

// envOrDefault returns the environment variable value or the default if not set.
var envOrDefault = shared.EnvOrDefault

// Connection bounds for the source database. Mirrors the Drupal source: a
// migration is a long sequence of modest reads, so the pool stays small and
// every statement is bounded.
const (
	connectTimeout          = 10 * time.Second
	readTimeout             = 60 * time.Second
	trackingRollbackTimeout = 30 * time.Second
	maxOpenConns            = 4
	connMaxLifetime         = 30 * time.Minute
)

// buildDSN builds a MySQL DSN from the config.
//
// It delegates to shared.BuildMySQLDSN rather than formatting a string: raw
// interpolation let a database name such as "db?allowAllFiles=true" inject
// driver parameters and turn on LOCAL INFILE handling, and it bypassed the
// OCMS_MIGRATOR_ALLOWED_DB_HOSTS allowlist entirely.
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
		slog.Error("failed to close elefant reader", "error", err)
	}
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

// createTrackedPageAlias creates an alias and makes it subject to the same
// compensating tracking guarantee as every other imported entity.
func (s *Source) createTrackedPageAlias(ctx context.Context, queries *store.Queries,
	pageID int64, alias string, createdAt time.Time, result *types.ImportResult,
	tracker types.ImportTracker) error {
	page, err := queries.GetPageByID(ctx, pageID)
	if err != nil {
		return fmt.Errorf("load page %d for alias %q: %w", pageID, alias, err)
	}
	alias = strings.Trim(alias, "/")
	if alias == "" {
		return errors.New("alias is empty")
	}
	if elefantCorePathReserved(alias) && !isElefantBlogPostAlias(alias) {
		return fmt.Errorf("alias %q conflicts with a reserved oCMS route", alias)
	}
	if s.publicRouteChecker != nil && s.publicRouteChecker.OwnsPublicPath("/"+alias) {
		return fmt.Errorf("alias %q conflicts with a registered oCMS module route", alias)
	}
	if prefix, remainder, prefixed, prefixErr := activeLanguageAliasPrefix(ctx, queries, alias); prefixErr != nil {
		return prefixErr
	} else if prefixed {
		if remainder == "" || languageChildRouteReserved(remainder) {
			return fmt.Errorf("alias %q is owned by the %q language router", alias, prefix)
		}
		occupied, occupiedErr := languageAliasRouteOccupied(ctx, queries, prefix, remainder)
		if occupiedErr != nil {
			return fmt.Errorf("check language-prefixed alias %q: %w", alias, occupiedErr)
		}
		if occupied {
			return fmt.Errorf("alias %q is already owned in the %q language namespace", alias, prefix)
		}
		return s.createTrackedRedirect(ctx, queries, "/"+alias, canonicalPagePath(ctx, queries, page), createdAt, result, tracker)
	}

	slugOwner, err := queries.GetPageBySlug(ctx, alias)
	switch {
	case err == nil && slugOwner.ID == pageID:
		return nil
	case err == nil && slugOwner.LanguageCode != page.LanguageCode:
		aliasOwner, aliasErr := queries.GetPageByAlias(ctx, alias)
		if aliasErr == nil && aliasOwner.ID != pageID {
			return fmt.Errorf("alias %q is already owned by page %d", alias, aliasOwner.ID)
		}
		if aliasErr != nil && !errors.Is(aliasErr, sql.ErrNoRows) {
			return fmt.Errorf("check alias %q ownership: %w", alias, aliasErr)
		}
		return s.createTrackedRedirect(ctx, queries, "/"+alias, canonicalPagePath(ctx, queries, page), createdAt, result, tracker)
	case err == nil:
		return fmt.Errorf("alias %q is shadowed by existing page slug owned by page %d", alias, slugOwner.ID)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check alias %q against page slugs: %w", alias, err)
	}
	redirectOccupied, err := redirectPathOccupied(ctx, queries, "/"+alias)
	if err != nil {
		return fmt.Errorf("check redirect ownership for alias %q: %w", alias, err)
	}
	if redirectOccupied {
		return fmt.Errorf("alias %q is shadowed by an existing redirect", alias)
	}
	created, err := queries.CreatePageAlias(ctx, store.CreatePageAliasParams{
		PageID: pageID, Alias: alias, CreatedAt: createdAt,
	})
	if err != nil {
		return err
	}
	if !s.track(ctx, tracker, result, types.EntityAlias, created.ID, func(rollbackCtx context.Context) error {
		return queries.DeletePageAlias(rollbackCtx, created.ID)
	}) {
		return nil
	}
	result.AliasesImported++
	return nil
}

func activeLanguageAliasPrefix(ctx context.Context, queries *store.Queries, alias string) (string, string, bool, error) {
	first, remainder, hasRemainder := strings.Cut(alias, "/")
	first = strings.ToLower(first)
	languages, err := queries.ListActiveLanguages(ctx)
	if err != nil {
		return "", "", false, fmt.Errorf("list active languages: %w", err)
	}
	for _, language := range languages {
		if strings.EqualFold(language.Code, first) && util.IsValidLangCode(language.Code) && !util.IsReservedLanguageCode(language.Code) {
			if !hasRemainder {
				remainder = ""
			}
			return language.Code, remainder, true, nil
		}
	}
	return "", "", false, nil
}

func languageChildRouteReserved(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
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

func languageAliasRouteOccupied(ctx context.Context, queries *store.Queries, languageCode, alias string) (bool, error) {
	page, err := queries.GetPageBySlug(ctx, alias)
	switch {
	case err == nil && page.LanguageCode == languageCode:
		return true, nil
	case err == nil, errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, err
	}
	page, err = queries.GetPageByAlias(ctx, alias)
	switch {
	case err == nil:
		return page.LanguageCode == languageCode, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

func canonicalPagePath(ctx context.Context, queries *store.Queries, page store.Page) string {
	defaultLanguage, err := queries.GetDefaultLanguage(ctx)
	if err == nil && page.LanguageCode != "" && page.LanguageCode != defaultLanguage.Code {
		return "/" + page.LanguageCode + "/" + page.Slug
	}
	return "/" + page.Slug
}

func (s *Source) makeUniquePageSlug(ctx context.Context, queries *store.Queries, baseSlug string) string {
	return shared.MakeUniqueSlugWithGuard(ctx, queries, baseSlug, func(slug string) bool {
		if elefantCorePathReserved(slug) {
			return false
		}
		if s.publicRouteChecker != nil && s.publicRouteChecker.OwnsPublicPath("/"+slug) {
			return false
		}
		occupied, err := redirectPathOccupied(ctx, queries, "/"+slug)
		return err == nil && !occupied
	})
}

func elefantCorePathReserved(path string) bool {
	path = strings.Trim(path, "/")
	if path == "" {
		return true
	}
	first, _, _ := strings.Cut(path, "/")
	if util.IsReservedLanguageCode(strings.ToLower(first)) {
		return true
	}
	switch path {
	case "sitemap.xml", "robots.txt", "favicon.ico":
		return true
	}
	return path == ".well-known" || strings.HasPrefix(path, ".well-known/")
}

func isElefantBlogPostAlias(alias string) bool {
	parts := strings.Split(strings.Trim(alias, "/"), "/")
	if len(parts) != 3 || parts[0] != "blog" || parts[1] != "post" {
		return false
	}
	for _, char := range parts[2] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return parts[2] != ""
}

func redirectPathOccupied(ctx context.Context, queries *store.Queries, sourcePath string) (bool, error) {
	_, err := queries.GetRedirectBySourcePath(ctx, sourcePath)
	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
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

func (s *Source) createTrackedRedirect(ctx context.Context, queries *store.Queries,
	sourcePath, targetURL string, createdAt time.Time, result *types.ImportResult,
	tracker types.ImportTracker) error {
	redirects, err := queries.ListEnabledRedirects(ctx)
	if err != nil {
		return fmt.Errorf("list redirects before creating %q: %w", sourcePath, err)
	}
	for _, redirect := range redirects {
		if redirect.IsWildcard && wildcardRedirectMatchesPath(redirect.SourcePath, sourcePath) {
			return fmt.Errorf("redirect %q is shadowed by enabled wildcard redirect %q", sourcePath, redirect.SourcePath)
		}
	}
	existing, err := queries.GetRedirectBySourcePath(ctx, sourcePath)
	switch {
	case err == nil && existing.TargetUrl == targetURL && existing.Enabled:
		result.RedirectsSkipped++
		return nil
	case err == nil:
		return fmt.Errorf("redirect %q already targets %q", sourcePath, existing.TargetUrl)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check redirect %q: %w", sourcePath, err)
	}
	redirect, err := queries.CreateRedirect(ctx, store.CreateRedirectParams{
		SourcePath: sourcePath,
		TargetUrl:  targetURL,
		StatusCode: http.StatusMovedPermanently,
		IsWildcard: false,
		TargetType: model.TargetSelf,
		Enabled:    true,
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
	})
	if err != nil {
		return err
	}
	if !s.track(ctx, tracker, result, types.EntityRedirect, redirect.ID, func(rollbackCtx context.Context) error {
		return queries.DeleteRedirect(rollbackCtx, redirect.ID)
	}) {
		return nil
	}
	result.RedirectsImported++
	return nil
}

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

func (s *Source) cleanupMediaFiles(ctx context.Context, tracker types.ImportTracker,
	canonicalUploadRoot, mediaUUID string) error {
	err := imaging.DeleteMediaFilesFromCanonicalRoot(canonicalUploadRoot, mediaUUID)
	if err == nil {
		return nil
	}
	queuer, ok := tracker.(types.MediaCleanupQueuer)
	if !ok {
		return err
	}
	queueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trackingRollbackTimeout)
	queueErr := queuer.QueueMediaCleanup(queueCtx, s.Name(), canonicalUploadRoot, mediaUUID)
	cancel()
	if queueErr != nil {
		return errors.Join(err, fmt.Errorf("queue media cleanup: %w", queueErr))
	}
	return fmt.Errorf("%w (durable cleanup retry queued)", err)
}

// TestConnection tests the connection to the Elefant database.
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

	// Query both counts to verify the expected tables exist.
	if _, err := reader.GetPostCount(ctx); err != nil {
		return fmt.Errorf("failed to query %sblog_post table: %w", prefix, err)
	}
	if _, err := reader.GetTagCount(ctx); err != nil {
		return fmt.Errorf("failed to query %sblog_tag table: %w", prefix, err)
	}

	return nil
}

// getUploadDir returns the oCMS uploads directory from env or default.
var getUploadDir = shared.UploadDir

// Import imports content from Elefant CMS into oCMS.
func (s *Source) Import(ctx context.Context, db *sql.DB, cfg map[string]string, opts types.ImportOptions, tracker types.ImportTracker) (*types.ImportResult, error) {
	result := &types.ImportResult{}

	// Connect to Elefant database
	reader, err := s.openReader(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Elefant database: %w", err)
	}
	defer closeReader(reader)

	// Get oCMS store
	queries := store.New(db)

	// Get the first admin user as the author for imported content
	authorID, err := s.getDefaultAuthorID(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("failed to get default author: %w", err)
	}

	// Get the default language for imported content
	defaultLang, err := shared.RoutableDefaultLanguage(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("failed to get default language: %w", err)
	}

	// Import tags first (posts reference them)
	var tagMap map[string]int64
	if opts.ImportTags {
		tagMap, err = s.importTags(ctx, queries, reader, defaultLang.Code, opts, result, tracker)
		if err != nil {
			result.AddError("Tags import error: %v", err)
		}
	} else {
		// Build tag map from existing tags
		tagMap, err = s.buildExistingTagMap(ctx, queries)
		if err != nil {
			return nil, fmt.Errorf("failed to build tag map: %w", err)
		}
	}

	// Import media files (before posts so we can replace URLs in body)
	var mediaMap map[string]string
	if opts.ImportMedia {
		filesPath := cfg["files_path"]
		if filesPath != "" {
			uploadDir := getUploadDir()
			mediaMap, err = s.importMedia(ctx, queries, filesPath, uploadDir, authorID, result, tracker)
			if err != nil {
				result.AddError("Media import error: %v", err)
			}
		}
	}

	// Import posts
	if opts.ImportPosts {
		if err := s.importPosts(ctx, queries, reader, authorID, defaultLang.Code, tagMap, mediaMap, opts, result, tracker); err != nil {
			result.AddError("Posts import error: %v", err)
		}
	}

	// Import pages (static webpages)
	if opts.ImportPages {
		if err := s.importPages(ctx, queries, reader, authorID, defaultLang.Code, mediaMap, opts, result, tracker); err != nil {
			result.AddError("Pages import error: %v", err)
		}
	}

	// Import users (as public users only)
	if opts.ImportUsers {
		if err := s.importUsers(ctx, queries, reader, opts, result, tracker); err != nil {
			result.AddError("Users import error: %v", err)
		}
	}

	// Rebuild FTS index to ensure imported pages are searchable
	if opts.ImportPosts || opts.ImportPages {
		searchService := service.NewSearchService(db)
		if err := searchService.RebuildIndex(ctx); err != nil {
			result.AddError("FTS index rebuild error: %v", err)
		}
	}

	return result, nil
}

// getDefaultAuthorID gets the first admin user's ID.
func (s *Source) getDefaultAuthorID(ctx context.Context, queries *store.Queries) (int64, error) {
	users, err := queries.ListUsers(ctx, store.ListUsersParams{
		Limit:  1,
		Offset: 0,
	})
	if err != nil {
		return 0, err
	}
	if len(users) == 0 {
		return 0, fmt.Errorf("no users found in oCMS database")
	}
	return users[0].ID, nil
}

// buildExistingTagMap builds a map of slug -> tag ID for existing tags.
func (s *Source) buildExistingTagMap(ctx context.Context, queries *store.Queries) (map[string]int64, error) {
	tags, err := queries.ListAllTags(ctx)
	if err != nil {
		return nil, err
	}

	tagMap := make(map[string]int64)
	for _, tag := range tags {
		tagMap[tag.Slug] = tag.ID
	}
	return tagMap, nil
}

// importTags imports tags from Elefant.
func (s *Source) importTags(ctx context.Context, queries *store.Queries, reader *Reader, defaultLangCode string, opts types.ImportOptions, result *types.ImportResult, tracker types.ImportTracker) (map[string]int64, error) {
	elefantTags, err := reader.GetTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get tags from Elefant: %w", err)
	}

	tagMap := make(map[string]int64)
	now := time.Now()

	for _, et := range elefantTags {
		// Use the tag ID as both name and slug (Elefant stores tag name as ID)
		slug := util.Slugify(et.ID)
		name := et.ID

		// Check if tag already exists
		existing, err := queries.GetTagBySlug(ctx, slug)
		if err == nil {
			// Tag exists
			if opts.SkipExisting {
				result.TagsSkipped++
				tagMap[slug] = existing.ID
				continue
			}
			// Use existing tag
			tagMap[slug] = existing.ID
			result.TagsSkipped++
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			result.AddError("Failed to check for existing tag '%s': %v", name, err)
			continue
		}

		// Create new tag
		tag, err := queries.CreateTag(ctx, store.CreateTagParams{
			Name:         name,
			Slug:         slug,
			LanguageCode: defaultLangCode,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			result.AddError("Failed to create tag '%s': %v", name, err)
			continue
		}

		if !s.track(ctx, tracker, result, types.EntityTag, tag.ID, func(rollbackCtx context.Context) error {
			return queries.DeleteTag(rollbackCtx, tag.ID)
		}) {
			continue
		}

		tagMap[slug] = tag.ID
		result.TagsImported++
	}

	return tagMap, nil
}

// importMedia imports media files from Elefant's files directory.
// It returns a map of old Elefant paths to new oCMS media URLs for replacing in post bodies.
func (s *Source) importMedia(ctx context.Context, queries *store.Queries, filesPath, uploadDir string,
	userID int64, result *types.ImportResult, tracker types.ImportTracker) (map[string]string, error) {

	// Map: old Elefant path → new oCMS URL
	mediaMap := make(map[string]string)

	mediaRoot, err := shared.OpenMediaRoot(filesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open media root: %w", err)
	}
	defer func() {
		if err := mediaRoot.Close(); err != nil {
			slog.Error("failed to close elefant media root", "error", err)
		}
	}()

	files, err := mediaRoot.Scan()
	if err != nil {
		return nil, fmt.Errorf("failed to scan media files: %w", err)
	}

	if len(files) == 0 {
		return mediaMap, nil
	}

	// Get default language for media creation
	defaultLang, err := shared.RoutableDefaultLanguage(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("failed to get default language: %w", err)
	}

	canonicalUploadRoot, err := imaging.CanonicalUploadRoot(uploadDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open uploads root: %w", err)
	}
	processor := imaging.NewProcessor(canonicalUploadRoot)
	now := time.Now()

	for _, file := range files {
		// Check context for cancellation
		select {
		case <-ctx.Done():
			return mediaMap, ctx.Err()
		default:
		}

		// Note: We don't skip existing media by filename because different files
		// might have the same name. Each import creates new media entries.

		// Open source file
		srcFile, err := mediaRoot.Open(file.Path)
		if err != nil {
			result.AddError("Failed to open %s: %v", file.Path, err)
			continue
		}

		fileUUID := uuid.New().String()

		// Process based on type
		if processor.IsImage(file.MimeType) {
			// Process image - creates original and variants. Migration files
			// come from a trusted local directory, so an oversized photo is
			// downscaled rather than dropped.
			processResult, processErr := processor.ProcessImageWithOptions(srcFile, fileUUID, file.Filename,
				imaging.ProcessOptions{DownscaleOversized: true})
			closeErr := srcFile.Close()
			if processErr != nil {
				cleanupErr := s.cleanupMediaFiles(ctx, tracker, canonicalUploadRoot, fileUUID)
				result.AddError("Failed to process %s: %v", file.Path,
					errors.Join(processErr, closeErr, cleanupErr))
				continue
			}
			if closeErr != nil {
				cleanupErr := s.cleanupMediaFiles(ctx, tracker, canonicalUploadRoot, fileUUID)
				result.AddError("Failed to close %s: %v", file.Path, errors.Join(closeErr, cleanupErr))
				continue
			}

			// Create media record
			media, err := queries.CreateMedia(ctx, store.CreateMediaParams{
				Uuid:         fileUUID,
				Filename:     file.Filename,
				MimeType:     processResult.MimeType,
				Size:         processResult.Size,
				Width:        sql.NullInt64{Int64: int64(processResult.Width), Valid: true},
				Height:       sql.NullInt64{Int64: int64(processResult.Height), Valid: true},
				Alt:          sql.NullString{String: "", Valid: true},
				Caption:      sql.NullString{String: "", Valid: true},
				FolderID:     sql.NullInt64{Valid: false},
				UploadedBy:   userID,
				LanguageCode: defaultLang.Code,
				CreatedAt:    now,
				UpdatedAt:    now,
			})
			if err != nil {
				cleanupErr := s.cleanupMediaFiles(ctx, tracker, canonicalUploadRoot, fileUUID)
				result.AddError("Failed to create media record for %s: %v", file.Path,
					errors.Join(err, cleanupErr))
				continue
			}

			if !s.track(ctx, tracker, result, types.EntityMedia, media.ID, func(rollbackCtx context.Context) error {
				if err := queries.DeleteMedia(rollbackCtx, media.ID); err != nil {
					return err
				}
				return s.cleanupMediaFiles(rollbackCtx, tracker, canonicalUploadRoot, fileUUID)
			}) {
				continue
			}

			// Best effort for a partial failure, but CreateAllVariants errors
			// only when every variant failed — the signal that the whole
			// library will have no thumbnails. See the Drupal source.
			variants, varErr := processor.CreateAllVariants(processResult.FilePath, fileUUID, file.Filename)
			if varErr != nil {
				result.AddError("%s: no resized variants could be created: %v", file.Filename, varErr)
			}
			for _, v := range variants {
				if _, err := queries.CreateMediaVariant(ctx, store.CreateMediaVariantParams{
					MediaID:   media.ID,
					Type:      v.Type,
					Width:     int64(v.Width),
					Height:    int64(v.Height),
					Size:      v.Size,
					CreatedAt: now,
				}); err != nil {
					slog.Warn("failed to record media variant",
						"media_id", media.ID, "variant", v.Type, "error", err)
				}
			}

			// Map old path to new URL
			mediaMap["/files/"+file.Path] = model.MediaURL(model.VariantOriginal, fileUUID, file.Filename)

		} else {
			// Non-image file - save directly without processing
			writtenRoot, saveErr := s.saveNonImageFile(srcFile, canonicalUploadRoot, fileUUID, file.Filename)
			closeErr := srcFile.Close()
			if err := errors.Join(saveErr, closeErr); err != nil {
				var cleanupErr error
				if writtenRoot != "" {
					cleanupErr = s.cleanupMediaFiles(ctx, tracker, writtenRoot, fileUUID)
				}
				result.AddError("Failed to save %s: %v", file.Path, errors.Join(err, cleanupErr))
				continue
			}

			// Create media record for non-image
			media, err := queries.CreateMedia(ctx, store.CreateMediaParams{
				Uuid:         fileUUID,
				Filename:     file.Filename,
				MimeType:     file.MimeType,
				Size:         file.Size,
				Width:        sql.NullInt64{Valid: false},
				Height:       sql.NullInt64{Valid: false},
				Alt:          sql.NullString{String: "", Valid: true},
				Caption:      sql.NullString{String: "", Valid: true},
				FolderID:     sql.NullInt64{Valid: false},
				UploadedBy:   userID,
				LanguageCode: defaultLang.Code,
				CreatedAt:    now,
				UpdatedAt:    now,
			})
			if err != nil {
				cleanupErr := s.cleanupMediaFiles(ctx, tracker, writtenRoot, fileUUID)
				result.AddError("Failed to create media record for %s: %v", file.Path,
					errors.Join(err, cleanupErr))
				continue
			}

			if !s.track(ctx, tracker, result, types.EntityMedia, media.ID, func(rollbackCtx context.Context) error {
				if err := queries.DeleteMedia(rollbackCtx, media.ID); err != nil {
					return err
				}
				return s.cleanupMediaFiles(rollbackCtx, tracker, writtenRoot, fileUUID)
			}) {
				continue
			}

			// Map old path to new URL
			mediaMap["/files/"+file.Path] = model.MediaURL(model.VariantOriginal, fileUUID, file.Filename)
		}

		result.MediaImported++
	}

	return mediaMap, nil
}

// saveNonImageFile saves a non-image file to the uploads directory.
// The filename is sanitized to prevent path traversal attacks.
func (s *Source) saveNonImageFile(src *os.File, uploadDir, fileUUID, filename string) (string, error) {
	return shared.SaveNonImageFileWithCanonicalRoot(src, uploadDir, fileUUID, filename)
}

// importPosts imports blog posts from Elefant.
func (s *Source) importPosts(ctx context.Context, queries *store.Queries, reader *Reader, authorID int64, defaultLangCode string, tagMap map[string]int64, mediaMap map[string]string, opts types.ImportOptions, result *types.ImportResult, tracker types.ImportTracker) error {
	posts, err := reader.GetBlogPosts(ctx)
	if err != nil {
		return fmt.Errorf("failed to get posts from Elefant: %w", err)
	}

	now := time.Now()

	for _, post := range posts {
		// Generate slug from title if not present (older Elefant versions)
		baseSlug := post.Slug
		if baseSlug == "" {
			baseSlug = util.Slugify(post.Title)
		}

		// Check if page already exists by slug
		if opts.SkipExisting {
			_, err := queries.GetPageBySlug(ctx, baseSlug)
			if err == nil {
				// Page exists, skip it
				result.PostsSkipped++
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				result.AddError("Failed to check for existing page '%s': %v", post.Title, err)
				continue
			}
		}

		// Make slug unique if it already exists (handles duplicates)
		slug := s.makeUniquePageSlug(ctx, queries, baseSlug)
		if slug == "" {
			result.AddError("Failed to allocate a reachable slug for post '%s'", post.Title)
			continue
		}

		// Map Elefant published status to oCMS status
		status := "draft"
		if post.IsPublished() {
			status = "published"
		}

		// Replace Elefant file paths with oCMS media URLs in body
		body := post.Body
		if mediaMap != nil {
			body = replaceMediaURLs(body, mediaMap)
		}
		// Sanitize imported HTML to match the admin UI policy
		body = security.SanitizePageHTML(body)

		// Create page
		page, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title:           post.Title,
			Slug:            slug,
			Body:            body,
			Status:          status,
			AuthorID:        authorID,
			LanguageCode:    defaultLangCode,
			PageType:        "post",
			MetaTitle:       post.Title,
			MetaDescription: nullStringToString(post.Description),
			MetaKeywords:    nullStringToString(post.Keywords),
			CreatedAt:       post.Timestamp,
			UpdatedAt:       now,
		})
		if err != nil {
			result.AddError("Failed to create page '%s': %v", post.Title, err)
			continue
		}

		if !s.track(ctx, tracker, result, types.EntityPost, page.ID, func(rollbackCtx context.Context) error {
			return queries.DeletePage(rollbackCtx, page.ID)
		}) {
			continue
		}

		// Create page alias for old Elefant URL (blog/post/{id})
		alias := fmt.Sprintf("blog/post/%d", post.ID)
		aliasErr := s.createTrackedPageAlias(ctx, queries, page.ID, alias, now, result, tracker)
		if aliasErr != nil {
			result.AddError("Failed to create legacy alias '%s' for page '%s': %v",
				alias, post.Title, aliasErr)
			slog.Warn("failed to create blog alias for page",
				"page_id", page.ID,
				"alias", alias,
				"error", aliasErr)
		}

		// Set published_at if published
		if status == "published" {
			if _, err := queries.PublishPage(ctx, store.PublishPageParams{
				PublishedAt: sql.NullTime{Time: post.Timestamp, Valid: true},
				UpdatedAt:   now,
				ID:          page.ID,
			}); err != nil {
				result.AddError("Failed to set published_at for '%s': %v", post.Title, err)
			}
		}

		// Parse and associate tags
		if opts.ImportTags && post.Tags != "" {
			tagSlugs := parseElefantTags(post.Tags)
			for _, tagSlug := range tagSlugs {
				tSlug := util.Slugify(tagSlug)
				if tagID, ok := tagMap[tSlug]; ok {
					if err := queries.AddTagToPage(ctx, store.AddTagToPageParams{
						PageID: page.ID,
						TagID:  tagID,
					}); err != nil {
						result.AddError("Failed to add tag '%s' to page '%s': %v", tagSlug, post.Title, err)
					}
				}
			}
		}

		result.PostsImported++
	}

	return nil
}

// importPages imports static webpages from Elefant.
func (s *Source) importPages(ctx context.Context, queries *store.Queries, reader *Reader, authorID int64, defaultLangCode string, mediaMap map[string]string, opts types.ImportOptions, result *types.ImportResult, tracker types.ImportTracker) error {
	pages, err := reader.GetWebpages(ctx)
	if err != nil {
		return fmt.Errorf("failed to get webpages from Elefant: %w", err)
	}

	now := time.Now()

	for _, wp := range pages {
		// Convert Elefant page ID (path like "about/team") to slug
		baseSlug := util.Slugify(wp.ID)
		if baseSlug == "" {
			baseSlug = util.Slugify(wp.Title)
		}

		// Check if page already exists by slug
		if opts.SkipExisting {
			_, err := queries.GetPageBySlug(ctx, baseSlug)
			if err == nil {
				result.PagesSkipped++
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				result.AddError("Failed to check for existing page '%s': %v", wp.Title, err)
				continue
			}
		}

		// Make slug unique if it already exists
		slug := s.makeUniquePageSlug(ctx, queries, baseSlug)
		if slug == "" {
			result.AddError("Failed to allocate a reachable slug for page '%s'", wp.Title)
			continue
		}

		// Map Elefant access to oCMS status
		status := "draft"
		if wp.IsPublic() {
			status = "published"
		}

		// Replace Elefant file paths with oCMS media URLs in body
		body := nullStringToString(wp.Body)
		if mediaMap != nil {
			body = replaceMediaURLs(body, mediaMap)
		}
		// Sanitize imported HTML to match the admin UI policy
		body = security.SanitizePageHTML(body)

		// Use window_title as meta_title, fall back to title
		metaTitle := wp.WindowTitle
		if metaTitle == "" {
			metaTitle = wp.Title
		}

		page, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title:           wp.Title,
			Slug:            slug,
			Body:            body,
			Status:          status,
			AuthorID:        authorID,
			LanguageCode:    defaultLangCode,
			PageType:        "page",
			MetaTitle:       metaTitle,
			MetaDescription: nullStringToString(wp.Description),
			MetaKeywords:    nullStringToString(wp.Keywords),
			CreatedAt:       now,
			UpdatedAt:       now,
		})
		if err != nil {
			result.AddError("Failed to create page '%s': %v", wp.Title, err)
			continue
		}

		if !s.track(ctx, tracker, result, types.EntityPage, page.ID, func(rollbackCtx context.Context) error {
			return queries.DeletePage(rollbackCtx, page.ID)
		}) {
			continue
		}

		// Preserve safe source paths verbatim. Imported aliases intentionally
		// allow established mixed-case, underscore, and Unicode paths that are
		// broader than the administrator slug grammar.
		if wp.ID != slug && shared.IsSafeImportedAliasPath(wp.ID) {
			aliasErr := s.createTrackedPageAlias(ctx, queries, page.ID, wp.ID, now, result, tracker)
			if aliasErr != nil {
				result.AddError("Failed to create legacy alias '%s' for page '%s': %v",
					wp.ID, wp.Title, aliasErr)
				slog.Warn("failed to create page alias",
					"page_id", page.ID,
					"alias", wp.ID,
					"error", aliasErr)
			}
		} else if wp.ID != slug {
			slog.Warn("skipping unsafe page alias from elefant import",
				"page_id", page.ID,
				"alias", wp.ID)
		}

		// Set published_at if published
		if status == "published" {
			if _, err := queries.PublishPage(ctx, store.PublishPageParams{
				PublishedAt: sql.NullTime{Time: now, Valid: true},
				UpdatedAt:   now,
				ID:          page.ID,
			}); err != nil {
				result.AddError("Failed to set published_at for page '%s': %v", wp.Title, err)
			}
		}

		result.PagesImported++
	}

	return nil
}

// replaceMediaURLs replaces Elefant file paths with oCMS media URLs in HTML content.
var replaceMediaURLs = shared.ReplaceURLs

// makeUniqueSlug generates a unique slug by appending -2, -3, etc. if needed.
var makeUniqueSlug = shared.MakeUniqueSlug

// parseElefantTags parses the JSON array of tags from Elefant.
func parseElefantTags(tagsJSON string) []string {
	if tagsJSON == "" {
		return nil
	}

	var tags []string
	if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
		// Try splitting by comma as fallback
		parts := strings.Split(tagsJSON, ",")
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				tags = append(tags, t)
			}
		}
	}
	return tags
}

// nullStringToString converts sql.NullString to string.
var nullStringToString = shared.NullString

// importUsers imports users from Elefant as public users.
// Note: Passwords cannot be migrated due to different hashing algorithms,
// so new random passwords are generated for imported users.
// Users will need to use "forgot password" to set their own passwords.
func (s *Source) importUsers(ctx context.Context, queries *store.Queries, reader *Reader, opts types.ImportOptions, result *types.ImportResult, tracker types.ImportTracker) error {
	users, err := reader.GetUsers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get users from Elefant: %w", err)
	}

	now := time.Now()

	// One hash for every user in this run — hashing per user is needlessly
	// expensive when nobody can use the credential anyway. It must be random,
	// though: this previously bcrypt-hashed the constant
	// "imported-user-must-reset" at MinCost, which let anyone sign in as any
	// imported account.
	passwordHash, err := shared.UnguessablePlaceholderHash()
	if err != nil {
		return err
	}

	for _, user := range users {
		// Check context for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if user already exists by email
		if opts.SkipExisting {
			_, lookupErr := queries.GetUserByEmail(ctx, user.Email)
			switch {
			case lookupErr == nil:
				// User exists, skip
				result.UsersSkipped++
				continue
			case errors.Is(lookupErr, sql.ErrNoRows):
				// Not present: create it below.
			default:
				result.AddError("Failed to check for existing user '%s': %v", user.Email, lookupErr)
				continue
			}
		}

		// Create user with "public" role (no admin access)
		createdUser, err := queries.CreateUser(ctx, store.CreateUserParams{
			Email:        user.Email,
			PasswordHash: passwordHash, // Placeholder - users must reset password
			Role:         "public",     // Public users only - no admin access
			Name:         user.Name,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			result.AddError("Failed to create user '%s': %v", user.Email, err)
			continue
		}

		if !s.track(ctx, tracker, result, types.EntityUser, createdUser.ID, func(rollbackCtx context.Context) error {
			return queries.DeleteUser(rollbackCtx, createdUser.ID)
		}) {
			continue
		}

		result.UsersImported++
	}

	return nil
}
