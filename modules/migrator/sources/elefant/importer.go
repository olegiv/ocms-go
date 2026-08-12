// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
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
	"golang.org/x/crypto/bcrypt"
)

// Source implements the migrator.Source interface for Elefant CMS.
type Source struct{}

// NewSource creates a new Elefant CMS source.
func NewSource() *Source {
	return &Source{}
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

// envOrDefault returns the environment variable value or the default if not set.
var envOrDefault = shared.EnvOrDefault

// Connection bounds for the source database. Mirrors the Drupal source: a
// migration is a long sequence of modest reads, so the pool stays small and
// every statement is bounded.
const (
	connectTimeout  = 10 * time.Second
	readTimeout     = 60 * time.Second
	maxOpenConns    = 4
	connMaxLifetime = 30 * time.Minute
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

// TestConnection tests the connection to the Elefant database.
func (s *Source) TestConnection(cfg map[string]string) error {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout+readTimeout)
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
	defaultLang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get default language: %w", err)
	}

	// Import tags first (posts reference them)
	var tagMap map[string]int64
	if opts.ImportTags {
		tagMap, err = s.importTags(ctx, queries, reader, defaultLang.Code, opts, result, tracker)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Tags import error: %v", err))
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
				result.Errors = append(result.Errors, fmt.Sprintf("Media import error: %v", err))
			}
		}
	}

	// Import posts
	if opts.ImportPosts {
		if err := s.importPosts(ctx, queries, reader, authorID, defaultLang.Code, tagMap, mediaMap, opts, result, tracker); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Posts import error: %v", err))
		}
	}

	// Import pages (static webpages)
	if opts.ImportPages {
		if err := s.importPages(ctx, queries, reader, authorID, defaultLang.Code, mediaMap, opts, result, tracker); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Pages import error: %v", err))
		}
	}

	// Import users (as public users only)
	if opts.ImportUsers {
		if err := s.importUsers(ctx, queries, reader, opts, result, tracker); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Users import error: %v", err))
		}
	}

	// Rebuild FTS index to ensure imported pages are searchable
	if opts.ImportPosts || opts.ImportPages {
		searchService := service.NewSearchService(db)
		if err := searchService.RebuildIndex(ctx); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("FTS index rebuild error: %v", err))
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

		// Create new tag
		tag, err := queries.CreateTag(ctx, store.CreateTagParams{
			Name:         name,
			Slug:         slug,
			LanguageCode: defaultLangCode,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to create tag '%s': %v", name, err))
			continue
		}

		// Track imported tag for later deletion
		if tracker != nil {
			_ = tracker.TrackImportedItem(ctx, s.Name(), "tag", tag.ID)
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

	// Scan Elefant files directory
	files, err := ScanMediaFiles(filesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to scan media files: %w", err)
	}

	if len(files) == 0 {
		return mediaMap, nil
	}

	// Get default language for media creation
	defaultLang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get default language: %w", err)
	}

	processor := imaging.NewProcessor(uploadDir)
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
		srcFile, err := os.Open(file.FullPath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to open %s: %v", file.Path, err))
			continue
		}

		fileUUID := uuid.New().String()

		// Process based on type
		if processor.IsImage(file.MimeType) {
			// Process image - creates original and variants. Migration files
			// come from a trusted local directory, so an oversized photo is
			// downscaled rather than dropped.
			processResult, err := processor.ProcessImageWithOptions(srcFile, fileUUID, file.Filename,
				imaging.ProcessOptions{DownscaleOversized: true})
			if closeErr := srcFile.Close(); closeErr != nil {
				slog.Error("failed to close source file", "path", file.Path, "error", closeErr)
			}
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to process %s: %v", file.Path, err))
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
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to create media record for %s: %v", file.Path, err))
				continue
			}

			// Create variants (best effort - don't fail if variants fail)
			variants, _ := processor.CreateAllVariants(processResult.FilePath, fileUUID, file.Filename)
			for _, v := range variants {
				_, _ = queries.CreateMediaVariant(ctx, store.CreateMediaVariantParams{
					MediaID:   media.ID,
					Type:      v.Type,
					Width:     int64(v.Width),
					Height:    int64(v.Height),
					Size:      v.Size,
					CreatedAt: now,
				})
			}

			// Track imported media for later deletion
			if tracker != nil {
				_ = tracker.TrackImportedItem(ctx, s.Name(), "media", media.ID)
			}

			// Map old path to new URL
			mediaMap["/files/"+file.Path] = model.MediaURL(model.VariantOriginal, fileUUID, file.Filename)

		} else {
			// Non-image file - save directly without processing
			err := s.saveNonImageFile(srcFile, uploadDir, fileUUID, file.Filename)
			if closeErr := srcFile.Close(); closeErr != nil {
				slog.Error("failed to close source file", "path", file.Path, "error", closeErr)
			}
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to save %s: %v", file.Path, err))
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
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to create media record for %s: %v", file.Path, err))
				continue
			}

			// Track imported media for later deletion
			if tracker != nil {
				_ = tracker.TrackImportedItem(ctx, s.Name(), "media", media.ID)
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
func (s *Source) saveNonImageFile(src *os.File, uploadDir, fileUUID, filename string) error {
	return shared.SaveNonImageFile(src, uploadDir, fileUUID, filename)
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
		}

		// Make slug unique if it already exists (handles duplicates)
		slug := makeUniqueSlug(ctx, queries, baseSlug)

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
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to create page '%s': %v", post.Title, err))
			continue
		}

		// Track imported post for later deletion
		if tracker != nil {
			_ = tracker.TrackImportedItem(ctx, s.Name(), "post", page.ID)
		}

		// Create page alias for old Elefant URL (blog/post/{id})
		alias := fmt.Sprintf("blog/post/%d", post.ID)
		_, aliasErr := queries.CreatePageAlias(ctx, store.CreatePageAliasParams{
			PageID:    page.ID,
			Alias:     alias,
			CreatedAt: now,
		})
		if aliasErr != nil {
			// Log warning but continue - alias is not critical
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
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to set published_at for '%s': %v", post.Title, err))
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
						result.Errors = append(result.Errors, fmt.Sprintf("Failed to add tag '%s' to page '%s': %v", tagSlug, post.Title, err))
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
		}

		// Make slug unique if it already exists
		slug := makeUniqueSlug(ctx, queries, baseSlug)

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
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to create page '%s': %v", wp.Title, err))
			continue
		}

		// Track imported page for later deletion
		if tracker != nil {
			_ = tracker.TrackImportedItem(ctx, s.Name(), "page", page.ID)
		}

		// Create page alias for old Elefant URL path (only if it is a safe alias format)
		if wp.ID != slug && util.IsValidAlias(wp.ID) {
			_, aliasErr := queries.CreatePageAlias(ctx, store.CreatePageAliasParams{
				PageID:    page.ID,
				Alias:     wp.ID,
				CreatedAt: now,
			})
			if aliasErr != nil {
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
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to set published_at for page '%s': %v", wp.Title, err))
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

	// Pre-generate a single password hash to use for all imported users.
	// This is much faster than hashing individually, and since users need
	// to reset their passwords anyway, using the same placeholder is fine.
	// We use MinCost since this is just a placeholder password.
	placeholderHash, err := bcrypt.GenerateFromPassword([]byte("imported-user-must-reset"), bcrypt.MinCost)
	if err != nil {
		return fmt.Errorf("failed to generate placeholder password hash: %w", err)
	}
	passwordHash := string(placeholderHash)

	for _, user := range users {
		// Check context for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if user already exists by email
		if opts.SkipExisting {
			_, err := queries.GetUserByEmail(ctx, user.Email)
			if err == nil {
				// User exists, skip
				result.UsersSkipped++
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
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to create user '%s': %v", user.Email, err))
			continue
		}

		// Track imported user for later deletion
		if tracker != nil {
			_ = tracker.TrackImportedItem(ctx, s.Name(), "user", createdUser.ID)
		}

		result.UsersImported++
	}

	return nil
}
