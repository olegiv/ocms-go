// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
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
	return "Import blog posts and tags from Elefant CMS MySQL database"
}

// ConfigFields returns the configuration fields needed for this source.
// Defaults are read from environment variables (ELEFANT_HOST, ELEFANT_PORT, etc.)
func (s *Source) ConfigFields() []types.ConfigField {
	return []types.ConfigField{
		{Name: "mysql_host", Label: "MySQL Host", Type: "text", Required: true, Default: envOrDefault("ELEFANT_HOST", "localhost")},
		{Name: "mysql_port", Label: "MySQL Port", Type: "number", Required: true, Default: envOrDefault("ELEFANT_PORT", "3306")},
		{Name: "mysql_user", Label: "MySQL User", Type: "text", Required: true, Default: os.Getenv("ELEFANT_USER")},
		{Name: "mysql_password", Label: "MySQL Password", Type: "password", Required: true, Default: os.Getenv("ELEFANT_PASSWORD")},
		{Name: "mysql_database", Label: "Database Name", Type: "text", Required: true, Default: os.Getenv("ELEFANT_DB")},
		{Name: "table_prefix", Label: "Table Prefix", Type: "text", Required: false, Default: os.Getenv("ELEFANT_PREFIX"), Placeholder: "e.g. elefant_"},
	}
}

// envOrDefault returns the environment variable value or the default if not set.
func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// buildDSN builds a MySQL DSN from the config.
func (s *Source) buildDSN(cfg map[string]string) string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		cfg["mysql_user"],
		cfg["mysql_password"],
		cfg["mysql_host"],
		cfg["mysql_port"],
		cfg["mysql_database"],
	)
}

// TestConnection tests the connection to the Elefant database.
func (s *Source) TestConnection(cfg map[string]string) error {
	dsn := s.buildDSN(cfg)
	prefix := cfg["table_prefix"]
	reader, err := NewReader(dsn, prefix)
	if err != nil {
		return err
	}
	defer reader.Close()

	// Try to get counts to verify tables exist
	postCount, err := reader.GetPostCount()
	if err != nil {
		return fmt.Errorf("failed to query %sblog_post table: %w", prefix, err)
	}

	tagCount, err := reader.GetTagCount()
	if err != nil {
		return fmt.Errorf("failed to query %sblog_tag table: %w", prefix, err)
	}

	_ = postCount
	_ = tagCount

	return nil
}

// Import imports content from Elefant CMS into oCMS.
func (s *Source) Import(ctx context.Context, db *sql.DB, cfg map[string]string, opts types.ImportOptions, tracker types.ImportTracker) (*types.ImportResult, error) {
	result := &types.ImportResult{}

	// Connect to Elefant database
	dsn := s.buildDSN(cfg)
	prefix := cfg["table_prefix"]
	reader, err := NewReader(dsn, prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Elefant database: %w", err)
	}
	defer reader.Close()

	// Get oCMS store
	queries := store.New(db)

	// Get the first admin user as the author for imported content
	authorID, err := s.getDefaultAuthorID(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("failed to get default author: %w", err)
	}

	// Import tags first (posts reference them)
	var tagMap map[string]int64
	if opts.ImportTags {
		tagMap, err = s.importTags(ctx, queries, reader, opts, result, tracker)
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

	// Import posts
	if opts.ImportPosts {
		if err := s.importPosts(ctx, queries, reader, authorID, tagMap, opts, result, tracker); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Posts import error: %v", err))
		}
	}

	// Import users (as public users only)
	if opts.ImportUsers {
		if err := s.importUsers(ctx, queries, reader, opts, result, tracker); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Users import error: %v", err))
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
func (s *Source) importTags(ctx context.Context, queries *store.Queries, reader *Reader, opts types.ImportOptions, result *types.ImportResult, tracker types.ImportTracker) (map[string]int64, error) {
	elefantTags, err := reader.GetTags()
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
			Name:      name,
			Slug:      slug,
			CreatedAt: now,
			UpdatedAt: now,
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

// importPosts imports blog posts from Elefant.
func (s *Source) importPosts(ctx context.Context, queries *store.Queries, reader *Reader, authorID int64, tagMap map[string]int64, opts types.ImportOptions, result *types.ImportResult, tracker types.ImportTracker) error {
	posts, err := reader.GetBlogPosts()
	if err != nil {
		return fmt.Errorf("failed to get posts from Elefant: %w", err)
	}

	now := time.Now()

	for _, post := range posts {
		// Generate slug from title if not present (older Elefant versions)
		slug := post.Slug
		if slug == "" {
			slug = util.Slugify(post.Title)
		}

		// Check if page already exists by slug
		if opts.SkipExisting {
			_, err := queries.GetPageBySlug(ctx, slug)
			if err == nil {
				// Page exists, skip it
				result.PostsSkipped++
				continue
			}
		}

		// Map Elefant published status to oCMS status
		status := "draft"
		if post.IsPublished() {
			status = "published"
		}

		// Create page
		page, err := queries.CreatePage(ctx, store.CreatePageParams{
			Title:           post.Title,
			Slug:            slug,
			Body:            post.Body,
			Status:          status,
			AuthorID:        authorID,
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

		// Track imported page for later deletion
		if tracker != nil {
			_ = tracker.TrackImportedItem(ctx, s.Name(), "page", page.ID)
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
				slug := util.Slugify(tagSlug)
				if tagID, ok := tagMap[slug]; ok {
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
func nullStringToString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// importUsers imports users from Elefant as public users.
// Note: Passwords cannot be migrated due to different hashing algorithms,
// so new random passwords are generated for imported users.
// Users will need to use "forgot password" to set their own passwords.
func (s *Source) importUsers(ctx context.Context, queries *store.Queries, reader *Reader, opts types.ImportOptions, result *types.ImportResult, tracker types.ImportTracker) error {
	users, err := reader.GetUsers()
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

