// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// Importer handles importing CMS content from JSON format.
type Importer struct {
	store     *store.Queries
	db        *sql.DB
	logger    *slog.Logger
	uploadDir string
	processor *imaging.Processor
}

// NewImporter creates a new Importer instance.
func NewImporter(queries *store.Queries, db *sql.DB, logger *slog.Logger) *Importer {
	return &Importer{
		store:     queries,
		db:        db,
		logger:    logger,
		uploadDir: "./uploads",
	}
}

// SetUploadDir sets the upload directory for media files.
func (i *Importer) SetUploadDir(dir string) {
	i.uploadDir = dir
	// Create processor with the new upload directory
	i.processor = imaging.NewProcessor(dir)
}

// SetProcessor sets the imaging processor for generating image variants.
func (i *Importer) SetProcessor(p *imaging.Processor) {
	i.processor = p
}

// getProcessor returns the imaging processor, creating one if needed.
func (i *Importer) getProcessor() *imaging.Processor {
	if i.processor == nil {
		i.processor = imaging.NewProcessor(i.uploadDir)
	}
	return i.processor
}

// Import performs the import operation based on the provided options.
// The import runs in a transaction and rolls back on error.
func (i *Importer) Import(ctx context.Context, data *ExportData, opts ImportOptions) (*ImportResult, error) {
	result := NewImportResult(opts.DryRun)

	// Validate the import data first
	validationErrors := i.Validate(data)
	if len(validationErrors) > 0 {
		for _, err := range validationErrors {
			result.AddError(err.Entity, err.ID, err.Message)
		}
		return result, errors.New("validation failed")
	}

	// If dry run, just validate and count entities
	if opts.DryRun {
		i.countEntities(ctx, data, opts, result)
		return result, nil
	}

	// Start transaction
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Create queries with transaction
	queries := i.store.WithTx(tx)

	// Import in order of dependencies:
	// 1. Languages (no dependencies)
	// 2. Users (no dependencies)
	// 3. Categories (depends on languages)
	// 4. Tags (depends on languages)
	// 5. Media (depends on users)
	// 6. Pages (depends on users, categories, tags, media, languages)
	// 7. Menus (depends on pages, languages)
	// 8. Forms (no dependencies)
	// 9. Config (no dependencies)
	// 10. Translations (depends on all entities)

	// Import languages first
	if opts.ImportLanguages && len(data.Languages) > 0 {
		i.importLanguages(ctx, queries, data.Languages, opts, result)
	}

	// Import users
	if opts.ImportUsers && len(data.Users) > 0 {
		i.importUsers(ctx, queries, data.Users, opts, result)
	}

	// Build user email to ID map for later use
	userMap, err := i.buildLookupMap(ctx, queries, entityUser)
	if err != nil {
		i.logger.Warn("failed to build user map", "error", err)
		userMap = make(map[string]int64)
	}

	// Import categories
	if opts.ImportCategories && len(data.Categories) > 0 {
		i.importCategories(ctx, queries, data.Categories, opts, result)
	}

	// Build category slug to ID map
	categoryMap, err := i.buildLookupMap(ctx, queries, entityCategory)
	if err != nil {
		i.logger.Warn("failed to build category map", "error", err)
		categoryMap = make(map[string]int64)
	}

	// Import tags
	if opts.ImportTags && len(data.Tags) > 0 {
		i.importTags(ctx, queries, data.Tags, opts, result)
	}

	// Build tag slug to ID map
	tagMap, err := i.buildLookupMap(ctx, queries, entityTag)
	if err != nil {
		i.logger.Warn("failed to build tag map", "error", err)
		tagMap = make(map[string]int64)
	}

	// Get default language code for entities that need it
	defaultLangCode := "en"
	if defaultLang, err := queries.GetDefaultLanguage(ctx); err == nil {
		defaultLangCode = defaultLang.Code
	}

	// Import media metadata
	if opts.ImportMedia && len(data.Media) > 0 {
		i.importMedia(ctx, queries, data.Media, userMap, defaultLangCode, opts, result)
	}

	// Build media UUID to ID map
	mediaMap, err := i.buildLookupMap(ctx, queries, entityMedia)
	if err != nil {
		i.logger.Warn("failed to build media map", "error", err)
		mediaMap = make(map[string]int64)
	}

	// Import pages
	if opts.ImportPages && len(data.Pages) > 0 {
		i.importPages(ctx, queries, data.Pages, userMap, categoryMap, tagMap, mediaMap, defaultLangCode, opts, result)
	}

	// Build page slug to ID map
	pageMap, err := i.buildLookupMap(ctx, queries, entityPage)
	if err != nil {
		i.logger.Warn("failed to build page map", "error", err)
		pageMap = make(map[string]int64)
	}

	// Import menus
	if opts.ImportMenus && len(data.Menus) > 0 {
		i.importMenus(ctx, queries, data.Menus, pageMap, opts, result)
	}

	// Import forms
	if opts.ImportForms && len(data.Forms) > 0 {
		i.importForms(ctx, queries, data.Forms, defaultLangCode, opts, result)
	}

	// Import config
	if opts.ImportConfig && len(data.Config) > 0 {
		i.importConfig(ctx, queries, data.Config, userMap, defaultLangCode, result)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return result, nil
}

// ImportFromReader reads and imports from an io.Reader.
func (i *Importer) ImportFromReader(ctx context.Context, r io.Reader, opts ImportOptions) (*ImportResult, error) {
	var data ExportData
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return i.Import(ctx, &data, opts)
}

// ImportFromFile reads and imports from a file path.
func (i *Importer) ImportFromFile(ctx context.Context, path string, opts ImportOptions) (*ImportResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = f.Close() }()

	return i.ImportFromReader(ctx, f, opts)
}

// ImportFromZip imports from a zip archive containing export.json and media files.
func (i *Importer) ImportFromZip(ctx context.Context, zipReader *zip.Reader, opts ImportOptions) (*ImportResult, error) {
	// Find and read export.json
	var exportData ExportData
	exportFound := false

	for _, f := range zipReader.File {
		if f.Name == "export.json" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("failed to open export.json: %w", err)
			}

			decoder := json.NewDecoder(rc)
			if err := decoder.Decode(&exportData); err != nil {
				_ = rc.Close()
				return nil, fmt.Errorf("failed to parse export.json: %w", err)
			}
			_ = rc.Close()
			exportFound = true
			break
		}
	}

	if !exportFound {
		return nil, errors.New("export.json not found in zip archive")
	}

	// If importing media files, extract them first (before the transaction)
	var mediaFileMap map[string]string // maps FilePath in JSON to extracted path
	if opts.ImportMediaFiles && !opts.DryRun {
		mediaFileMap = i.extractMediaFiles(zipReader)
	}

	// Perform the regular import
	result, err := i.Import(ctx, &exportData, opts)
	if err != nil {
		// Clean up extracted files on failure
		for _, path := range mediaFileMap {
			_ = os.RemoveAll(filepath.Dir(path))
		}
		return result, err
	}

	return result, nil
}

// ImportFromZipFile imports from a zip file path.
func (i *Importer) ImportFromZipFile(ctx context.Context, path string, opts ImportOptions) (*ImportResult, error) {
	zipReader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open zip file: %w", err)
	}
	defer func() { _ = zipReader.Close() }()

	return i.ImportFromZip(ctx, &zipReader.Reader, opts)
}

// ImportFromZipBytes imports from zip archive bytes (useful for HTTP uploads).
func (i *Importer) ImportFromZipBytes(ctx context.Context, data []byte, opts ImportOptions) (*ImportResult, error) {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("failed to read zip data: %w", err)
	}

	return i.ImportFromZip(ctx, zipReader, opts)
}

// extractMediaFiles extracts media files from the zip to the uploads directory
// and generates any missing image variants.
func (i *Importer) extractMediaFiles(zipReader *zip.Reader) map[string]string {
	mediaFileMap := make(map[string]string)
	// Track originals for variant generation: uuid -> filename
	originals := make(map[string]string)

	for _, f := range zipReader.File {
		// Check if this is a media file (in media/ directory)
		if !strings.HasPrefix(f.Name, "media/") {
			continue
		}

		// Skip directories
		if f.FileInfo().IsDir() {
			continue
		}

		// Extract the file
		extractedPath, err := i.extractMediaFile(f)
		if err != nil {
			i.logger.Warn("failed to extract media file", "file", f.Name, "error", err)
			continue
		}

		mediaFileMap[f.Name] = extractedPath

		// Track originals for variant generation
		parts := strings.Split(f.Name, "/")
		if len(parts) >= 4 && parts[1] == "originals" {
			originals[parts[2]] = parts[3] // uuid -> filename
		}
	}

	// Generate missing variants for extracted originals
	if len(originals) > 0 {
		i.generateMissingVariants(originals)
	}

	return mediaFileMap
}

// generateMissingVariants creates any missing image variants for the given originals.
func (i *Importer) generateMissingVariants(originals map[string]string) {
	processor := i.getProcessor()

	for uuid, filename := range originals {
		originalPath := filepath.Join(i.uploadDir, "originals", uuid, filename)

		// Check if original exists
		if _, err := os.Stat(originalPath); os.IsNotExist(err) {
			continue
		}

		// Check if this is an image that can have variants
		if !processor.IsImage(i.detectMimeType(filename)) {
			continue
		}

		// Generate all variants (processor skips existing ones and ones where source is smaller)
		variants, err := processor.CreateAllVariants(originalPath, uuid, filename)
		if err != nil {
			i.logger.Warn("failed to generate variants", "uuid", uuid, "filename", filename, "error", err)
			continue
		}

		if len(variants) > 0 {
			i.logger.Info("generated image variants", "uuid", uuid, "filename", filename, "count", len(variants))
		}
	}
}

// detectMimeType returns the MIME type based on file extension.
func (i *Importer) detectMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// extractMediaFile extracts a single media file from the zip.
func (i *Importer) extractMediaFile(f *zip.File) (string, error) {
	// Parse the path: media/{type}/{uuid}/{filename}
	parts := strings.Split(f.Name, "/")
	if len(parts) < 4 {
		return "", fmt.Errorf("invalid media path: %s", f.Name)
	}

	// Construct destination path
	// f.Name example: media/originals/{uuid}/{filename}
	// Destination: {uploadDir}/{type}/{uuid}/{filename}
	mediaType := parts[1] // "originals", "thumbnail", etc.
	uuid := parts[2]      // UUID
	filename := parts[3]  // filename

	destDir := filepath.Join(i.uploadDir, mediaType, uuid)
	destPath := filepath.Join(destDir, filename)

	// Create directory structure
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// Open zip file
	rc, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open zip entry: %w", err)
	}
	defer func() { _ = rc.Close() }()

	// Create destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() { _ = destFile.Close() }()

	// Copy content
	if _, err := io.Copy(destFile, rc); err != nil {
		return "", fmt.Errorf("failed to copy file content: %w", err)
	}

	return destPath, nil
}

// ValidateZipFile validates a zip import file and returns information about its contents.
func (i *Importer) ValidateZipFile(ctx context.Context, path string) (*ValidationResult, error) {
	zipReader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open zip file: %w", err)
	}
	defer func() { _ = zipReader.Close() }()

	return i.ValidateZip(ctx, &zipReader.Reader)
}

// ValidateZip validates a zip archive and returns information about its contents.
func (i *Importer) ValidateZip(ctx context.Context, zipReader *zip.Reader) (*ValidationResult, error) {
	// Find and read export.json
	for _, f := range zipReader.File {
		if f.Name == "export.json" {
			rc, err := f.Open()
			if err != nil {
				return &ValidationResult{
					Valid:  false,
					Errors: []ImportError{{Entity: "zip", ID: "", Message: "failed to open export.json: " + err.Error()}},
				}, nil
			}

			var data ExportData
			decoder := json.NewDecoder(rc)
			if err := decoder.Decode(&data); err != nil {
				_ = rc.Close()
				return &ValidationResult{
					Valid:  false,
					Errors: []ImportError{{Entity: "json", ID: "", Message: err.Error()}},
				}, nil
			}
			_ = rc.Close()

			result, err := i.ValidateData(ctx, &data)
			if err != nil {
				return nil, err
			}

			// Add media file count
			mediaCount := 0
			for _, mf := range zipReader.File {
				if strings.HasPrefix(mf.Name, "media/") && !mf.FileInfo().IsDir() {
					mediaCount++
				}
			}
			result.Entities["media_files"] = mediaCount

			return result, nil
		}
	}

	return &ValidationResult{
		Valid:  false,
		Errors: []ImportError{{Entity: "zip", ID: "", Message: "export.json not found in zip archive"}},
	}, nil
}

// ValidateZipBytes validates zip data and returns information about its contents.
func (i *Importer) ValidateZipBytes(ctx context.Context, data []byte) (*ValidationResult, error) {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return &ValidationResult{
			Valid:  false,
			Errors: []ImportError{{Entity: "zip", ID: "", Message: "failed to read zip data: " + err.Error()}},
		}, nil
	}

	return i.ValidateZip(ctx, zipReader)
}

// validateEntities validates a slice of entities and appends errors for missing required fields.
func validateEntities[T any](
	errs []ImportError,
	items []T,
	entity string,
	primaryField string,
	getPrimary func(T) string,
	secondaryField string,
	getSecondary func(T) string,
) []ImportError {
	for idx, item := range items {
		primary := getPrimary(item)
		if primary == "" {
			errs = append(errs, ImportError{
				Entity:  entity,
				ID:      strconv.Itoa(idx),
				Message: "missing " + entity + " " + primaryField,
			})
		}
		if getSecondary(item) == "" {
			errs = append(errs, ImportError{
				Entity:  entity,
				ID:      primary,
				Message: "missing " + entity + " " + secondaryField,
			})
		}
	}
	return errs
}

// Validate validates the import data without making changes.
func (i *Importer) Validate(data *ExportData) []ImportError {
	var errs []ImportError

	// Check version
	if data.Version == "" {
		errs = append(errs, ImportError{
			Entity:  "export",
			ID:      "",
			Message: "missing version field",
		})
	}

	errs = validateEntities(errs, data.Languages, "language", "code",
		func(l ExportLanguage) string { return l.Code }, "name",
		func(l ExportLanguage) string { return l.Name })

	errs = validateEntities(errs, data.Users, "user", "email",
		func(u ExportUser) string { return u.Email }, "role",
		func(u ExportUser) string { return u.Role })

	errs = validateEntities(errs, data.Categories, "category", "slug",
		func(c ExportCategory) string { return c.Slug }, "name",
		func(c ExportCategory) string { return c.Name })

	errs = validateEntities(errs, data.Tags, "tag", "slug",
		func(t ExportTag) string { return t.Slug }, "name",
		func(t ExportTag) string { return t.Name })

	errs = validateEntities(errs, data.Pages, "page", "slug",
		func(p ExportPage) string { return p.Slug }, "title",
		func(p ExportPage) string { return p.Title })

	errs = validateEntities(errs, data.Media, "media", "UUID",
		func(m ExportMedia) string { return m.UUID }, "filename",
		func(m ExportMedia) string { return m.Filename })

	errs = validateEntities(errs, data.Menus, "menu", "slug",
		func(m ExportMenu) string { return m.Slug }, "name",
		func(m ExportMenu) string { return m.Name })

	errs = validateEntities(errs, data.Forms, "form", "slug",
		func(f ExportForm) string { return f.Slug }, "name",
		func(f ExportForm) string { return f.Name })

	return errs
}

// ValidateFile validates an import file and returns information about its contents.
func (i *Importer) ValidateFile(ctx context.Context, path string) (*ValidationResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = f.Close() }()

	return i.ValidateReader(ctx, f)
}

// ValidateReader validates import data from a reader.
func (i *Importer) ValidateReader(ctx context.Context, r io.Reader) (*ValidationResult, error) {
	var data ExportData
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&data); err != nil {
		return &ValidationResult{
			Valid:  false,
			Errors: []ImportError{{Entity: "json", ID: "", Message: err.Error()}},
		}, nil
	}

	return i.ValidateData(ctx, &data)
}

// ValidateData validates import data and checks for conflicts.
func (i *Importer) ValidateData(ctx context.Context, data *ExportData) (*ValidationResult, error) {
	result := &ValidationResult{
		Valid:     true,
		Version:   data.Version,
		Entities:  make(map[string]int),
		Conflicts: make(map[string][]string),
		Errors:    []ImportError{},
	}

	// Count entities
	result.Entities["languages"] = len(data.Languages)
	result.Entities["users"] = len(data.Users)
	result.Entities["categories"] = len(data.Categories)
	result.Entities["tags"] = len(data.Tags)
	result.Entities["pages"] = len(data.Pages)
	result.Entities["media"] = len(data.Media)
	result.Entities["menus"] = len(data.Menus)
	result.Entities["forms"] = len(data.Forms)
	result.Entities["config"] = len(data.Config)

	// Run validation
	validationErrors := i.Validate(data)
	if len(validationErrors) > 0 {
		result.Valid = false
		result.Errors = validationErrors
	}

	// Check for conflicts (entities that already exist)
	// Languages
	for _, lang := range data.Languages {
		exists, _ := i.store.LanguageCodeExists(ctx, lang.Code)
		if exists != 0 {
			result.Conflicts["languages"] = append(result.Conflicts["languages"], lang.Code)
		}
	}

	// Users
	for _, user := range data.Users {
		_, err := i.store.GetUserByEmail(ctx, user.Email)
		if err == nil {
			result.Conflicts["users"] = append(result.Conflicts["users"], user.Email)
		}
	}

	// Categories
	for _, cat := range data.Categories {
		_, err := i.store.GetCategoryBySlug(ctx, cat.Slug)
		if err == nil {
			result.Conflicts["categories"] = append(result.Conflicts["categories"], cat.Slug)
		}
	}

	// Tags
	for _, tag := range data.Tags {
		_, err := i.store.GetTagBySlug(ctx, tag.Slug)
		if err == nil {
			result.Conflicts["tags"] = append(result.Conflicts["tags"], tag.Slug)
		}
	}

	// Pages
	for _, page := range data.Pages {
		_, err := i.store.GetPageBySlug(ctx, page.Slug)
		if err == nil {
			result.Conflicts["pages"] = append(result.Conflicts["pages"], page.Slug)
		}
	}

	// Media
	for _, media := range data.Media {
		_, err := i.store.GetMediaByUUID(ctx, media.UUID)
		if err == nil {
			result.Conflicts["media"] = append(result.Conflicts["media"], media.UUID)
		}
	}

	// Menus
	for _, menu := range data.Menus {
		_, err := i.store.GetMenuBySlug(ctx, menu.Slug)
		if err == nil {
			result.Conflicts["menus"] = append(result.Conflicts["menus"], menu.Slug)
		}
	}

	// Forms
	for _, form := range data.Forms {
		_, err := i.store.GetFormBySlug(ctx, form.Slug)
		if err == nil {
			result.Conflicts["forms"] = append(result.Conflicts["forms"], form.Slug)
		}
	}

	return result, nil
}

// countEntity increments the appropriate counter based on existence and conflict strategy.
func countEntity(result *ImportResult, strategy ConflictStrategy, exists bool, category string) {
	if exists {
		switch strategy {
		case ConflictSkip:
			result.IncrementSkipped(category)
		case ConflictOverwrite:
			result.IncrementUpdated(category)
		}
	} else {
		result.IncrementCreated(category)
	}
}

// countEntities counts entities that would be imported (for dry run).
// It checks existing entities to properly categorize as created, updated, or skipped.
func (i *Importer) countEntities(ctx context.Context, data *ExportData, opts ImportOptions, result *ImportResult) {
	if opts.ImportLanguages {
		for _, lang := range data.Languages {
			exists, _ := i.store.LanguageCodeExists(ctx, lang.Code)
			countEntity(result, opts.ConflictStrategy, exists != 0, "languages")
		}
	}
	if opts.ImportUsers {
		for _, user := range data.Users {
			_, err := i.store.GetUserByEmail(ctx, user.Email)
			countEntity(result, opts.ConflictStrategy, err == nil, "users")
		}
	}
	if opts.ImportCategories {
		for _, cat := range data.Categories {
			_, err := i.store.GetCategoryBySlug(ctx, cat.Slug)
			countEntity(result, opts.ConflictStrategy, err == nil, "categories")
		}
	}
	if opts.ImportTags {
		for _, tag := range data.Tags {
			_, err := i.store.GetTagBySlug(ctx, tag.Slug)
			countEntity(result, opts.ConflictStrategy, err == nil, "tags")
		}
	}
	if opts.ImportPages {
		for _, page := range data.Pages {
			_, err := i.store.GetPageBySlug(ctx, page.Slug)
			countEntity(result, opts.ConflictStrategy, err == nil, "pages")
		}
	}
	if opts.ImportMedia {
		for _, media := range data.Media {
			_, err := i.store.GetMediaByUUID(ctx, media.UUID)
			countEntity(result, opts.ConflictStrategy, err == nil, "media")
		}
	}
	if opts.ImportMenus {
		for _, menu := range data.Menus {
			_, err := i.store.GetMenuBySlug(ctx, menu.Slug)
			countEntity(result, opts.ConflictStrategy, err == nil, "menus")
		}
	}
	if opts.ImportForms {
		for _, form := range data.Forms {
			_, err := i.store.GetFormBySlug(ctx, form.Slug)
			countEntity(result, opts.ConflictStrategy, err == nil, "forms")
		}
	}
	if opts.ImportConfig {
		for key := range data.Config {
			_, err := i.store.GetConfigByKey(ctx, key)
			countEntity(result, opts.ConflictStrategy, err == nil, "config")
		}
	}
}

// Import methods for each entity type

func (i *Importer) importLanguages(ctx context.Context, queries *store.Queries, languages []ExportLanguage, opts ImportOptions, result *ImportResult) {
	now := time.Now()

	for _, lang := range languages {
		// Check if language exists
		exists, _ := queries.LanguageCodeExists(ctx, lang.Code)

		if exists != 0 {
			switch opts.ConflictStrategy {
			case ConflictSkip:
				result.IncrementSkipped("languages")
				continue
			case ConflictOverwrite:
				// Get existing language and update
				existing, err := queries.GetLanguageByCode(ctx, lang.Code)
				if err != nil {
					result.AddError("language", lang.Code, err.Error())
					continue
				}
				_, err = queries.UpdateLanguage(ctx, store.UpdateLanguageParams{
					ID:         existing.ID,
					Code:       lang.Code,
					Name:       lang.Name,
					NativeName: lang.NativeName,
					IsDefault:  lang.IsDefault,
					IsActive:   lang.IsActive,
					Direction:  lang.Direction,
					Position:   lang.Position,
					UpdatedAt:  now,
				})
				if err != nil {
					result.AddError("language", lang.Code, err.Error())
					continue
				}
				result.IncrementUpdated("languages")
				continue
			case ConflictRename:
				// Languages can't be renamed (code is unique identifier)
				result.IncrementSkipped("languages")
				continue
			}
		}

		// Create new language
		created, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
			Code:       lang.Code,
			Name:       lang.Name,
			NativeName: lang.NativeName,
			IsDefault:  lang.IsDefault,
			IsActive:   lang.IsActive,
			Direction:  lang.Direction,
			Position:   lang.Position,
			CreatedAt:  now,
			UpdatedAt:  now,
		})
		if err != nil {
			result.AddError("language", lang.Code, err.Error())
			continue
		}

		result.GetIDMap("languages")[int64(len(result.GetIDMap("languages"))+1)] = created.ID
		result.IncrementCreated("languages")
	}
}

func (i *Importer) importUsers(ctx context.Context, queries *store.Queries, users []ExportUser, opts ImportOptions, result *ImportResult) {
	now := time.Now()

	for _, user := range users {
		// Check if user exists
		existing, err := queries.GetUserByEmail(ctx, user.Email)

		if err == nil {
			// User exists
			switch opts.ConflictStrategy {
			case ConflictSkip:
				result.IncrementSkipped("users")
				continue
			case ConflictOverwrite:
				_, err = queries.UpdateUser(ctx, store.UpdateUserParams{
					ID:        existing.ID,
					Email:     user.Email,
					Role:      user.Role,
					Name:      user.Name,
					UpdatedAt: now,
				})
				if err != nil {
					result.AddError("user", user.Email, err.Error())
					continue
				}
				result.IncrementUpdated("users")
				continue
			case ConflictRename:
				// Users can't be renamed (email is unique identifier)
				result.IncrementSkipped("users")
				continue
			}
		}

		// Create new user with random password (they'll need to reset it)
		randomPassword := generateRandomPassword()
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(randomPassword), bcrypt.DefaultCost)
		if err != nil {
			result.AddError("user", user.Email, "failed to generate password hash")
			continue
		}

		created, err := queries.CreateUser(ctx, store.CreateUserParams{
			Email:        user.Email,
			PasswordHash: string(passwordHash),
			Role:         user.Role,
			Name:         user.Name,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			result.AddError("user", user.Email, err.Error())
			continue
		}

		result.GetIDMap("users")[int64(len(result.GetIDMap("users"))+1)] = created.ID
		result.IncrementCreated("users")
	}
}

func (i *Importer) importCategories(ctx context.Context, queries *store.Queries, categories []ExportCategory, opts ImportOptions, result *ImportResult) {
	now := time.Now()

	// First pass: create all categories without parent relationships
	categoryOldToNew := make(map[int64]int64) // maps export ID to new ID
	slugToID := make(map[string]int64)

	for _, cat := range categories {
		// Check if category exists
		existing, err := queries.GetCategoryBySlug(ctx, cat.Slug)

		if err == nil {
			// Category exists
			switch opts.ConflictStrategy {
			case ConflictSkip:
				slugToID[cat.Slug] = existing.ID
				categoryOldToNew[cat.ID] = existing.ID
				result.IncrementSkipped("categories")
				continue
			case ConflictOverwrite:
				_, err = queries.UpdateCategory(ctx, store.UpdateCategoryParams{
					ID:          existing.ID,
					Name:        cat.Name,
					Slug:        cat.Slug,
					Description: toNullString(cat.Description),
					ParentID:    sql.NullInt64{}, // Will update in second pass
					Position:    cat.Position,
					UpdatedAt:   now,
				})
				if err != nil {
					result.AddError("category", cat.Slug, err.Error())
					continue
				}
				slugToID[cat.Slug] = existing.ID
				categoryOldToNew[cat.ID] = existing.ID
				result.IncrementUpdated("categories")
				continue
			case ConflictRename:
				cat.Slug = i.generateUniqueSlug(ctx, queries, cat.Slug, "category")
			}
		}

		// Create new category
		created, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
			Name:        cat.Name,
			Slug:        cat.Slug,
			Description: toNullString(cat.Description),
			ParentID:    sql.NullInt64{}, // Will update in second pass
			Position:    cat.Position,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
		if err != nil {
			result.AddError("category", cat.Slug, err.Error())
			continue
		}

		slugToID[cat.Slug] = created.ID
		categoryOldToNew[cat.ID] = created.ID
		result.IncrementCreated("categories")
	}

	// Second pass: update parent relationships
	for _, cat := range categories {
		if cat.ParentSlug == "" {
			continue
		}

		newID, ok := slugToID[cat.Slug]
		if !ok {
			continue
		}

		parentID, ok := slugToID[cat.ParentSlug]
		if !ok {
			// Try to find parent by slug in database
			parent, err := queries.GetCategoryBySlug(ctx, cat.ParentSlug)
			if err != nil {
				i.logger.Warn("parent category not found", "slug", cat.Slug, "parent_slug", cat.ParentSlug)
				continue
			}
			parentID = parent.ID
		}

		_, err := queries.UpdateCategory(ctx, store.UpdateCategoryParams{
			ID:          newID,
			Name:        cat.Name,
			Slug:        cat.Slug,
			Description: toNullString(cat.Description),
			ParentID:    sql.NullInt64{Int64: parentID, Valid: true},
			Position:    cat.Position,
			UpdatedAt:   now,
		})
		if err != nil {
			i.logger.Warn("failed to update category parent", "slug", cat.Slug, "error", err)
		}
	}

	// Store mapping for use in page import
	for oldID, newID := range categoryOldToNew {
		result.GetIDMap("categories")[oldID] = newID
	}
}

func (i *Importer) importTags(ctx context.Context, queries *store.Queries, tags []ExportTag, opts ImportOptions, result *ImportResult) {
	now := time.Now()

	for _, tag := range tags {
		// Check if tag exists
		existing, err := queries.GetTagBySlug(ctx, tag.Slug)

		if err == nil {
			// Tag exists
			switch opts.ConflictStrategy {
			case ConflictSkip:
				result.GetIDMap("tags")[tag.ID] = existing.ID
				result.IncrementSkipped("tags")
				continue
			case ConflictOverwrite:
				_, err = queries.UpdateTag(ctx, store.UpdateTagParams{
					ID:        existing.ID,
					Name:      tag.Name,
					Slug:      tag.Slug,
					UpdatedAt: now,
				})
				if err != nil {
					result.AddError("tag", tag.Slug, err.Error())
					continue
				}
				result.GetIDMap("tags")[tag.ID] = existing.ID
				result.IncrementUpdated("tags")
				continue
			case ConflictRename:
				tag.Slug = i.generateUniqueSlug(ctx, queries, tag.Slug, "tag")
			}
		}

		// Create new tag
		created, err := queries.CreateTag(ctx, store.CreateTagParams{
			Name:      tag.Name,
			Slug:      tag.Slug,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			result.AddError("tag", tag.Slug, err.Error())
			continue
		}

		result.GetIDMap("tags")[tag.ID] = created.ID
		result.IncrementCreated("tags")
	}
}

func (i *Importer) importMedia(ctx context.Context, queries *store.Queries, media []ExportMedia, userMap map[string]int64, defaultLangCode string, opts ImportOptions, result *ImportResult) {
	now := time.Now()

	// Build folder path to ID map
	folderMap, err := i.buildOrCreateFolders(ctx, queries, media)
	if err != nil {
		i.logger.Warn("failed to build folder map", "error", err)
		folderMap = make(map[string]int64)
	}

	for _, m := range media {
		// Use language code from import data or default
		langCode := defaultLangCode
		if m.LanguageCode != "" {
			langCode = m.LanguageCode
		}

		// Check if media exists
		existing, err := queries.GetMediaByUUID(ctx, m.UUID)

		if err == nil {
			// Media exists
			switch opts.ConflictStrategy {
			case ConflictSkip:
				result.GetIDMap("media")[int64(len(result.GetIDMap("media"))+1)] = existing.ID
				result.IncrementSkipped("media")
				continue
			case ConflictOverwrite:
				folderID := sql.NullInt64{}
				if m.FolderPath != "" {
					if fID, ok := folderMap[m.FolderPath]; ok {
						folderID = sql.NullInt64{Int64: fID, Valid: true}
					}
				}
				_, err = queries.UpdateMedia(ctx, store.UpdateMediaParams{
					ID:           existing.ID,
					Filename:     m.Filename,
					Alt:          toNullString(m.Alt),
					Caption:      toNullString(m.Caption),
					FolderID:     folderID,
					LanguageCode: existing.LanguageCode,
					UpdatedAt:    now,
				})
				if err != nil {
					result.AddError("media", m.UUID, err.Error())
					continue
				}
				result.GetIDMap("media")[int64(len(result.GetIDMap("media"))+1)] = existing.ID
				result.IncrementUpdated("media")
				continue
			case ConflictRename:
				// Media can't be renamed (UUID is unique identifier)
				result.IncrementSkipped("media")
				continue
			}
		}

		// Get uploader ID
		uploaderID := int64(1) // Default to first user
		if m.UploadedBy != "" {
			if id, ok := userMap[m.UploadedBy]; ok {
				uploaderID = id
			}
		}

		// Get folder ID
		folderID := sql.NullInt64{}
		if m.FolderPath != "" {
			if fID, ok := folderMap[m.FolderPath]; ok {
				folderID = sql.NullInt64{Int64: fID, Valid: true}
			}
		}

		// Note: This only creates metadata. Actual file import would require
		// copying files which is handled in Iteration 19.
		created, err := queries.CreateMedia(ctx, store.CreateMediaParams{
			Uuid:         m.UUID,
			Filename:     m.Filename,
			MimeType:     m.MimeType,
			Size:         m.Size,
			Width:        util.NullInt64FromPtr(m.Width),
			Height:       util.NullInt64FromPtr(m.Height),
			Alt:          toNullString(m.Alt),
			Caption:      toNullString(m.Caption),
			FolderID:     folderID,
			UploadedBy:   uploaderID,
			LanguageCode: langCode,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			result.AddError("media", m.UUID, err.Error())
			continue
		}

		result.GetIDMap("media")[int64(len(result.GetIDMap("media"))+1)] = created.ID
		result.IncrementCreated("media")
	}
}

func (i *Importer) importPages(
	ctx context.Context,
	queries *store.Queries,
	pages []ExportPage,
	userMap map[string]int64,
	categoryMap map[string]int64,
	tagMap map[string]int64,
	mediaMap map[string]int64,
	defaultLangCode string,
	opts ImportOptions,
	result *ImportResult,
) {
	now := time.Now()

	pageOldToNew := make(map[int64]int64) // maps export ID to new ID

	for _, page := range pages {
		// Check if page exists
		existing, existsErr := queries.GetPageBySlug(ctx, page.Slug)
		pageExists := existsErr == nil

		var pageID int64
		shouldCreate := false

		if pageExists {
			// Page exists - handle based on conflict strategy
			switch opts.ConflictStrategy {
			case ConflictSkip:
				pageOldToNew[page.ID] = existing.ID
				result.IncrementSkipped("pages")
				continue
			case ConflictOverwrite:
				pageID, existsErr = i.updateExistingPage(ctx, queries, page, existing.ID, mediaMap, defaultLangCode, now)
				if existsErr != nil {
					result.AddError("page", page.Slug, existsErr.Error())
					continue
				}
				pageOldToNew[page.ID] = pageID

				// Update categories and tags
				_ = queries.ClearPageCategories(ctx, pageID)
				_ = queries.ClearPageTags(ctx, pageID)

				result.IncrementUpdated("pages")
			case ConflictRename:
				page.Slug = i.generateUniqueSlug(ctx, queries, page.Slug, "page")
				shouldCreate = true
			}
		} else {
			shouldCreate = true
		}

		if shouldCreate {
			var createErr error
			pageID, createErr = i.createNewPage(ctx, queries, page, userMap, mediaMap, defaultLangCode, now)
			if createErr != nil {
				result.AddError("page", page.Slug, createErr.Error())
				continue
			}
			pageOldToNew[page.ID] = pageID
			result.IncrementCreated("pages")
		}

		// Add categories
		for _, catSlug := range page.Categories {
			if catID, ok := categoryMap[catSlug]; ok {
				_ = queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
					PageID:     pageID,
					CategoryID: catID,
				})
			}
		}

		// Add tags
		for _, tagSlug := range page.Tags {
			if tagID, ok := tagMap[tagSlug]; ok {
				_ = queries.AddTagToPage(ctx, store.AddTagToPageParams{
					PageID: pageID,
					TagID:  tagID,
				})
			}
		}
	}

	// Store mapping for use later
	for oldID, newID := range pageOldToNew {
		result.GetIDMap("pages")[oldID] = newID
	}
}

// pageImportFields holds common fields extracted from an ExportPage.
type pageImportFields struct {
	FeaturedImageID sql.NullInt64
	OgImageID       sql.NullInt64
	LanguageCode    string
	MetaTitle       string
	MetaDescription string
	MetaKeywords    string
	CanonicalURL    string
	NoIndex         int64
	NoFollow        int64
	ScheduledAt     sql.NullTime
}

// extractPageFields extracts common fields from an ExportPage using the provided maps.
func extractPageFields(page ExportPage, mediaMap map[string]int64, defaultLangCode string) pageImportFields {
	f := pageImportFields{}

	// Get featured image ID
	if page.FeaturedImage != nil && page.FeaturedImage.UUID != "" {
		if id, ok := mediaMap[page.FeaturedImage.UUID]; ok {
			f.FeaturedImageID = sql.NullInt64{Int64: id, Valid: true}
		}
	}

	// Get OG image ID
	if page.SEO != nil && page.SEO.OgImage != nil && page.SEO.OgImage.UUID != "" {
		if id, ok := mediaMap[page.SEO.OgImage.UUID]; ok {
			f.OgImageID = sql.NullInt64{Int64: id, Valid: true}
		}
	}

	// Get language code from import data or use default
	if page.LanguageCode != "" {
		f.LanguageCode = page.LanguageCode
	} else {
		f.LanguageCode = defaultLangCode
	}

	// Build SEO fields
	if page.SEO != nil {
		f.MetaTitle = page.SEO.MetaTitle
		f.MetaDescription = page.SEO.MetaDescription
		f.MetaKeywords = page.SEO.MetaKeywords
		f.CanonicalURL = page.SEO.CanonicalURL
		if page.SEO.NoIndex {
			f.NoIndex = 1
		}
		if page.SEO.NoFollow {
			f.NoFollow = 1
		}
	}

	// Scheduled at handling
	if page.ScheduledAt != nil {
		f.ScheduledAt = sql.NullTime{Time: *page.ScheduledAt, Valid: true}
	}

	return f
}

// updateExistingPage updates an existing page with imported data.
func (i *Importer) updateExistingPage(
	ctx context.Context,
	queries *store.Queries,
	page ExportPage,
	existingID int64,
	mediaMap map[string]int64,
	defaultLangCode string,
	now time.Time,
) (int64, error) {
	f := extractPageFields(page, mediaMap, defaultLangCode)

	updated, err := queries.UpdatePage(ctx, store.UpdatePageParams{
		ID:              existingID,
		Title:           page.Title,
		Slug:            page.Slug,
		Body:            page.Body,
		Status:          page.Status,
		FeaturedImageID: f.FeaturedImageID,
		MetaTitle:       f.MetaTitle,
		MetaDescription: f.MetaDescription,
		MetaKeywords:    f.MetaKeywords,
		OgImageID:       f.OgImageID,
		NoIndex:         f.NoIndex,
		NoFollow:        f.NoFollow,
		CanonicalUrl:    f.CanonicalURL,
		ScheduledAt:     f.ScheduledAt,
		LanguageCode:    f.LanguageCode,
		UpdatedAt:       now,
	})
	if err != nil {
		return 0, err
	}

	return updated.ID, nil
}

// createNewPage creates a new page from imported data.
func (i *Importer) createNewPage(
	ctx context.Context,
	queries *store.Queries,
	page ExportPage,
	userMap map[string]int64,
	mediaMap map[string]int64,
	defaultLangCode string,
	now time.Time,
) (int64, error) {
	// Get author ID
	authorID := int64(1)
	if page.AuthorEmail != "" {
		if id, ok := userMap[page.AuthorEmail]; ok {
			authorID = id
		}
	}

	f := extractPageFields(page, mediaMap, defaultLangCode)

	created, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title:           page.Title,
		Slug:            page.Slug,
		Body:            page.Body,
		Status:          page.Status,
		AuthorID:        authorID,
		FeaturedImageID: f.FeaturedImageID,
		MetaTitle:       f.MetaTitle,
		MetaDescription: f.MetaDescription,
		MetaKeywords:    f.MetaKeywords,
		OgImageID:       f.OgImageID,
		NoIndex:         f.NoIndex,
		NoFollow:        f.NoFollow,
		CanonicalUrl:    f.CanonicalURL,
		ScheduledAt:     f.ScheduledAt,
		LanguageCode:    f.LanguageCode,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		return 0, err
	}

	return created.ID, nil
}

func (i *Importer) importMenus(ctx context.Context, queries *store.Queries, menus []ExportMenu, pageMap map[string]int64, opts ImportOptions, result *ImportResult) {
	now := time.Now()

	for _, menu := range menus {
		// Check if menu exists
		existing, existsErr := queries.GetMenuBySlug(ctx, menu.Slug)
		menuExists := existsErr == nil

		var menuID int64
		shouldCreate := false

		if menuExists {
			// Menu exists - handle based on conflict strategy
			switch opts.ConflictStrategy {
			case ConflictSkip:
				result.IncrementSkipped("menus")
				continue
			case ConflictOverwrite:
				updated, updateErr := queries.UpdateMenu(ctx, store.UpdateMenuParams{
					ID:        existing.ID,
					Name:      menu.Name,
					Slug:      menu.Slug,
					UpdatedAt: now,
				})
				if updateErr != nil {
					result.AddError("menu", menu.Slug, updateErr.Error())
					continue
				}
				menuID = updated.ID

				// Delete existing menu items
				_ = queries.DeleteMenuItems(ctx, menuID)

				result.IncrementUpdated("menus")
			case ConflictRename:
				menu.Slug = i.generateUniqueSlug(ctx, queries, menu.Slug, "menu")
				shouldCreate = true
			}
		} else {
			shouldCreate = true
		}

		if shouldCreate {
			created, createErr := queries.CreateMenu(ctx, store.CreateMenuParams{
				Name:      menu.Name,
				Slug:      menu.Slug,
				CreatedAt: now,
				UpdatedAt: now,
			})
			if createErr != nil {
				result.AddError("menu", menu.Slug, createErr.Error())
				continue
			}
			menuID = created.ID
			result.IncrementCreated("menus")
		}

		// Import menu items
		if err := i.importMenuItems(ctx, queries, menuID, menu.Items, pageMap, sql.NullInt64{}, now); err != nil {
			i.logger.Warn("failed to import menu items", "menu", menu.Slug, "error", err)
		}
	}
}

func (i *Importer) importMenuItems(ctx context.Context, queries *store.Queries, menuID int64, items []ExportMenuItem, pageMap map[string]int64, parentID sql.NullInt64, now time.Time) error {
	for _, item := range items {
		// Get page ID if linked
		pageID := sql.NullInt64{}
		if item.PageSlug != "" {
			if id, ok := pageMap[item.PageSlug]; ok {
				pageID = sql.NullInt64{Int64: id, Valid: true}
			}
		}

		created, err := queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
			MenuID:    menuID,
			ParentID:  parentID,
			Title:     item.Title,
			Url:       toNullString(item.URL),
			Target:    toNullString(item.Target),
			PageID:    pageID,
			Position:  item.Position,
			CssClass:  toNullString(item.CSSClass),
			IsActive:  item.IsActive,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			return err
		}

		// Import children recursively
		if len(item.Children) > 0 {
			newParentID := sql.NullInt64{Int64: created.ID, Valid: true}
			if err := i.importMenuItems(ctx, queries, menuID, item.Children, pageMap, newParentID, now); err != nil {
				return err
			}
		}
	}

	return nil
}

func (i *Importer) importForms(ctx context.Context, queries *store.Queries, forms []ExportForm, defaultLangCode string, opts ImportOptions, result *ImportResult) {
	now := time.Now()

	for _, form := range forms {
		// Use language code from import data or default
		langCode := defaultLangCode
		if form.LanguageCode != "" {
			langCode = form.LanguageCode
		}

		// Check if form exists
		existing, existsErr := queries.GetFormBySlug(ctx, form.Slug)
		formExists := existsErr == nil

		var formID int64
		formLangCode := langCode
		shouldCreate := false

		if formExists {
			formLangCode = existing.LanguageCode
			// Form exists - handle based on conflict strategy
			switch opts.ConflictStrategy {
			case ConflictSkip:
				result.IncrementSkipped("forms")
				continue
			case ConflictOverwrite:
				updated, updateErr := queries.UpdateForm(ctx, store.UpdateFormParams{
					ID:             existing.ID,
					Name:           form.Name,
					Slug:           form.Slug,
					Title:          form.Title,
					Description:    toNullString(form.Description),
					SuccessMessage: toNullString(form.SuccessMessage),
					EmailTo:        toNullString(form.EmailTo),
					IsActive:       form.IsActive,
					LanguageCode:   existing.LanguageCode,
					UpdatedAt:      now,
				})
				if updateErr != nil {
					result.AddError("form", form.Slug, updateErr.Error())
					continue
				}
				formID = updated.ID

				// Delete existing form fields
				_ = queries.DeleteFormFields(ctx, formID)

				result.IncrementUpdated("forms")
			case ConflictRename:
				form.Slug = i.generateUniqueSlug(ctx, queries, form.Slug, "form")
				shouldCreate = true
			}
		} else {
			shouldCreate = true
		}

		if shouldCreate {
			created, createErr := queries.CreateForm(ctx, store.CreateFormParams{
				Name:           form.Name,
				Slug:           form.Slug,
				Title:          form.Title,
				Description:    toNullString(form.Description),
				SuccessMessage: toNullString(form.SuccessMessage),
				EmailTo:        toNullString(form.EmailTo),
				IsActive:       form.IsActive,
				LanguageCode:   langCode,
				CreatedAt:      now,
				UpdatedAt:      now,
			})
			if createErr != nil {
				result.AddError("form", form.Slug, createErr.Error())
				continue
			}
			formID = created.ID
			formLangCode = langCode
			result.IncrementCreated("forms")
		}

		// Import form fields
		for _, field := range form.Fields {
			_, err := queries.CreateFormField(ctx, store.CreateFormFieldParams{
				FormID:       formID,
				Type:         field.Type,
				Name:         field.Name,
				Label:        field.Label,
				Placeholder:  toNullString(field.Placeholder),
				HelpText:     toNullString(field.HelpText),
				Options:      toNullString(field.Options),
				Validation:   toNullString(field.Validation),
				IsRequired:   field.IsRequired,
				Position:     field.Position,
				LanguageCode: formLangCode,
				CreatedAt:    now,
				UpdatedAt:    now,
			})
			if err != nil {
				i.logger.Warn("failed to create form field", "form", form.Slug, "field", field.Name, "error", err)
			}
		}

		// Import submissions if present
		for _, sub := range form.Submissions {
			_, err := queries.CreateFormSubmission(ctx, store.CreateFormSubmissionParams{
				FormID:       formID,
				Data:         sub.Data,
				IpAddress:    toNullString(sub.IPAddress),
				UserAgent:    toNullString(sub.UserAgent),
				IsRead:       sub.IsRead,
				LanguageCode: formLangCode,
				CreatedAt:    sub.CreatedAt,
			})
			if err != nil {
				i.logger.Warn("failed to create form submission", "form", form.Slug, "error", err)
			}
		}
	}
}

func (i *Importer) importConfig(ctx context.Context, queries *store.Queries, config map[string]string, userMap map[string]int64, defaultLangCode string, result *ImportResult) {
	now := time.Now()

	// Get a default user ID for the updated_by field
	updatedBy := int64(1) // Default to first user
	for _, id := range userMap {
		updatedBy = id
		break
	}

	for key, value := range config {
		_, err := queries.UpsertConfig(ctx, store.UpsertConfigParams{
			Key:          key,
			Value:        value,
			Type:         "string",
			Description:  "",
			LanguageCode: defaultLangCode,
			UpdatedAt:    now,
			UpdatedBy:    sql.NullInt64{Int64: updatedBy, Valid: true},
		})
		if err != nil {
			result.AddError("config", key, err.Error())
			continue
		}
		result.IncrementCreated("config")
	}
}

// Helper functions

// buildEntityMap creates a map from a slice of entities using the provided key and ID extractors.
func buildEntityMap[T any](items []T, keyFn func(T) string, idFn func(T) int64) map[string]int64 {
	m := make(map[string]int64, len(items))
	for _, item := range items {
		m[keyFn(item)] = idFn(item)
	}
	return m
}

// importEntityType defines the type of entity for building lookup maps.
type importEntityType int

const (
	entityLanguage importEntityType = iota
	entityUser
	entityCategory
	entityTag
	entityMedia
	entityPage
)

// buildLookupMap builds a string-to-ID lookup map for the specified entity type.
func (i *Importer) buildLookupMap(ctx context.Context, queries *store.Queries, entityType importEntityType) (map[string]int64, error) {
	switch entityType {
	case entityLanguage:
		languages, err := queries.ListLanguages(ctx)
		if err != nil {
			return nil, err
		}
		return buildEntityMap(languages, func(l store.Language) string { return l.Code }, func(l store.Language) int64 { return l.ID }), nil
	case entityUser:
		users, err := queries.ListUsers(ctx, store.ListUsersParams{Limit: 10000, Offset: 0})
		if err != nil {
			return nil, err
		}
		return buildEntityMap(users, func(u store.User) string { return u.Email }, func(u store.User) int64 { return u.ID }), nil
	case entityCategory:
		categories, err := queries.ListCategories(ctx)
		if err != nil {
			return nil, err
		}
		return buildEntityMap(categories, func(c store.Category) string { return c.Slug }, func(c store.Category) int64 { return c.ID }), nil
	case entityTag:
		tags, err := queries.ListAllTags(ctx)
		if err != nil {
			return nil, err
		}
		return buildEntityMap(tags, func(t store.Tag) string { return t.Slug }, func(t store.Tag) int64 { return t.ID }), nil
	case entityMedia:
		media, err := queries.ListMedia(ctx, store.ListMediaParams{Limit: 100000, Offset: 0})
		if err != nil {
			return nil, err
		}
		return buildEntityMap(media, func(m store.Medium) string { return m.Uuid }, func(m store.Medium) int64 { return m.ID }), nil
	case entityPage:
		pages, err := queries.ListPages(ctx, store.ListPagesParams{Limit: 100000, Offset: 0})
		if err != nil {
			return nil, err
		}
		return buildEntityMap(pages, func(p store.Page) string { return p.Slug }, func(p store.Page) int64 { return p.ID }), nil
	default:
		return make(map[string]int64), nil
	}
}

func (i *Importer) buildOrCreateFolders(ctx context.Context, queries *store.Queries, media []ExportMedia) (map[string]int64, error) {
	folderMap := make(map[string]int64)

	// Collect unique folder paths
	paths := make(map[string]bool)
	for _, m := range media {
		if m.FolderPath != "" {
			paths[m.FolderPath] = true
		}
	}

	now := time.Now()

	for path := range paths {
		// Check if folder exists by building/finding path
		parts := strings.Split(path, "/")
		var parentID sql.NullInt64

		currentPath := ""
		for _, part := range parts {
			if part == "" {
				continue
			}
			if currentPath == "" {
				currentPath = part
			} else {
				currentPath = currentPath + "/" + part
			}

			// Check if this folder exists
			if id, ok := folderMap[currentPath]; ok {
				parentID = sql.NullInt64{Int64: id, Valid: true}
				continue
			}

			// Try to find or create the folder
			folders, err := queries.ListMediaFolders(ctx)
			if err != nil {
				return nil, err
			}

			found := false
			for _, folder := range folders {
				if folder.Name == part && folder.ParentID == parentID {
					folderMap[currentPath] = folder.ID
					parentID = sql.NullInt64{Int64: folder.ID, Valid: true}
					found = true
					break
				}
			}

			if !found {
				// Create the folder
				folder, err := queries.CreateMediaFolder(ctx, store.CreateMediaFolderParams{
					Name:      part,
					ParentID:  parentID,
					Position:  0,
					CreatedAt: now,
				})
				if err != nil {
					return nil, err
				}
				folderMap[currentPath] = folder.ID
				parentID = sql.NullInt64{Int64: folder.ID, Valid: true}
			}
		}
	}

	return folderMap, nil
}

func (i *Importer) generateUniqueSlug(ctx context.Context, queries *store.Queries, baseSlug string, entityType string) string {
	slug := baseSlug
	counter := 1

	for {
		var exists bool
		var err error

		switch entityType {
		case "page":
			count, e := queries.SlugExists(ctx, slug)
			exists = count > 0
			err = e
		case "category":
			count, e := queries.CategorySlugExists(ctx, slug)
			exists = count > 0
			err = e
		case "tag":
			count, e := queries.TagSlugExists(ctx, slug)
			exists = count > 0
			err = e
		case "menu":
			count, e := queries.MenuSlugExists(ctx, slug)
			exists = count > 0
			err = e
		case "form":
			_, e := queries.GetFormBySlug(ctx, slug)
			exists = e == nil
			err = nil
		default:
			return slug
		}

		if err != nil || !exists {
			return slug
		}

		counter++
		slug = fmt.Sprintf("%s-%d", baseSlug, counter)
	}
}

// generateRandomPassword generates a random password for imported users.
func generateRandomPassword() string {
	// Generate a random 16-character password
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
	result := make([]byte, 16)
	for i := range result {
		result[i] = chars[time.Now().UnixNano()%int64(len(chars))]
		time.Sleep(time.Nanosecond)
	}
	return string(result)
}

// toNullString converts a string to sql.NullString.
func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
