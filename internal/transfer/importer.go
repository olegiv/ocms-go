// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

const (
	maxZipMediaFiles                  = 5000
	maxZipMediaFileUncompressedBytes  = 32 << 20  // 32 MB per media file
	maxZipMediaTotalUncompressedBytes = 512 << 20 // 512 MB total extracted media
	maxZipExportJSONUncompressedBytes = 32 << 20  // 32 MB archive metadata
	maxZipEntries                     = maxZipMediaFiles*2 + 16
	// MaxZipArchiveBytes is the maximum compressed ZIP accepted by transfer
	// APIs and emitted by the media exporter.
	MaxZipArchiveBytes int64 = 100 << 20 // 100 MiB
)

const (
	zipEndOfCentralDirectorySignature = 0x06054b50
	zipEndOfCentralDirectorySize      = 22
	zipMaxCommentBytes                = 1<<16 - 1
	zipCentralDirectorySignature      = 0x02014b50
	zipCentralDirectoryHeaderSize     = 46
)

func validateZipArchiveSize(size int64) error {
	if size < 0 || size > MaxZipArchiveBytes {
		return fmt.Errorf("zip archive exceeds max size (%d bytes)", MaxZipArchiveBytes)
	}
	return nil
}

// validateZipContainer rejects oversized or entry-dense archives before the
// standard library allocates the central-directory file list. The transfer
// format never needs multi-disk or ZIP64 metadata under its explicit limits.
func validateZipContainer(reader io.ReaderAt, size int64) error {
	if err := validateZipArchiveSize(size); err != nil {
		return err
	}
	if size < zipEndOfCentralDirectorySize {
		return errors.New("zip archive has no end-of-central-directory record")
	}
	tailSize := int64(zipEndOfCentralDirectorySize + zipMaxCommentBytes)
	if size < tailSize {
		tailSize = size
	}
	tail := make([]byte, tailSize)
	if _, err := reader.ReadAt(tail, size-tailSize); err != nil {
		return fmt.Errorf("read zip central directory: %w", err)
	}
	for offset := len(tail) - zipEndOfCentralDirectorySize; offset >= 0; offset-- {
		if binary.LittleEndian.Uint32(tail[offset:offset+4]) != zipEndOfCentralDirectorySignature {
			continue
		}
		commentLength := int(binary.LittleEndian.Uint16(tail[offset+20 : offset+22]))
		if offset+zipEndOfCentralDirectorySize+commentLength != len(tail) {
			continue
		}
		diskNumber := binary.LittleEndian.Uint16(tail[offset+4 : offset+6])
		centralDisk := binary.LittleEndian.Uint16(tail[offset+6 : offset+8])
		entriesOnDisk := binary.LittleEndian.Uint16(tail[offset+8 : offset+10])
		entriesTotal := binary.LittleEndian.Uint16(tail[offset+10 : offset+12])
		centralSize := binary.LittleEndian.Uint32(tail[offset+12 : offset+16])
		centralOffset := binary.LittleEndian.Uint32(tail[offset+16 : offset+20])
		if diskNumber != 0 || centralDisk != 0 || entriesOnDisk != entriesTotal {
			return errors.New("multi-disk zip archives are not supported")
		}
		if entriesTotal == ^uint16(0) || centralSize == ^uint32(0) || centralOffset == ^uint32(0) {
			return errors.New("ZIP64 archives are not supported")
		}
		centralStart := int64(centralOffset)
		centralEnd := centralStart + int64(centralSize)
		eocdOffset := size - tailSize + int64(offset)
		if centralStart < 0 || centralEnd < centralStart || centralEnd != eocdOffset {
			return errors.New("zip central directory has invalid bounds")
		}
		entryCount, err := countZipCentralDirectoryEntries(reader, centralStart, centralEnd)
		if err != nil {
			return err
		}
		if entryCount != int(entriesTotal) {
			return fmt.Errorf("zip central directory entry count %d does not match footer count %d",
				entryCount, entriesTotal)
		}
		return nil
	}
	return errors.New("zip archive has no valid end-of-central-directory record")
}

func countZipCentralDirectoryEntries(reader io.ReaderAt, start, end int64) (int, error) {
	count := 0
	header := make([]byte, zipCentralDirectoryHeaderSize)
	for offset := start; offset < end; {
		if count >= maxZipEntries {
			return 0, fmt.Errorf("zip contains too many entries (%d > %d)", count+1, maxZipEntries)
		}
		if end-offset < zipCentralDirectoryHeaderSize {
			return 0, errors.New("zip central directory has a truncated entry header")
		}
		if _, err := reader.ReadAt(header, offset); err != nil {
			return 0, fmt.Errorf("read zip central directory entry: %w", err)
		}
		if binary.LittleEndian.Uint32(header[:4]) != zipCentralDirectorySignature {
			return 0, errors.New("zip central directory contains an invalid entry signature")
		}
		nameLength := int64(binary.LittleEndian.Uint16(header[28:30]))
		extraLength := int64(binary.LittleEndian.Uint16(header[30:32]))
		commentLength := int64(binary.LittleEndian.Uint16(header[32:34]))
		next := offset + zipCentralDirectoryHeaderSize + nameLength + extraLength + commentLength
		if next <= offset || next > end {
			return 0, errors.New("zip central directory entry exceeds declared bounds")
		}
		offset = next
		count++
	}
	return count, nil
}

type mediaZipPath struct {
	mediaType string
	uuid      string
	filename  string
}

type mediaZipEntry struct {
	file *zip.File
	path mediaZipPath
}

type declaredMediaZipEntry struct {
	size int64
}

type mediaIdentityMismatchError struct {
	message string
}

func (e *mediaIdentityMismatchError) Error() string { return e.message }

func mediaIdentityMismatch(format string, args ...any) error {
	return &mediaIdentityMismatchError{message: fmt.Sprintf(format, args...)}
}

type mediaZipManifest struct {
	entries       []mediaZipEntry
	mediaByUUID   map[string]ExportMedia
	entryPaths    map[string]struct{}
	originalUUIDs map[string]struct{}
	affectedUUIDs []string
	totalBytes    uint64
}

// Importer handles importing CMS content from JSON format.
type Importer struct {
	store                 *store.Queries
	db                    *sql.DB
	logger                *slog.Logger
	uploadDir             string
	processor             *imaging.Processor
	beforeMediaImport     func()
	beforeMediaOwnerCheck func()
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

// getProcessor returns the optional caller-supplied imaging processor,
// creating one if needed. ZIP writes still use a processor rooted at the
// captured canonical upload directory; this instance is used only for
// content-type capability checks.
func (i *Importer) getProcessor() *imaging.Processor {
	if i.processor == nil {
		i.processor = imaging.NewProcessor(i.uploadDir)
	}
	return i.processor
}

// Import performs the import operation based on the provided options.
// The import runs in a transaction and rolls back on error.
func (i *Importer) Import(ctx context.Context, data *ExportData, opts ImportOptions) (*ImportResult, error) {
	if opts.ImportMediaFiles {
		result := NewImportResult(opts.DryRun)
		message := "media file import requires a ZIP archive"
		result.AddError("media", "files", message)
		return result, errors.New(message)
	}
	return i.importWithPreCommit(ctx,
		normalizedTransferMediaIdentities(data, opts.ImportMedia || opts.ImportMediaFiles), opts, nil)
}

// normalizedTransferMediaIdentities gives each imported media row one logical
// database and filesystem identity. UUID syntax is case-insensitive, while
// SQLite uniqueness and Linux paths are not, so archive media metadata and
// declared paths are always normalized to lowercase. Content references are
// normalized only when media rows are selected for import; an unselected media
// section must not change page-only or config-only destination references. The
// caller-owned payload is never modified.
func normalizedTransferMediaIdentities(data *ExportData, normalizeContentReferences bool) *ExportData {
	if data == nil {
		return nil
	}
	normalized := *data
	normalized.Media = append([]ExportMedia(nil), data.Media...)
	uuidReplacements := make(map[string]string)
	for index := range normalized.Media {
		medium := &normalized.Media[index]
		originalUUID := medium.UUID
		medium.UUID = strings.ToLower(medium.UUID)
		if normalizeContentReferences && imaging.IsCanonicalMediaUUID(originalUUID) {
			uuidReplacements[strings.ToLower(originalUUID)] = medium.UUID
		}
		medium.FilePath = normalizedDeclaredMediaPath(medium.FilePath)
		medium.Variants = append([]ExportVariant(nil), medium.Variants...)
		for variantIndex := range medium.Variants {
			medium.Variants[variantIndex].FilePath = normalizedDeclaredMediaPath(
				medium.Variants[variantIndex].FilePath,
			)
		}
	}
	normalized.Pages = append([]ExportPage(nil), data.Pages...)
	for index := range normalized.Pages {
		page := &normalized.Pages[index]
		page.Body = rewriteKnownMediaURLs(page.Body, uuidReplacements)
		page.VideoURL = rewriteKnownMediaURLs(page.VideoURL, uuidReplacements)
		if page.FeaturedImage != nil {
			ref := *page.FeaturedImage
			if replacement, ok := uuidReplacements[strings.ToLower(ref.UUID)]; ok {
				ref.UUID = replacement
			}
			page.FeaturedImage = &ref
		}
		if page.SEO != nil {
			seo := *page.SEO
			seo.CanonicalURL = rewriteKnownMediaURLs(seo.CanonicalURL, uuidReplacements)
			if seo.OgImage != nil {
				ref := *seo.OgImage
				if replacement, ok := uuidReplacements[strings.ToLower(ref.UUID)]; ok {
					ref.UUID = replacement
				}
				seo.OgImage = &ref
			}
			page.SEO = &seo
		}
	}
	normalized.Categories = append([]ExportCategory(nil), data.Categories...)
	for index := range normalized.Categories {
		normalized.Categories[index].Description = rewriteKnownMediaURLs(
			normalized.Categories[index].Description, uuidReplacements,
		)
	}
	normalized.Menus = append([]ExportMenu(nil), data.Menus...)
	for index := range normalized.Menus {
		normalized.Menus[index].Items = normalizedMenuMediaURLs(normalized.Menus[index].Items, uuidReplacements)
	}
	normalized.Forms = append([]ExportForm(nil), data.Forms...)
	for index := range normalized.Forms {
		form := &normalized.Forms[index]
		form.Description = rewriteKnownMediaURLs(form.Description, uuidReplacements)
		form.SuccessMessage = rewriteKnownMediaURLs(form.SuccessMessage, uuidReplacements)
		form.Fields = append([]ExportFormField(nil), form.Fields...)
		for fieldIndex := range form.Fields {
			field := &form.Fields[fieldIndex]
			field.Placeholder = rewriteKnownMediaURLs(field.Placeholder, uuidReplacements)
			field.HelpText = rewriteKnownMediaURLs(field.HelpText, uuidReplacements)
			field.Options = rewriteKnownMediaURLs(field.Options, uuidReplacements)
			field.Validation = rewriteKnownMediaURLs(field.Validation, uuidReplacements)
		}
		form.Submissions = append([]ExportFormSubmission(nil), form.Submissions...)
		for submissionIndex := range form.Submissions {
			form.Submissions[submissionIndex].Data = rewriteKnownMediaURLs(
				form.Submissions[submissionIndex].Data, uuidReplacements,
			)
		}
	}
	if data.Config != nil {
		normalized.Config = make(map[string]string, len(data.Config))
		for key, value := range data.Config {
			normalized.Config[key] = rewriteKnownMediaURLs(value, uuidReplacements)
		}
	}
	return &normalized
}

func normalizedMenuMediaURLs(items []ExportMenuItem, replacements map[string]string) []ExportMenuItem {
	normalized := append([]ExportMenuItem(nil), items...)
	for index := range normalized {
		normalized[index].URL = rewriteKnownMediaURLs(normalized[index].URL, replacements)
		normalized[index].Children = normalizedMenuMediaURLs(normalized[index].Children, replacements)
	}
	return normalized
}

func rewriteKnownMediaURLs(value string, replacements map[string]string) string {
	if value == "" || len(replacements) == 0 {
		return value
	}
	for _, storageDir := range model.MediaStorageDirs() {
		prefix := mediaURLStoragePrefix(storageDir)
		searchFrom := 0
		for searchFrom < len(value) {
			offset := strings.Index(value[searchFrom:], prefix)
			if offset < 0 {
				break
			}
			matchStart := searchFrom + offset
			uuidStart := matchStart + len(prefix)
			// Only this site's own URLs are rewritten. An external link whose
			// path happens to carry an imported UUID belongs to another server,
			// which may treat the path as case-sensitive, so recasing it would
			// quietly break a link this import does not own.
			if !startsLocalMediaURL(value, matchStart) {
				searchFrom = uuidStart
				continue
			}
			uuidEnd := uuidStart + 36
			if uuidEnd >= len(value) || value[uuidEnd] != '/' {
				searchFrom = uuidStart
				continue
			}
			candidate := value[uuidStart:uuidEnd]
			replacement, ok := replacements[strings.ToLower(candidate)]
			if !ok || !imaging.IsCanonicalMediaUUID(candidate) {
				searchFrom = uuidEnd + 1
				continue
			}
			value = value[:uuidStart] + replacement + value[uuidEnd:]
			searchFrom = uuidStart + len(replacement) + 1
		}
	}
	return value
}

func normalizedDeclaredMediaPath(declaredPath string) string {
	if declaredPath == "" {
		return ""
	}
	parsed, err := parseMediaZipPath(declaredPath)
	if err != nil {
		return declaredPath
	}
	parsed.uuid = strings.ToLower(parsed.uuid)
	return canonicalMediaZipPath(parsed)
}

func canonicalMediaZipPath(parsed mediaZipPath) string {
	return path.Join("media", parsed.mediaType, parsed.uuid, parsed.filename)
}

func (i *Importer) importWithPreCommit(
	ctx context.Context,
	data *ExportData,
	opts ImportOptions,
	preCommitCheck func() error,
) (*ImportResult, error) {
	if opts.ConflictStrategy == "" {
		opts.ConflictStrategy = ConflictSkip
	}
	result := NewImportResult(opts.DryRun)
	if !isValidConflictStrategy(opts.ConflictStrategy) {
		result.AddError("import", "conflict_strategy", fmt.Sprintf("invalid conflict strategy %q", opts.ConflictStrategy))
		return result, errors.New("invalid conflict strategy")
	}

	// Validate the import data first
	validationErrors := i.validate(data, opts.ImportLanguages, opts.ImportMedia || opts.ImportMediaFiles)
	if len(validationErrors) > 0 {
		for _, err := range validationErrors {
			result.AddError(err.Entity, err.ID, err.Message)
		}
		return result, fmt.Errorf("validation failed: %s", validationErrors[0].Message)
	}

	plan, contractErrors, err := i.preflightLanguageContract(ctx, data, opts)
	if err != nil {
		result.AddError("language", "", err.Error())
		return result, err
	}
	if len(contractErrors) > 0 {
		for _, contractErr := range contractErrors {
			result.AddError(contractErr.Entity, contractErr.ID, contractErr.Message)
		}
		return result, fmt.Errorf("language contract validation failed: %s", contractErrors[0].Message)
	}

	// Canonical URLs are normalized unconditionally, unlike the media checks
	// below: an archive is an untrusted payload, and this is the only gate
	// between it and a value that gets published into a canonical link and an
	// og:url meta tag.
	data = normalizeImportedPageCanonicalURLs(data, opts, result)

	if importNeedsMediaIdentityResolution(data, opts) {
		if i.store == nil {
			return result, errors.New("import requires a destination store")
		}
		destinationMedia, err := loadDestinationMediaIdentityIndex(ctx, i.store)
		if err != nil {
			return result, fmt.Errorf("index destination media identities: %w", err)
		}
		if err := validateImportedMediaIdentities(data, opts, destinationMedia); err != nil {
			result.AddError("media", "", err.Error())
			return result, err
		}
		if err := validateImportedPageMediaReferences(data, opts, destinationMedia); err != nil {
			result.AddError("media", "", err.Error())
			return result, err
		}
		if err := validateImportedContentMediaURLs(data, opts, destinationMedia); err != nil {
			result.AddError("media", "", err.Error())
			return result, err
		}
	}

	// If dry run, validate and count exactly what the real import would do.
	if opts.DryRun {
		if err := i.countEntities(ctx, data, opts, plan.defaultCode, result); err != nil {
			return result, fmt.Errorf("failed to count dry-run entities: %w", err)
		}
		if err := i.countTranslationEdges(ctx, data, opts, plan.defaultCode, result); err != nil {
			return result, fmt.Errorf("failed to count dry-run translations: %w", err)
		}
		return result, nil
	}

	// Start transaction
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		_ = tx.Rollback()
		// None of the accumulated entity mutations survived. Keep diagnostics,
		// but never report rolled-back counters or ID mappings as imported work.
		result.Success = false
		result.Created = make(map[string]int)
		result.Updated = make(map[string]int)
		result.Skipped = make(map[string]int)
		result.IDMaps = make(map[string]IDMapping)
	}()

	// Create queries with transaction
	queries := i.store.WithTx(tx)
	if err := i.importSelectedEntities(ctx, queries, data, opts, result); err != nil {
		return result, err
	}

	if preCommitCheck != nil {
		if err := preCommitCheck(); err != nil {
			return result, fmt.Errorf("pre-commit import validation failed: %w", err)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true

	return result, nil
}

func (i *Importer) importSelectedEntities(
	ctx context.Context,
	queries *store.Queries,
	data *ExportData,
	opts ImportOptions,
	result *ImportResult,
) error {
	// Import in dependency order: languages, users, taxonomy, media, pages,
	// menus, forms, config, then translation edges.
	if opts.ImportLanguages && len(data.Languages) > 0 {
		if err := i.importLanguages(ctx, queries, data.Languages, opts, result); err != nil {
			return fmt.Errorf("failed to import languages: %w", err)
		}
	}

	defaultLang, err := destinationDefaultLanguage(ctx, queries)
	if err != nil {
		return fmt.Errorf("failed to resolve the destination default language: %w", err)
	}
	defaultLangCode := defaultLang.Code

	if refErrors := i.validateLanguageReferences(ctx, queries, data, opts, defaultLangCode); len(refErrors) > 0 {
		for _, refErr := range refErrors {
			result.AddError(refErr.Entity, refErr.ID, refErr.Message)
		}
		return errors.New("unknown language reference")
	}

	if opts.ImportUsers && len(data.Users) > 0 {
		i.importUsers(ctx, queries, data.Users, opts, result)
	}
	userMap, err := i.buildLookupMap(ctx, queries, entityUser)
	if err != nil {
		i.logger.Warn("failed to build user map", "error", err)
		userMap = make(map[string]int64)
	}

	if opts.ImportCategories && len(data.Categories) > 0 {
		if err := i.importCategories(ctx, queries, data.Categories, defaultLangCode, opts, result); err != nil {
			return fmt.Errorf("failed to import categories: %w", err)
		}
	}
	categoryMap, err := i.buildLookupMap(ctx, queries, entityCategory)
	if err != nil {
		i.logger.Warn("failed to build category map", "error", err)
		categoryMap = make(map[string]int64)
	}
	if opts.ImportCategories {
		for _, category := range data.Categories {
			delete(categoryMap, category.Slug)
		}
	}
	for _, category := range data.Categories {
		if id, ok := result.GetIDMap("categories")[category.ID]; ok {
			categoryMap[category.Slug] = id
		}
	}

	if opts.ImportTags && len(data.Tags) > 0 {
		i.importTags(ctx, queries, data.Tags, defaultLangCode, opts, result)
	}
	tagMap, err := i.buildLookupMap(ctx, queries, entityTag)
	if err != nil {
		i.logger.Warn("failed to build tag map", "error", err)
		tagMap = make(map[string]int64)
	}
	if opts.ImportTags {
		for _, tag := range data.Tags {
			delete(tagMap, tag.Slug)
		}
	}
	for _, tag := range data.Tags {
		if id, ok := result.GetIDMap("tags")[tag.ID]; ok {
			tagMap[tag.Slug] = id
		}
	}

	if opts.ImportMedia && len(data.Media) > 0 {
		if err := i.importMedia(ctx, queries, data.Media, userMap, defaultLangCode, opts, result); err != nil {
			return fmt.Errorf("failed to import media: %w", err)
		}
	}

	mediaMap := make(map[string]int64)
	if opts.ImportPages {
		mediaMap, err = i.buildLookupMap(ctx, queries, entityMedia)
		if err != nil {
			return fmt.Errorf("build media map: %w", err)
		}
	}
	if opts.ImportPages && len(data.Pages) > 0 {
		if err := i.importPages(ctx, queries, data.Pages, userMap, categoryMap, tagMap, mediaMap,
			defaultLangCode, opts, result); err != nil {
			return fmt.Errorf("failed to import pages: %w", err)
		}
	}

	pageMap, err := i.buildLookupMap(ctx, queries, entityPage)
	if err != nil {
		i.logger.Warn("failed to build page map", "error", err)
		pageMap = make(map[string]int64)
	}
	if opts.ImportPages {
		for _, page := range data.Pages {
			delete(pageMap, page.Slug)
		}
	}
	// Menu items retain the archive's original page slug. Overlay it with the
	// exact imported ID when ConflictRename changes the destination slug.
	for _, page := range data.Pages {
		if id, ok := result.GetIDMap("pages")[page.ID]; ok {
			pageMap[page.Slug] = id
		}
	}

	if opts.ImportMenus && len(data.Menus) > 0 {
		if err := i.importMenus(ctx, queries, data.Menus, pageMap, defaultLangCode, opts, result); err != nil {
			return fmt.Errorf("failed to import menus: %w", err)
		}
	}
	if opts.ImportForms && len(data.Forms) > 0 {
		i.importForms(ctx, queries, data.Forms, defaultLangCode, opts, result)
	}
	if opts.ImportConfig && len(data.Config) > 0 {
		i.importConfig(ctx, queries, data.Config, userMap, defaultLangCode, opts, result)
	}
	if err := i.importTranslations(ctx, queries, data, defaultLangCode, opts, result); err != nil {
		return fmt.Errorf("failed to import translations: %w", err)
	}

	return nil
}

func validateImportedMediaIdentities(
	data *ExportData,
	opts ImportOptions,
	destination destinationMediaIdentityIndex,
) error {
	if !opts.ImportMedia {
		return nil
	}
	for _, medium := range data.Media {
		if _, _, err := destination.exact(medium.UUID); err != nil {
			return fmt.Errorf("media UUID %q: %w", medium.UUID, err)
		}
	}
	return nil
}

func validateImportedPageMediaReferences(
	data *ExportData,
	opts ImportOptions,
	destination destinationMediaIdentityIndex,
) error {
	if !opts.ImportPages {
		return nil
	}
	availableFromArchive := make(map[string]struct{}, len(data.Media))
	if opts.ImportMedia {
		for _, medium := range data.Media {
			availableFromArchive[strings.ToLower(medium.UUID)] = struct{}{}
		}
	}
	for _, page := range data.Pages {
		refs := []*ExportMediaRef{page.FeaturedImage}
		if page.SEO != nil {
			refs = append(refs, page.SEO.OgImage)
		}
		for _, ref := range refs {
			if ref == nil || ref.UUID == "" {
				continue
			}
			if !imaging.IsCanonicalMediaUUID(ref.UUID) {
				return fmt.Errorf("page %q references invalid media UUID %q", page.Slug, ref.UUID)
			}
			if _, exists := availableFromArchive[strings.ToLower(ref.UUID)]; exists {
				continue
			}
			_, exists, err := destination.exact(ref.UUID)
			if err != nil {
				return fmt.Errorf("page %q media reference %q: %w", page.Slug, ref.UUID, err)
			}
			if !exists {
				return fmt.Errorf("page %q references unavailable media UUID %q", page.Slug, ref.UUID)
			}
		}
	}
	return nil
}

// normalizeImportedPageCanonicalURLs brings archive canonical URLs in line with
// the rule the admin form and the v2 API enforce, so all three write paths agree
// on what may reach the pages table.
//
// It clears rather than refuses. Every release before this rule shipped let the
// admin form store any string, and the exporter writes the column out verbatim,
// so refusing would make an instance's own backups unrestorable — and the rows
// that fail here are exactly the ones the startup audit already reports. A
// cleared value is recorded as a warning so the operator sees what changed, and
// the page still renders: BuildMeta computes the canonical URL when the stored
// one is empty.
//
// Valid values are written back trimmed, so the string that was validated is
// the string that gets stored.
//
// The caller's payload is never modified: a ZIP import runs this whole function
// twice over one ExportData, once for the dry-run preflight and once for real.
// Mutating in place would let the preflight clear the value and keep the
// warning, leaving the real result silent about a URL it had already discarded.
// Pages and the SEO blocks that change are copied instead, so each pass sees the
// archive as it arrived and reports what it did.
func normalizeImportedPageCanonicalURLs(data *ExportData, opts ImportOptions, result *ImportResult) *ExportData {
	if !opts.ImportPages {
		return data
	}

	// Copy on first change: an archive with nothing to fix keeps the original.
	normalized := data
	ownPages := func() {
		if normalized != data {
			return
		}
		clone := *data
		clone.Pages = append([]ExportPage(nil), data.Pages...)
		normalized = &clone
	}

	for index := range data.Pages {
		seo := data.Pages[index].SEO
		if seo == nil || seo.CanonicalURL == "" {
			continue
		}
		trimmed, err := util.ValidateCanonicalURL(seo.CanonicalURL)
		if err == nil && trimmed == seo.CanonicalURL {
			continue
		}
		ownPages()
		updated := *seo
		if err != nil {
			result.AddWarning("page", data.Pages[index].Slug, fmt.Sprintf(
				"canonical URL %q was cleared: %v", seo.CanonicalURL, err))
			updated.CanonicalURL = ""
		} else {
			updated.CanonicalURL = trimmed
		}
		normalized.Pages[index].SEO = &updated
	}
	return normalized
}

func validateImportedContentMediaURLs(
	data *ExportData,
	opts ImportOptions,
	destination destinationMediaIdentityIndex,
) error {
	availableFromArchive := make(map[string]struct{}, len(data.Media))
	if opts.ImportMedia {
		for _, medium := range data.Media {
			availableFromArchive[strings.ToLower(medium.UUID)] = struct{}{}
		}
	}
	validateValue := func(label, value string) error {
		mediaUUIDs, err := knownMediaUUIDs(value)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		for _, mediaUUID := range mediaUUIDs {
			if _, exists := availableFromArchive[strings.ToLower(mediaUUID)]; exists {
				continue
			}
			_, exists, err := destination.exact(mediaUUID)
			if err != nil {
				return fmt.Errorf("%s media URL UUID %q: %w", label, mediaUUID, err)
			}
			if !exists {
				return fmt.Errorf("%s references unavailable media UUID %q", label, mediaUUID)
			}
		}
		return nil
	}
	if opts.ImportPages {
		for _, page := range data.Pages {
			if err := validateValue(fmt.Sprintf("page %q body", page.Slug), page.Body); err != nil {
				return err
			}
			if err := validateValue(fmt.Sprintf("page %q video URL", page.Slug), page.VideoURL); err != nil {
				return err
			}
			if page.SEO != nil {
				if err := validateValue(fmt.Sprintf("page %q canonical URL", page.Slug), page.SEO.CanonicalURL); err != nil {
					return err
				}
			}
		}
	}
	if opts.ImportCategories {
		for _, category := range data.Categories {
			if err := validateValue(fmt.Sprintf("category %q description", category.Slug), category.Description); err != nil {
				return err
			}
		}
	}
	if opts.ImportMenus {
		var validateMenuItems func(string, []ExportMenuItem) error
		validateMenuItems = func(menuSlug string, items []ExportMenuItem) error {
			for _, item := range items {
				if err := validateValue(fmt.Sprintf("menu %q item %q URL", menuSlug, item.Title), item.URL); err != nil {
					return err
				}
				if err := validateMenuItems(menuSlug, item.Children); err != nil {
					return err
				}
			}
			return nil
		}
		for _, menu := range data.Menus {
			if err := validateMenuItems(menu.Slug, menu.Items); err != nil {
				return err
			}
		}
	}
	if opts.ImportForms {
		for _, form := range data.Forms {
			values := []struct {
				label string
				value string
			}{
				{"description", form.Description},
				{"success message", form.SuccessMessage},
			}
			for _, value := range values {
				if err := validateValue(fmt.Sprintf("form %q %s", form.Slug, value.label), value.value); err != nil {
					return err
				}
			}
			for _, field := range form.Fields {
				fieldValues := []struct {
					label string
					value string
				}{
					{"placeholder", field.Placeholder},
					{"help text", field.HelpText},
					{"options", field.Options},
					{"validation", field.Validation},
				}
				for _, value := range fieldValues {
					if err := validateValue(
						fmt.Sprintf("form %q field %q %s", form.Slug, field.Name, value.label), value.value,
					); err != nil {
						return err
					}
				}
			}
			for _, submission := range form.Submissions {
				if err := validateValue(fmt.Sprintf("form %q submission", form.Slug), submission.Data); err != nil {
					return err
				}
			}
		}
	}
	if opts.ImportConfig {
		for key, value := range data.Config {
			if err := validateValue(fmt.Sprintf("config %q", key), value); err != nil {
				return err
			}
		}
	}
	return nil
}

func importNeedsMediaIdentityResolution(data *ExportData, opts ImportOptions) bool {
	if len(data.Media) > 0 && (opts.ImportMedia || opts.ImportMediaFiles) {
		return true
	}
	if opts.ImportPages {
		for _, page := range data.Pages {
			if page.FeaturedImage != nil || (page.SEO != nil && page.SEO.OgImage != nil) ||
				containsKnownMediaURL(page.Body) || containsKnownMediaURL(page.VideoURL) ||
				(page.SEO != nil && containsKnownMediaURL(page.SEO.CanonicalURL)) {
				return true
			}
		}
	}
	if opts.ImportCategories {
		for _, category := range data.Categories {
			if containsKnownMediaURL(category.Description) {
				return true
			}
		}
	}
	if opts.ImportMenus {
		for _, menu := range data.Menus {
			if menuItemsContainKnownMediaURL(menu.Items) {
				return true
			}
		}
	}
	if opts.ImportForms {
		for _, form := range data.Forms {
			if containsKnownMediaURL(form.Description) || containsKnownMediaURL(form.SuccessMessage) {
				return true
			}
			for _, field := range form.Fields {
				if containsKnownMediaURL(field.Placeholder) || containsKnownMediaURL(field.HelpText) ||
					containsKnownMediaURL(field.Options) || containsKnownMediaURL(field.Validation) {
					return true
				}
			}
			for _, submission := range form.Submissions {
				if containsKnownMediaURL(submission.Data) {
					return true
				}
			}
		}
	}
	if opts.ImportConfig {
		for _, value := range data.Config {
			if containsKnownMediaURL(value) {
				return true
			}
		}
	}
	return false
}

func containsKnownMediaURL(value string) bool {
	for _, storageDir := range model.MediaStorageDirs() {
		if len(localMediaURLOffsets(value, mediaURLStoragePrefix(storageDir))) > 0 {
			return true
		}
	}
	return false
}

func knownMediaUUIDs(value string) ([]string, error) {
	mediaUUIDs := make([]string, 0)
	seen := make(map[string]struct{})
	for _, storageDir := range model.MediaStorageDirs() {
		prefix := mediaURLStoragePrefix(storageDir)
		for _, offset := range localMediaURLOffsets(value, prefix) {
			remainder := value[offset+len(prefix):]
			slashOffset := strings.IndexByte(remainder, '/')
			if slashOffset < 0 {
				return nil, fmt.Errorf("media URL under %q has no filename", prefix)
			}
			candidate := remainder[:slashOffset]
			if !imaging.IsCanonicalMediaUUID(candidate) {
				return nil, fmt.Errorf("media URL under %q has invalid UUID %q", prefix, candidate)
			}
			if _, exists := seen[candidate]; !exists {
				seen[candidate] = struct{}{}
				mediaUUIDs = append(mediaUUIDs, candidate)
			}
		}
	}
	return mediaUUIDs, nil
}

// mediaURLBoundaryChars are the characters that may sit immediately before a
// root-relative URL: HTML attribute quoting and delimiters, CSS url(),
// Markdown link syntax, JSON, and ordinary prose punctuation.
const mediaURLBoundaryChars = "\"'()<>[]{},;=|`"

// localMediaURLOffsets returns every offset in value where a root-relative URL
// under prefix begins.
//
// Only root-relative matches belong to this installation. Content carries
// other sites' URLs whose paths happen to read like local media — a CDN
// serving https://cdn.example/uploads/originals/avatar/file.png, say — and
// treating the text after the prefix as a media UUID rejected one such link as
// malformed and failed the entire export over a resource this site does not
// own. A match preceded by anything other than a boundary is part of some
// other URL, so it is left alone.
func localMediaURLOffsets(value, prefix string) []int {
	var offsets []int
	for searchFrom := 0; searchFrom < len(value); {
		offset := strings.Index(value[searchFrom:], prefix)
		if offset < 0 {
			break
		}
		matchStart := searchFrom + offset
		if startsLocalMediaURL(value, matchStart) {
			offsets = append(offsets, matchStart)
		}
		searchFrom = matchStart + len(prefix)
	}
	return offsets
}

func startsLocalMediaURL(value string, index int) bool {
	if index == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(value[:index])
	return unicode.IsSpace(previous) || strings.ContainsRune(mediaURLBoundaryChars, previous)
}

func mediaURLStoragePrefix(storageDir string) string {
	return strings.TrimSuffix(model.MediaURL(storageDir, "", ""), "/")
}

func menuItemsContainKnownMediaURL(items []ExportMenuItem) bool {
	for _, item := range items {
		if containsKnownMediaURL(item.URL) || menuItemsContainKnownMediaURL(item.Children) {
			return true
		}
	}
	return false
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
func (i *Importer) ImportFromFile(ctx context.Context, filePath string, opts ImportOptions) (*ImportResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = f.Close() }()

	return i.ImportFromReader(ctx, f, opts)
}

// ImportFromZip imports from a zip archive containing export.json and media files.
func (i *Importer) ImportFromZip(ctx context.Context, zipReader *zip.Reader, opts ImportOptions) (result *ImportResult, resultErr error) {
	if opts.ConflictStrategy == "" {
		opts.ConflictStrategy = ConflictSkip
	}
	if !isValidConflictStrategy(opts.ConflictStrategy) {
		return nil, fmt.Errorf("invalid conflict strategy %q", opts.ConflictStrategy)
	}
	if err := validateZipStructure(zipReader); err != nil {
		return nil, err
	}
	// Find and read export.json
	var exportData ExportData
	exportFound := false

	for _, f := range zipReader.File {
		if f.Name == "export.json" {
			if exportFound {
				return nil, errors.New("duplicate export.json entry")
			}
			var err error
			exportData, err = readZipExportData(f)
			if err != nil {
				return nil, err
			}
			exportFound = true
		}
	}

	if !exportFound {
		return nil, errors.New("export.json not found in zip archive")
	}
	exportData = *normalizedTransferMediaIdentities(&exportData, opts.ImportMedia || opts.ImportMediaFiles)
	manifest, err := buildMediaZipManifest(zipReader, &exportData)
	if err != nil {
		return nil, fmt.Errorf("invalid media manifest: %w", err)
	}
	// Prove the complete option-aware archive contract and every destination
	// relation before creating or opening the upload root. The real import repeats
	// these checks inside its transaction, but extraction must not precede them.
	preflightOpts := opts
	preflightOpts.DryRun = true
	preflightResult, preflightErr := i.importWithPreCommit(ctx, &exportData, preflightOpts, nil)
	if preflightErr != nil {
		return preflightResult, fmt.Errorf("zip import preflight failed: %w", preflightErr)
	}
	if !preflightResult.Success {
		return preflightResult, errors.New("zip import preflight reported errors")
	}
	if err := i.preflightMediaFileOwners(ctx, manifest, opts); err != nil {
		return nil, fmt.Errorf("invalid media file restore: %w", err)
	}
	if opts.DryRun {
		if opts.ImportMediaFiles && len(manifest.entries) > 0 {
			uploadRoot, openErr := imaging.OpenExistingUploadRoot(i.uploadDir)
			if openErr != nil {
				return preflightResult, fmt.Errorf("failed to inspect uploads root: %w", openErr)
			}
			if uploadRoot != nil {
				freshErr := ensureFreshMediaStorage(uploadRoot, manifest.affectedUUIDs)
				closeErr := uploadRoot.Close()
				if err := errors.Join(freshErr, closeErr); err != nil {
					return preflightResult, fmt.Errorf("media storage preflight failed: %w", err)
				}
			}
			if err := i.validateMediaExtractionDryRun(ctx, manifest, &exportData, opts); err != nil {
				return preflightResult, fmt.Errorf("media file dry-run failed: %w", err)
			}
		}
		return preflightResult, nil
	}

	// Media extraction happens before the database transaction. Hold one verified
	// root capability throughout the write and retain its canonical name for all
	// compensation, so a configured symlink cannot retarget failure cleanup.
	canonicalUploadRoot := ""
	var uploadRoot *os.Root
	ownedMediaFiles := newMediaFileLedger()
	defer func() {
		if uploadRoot != nil {
			if err := uploadRoot.Close(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("close uploads root: %w", err))
			}
		}
	}()
	if opts.ImportMediaFiles && len(manifest.entries) > 0 {
		var openErr error
		uploadRoot, openErr = imaging.OpenUploadRoot(i.uploadDir)
		if openErr != nil {
			return nil, fmt.Errorf("failed to open uploads root: %w", openErr)
		}
		canonicalUploadRoot = uploadRoot.Name()
		if freshErr := ensureFreshMediaStorage(uploadRoot, manifest.affectedUUIDs); freshErr != nil {
			return nil, freshErr
		}
		var extractErr error
		ownedMediaFiles, extractErr = i.extractMediaFiles(manifest, uploadRoot)
		if extractErr != nil {
			cleanupErr := cleanupOwnedMediaFiles(uploadRoot, ownedMediaFiles, nil)
			return nil, fmt.Errorf("failed to extract media files: %w", errors.Join(extractErr, cleanupErr))
		}
		if i.beforeMediaImport != nil {
			i.beforeMediaImport()
		}
		if err := i.validateExtractedImages(uploadRoot, manifest); err != nil {
			cleanupErr := cleanupOwnedMediaFiles(uploadRoot, ownedMediaFiles, nil)
			return nil, fmt.Errorf("validate restored media images: %w", errors.Join(err, cleanupErr))
		}
		if err := i.generateMissingVariantsBeforeImport(ctx, uploadRoot, ownedMediaFiles, &manifest, &exportData, opts); err != nil {
			cleanupErr := cleanupOwnedMediaFiles(uploadRoot, ownedMediaFiles, nil)
			return nil, fmt.Errorf("generate missing media variants: %w", errors.Join(err, cleanupErr))
		}
		if err := validateOwnedMediaStorage(uploadRoot, ownedMediaFiles, manifest.affectedUUIDs); err != nil {
			cleanupErr := cleanupOwnedMediaFiles(uploadRoot, ownedMediaFiles, nil)
			return nil, fmt.Errorf("validate imported media storage: %w", errors.Join(err, cleanupErr))
		}
	}

	// Perform the regular import
	result, err = i.importWithPreCommit(ctx, &exportData, opts, func() error {
		if uploadRoot == nil {
			return nil
		}
		return errors.Join(
			imaging.ValidateOpenUploadRootIdentity(uploadRoot, canonicalUploadRoot),
			validateOwnedMediaStorage(uploadRoot, ownedMediaFiles, manifest.affectedUUIDs),
		)
	})
	if err != nil {
		cleanupErr := cleanupOwnedMediaFiles(uploadRoot, ownedMediaFiles, nil)
		return result, errors.Join(err, cleanupErr)
	}

	if canonicalUploadRoot != "" {
		if i.beforeMediaOwnerCheck != nil {
			i.beforeMediaOwnerCheck()
		}
		unowned, ownerErr := i.unownedExtractedMedia(ctx, manifest)
		unownedFilter := make([]string, len(unowned))
		copy(unownedFilter, unowned)
		cleanupErr := cleanupOwnedMediaFiles(uploadRoot, ownedMediaFiles, unownedFilter)
		if ownerErr != nil || cleanupErr != nil {
			return result, fmt.Errorf("verify imported media file ownership: %w", errors.Join(ownerErr, cleanupErr))
		}
		if err := imaging.ValidateOpenUploadRootIdentity(uploadRoot, canonicalUploadRoot); err != nil {
			return result, fmt.Errorf("uploads root changed before media variant generation: %w", err)
		}
	}

	return result, nil
}

// ImportFromZipFile imports from a zip file path.
func (i *Importer) ImportFromZipFile(ctx context.Context, filePath string, opts ImportOptions) (*ImportResult, error) {
	// #nosec G304 -- this file-oriented API intentionally opens the archive path
	// explicitly authorized by its caller.
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open zip file: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect zip file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("failed to open zip file: %q is not a regular file", filePath)
	}
	if err := validateZipContainer(file, info.Size()); err != nil {
		return nil, err
	}
	zipReader, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, fmt.Errorf("failed to open zip file: %w", err)
	}
	return i.ImportFromZip(ctx, zipReader, opts)
}

// ImportFromZipBytes imports from zip archive bytes (useful for HTTP uploads).
func (i *Importer) ImportFromZipBytes(ctx context.Context, data []byte, opts ImportOptions) (*ImportResult, error) {
	reader := bytes.NewReader(data)
	if err := validateZipContainer(reader, int64(len(data))); err != nil {
		return nil, err
	}
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("failed to read zip data: %w", err)
	}

	return i.ImportFromZip(ctx, zipReader, opts)
}

func readZipExportData(file *zip.File) (ExportData, error) {
	if file.UncompressedSize64 > uint64(maxZipExportJSONUncompressedBytes) {
		return ExportData{}, fmt.Errorf("export.json exceeds max size (%d bytes)", maxZipExportJSONUncompressedBytes)
	}
	reader, err := file.Open()
	if err != nil {
		return ExportData{}, fmt.Errorf("failed to open export.json: %w", err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(reader, maxZipExportJSONUncompressedBytes+1))
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return ExportData{}, fmt.Errorf("failed to read export.json: %w", err)
	}
	if len(payload) > maxZipExportJSONUncompressedBytes {
		return ExportData{}, fmt.Errorf("export.json exceeds max size (%d bytes)", maxZipExportJSONUncompressedBytes)
	}
	var data ExportData
	if err := json.Unmarshal(payload, &data); err != nil {
		return ExportData{}, fmt.Errorf("failed to parse export.json: %w", err)
	}
	return data, nil
}

func validateZipStructure(zipReader *zip.Reader) error {
	if len(zipReader.File) > maxZipEntries {
		return fmt.Errorf("zip contains too many entries (%d > %d)", len(zipReader.File), maxZipEntries)
	}
	seen := make(map[string]struct{}, len(zipReader.File))
	for _, file := range zipReader.File {
		if _, duplicate := seen[file.Name]; duplicate {
			if file.Name == "export.json" {
				return errors.New("duplicate export.json entry")
			}
			if strings.HasPrefix(file.Name, "media/") {
				return fmt.Errorf("duplicate media archive path %q", file.Name)
			}
			return fmt.Errorf("duplicate zip archive path %q", file.Name)
		}
		seen[file.Name] = struct{}{}
		if file.Name == "export.json" {
			if file.FileInfo().IsDir() {
				return errors.New("export.json must be a regular file")
			}
			continue
		}
		if file.FileInfo().IsDir() || strings.HasSuffix(file.Name, "/") {
			return fmt.Errorf("undeclared zip archive directory %q", file.Name)
		}
		if !strings.HasPrefix(file.Name, "media/") {
			return fmt.Errorf("unknown zip archive entry %q", file.Name)
		}
	}
	return nil
}

func buildMediaZipManifest(zipReader *zip.Reader, data *ExportData) (mediaZipManifest, error) {
	if err := validateZipStructure(zipReader); err != nil {
		return mediaZipManifest{}, err
	}
	data = normalizedTransferMediaIdentities(data, false)
	manifest := mediaZipManifest{
		mediaByUUID:   make(map[string]ExportMedia),
		entryPaths:    make(map[string]struct{}),
		originalUUIDs: make(map[string]struct{}),
	}
	declaredByFoldedUUID := make(map[string]string, len(data.Media))
	for index, medium := range data.Media {
		if !imaging.IsCanonicalMediaUUID(medium.UUID) {
			return mediaZipManifest{}, fmt.Errorf("media %d has invalid canonical UUID %q", index, medium.UUID)
		}
		folded := strings.ToLower(medium.UUID)
		if previous, exists := declaredByFoldedUUID[folded]; exists {
			return mediaZipManifest{}, fmt.Errorf("duplicate media UUID %q conflicts with %q", medium.UUID, previous)
		}
		declaredByFoldedUUID[folded] = medium.UUID
		manifest.mediaByUUID[medium.UUID] = medium
	}

	declaredPaths := make(map[string]declaredMediaZipEntry)
	declarePath := func(owner, declaredPath, expectedType, expectedUUID, expectedFilename string, expectedSize int64) error {
		if declaredPath == "" {
			return nil
		}
		if expectedSize < 0 {
			return fmt.Errorf("%s declares negative file size %d", owner, expectedSize)
		}
		parsed, err := parseMediaZipPath(declaredPath)
		if err != nil {
			return fmt.Errorf("%s declares invalid file path %q: %w", owner, declaredPath, err)
		}
		if parsed.mediaType != expectedType || parsed.uuid != expectedUUID || parsed.filename != expectedFilename {
			return fmt.Errorf("%s file path %q does not match %s/%s/%s", owner, declaredPath,
				expectedType, expectedUUID, expectedFilename)
		}
		if _, duplicate := declaredPaths[declaredPath]; duplicate {
			return fmt.Errorf("duplicate declared media archive path %q", declaredPath)
		}
		declaredPaths[declaredPath] = declaredMediaZipEntry{size: expectedSize}
		return nil
	}
	for _, medium := range data.Media {
		if err := declarePath("media "+medium.UUID, medium.FilePath,
			model.OriginalsDir, medium.UUID, medium.Filename, medium.Size); err != nil {
			return mediaZipManifest{}, err
		}
		seenVariantTypes := make(map[string]struct{}, len(medium.Variants))
		for _, variant := range medium.Variants {
			if _, supported := model.ImageVariants[variant.Type]; !supported {
				return mediaZipManifest{}, fmt.Errorf("media %s declares unsupported variant type %q", medium.UUID, variant.Type)
			}
			if _, duplicate := seenVariantTypes[variant.Type]; duplicate {
				return mediaZipManifest{}, fmt.Errorf("media %s declares duplicate variant type %q", medium.UUID, variant.Type)
			}
			seenVariantTypes[variant.Type] = struct{}{}
			if err := declarePath("media "+medium.UUID+" variant "+variant.Type,
				variant.FilePath, variant.Type, medium.UUID, medium.Filename, variant.Size); err != nil {
				return mediaZipManifest{}, err
			}
		}
	}

	seenPaths := make(map[string]struct{})
	affected := make(map[string]struct{})
	for _, file := range zipReader.File {
		if !strings.HasPrefix(file.Name, "media/") || file.FileInfo().IsDir() {
			continue
		}
		if len(manifest.entries) >= maxZipMediaFiles {
			return mediaZipManifest{}, fmt.Errorf("zip contains too many media files (%d > %d)", len(manifest.entries)+1, maxZipMediaFiles)
		}
		if file.UncompressedSize64 > uint64(maxZipMediaFileUncompressedBytes) {
			return mediaZipManifest{}, fmt.Errorf("media file %q exceeds max size (%d bytes)", file.Name, maxZipMediaFileUncompressedBytes)
		}
		manifest.totalBytes += file.UncompressedSize64
		if manifest.totalBytes > uint64(maxZipMediaTotalUncompressedBytes) {
			return mediaZipManifest{}, fmt.Errorf("total media size exceeds max size (%d bytes)", maxZipMediaTotalUncompressedBytes)
		}

		parsedPath, err := parseMediaZipPath(file.Name)
		if err != nil {
			return mediaZipManifest{}, err
		}
		parsedPath.uuid = strings.ToLower(parsedPath.uuid)
		entryPath := canonicalMediaZipPath(parsedPath)
		if _, duplicate := seenPaths[entryPath]; duplicate {
			return mediaZipManifest{}, fmt.Errorf("duplicate media archive path identity %q", entryPath)
		}
		seenPaths[entryPath] = struct{}{}
		manifest.entryPaths[entryPath] = struct{}{}
		medium, declared := manifest.mediaByUUID[parsedPath.uuid]
		if !declared {
			return mediaZipManifest{}, fmt.Errorf("media path %q uses undeclared UUID %q", file.Name, parsedPath.uuid)
		}
		if parsedPath.filename != medium.Filename {
			return mediaZipManifest{}, fmt.Errorf("media path %q filename %q does not match declared filename %q", file.Name, parsedPath.filename, medium.Filename)
		}
		declaredEntry, declared := declaredPaths[entryPath]
		if !declared {
			return mediaZipManifest{}, fmt.Errorf("undeclared media archive entry %q", file.Name)
		}
		if declaredEntry.size < 0 {
			return mediaZipManifest{}, fmt.Errorf("media archive entry %q has negative declared size %d", file.Name, declaredEntry.size)
		}
		declaredSize := uint64(declaredEntry.size)
		if declaredSize != file.UncompressedSize64 {
			return mediaZipManifest{}, fmt.Errorf("media archive entry %q size %d does not match declared size %d",
				file.Name, file.UncompressedSize64, declaredEntry.size)
		}

		manifest.entries = append(manifest.entries, mediaZipEntry{file: file, path: parsedPath})
		affected[parsedPath.uuid] = struct{}{}
		if parsedPath.mediaType == model.OriginalsDir {
			manifest.originalUUIDs[parsedPath.uuid] = struct{}{}
		}
	}

	declaredPathNames := make([]string, 0, len(declaredPaths))
	for declaredPath := range declaredPaths {
		declaredPathNames = append(declaredPathNames, declaredPath)
	}
	sort.Strings(declaredPathNames)
	for _, declaredPath := range declaredPathNames {
		if _, exists := manifest.entryPaths[declaredPath]; !exists {
			return mediaZipManifest{}, fmt.Errorf("media manifest declares missing media archive entry %q", declaredPath)
		}
	}

	manifest.affectedUUIDs = make([]string, 0, len(affected))
	for mediaUUID := range affected {
		manifest.affectedUUIDs = append(manifest.affectedUUIDs, mediaUUID)
	}
	sort.Strings(manifest.affectedUUIDs)
	return manifest, nil
}

func (i *Importer) preflightMediaFileOwners(
	ctx context.Context,
	manifest mediaZipManifest,
	opts ImportOptions,
) error {
	if !opts.ImportMediaFiles {
		return nil
	}
	mediaUUIDs := make([]string, 0, len(manifest.mediaByUUID))
	for mediaUUID := range manifest.mediaByUUID {
		mediaUUIDs = append(mediaUUIDs, mediaUUID)
	}
	sort.Strings(mediaUUIDs)
	for _, mediaUUID := range mediaUUIDs {
		if _, hasOriginal := manifest.originalUUIDs[mediaUUID]; !hasOriginal {
			return fmt.Errorf("media UUID %q has no original file in the archive", mediaUUID)
		}
		for _, variant := range manifest.mediaByUUID[mediaUUID].Variants {
			if variant.FilePath == "" {
				return fmt.Errorf("media UUID %q variant %q has no file in the archive", mediaUUID, variant.Type)
			}
		}
	}
	if len(manifest.affectedUUIDs) == 0 {
		return nil
	}
	if i.store == nil {
		return errors.New("media file import requires a destination store")
	}
	strategy := opts.ConflictStrategy
	if strategy == "" {
		strategy = ConflictSkip
	}
	destinationMedia, err := loadDestinationMediaIdentityIndex(ctx, i.store)
	if err != nil {
		return fmt.Errorf("index destination media identities: %w", err)
	}
	for _, mediaUUID := range manifest.affectedUUIDs {
		declared := manifest.mediaByUUID[mediaUUID]
		existing, exists, err := destinationMedia.exact(mediaUUID)
		if err != nil {
			return fmt.Errorf("check destination media %q: %w", mediaUUID, err)
		}
		switch {
		case !exists:
			if !opts.ImportMedia {
				return fmt.Errorf("media UUID %q has no existing destination row for file-only restore", mediaUUID)
			}
		case !opts.ImportMedia || strategy != ConflictOverwrite:
			if err := i.validateImmutableMediaIdentity(ctx, existing, declared); err != nil {
				return fmt.Errorf("destination media %q does not match immutable archive metadata: %w", mediaUUID, err)
			}
		}
	}
	return nil
}

func (i *Importer) validateImmutableMediaIdentity(ctx context.Context, existing store.Medium, declared ExportMedia) error {
	switch {
	case existing.Filename != declared.Filename:
		return mediaIdentityMismatch("filename %q does not match %q", existing.Filename, declared.Filename)
	case existing.MimeType != declared.MimeType:
		return mediaIdentityMismatch("MIME type %q does not match %q", existing.MimeType, declared.MimeType)
	case existing.Size != declared.Size:
		return mediaIdentityMismatch("size %d does not match %d", existing.Size, declared.Size)
	case !nullableIntMatches(existing.Width, declared.Width):
		return mediaIdentityMismatch("width does not match")
	case !nullableIntMatches(existing.Height, declared.Height):
		return mediaIdentityMismatch("height does not match")
	}

	existingVariants, err := i.store.GetMediaVariants(ctx, existing.ID)
	if err != nil {
		return fmt.Errorf("read destination variants: %w", err)
	}
	if len(existingVariants) != len(declared.Variants) {
		return mediaIdentityMismatch("variant count %d does not match %d", len(existingVariants), len(declared.Variants))
	}
	declaredByType := make(map[string]ExportVariant, len(declared.Variants))
	for _, variant := range declared.Variants {
		declaredByType[variant.Type] = variant
	}
	for _, variant := range existingVariants {
		archiveVariant, exists := declaredByType[variant.Type]
		if !exists {
			return mediaIdentityMismatch("destination variant %q is absent from the archive", variant.Type)
		}
		if variant.Width != archiveVariant.Width || variant.Height != archiveVariant.Height || variant.Size != archiveVariant.Size {
			return mediaIdentityMismatch("destination variant %q metadata does not match", variant.Type)
		}
	}
	return nil
}

func nullableIntMatches(value sql.NullInt64, expected *int64) bool {
	if expected == nil {
		return !value.Valid
	}
	return value.Valid && value.Int64 == *expected
}

type ownedMediaFile struct {
	mediaUUID    string
	relativePath string
	identity     fs.FileInfo
}

type ownedMediaDirectory struct {
	mediaUUID    string
	relativePath string
	identity     fs.FileInfo
}

type mediaFileLedger struct {
	files       []ownedMediaFile
	directories []ownedMediaDirectory
}

func newMediaFileLedger() *mediaFileLedger {
	return &mediaFileLedger{}
}

func (ledger *mediaFileLedger) recordFile(mediaUUID string, identity *imaging.RootFileIdentity) error {
	if ledger == nil {
		return errors.New("media file ledger is nil")
	}
	if identity == nil || identity.Info == nil {
		return errors.New("media file identity is empty")
	}
	parts := strings.Split(identity.Path, "/")
	if len(parts) != 3 || parts[1] != mediaUUID || !fs.ValidPath(identity.Path) {
		return fmt.Errorf("invalid owned media path %q for UUID %q", identity.Path, mediaUUID)
	}
	allowedStorageDir := false
	for _, storageDir := range model.MediaStorageDirs() {
		if parts[0] == storageDir {
			allowedStorageDir = true
			break
		}
	}
	if !allowedStorageDir {
		return fmt.Errorf("invalid owned media storage directory %q", parts[0])
	}
	ledger.files = append(ledger.files, ownedMediaFile{
		mediaUUID: mediaUUID, relativePath: identity.Path, identity: identity.Info,
	})
	return nil
}

func (ledger *mediaFileLedger) recordDirectory(mediaUUID string, identity *imaging.RootFileIdentity) error {
	if ledger == nil {
		return errors.New("media file ledger is nil")
	}
	if identity == nil || identity.Info == nil {
		return errors.New("media directory identity is empty")
	}
	parts := strings.Split(identity.Path, "/")
	if len(parts) != 2 || parts[1] != mediaUUID || !fs.ValidPath(identity.Path) {
		return fmt.Errorf("invalid owned media directory %q for UUID %q", identity.Path, mediaUUID)
	}
	for _, storageDir := range model.MediaStorageDirs() {
		if parts[0] == storageDir {
			ledger.directories = append(ledger.directories, ownedMediaDirectory{
				mediaUUID: mediaUUID, relativePath: identity.Path, identity: identity.Info,
			})
			return nil
		}
	}
	return fmt.Errorf("invalid owned media storage directory %q", parts[0])
}

func (ledger *mediaFileLedger) hasDirectory(relativePath string) bool {
	if ledger == nil {
		return false
	}
	for _, directory := range ledger.directories {
		if directory.relativePath == relativePath {
			return true
		}
	}
	return false
}

// cleanupOwnedMediaFiles removes only files whose current identity still
// matches the descriptor snapshot captured once the extracted bytes landed.
// UUID directories are pruned non-recursively, so a same-path replacement or
// another actor's same-UUID sentinel is never removed as compensation.
func cleanupOwnedMediaFiles(uploadRoot *os.Root, ledger *mediaFileLedger, mediaUUIDs []string) error {
	return cleanupMediaFiles(uploadRoot, ledger, mediaUUIDs, imaging.SameRootFile)
}

// cleanupPartialMediaFile withdraws a file whose own extraction just failed.
//
// Its recorded identity is the empty file O_EXCL created, so the partial write
// being withdrawn no longer matches it by size and imaging.SameRootFile would
// leave the fragment behind to block the next import. Inode identity is enough
// over the microseconds between that failed copy and this call, unlike the
// whole-import window cleanupOwnedMediaFiles has to survive.
func cleanupPartialMediaFile(uploadRoot *os.Root, ledger *mediaFileLedger) error {
	return cleanupMediaFiles(uploadRoot, ledger, nil, func(current, recorded fs.FileInfo) bool {
		return os.SameFile(current, recorded)
	})
}

func cleanupMediaFiles(
	uploadRoot *os.Root,
	ledger *mediaFileLedger,
	mediaUUIDs []string,
	sameFile func(current, recorded fs.FileInfo) bool,
) error {
	if uploadRoot == nil || ledger == nil || (len(ledger.files) == 0 && len(ledger.directories) == 0) {
		return nil
	}
	var selected map[string]struct{}
	if mediaUUIDs != nil {
		selected = make(map[string]struct{}, len(mediaUUIDs))
		for _, mediaUUID := range mediaUUIDs {
			selected[mediaUUID] = struct{}{}
		}
	}
	var cleanupErrors []error
	for index := len(ledger.files) - 1; index >= 0; index-- {
		owned := ledger.files[index]
		if selected != nil {
			if _, include := selected[owned.mediaUUID]; !include {
				continue
			}
		}
		current, err := uploadRoot.Lstat(owned.relativePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("inspect owned media path %q: %w", owned.relativePath, err))
			continue
		}
		if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !sameFile(current, owned.identity) {
			continue
		}
		if err := uploadRoot.Remove(owned.relativePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("remove owned media path %q: %w", owned.relativePath, err))
		}
	}
	for index := len(ledger.directories) - 1; index >= 0; index-- {
		owned := ledger.directories[index]
		if selected != nil {
			if _, include := selected[owned.mediaUUID]; !include {
				continue
			}
		}
		current, err := uploadRoot.Lstat(owned.relativePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("inspect owned media directory %q: %w", owned.relativePath, err))
			continue
		}
		if current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(current, owned.identity) {
			continue
		}
		if err := uploadRoot.Remove(owned.relativePath); err != nil &&
			!errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) {
			cleanupErrors = append(cleanupErrors,
				fmt.Errorf("remove empty owned media directory %q: %w", owned.relativePath, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

// validateOwnedMediaStorage proves that every affected UUID directory contains
// exactly the files created by this import, with the same identities, and that
// no case-fold alias appeared after the initial freshness check.
func validateOwnedMediaStorage(uploadRoot *os.Root, ledger *mediaFileLedger, mediaUUIDs []string) error {
	if uploadRoot == nil {
		return errors.New("uploads root is nil")
	}
	if ledger == nil {
		return errors.New("media file ledger is nil")
	}
	affected := make(map[string]string, len(mediaUUIDs))
	for _, mediaUUID := range mediaUUIDs {
		if !imaging.IsCanonicalMediaUUID(mediaUUID) {
			return fmt.Errorf("invalid affected media UUID %q", mediaUUID)
		}
		logicalUUID := strings.ToLower(mediaUUID)
		if previous, exists := affected[logicalUUID]; exists && previous != mediaUUID {
			return fmt.Errorf("duplicate logical affected media UUID %q", logicalUUID)
		}
		affected[logicalUUID] = mediaUUID
	}

	expected := make(map[string]ownedMediaFile, len(ledger.files))
	for _, owned := range ledger.files {
		if _, exists := affected[strings.ToLower(owned.mediaUUID)]; !exists {
			return fmt.Errorf("owned media path %q has undeclared UUID %q", owned.relativePath, owned.mediaUUID)
		}
		if _, duplicate := expected[owned.relativePath]; duplicate {
			return fmt.Errorf("duplicate owned media path %q", owned.relativePath)
		}
		expected[owned.relativePath] = owned
	}
	expectedDirectories := make(map[string]ownedMediaDirectory, len(ledger.directories))
	for _, owned := range ledger.directories {
		if _, exists := affected[strings.ToLower(owned.mediaUUID)]; !exists {
			return fmt.Errorf("owned media directory %q has undeclared UUID %q", owned.relativePath, owned.mediaUUID)
		}
		if _, duplicate := expectedDirectories[owned.relativePath]; duplicate {
			return fmt.Errorf("duplicate owned media directory %q", owned.relativePath)
		}
		expectedDirectories[owned.relativePath] = owned
	}

	storageDirs := append([]string(nil), model.MediaStorageDirs()...)
	sort.Strings(storageDirs)
	for _, storageDir := range storageDirs {
		storageInfo, err := uploadRoot.Lstat(storageDir)
		if errors.Is(err, os.ErrNotExist) {
			for relativePath := range expectedDirectories {
				if strings.HasPrefix(relativePath, storageDir+"/") {
					return fmt.Errorf("owned media storage directory %q is missing", storageDir)
				}
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect media storage directory %q: %w", storageDir, err)
		}
		if storageInfo.Mode()&os.ModeSymlink != 0 || !storageInfo.IsDir() {
			return fmt.Errorf("media storage path %q is not a directory", storageDir)
		}
		storage, err := uploadRoot.Open(storageDir)
		if err != nil {
			return fmt.Errorf("open media storage directory %q: %w", storageDir, err)
		}
		entries, readErr := storage.ReadDir(-1)
		closeErr := storage.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return fmt.Errorf("inspect media storage directory %q: %w", storageDir, err)
		}
		sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })

		entryByUUID := make(map[string]fs.DirEntry, len(entries))
		for _, entry := range entries {
			logicalUUID := strings.ToLower(entry.Name())
			mediaUUID, relevant := affected[logicalUUID]
			if !relevant {
				continue
			}
			if entry.Name() != mediaUUID {
				return fmt.Errorf("media storage contains case alias %q for UUID %q in %q",
					entry.Name(), mediaUUID, storageDir)
			}
			if !entry.IsDir() {
				return fmt.Errorf("media storage path %q is not a directory", path.Join(storageDir, mediaUUID))
			}
			entryByUUID[mediaUUID] = entry
		}

		affectedUUIDs := make([]string, 0, len(affected))
		for _, mediaUUID := range affected {
			affectedUUIDs = append(affectedUUIDs, mediaUUID)
		}
		sort.Strings(affectedUUIDs)
		for _, mediaUUID := range affectedUUIDs {
			directoryPath := path.Join(storageDir, mediaUUID)
			expectedNames := make(map[string]ownedMediaFile)
			for relativePath, owned := range expected {
				if path.Dir(relativePath) == directoryPath {
					expectedNames[path.Base(relativePath)] = owned
				}
			}
			_, directoryExists := entryByUUID[mediaUUID]
			if !directoryExists {
				if _, expectedDirectory := expectedDirectories[directoryPath]; expectedDirectory || len(expectedNames) > 0 {
					return fmt.Errorf("owned media directory %q is missing", directoryPath)
				}
				continue
			}
			expectedDirectory, directoryOwned := expectedDirectories[directoryPath]
			if !directoryOwned {
				return fmt.Errorf("media storage contains unowned directory %q", directoryPath)
			}
			currentDirectory, err := uploadRoot.Lstat(directoryPath)
			if err != nil {
				return fmt.Errorf("inspect owned media directory %q: %w", directoryPath, err)
			}
			// A directory keeps inode identity alone: this import adds the files it
			// owns after recording the directory, so its size and modification time
			// move by design. A directory swapped out from under the import is
			// caught by the per-file identity checks of the entries below, which a
			// replacement cannot reproduce.
			if currentDirectory.Mode()&os.ModeSymlink != 0 || !currentDirectory.IsDir() ||
				!os.SameFile(currentDirectory, expectedDirectory.identity) {
				return fmt.Errorf("owned media directory %q was replaced", directoryPath)
			}
			directory, err := uploadRoot.Open(directoryPath)
			if err != nil {
				return fmt.Errorf("open media directory %q: %w", directoryPath, err)
			}
			files, readErr := directory.ReadDir(-1)
			closeErr := directory.Close()
			if err := errors.Join(readErr, closeErr); err != nil {
				return fmt.Errorf("inspect media directory %q: %w", directoryPath, err)
			}
			sort.Slice(files, func(left, right int) bool { return files[left].Name() < files[right].Name() })
			for _, file := range files {
				owned, exists := expectedNames[file.Name()]
				if !exists {
					return fmt.Errorf("media directory %q contains unowned entry %q", directoryPath, file.Name())
				}
				current, err := uploadRoot.Lstat(owned.relativePath)
				if err != nil {
					return fmt.Errorf("inspect owned media path %q: %w", owned.relativePath, err)
				}
				if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
					!imaging.SameRootFile(current, owned.identity) {
					return fmt.Errorf("owned media path %q was replaced", owned.relativePath)
				}
				delete(expectedNames, file.Name())
			}
			if len(expectedNames) > 0 {
				missing := make([]string, 0, len(expectedNames))
				for filename := range expectedNames {
					missing = append(missing, filename)
				}
				sort.Strings(missing)
				return fmt.Errorf("media directory %q is missing owned entry %q", directoryPath, missing[0])
			}
		}
	}
	return nil
}

func ensureFreshMediaStorage(uploadRoot *os.Root, mediaUUIDs []string) error {
	targets := make(map[string]string, len(mediaUUIDs))
	for _, mediaUUID := range mediaUUIDs {
		if !imaging.IsCanonicalMediaUUID(mediaUUID) {
			return fmt.Errorf("invalid media storage UUID %q", mediaUUID)
		}
		logicalUUID := strings.ToLower(mediaUUID)
		if previous, duplicate := targets[logicalUUID]; duplicate && previous != mediaUUID {
			return fmt.Errorf("duplicate logical media storage UUID %q as %q and %q", logicalUUID, previous, mediaUUID)
		}
		targets[logicalUUID] = mediaUUID
	}
	for _, storageDir := range model.MediaStorageDirs() {
		directory, err := uploadRoot.Open(storageDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("open media storage directory %q: %w", storageDir, err)
		}
		entries, readErr := directory.ReadDir(-1)
		closeErr := directory.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return fmt.Errorf("inspect media storage directory %q: %w", storageDir, err)
		}
		for _, entry := range entries {
			requested, collision := targets[strings.ToLower(entry.Name())]
			if collision {
				return fmt.Errorf("media storage already exists for UUID %q as %q in %q",
					requested, entry.Name(), storageDir)
			}
		}
	}
	return nil
}

func (i *Importer) unownedExtractedMedia(ctx context.Context, manifest mediaZipManifest) ([]string, error) {
	var unowned []string
	var ownerErrors []error
	for _, mediaUUID := range manifest.affectedUUIDs {
		declared := manifest.mediaByUUID[mediaUUID]
		existing, err := i.store.GetMediaByUUID(ctx, mediaUUID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			unowned = append(unowned, mediaUUID)
			ownerErrors = append(ownerErrors, fmt.Errorf("media %q has no destination row after import", mediaUUID))
		case err != nil:
			// Ownership is uncertain after an operational query failure. Keep those
			// files intact; only a positively absent or mismatched row is safe to
			// compensate after the database transaction has committed.
			ownerErrors = append(ownerErrors, fmt.Errorf("verify destination media %q: %w", mediaUUID, err))
		case err == nil:
			identityErr := i.validateImmutableMediaIdentity(ctx, existing, declared)
			if identityErr == nil {
				continue
			}
			var mismatch *mediaIdentityMismatchError
			if errors.As(identityErr, &mismatch) {
				unowned = append(unowned, mediaUUID)
				ownerErrors = append(ownerErrors,
					fmt.Errorf("media %q destination metadata does not match the archive: %w", mediaUUID, identityErr))
				continue
			}
			ownerErrors = append(ownerErrors,
				fmt.Errorf("verify destination media %q metadata: %w", mediaUUID, identityErr))
		}
	}
	return unowned, errors.Join(ownerErrors...)
}

// extractMediaFiles writes a preflighted media manifest through one verified
// root capability and records the descriptor identity of every published file.
func (i *Importer) extractMediaFiles(manifest mediaZipManifest, uploadRoot *os.Root) (*mediaFileLedger, error) {
	ledger := newMediaFileLedger()
	for _, entry := range manifest.entries {
		if err := reserveOwnedMediaDirectory(uploadRoot, ledger, entry.path); err != nil {
			return ledger, err
		}
		identity, _, err := i.extractMediaFile(entry.file, entry.path, uploadRoot)
		if err != nil {
			return ledger, err
		}
		if err := ledger.recordFile(entry.path.uuid, identity); err != nil {
			return ledger, err
		}
	}
	return ledger, nil
}

func reserveOwnedMediaDirectory(
	uploadRoot *os.Root,
	ledger *mediaFileLedger,
	parsedPath mediaZipPath,
) error {
	directoryPath := path.Join(parsedPath.mediaType, parsedPath.uuid)
	if ledger.hasDirectory(directoryPath) {
		return nil
	}
	if err := uploadRoot.Mkdir(parsedPath.mediaType, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create media storage directory %q: %w", parsedPath.mediaType, err)
	}
	storageInfo, err := uploadRoot.Lstat(parsedPath.mediaType)
	if err != nil {
		return fmt.Errorf("inspect media storage directory %q: %w", parsedPath.mediaType, err)
	}
	if !storageInfo.IsDir() || storageInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("media storage path %q is not a directory", parsedPath.mediaType)
	}
	if err := uploadRoot.Mkdir(directoryPath, 0o750); err != nil {
		return fmt.Errorf("reserve media directory %q: %w", directoryPath, err)
	}
	directoryInfo, err := uploadRoot.Lstat(directoryPath)
	if err != nil {
		return fmt.Errorf("inspect reserved media directory %q: %w", directoryPath, err)
	}
	return ledger.recordDirectory(parsedPath.uuid, &imaging.RootFileIdentity{
		Path: directoryPath,
		Info: directoryInfo,
	})
}

// generateMissingVariantsBeforeImport creates only standard variants absent
// from the archive, then appends their actual metadata to the transactional
// import payload. Declared archive variants are never overwritten.
func (i *Importer) generateMissingVariantsBeforeImport(
	ctx context.Context,
	uploadRoot *os.Root,
	ownedFiles *mediaFileLedger,
	manifest *mediaZipManifest,
	data *ExportData,
	opts ImportOptions,
) error {
	if !opts.ImportMedia {
		return nil
	}
	originals := make(map[string]string)
	presentVariants := make(map[string]map[string]struct{})
	for _, entry := range manifest.entries {
		if entry.path.mediaType == model.OriginalsDir {
			originals[entry.path.uuid] = entry.path.filename
			continue
		}
		if presentVariants[entry.path.uuid] == nil {
			presentVariants[entry.path.uuid] = make(map[string]struct{})
		}
		presentVariants[entry.path.uuid][entry.path.mediaType] = struct{}{}
	}
	contentTypeProcessor := i.getProcessor()
	variantTypes := make([]string, 0, len(model.ImageVariants))
	for variantType := range model.ImageVariants {
		variantTypes = append(variantTypes, variantType)
	}
	sort.Strings(variantTypes)

	for index := range data.Media {
		medium := &data.Media[index]
		filename, hasOriginal := originals[medium.UUID]
		if !hasOriginal || !contentTypeProcessor.IsImage(medium.MimeType) {
			continue
		}
		_, err := i.store.GetMediaByUUID(ctx, medium.UUID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// A new media row will own the generated files.
		case err != nil:
			return fmt.Errorf("check destination media %q before variant generation: %w", medium.UUID, err)
		case opts.ConflictStrategy != ConflictOverwrite:
			// Skip/Rename and file-only restores must leave immutable destination
			// metadata and its variant set untouched.
			continue
		}

		for _, variantType := range variantTypes {
			if _, extracted := presentVariants[medium.UUID][variantType]; extracted {
				continue
			}
			variant, creation, err := imaging.CreateVariantFromRoot(uploadRoot, medium.UUID, filename, variantType)
			if err != nil {
				return fmt.Errorf("generate %q variant for media %q: %w", variantType, medium.UUID, err)
			}
			if variant == nil {
				continue
			}
			if creation.Directory != nil {
				if err := ownedFiles.recordDirectory(medium.UUID, creation.Directory); err != nil {
					return fmt.Errorf("record generated %q directory for media %q: %w", variantType, medium.UUID, err)
				}
			}
			if err := ownedFiles.recordFile(medium.UUID, &creation.File); err != nil {
				return fmt.Errorf("record generated %q variant for media %q: %w", variantType, medium.UUID, err)
			}
			medium.Variants = append(medium.Variants, ExportVariant{
				Type: variant.Type, Width: int64(variant.Width), Height: int64(variant.Height), Size: variant.Size,
			})
		}
		manifest.mediaByUUID[medium.UUID] = *medium
	}
	return nil
}

func (i *Importer) validateExtractedImages(
	uploadRoot *os.Root,
	manifest mediaZipManifest,
) error {
	contentTypeProcessor := i.getProcessor()
	entries := append([]mediaZipEntry(nil), manifest.entries...)
	sort.Slice(entries, func(left, right int) bool {
		return canonicalMediaZipPath(entries[left].path) < canonicalMediaZipPath(entries[right].path)
	})
	for _, entry := range entries {
		mediaUUID := entry.path.uuid
		medium, exists := manifest.mediaByUUID[mediaUUID]
		if !exists || !contentTypeProcessor.IsImage(medium.MimeType) {
			continue
		}
		storedPath := path.Join(entry.path.mediaType, mediaUUID, entry.path.filename)
		storedFile, err := uploadRoot.Open(storedPath)
		if err != nil {
			return fmt.Errorf("open %s for media %q: %w", entry.path.mediaType, mediaUUID, err)
		}
		width, height, detectedMimeType, validateErr := imaging.ValidateImage(storedFile)
		closeErr := storedFile.Close()
		if err := errors.Join(validateErr, closeErr); err != nil {
			return fmt.Errorf("decode %s for media %q: %w", entry.path.mediaType, mediaUUID, err)
		}
		if detectedMimeType != medium.MimeType {
			return fmt.Errorf("media %q %s MIME type %q does not match declared MIME type %q",
				mediaUUID, entry.path.mediaType, detectedMimeType, medium.MimeType)
		}
		if entry.path.mediaType == model.OriginalsDir {
			if medium.Width != nil && int64(width) != *medium.Width {
				return fmt.Errorf("media %q original width %d does not match declared width %d",
					mediaUUID, width, *medium.Width)
			}
			if medium.Height != nil && int64(height) != *medium.Height {
				return fmt.Errorf("media %q original height %d does not match declared height %d",
					mediaUUID, height, *medium.Height)
			}
			continue
		}
		var declaredVariant *ExportVariant
		for variantIndex := range medium.Variants {
			if medium.Variants[variantIndex].Type == entry.path.mediaType {
				declaredVariant = &medium.Variants[variantIndex]
				break
			}
		}
		if declaredVariant == nil {
			return fmt.Errorf("media %q has no metadata for extracted variant %q", mediaUUID, entry.path.mediaType)
		}
		if int64(width) != declaredVariant.Width || int64(height) != declaredVariant.Height {
			return fmt.Errorf("media %q variant %q dimensions %dx%d do not match declared %dx%d",
				mediaUUID, entry.path.mediaType, width, height, declaredVariant.Width, declaredVariant.Height)
		}
	}
	return nil
}

// validateMediaExtractionDryRun runs the filesystem-dependent half of a media
// import against an isolated root. It catches corrupt image data, copy/close
// failures, and variant-generation errors without creating or changing the
// configured uploads directory.
func (i *Importer) validateMediaExtractionDryRun(
	ctx context.Context,
	manifest mediaZipManifest,
	data *ExportData,
	opts ImportOptions,
) (resultErr error) {
	tempDir, err := os.MkdirTemp("", "ocms-transfer-media-dry-run-")
	if err != nil {
		return fmt.Errorf("create isolated uploads root: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, os.RemoveAll(tempDir))
	}()

	uploadRoot, err := imaging.OpenUploadRoot(tempDir)
	if err != nil {
		return fmt.Errorf("open isolated uploads root: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, uploadRoot.Close())
	}()
	ownedFiles, err := i.extractMediaFiles(manifest, uploadRoot)
	if err != nil {
		return fmt.Errorf("extract media files: %w", err)
	}
	if err := i.validateExtractedImages(uploadRoot, manifest); err != nil {
		return fmt.Errorf("validate restored media images: %w", err)
	}

	// Variant generation mutates the archive payload with actual generated
	// metadata. Clone the media and variant slices so dry-run remains a pure
	// validation of its caller-owned ExportData.
	dryRunData := *data
	dryRunData.Media = append([]ExportMedia(nil), data.Media...)
	for index := range dryRunData.Media {
		dryRunData.Media[index].Variants = append([]ExportVariant(nil), data.Media[index].Variants...)
	}
	dryRunManifest := manifest
	dryRunManifest.mediaByUUID = make(map[string]ExportMedia, len(manifest.mediaByUUID))
	for mediaUUID, medium := range manifest.mediaByUUID {
		dryRunManifest.mediaByUUID[mediaUUID] = medium
	}
	if err := i.generateMissingVariantsBeforeImport(ctx, uploadRoot, ownedFiles, &dryRunManifest, &dryRunData, opts); err != nil {
		return fmt.Errorf("generate missing media variants: %w", err)
	}
	if err := validateOwnedMediaStorage(uploadRoot, ownedFiles, manifest.affectedUUIDs); err != nil {
		return fmt.Errorf("validate imported media storage: %w", err)
	}
	return nil
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

// extractMediaFile extracts a single preflighted media file through uploadRoot.
// The identity comes from the O_EXCL-created descriptor, allowing later
// compensation to preserve a same-path replacement.
func (i *Importer) extractMediaFile(
	f *zip.File,
	parsedPath mediaZipPath,
	uploadRoot *os.Root,
) (*imaging.RootFileIdentity, int64, error) {
	destDir := path.Join(parsedPath.mediaType, parsedPath.uuid)
	destPath := path.Join(destDir, parsedPath.filename)
	if !fs.ValidPath(destDir) || !fs.ValidPath(destPath) {
		return nil, 0, fmt.Errorf("invalid root-relative media path %q", destPath)
	}

	rc, err := f.Open()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open zip entry: %w", err)
	}
	destFile, err := uploadRoot.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return nil, 0, errors.Join(fmt.Errorf("failed to create destination file: %w", err), rc.Close())
	}
	createdInfo, identityErr := destFile.Stat()
	if identityErr != nil {
		return nil, 0, errors.Join(
			fmt.Errorf("failed to inspect destination file: %w", identityErr),
			destFile.Close(),
			rc.Close(),
		)
	}

	written, copyErr := copyWithLimit(destFile, rc, maxZipMediaFileUncompressedBytes)
	// The identity handed to the ledger is taken after the bytes land, through
	// the same descriptor: it has to describe the extracted file rather than
	// the empty one, so imaging.SameRootFile can weigh size and modification
	// time against a same-path replacement that inherited the inode number.
	publishedInfo, publishedErr := destFile.Stat()
	closeDestErr := destFile.Close()
	closeSourceErr := rc.Close()
	if err := errors.Join(copyErr, publishedErr, closeDestErr, closeSourceErr); err != nil {
		partialLedger := newMediaFileLedger()
		recordErr := partialLedger.recordFile(parsedPath.uuid,
			&imaging.RootFileIdentity{Path: destPath, Info: createdInfo})
		cleanupErr := cleanupPartialMediaFile(uploadRoot, partialLedger)
		return nil, 0, errors.Join(fmt.Errorf("failed to copy media file: %w", err), recordErr, cleanupErr)
	}

	return &imaging.RootFileIdentity{Path: destPath, Info: publishedInfo}, written, nil
}

func parseMediaZipPath(zipPath string) (mediaZipPath, error) {
	cleanPath := path.Clean(zipPath)
	if cleanPath != zipPath {
		return mediaZipPath{}, fmt.Errorf("invalid media path %q: path normalization mismatch", zipPath)
	}

	parts := strings.Split(cleanPath, "/")
	if len(parts) != 4 || parts[0] != "media" {
		return mediaZipPath{}, fmt.Errorf("invalid media path %q: expected media/{type}/{uuid}/{filename}", zipPath)
	}

	if err := validateZipPathSegment(parts[1]); err != nil {
		return mediaZipPath{}, fmt.Errorf("invalid media type in path %q: %w", zipPath, err)
	}
	allowedStorageDir := false
	for _, storageDir := range model.MediaStorageDirs() {
		if parts[1] == storageDir {
			allowedStorageDir = true
			break
		}
	}
	if !allowedStorageDir {
		return mediaZipPath{}, fmt.Errorf("invalid media type in path %q: unsupported storage directory %q", zipPath, parts[1])
	}
	if err := validateZipPathSegment(parts[2]); err != nil {
		return mediaZipPath{}, fmt.Errorf("invalid media uuid in path %q: %w", zipPath, err)
	}
	if !imaging.IsCanonicalMediaUUID(parts[2]) {
		return mediaZipPath{}, fmt.Errorf("invalid media uuid in path %q: %q is not a canonical UUID", zipPath, parts[2])
	}
	if err := validateZipPathSegment(parts[3]); err != nil {
		return mediaZipPath{}, fmt.Errorf("invalid filename in path %q: %w", zipPath, err)
	}

	return mediaZipPath{
		mediaType: parts[1],
		uuid:      parts[2],
		filename:  parts[3],
	}, nil
}

func validateZipPathSegment(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("segment is empty")
	case value == "." || value == "..":
		return fmt.Errorf("segment contains traversal")
	case strings.Contains(value, "/") || strings.Contains(value, "\\"):
		return fmt.Errorf("segment contains path separators")
	case path.Clean(value) != value:
		return fmt.Errorf("segment is not normalized")
	default:
		return nil
	}
}

func ensurePathWithinBase(basePath, targetPath string) error {
	baseClean := filepath.Clean(basePath)
	targetClean := filepath.Clean(targetPath)

	rel, err := filepath.Rel(baseClean, targetClean)
	if err != nil {
		return fmt.Errorf("cannot validate path %q: %w", targetPath, err)
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes upload directory", targetPath)
	}

	return nil
}

func copyWithLimit(dst io.Writer, src io.Reader, maxBytes int64) (int64, error) {
	if maxBytes <= 0 {
		return 0, fmt.Errorf("copy limit must be positive")
	}

	limited := &io.LimitedReader{R: src, N: maxBytes + 1}
	written, err := io.Copy(dst, limited)
	if err != nil {
		return written, err
	}

	if written > maxBytes {
		return written, fmt.Errorf("content exceeds max size (%d bytes)", maxBytes)
	}

	return written, nil
}

// ValidateZipFile validates a zip import file and returns information about its contents.
func (i *Importer) ValidateZipFile(ctx context.Context, filePath string) (*ValidationResult, error) {
	// #nosec G304 -- this file-oriented API intentionally opens the archive path
	// explicitly authorized by its caller.
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open zip file: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect zip file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("failed to open zip file: %q is not a regular file", filePath)
	}
	if err := validateZipContainer(file, info.Size()); err != nil {
		return nil, err
	}
	zipReader, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, fmt.Errorf("failed to open zip file: %w", err)
	}
	return i.ValidateZip(ctx, zipReader)
}

// ValidateZip validates a zip archive and returns information about its contents.
func (i *Importer) ValidateZip(ctx context.Context, zipReader *zip.Reader) (*ValidationResult, error) {
	if err := validateZipStructure(zipReader); err != nil {
		return &ValidationResult{
			Valid:  false,
			Errors: []ImportError{{Entity: "zip", ID: "", Message: err.Error()}},
		}, nil
	}
	var data ExportData
	exportFound := false
	for _, f := range zipReader.File {
		if f.Name == "export.json" {
			if exportFound {
				return &ValidationResult{
					Valid:  false,
					Errors: []ImportError{{Entity: "zip", ID: "", Message: "duplicate export.json entry"}},
				}, nil
			}
			var err error
			data, err = readZipExportData(f)
			if err != nil {
				return &ValidationResult{
					Valid:  false,
					Errors: []ImportError{{Entity: "json", ID: "", Message: err.Error()}},
				}, nil
			}
			exportFound = true
		}
	}
	if !exportFound {
		return &ValidationResult{
			Valid:  false,
			Errors: []ImportError{{Entity: "zip", ID: "", Message: "export.json not found in zip archive"}},
		}, nil
	}

	manifest, err := buildMediaZipManifest(zipReader, &data)
	if err != nil {
		return &ValidationResult{
			Valid:   false,
			Version: data.Version,
			Errors:  []ImportError{{Entity: "zip", ID: "", Message: err.Error()}},
		}, nil
	}
	result, err := i.ValidateData(ctx, &data)
	if err != nil {
		return nil, err
	}
	result.Entities["media_files"] = len(manifest.entries)
	return result, nil
}

// ValidateZipBytes validates zip data and returns information about its contents.
func (i *Importer) ValidateZipBytes(ctx context.Context, data []byte) (*ValidationResult, error) {
	reader := bytes.NewReader(data)
	if err := validateZipContainer(reader, int64(len(data))); err != nil {
		result := &ValidationResult{Valid: false, Entities: make(map[string]int)}
		result.Errors = append(result.Errors, ImportError{Entity: "zip", Message: err.Error()})
		return result, nil
	}
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
	return i.validate(data, true, true)
}

// validate keeps archive-language state option-aware. A content-only restore
// must not be rejected because an unused legacy language list has zero or
// multiple defaults; those rows are neither written nor used when
// ImportLanguages is false. Entity language references are still checked
// against the destination by preflightLanguageContract.
func (i *Importer) validate(data *ExportData, importLanguages, importMedia bool) []ImportError {
	var errs []ImportError

	// Check version
	if data.Version == "" {
		errs = append(errs, ImportError{
			Entity:  "export",
			ID:      "",
			Message: "missing version field",
		})
	}

	if importLanguages {
		errs = validateEntities(errs, data.Languages, "language", "code",
			func(l ExportLanguage) string { return l.Code }, "name",
			func(l ExportLanguage) string { return l.Name })
		activeDefaults := 0
		for idx, lang := range data.Languages {
			if lang.IsDefault && !lang.IsActive {
				errs = append(errs, ImportError{
					Entity:  "language",
					ID:      strconv.Itoa(idx),
					Message: fmt.Sprintf("default language %q must be active", lang.Code),
				})
			}
			if lang.IsDefault && lang.IsActive {
				activeDefaults++
			}
			// Inactive legacy rows remain importable so administrators can rename or
			// delete them after restore. Only active rows participate in public URL
			// routing, so those must satisfy the same shared policy as the admin form.
			if !lang.IsActive || lang.Code == "" {
				continue
			}
			switch {
			case !util.IsValidLangCode(lang.Code):
				errs = append(errs, ImportError{
					Entity:  "language",
					ID:      strconv.Itoa(idx),
					Message: fmt.Sprintf("active language code %q has an invalid format", lang.Code),
				})
			case util.IsReservedLanguageCode(lang.Code):
				errs = append(errs, ImportError{
					Entity:  "language",
					ID:      strconv.Itoa(idx),
					Message: fmt.Sprintf("active language code %q uses a reserved route prefix", lang.Code),
				})
			}
		}
		if len(data.Languages) > 0 && activeDefaults != 1 {
			errs = append(errs, ImportError{
				Entity:  "language",
				Message: fmt.Sprintf("language archive must contain exactly one active default; found %d", activeDefaults),
			})
		}
	}

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

	if importMedia {
		errs = append(errs, validateTransferMediaArchive(data.Media)...)
	}

	errs = validateEntities(errs, data.Menus, "menu", "slug",
		func(m ExportMenu) string { return m.Slug }, "name",
		func(m ExportMenu) string { return m.Name })

	errs = validateEntities(errs, data.Forms, "form", "slug",
		func(f ExportForm) string { return f.Slug }, "name",
		func(f ExportForm) string { return f.Name })

	return errs
}

func validateTransferMediaArchive(media []ExportMedia) []ImportError {
	errs := validateEntities(nil, media, "media", "UUID",
		func(m ExportMedia) string { return m.UUID }, "filename",
		func(m ExportMedia) string { return m.Filename })
	seenMediaUUIDs := make(map[string]string, len(media))
	for _, medium := range media {
		for _, message := range validateTransferMediaMetadata(medium) {
			errs = append(errs, ImportError{Entity: "media", ID: medium.UUID, Message: message})
		}
		if medium.UUID == "" || !imaging.IsCanonicalMediaUUID(medium.UUID) {
			continue
		}
		folded := strings.ToLower(medium.UUID)
		if previous, duplicate := seenMediaUUIDs[folded]; duplicate {
			errs = append(errs, ImportError{
				Entity:  "media",
				ID:      medium.UUID,
				Message: fmt.Sprintf("duplicate archive media identity: media UUID %q conflicts with %q", medium.UUID, previous),
			})
			continue
		}
		seenMediaUUIDs[folded] = medium.UUID
	}
	return errs
}

func validateTransferMediaMetadata(medium ExportMedia) []string {
	messages := make([]string, 0)
	if medium.UUID != "" && !imaging.IsCanonicalMediaUUID(medium.UUID) {
		messages = append(messages, fmt.Sprintf("media UUID %q is not a canonical UUID", medium.UUID))
	}
	if err := validateMediaFilenameSegment(medium.Filename); err != nil {
		messages = append(messages, err.Error())
	}
	if isProcessableImageMimeType(medium.MimeType) {
		filenameMimeType, ok := processableImageMimeTypeForFilename(medium.Filename)
		if !ok || filenameMimeType != medium.MimeType {
			messages = append(messages, fmt.Sprintf(
				"media filename %q does not match image MIME type %q", medium.Filename, medium.MimeType,
			))
		}
	}
	if medium.Size < 0 {
		messages = append(messages, "media size must not be negative")
	}
	if medium.Width != nil && *medium.Width <= 0 {
		messages = append(messages, "media width must be positive when present")
	}
	if medium.Height != nil && *medium.Height <= 0 {
		messages = append(messages, "media height must be positive when present")
	}
	if err := validateMediaFolderPath(medium.FolderPath); err != nil {
		messages = append(messages, fmt.Sprintf("invalid media folder path %q: %v", medium.FolderPath, err))
	}
	seenVariantTypes := make(map[string]struct{}, len(medium.Variants))
	for _, variant := range medium.Variants {
		if _, supported := model.ImageVariants[variant.Type]; !supported {
			messages = append(messages, fmt.Sprintf("unsupported media variant type %q", variant.Type))
		}
		if _, duplicate := seenVariantTypes[variant.Type]; duplicate {
			messages = append(messages, fmt.Sprintf("duplicate media variant type %q", variant.Type))
		}
		seenVariantTypes[variant.Type] = struct{}{}
		if variant.Size < 0 {
			messages = append(messages, fmt.Sprintf("media variant %q size must not be negative", variant.Type))
		}
		if variant.Width <= 0 {
			messages = append(messages, fmt.Sprintf("media variant %q width must be positive", variant.Type))
		}
		if variant.Height <= 0 {
			messages = append(messages, fmt.Sprintf("media variant %q height must be positive", variant.Type))
		}
	}
	return messages
}

func validateMediaFilenameSegment(filename string) error {
	if filename == "" {
		return errors.New("media filename is required")
	}
	if filename == "." || filename == ".." || strings.ContainsAny(filename, `/\`) ||
		strings.ContainsRune(filename, '\x00') || !fs.ValidPath(filename) {
		return fmt.Errorf("media filename %q must be a safe path segment", filename)
	}
	return nil
}

func validateMediaFolderPath(folderPath string) error {
	if folderPath == "" {
		return nil
	}
	if path.Clean(folderPath) != folderPath || strings.HasPrefix(folderPath, "/") || strings.HasSuffix(folderPath, "/") {
		return errors.New("folder path is not normalized")
	}
	for _, segment := range strings.Split(folderPath, "/") {
		if err := validateZipPathSegment(segment); err != nil {
			return err
		}
	}
	return nil
}

func importedLanguageCode(code, defaultCode string) string {
	if code != "" {
		return code
	}
	return defaultCode
}

func isValidConflictStrategy(strategy ConflictStrategy) bool {
	return strategy == ConflictSkip || strategy == ConflictOverwrite || strategy == ConflictRename
}

type importLanguagePlan struct {
	defaultCode string
	known       map[string]struct{}
}

func validateRoutableDefaultLanguage(language store.Language) error {
	if !util.IsValidLangCode(language.Code) {
		return fmt.Errorf("destination default language %q has an invalid route code", language.Code)
	}
	if util.IsReservedLanguageCode(language.Code) {
		return fmt.Errorf("destination default language %q uses a reserved route prefix", language.Code)
	}
	return nil
}

// destinationDefaultLanguage enforces the routing invariant that a database
// has one and only one configured default, and that it is active. The store's
// GetDefaultLanguage query returns an arbitrary row when corrupt legacy data
// contains multiple defaults, so it cannot enforce this contract by itself.
func destinationDefaultLanguage(ctx context.Context, queries *store.Queries) (store.Language, error) {
	languages, err := queries.ListLanguages(ctx)
	if err != nil {
		return store.Language{}, err
	}
	var defaults []store.Language
	for _, language := range languages {
		if language.IsDefault {
			defaults = append(defaults, language)
		}
	}
	if len(defaults) != 1 {
		return store.Language{}, fmt.Errorf("destination must contain exactly one default language; found %d", len(defaults))
	}
	if !defaults[0].IsActive {
		return store.Language{}, fmt.Errorf("destination default language %q is inactive", defaults[0].Code)
	}
	if err := validateRoutableDefaultLanguage(defaults[0]); err != nil {
		return store.Language{}, err
	}
	return defaults[0], nil
}

// simulatedLanguagePlan applies language-import conflict semantics in memory.
// It lets preview and dry-run reject states that the transactional import would
// reject after attempting to reconcile the archive default.
func (i *Importer) simulatedLanguagePlan(ctx context.Context, data *ExportData, opts ImportOptions) (importLanguagePlan, error) {
	languages, err := i.store.ListLanguages(ctx)
	if err != nil {
		return importLanguagePlan{}, fmt.Errorf("list destination languages: %w", err)
	}
	byCode := make(map[string]store.Language, len(languages)+len(data.Languages))
	for _, language := range languages {
		byCode[language.Code] = language
	}

	if opts.ImportLanguages && len(data.Languages) > 0 {
		archiveDefault := ""
		for _, exported := range data.Languages {
			if exported.IsDefault {
				archiveDefault = exported.Code
			}
			existing, exists := byCode[exported.Code]
			if exists && opts.ConflictStrategy != ConflictOverwrite {
				continue
			}
			existing.Code = exported.Code
			existing.Name = exported.Name
			existing.NativeName = exported.NativeName
			existing.IsActive = exported.IsActive
			existing.Direction = exported.Direction
			existing.Position = exported.Position
			existing.IsDefault = false
			byCode[exported.Code] = existing
		}
		selected, ok := byCode[archiveDefault]
		if !ok {
			return importLanguagePlan{}, fmt.Errorf("archive default language %q is absent from the destination plan", archiveDefault)
		}
		if !selected.IsActive {
			return importLanguagePlan{}, fmt.Errorf("archive default language %q is inactive in the destination", archiveDefault)
		}
		for code, language := range byCode {
			language.IsDefault = code == archiveDefault
			byCode[code] = language
		}
	}

	defaults := make([]store.Language, 0, 1)
	known := make(map[string]struct{}, len(byCode))
	for code, language := range byCode {
		if language.IsActive && util.IsRoutableLanguageCode(code) {
			known[code] = struct{}{}
		}
		if language.IsDefault {
			defaults = append(defaults, language)
		}
	}
	if len(defaults) != 1 {
		return importLanguagePlan{}, fmt.Errorf("destination must contain exactly one default language; found %d", len(defaults))
	}
	if !defaults[0].IsActive {
		return importLanguagePlan{}, fmt.Errorf("destination default language %q is inactive", defaults[0].Code)
	}
	if err := validateRoutableDefaultLanguage(defaults[0]); err != nil {
		return importLanguagePlan{}, err
	}
	return importLanguagePlan{defaultCode: defaults[0].Code, known: known}, nil
}

func (i *Importer) preflightLanguageContract(
	ctx context.Context,
	data *ExportData,
	opts ImportOptions,
) (importLanguagePlan, []ImportError, error) {
	plan, err := i.simulatedLanguagePlan(ctx, data, opts)
	if err != nil {
		return importLanguagePlan{}, nil, err
	}
	errs := validateLanguageReferencesAgainst(plan.known, data, opts, plan.defaultCode)
	errs = append(errs, validateTranslationGraph(data, opts, plan.defaultCode, plan.known)...)
	if opts.ConflictStrategy == ConflictSkip {
		existingLanguageErrors, languageErr := i.validateExistingSlugLanguages(ctx, data, opts, plan.defaultCode)
		if languageErr != nil {
			return importLanguagePlan{}, nil, languageErr
		}
		errs = append(errs, existingLanguageErrors...)
		// A cross-language Skip mapping is already invalid and cannot be used
		// to model a destination translation component safely.
		if len(existingLanguageErrors) == 0 {
			mergeErrors, mergeErr := i.validateExistingTranslationComponentMerges(ctx, data, opts, plan.defaultCode)
			if mergeErr != nil {
				return importLanguagePlan{}, nil, mergeErr
			}
			errs = append(errs, mergeErrors...)
		}
	}
	errs = append(errs, validateArchiveSlugOwnership(data, opts, plan.defaultCode)...)
	errs = append(errs, validateSlugsAgainstLanguagePrefixes(data, opts, plan.known)...)
	relationErrors, err := i.preflightRelations(ctx, data, opts)
	if err != nil {
		return importLanguagePlan{}, nil, err
	}
	errs = append(errs, relationErrors...)

	return plan, errs, nil
}

// validateExistingSlugLanguages prevents ConflictSkip from mapping a selected
// archive entity onto a global-slug destination entity in another language.
// ConflictOverwrite deliberately changes the stored language and replaces its
// related translation graph, so it must not use this guard.
func (i *Importer) validateExistingSlugLanguages(
	ctx context.Context,
	data *ExportData,
	opts ImportOptions,
	defaultLangCode string,
) ([]ImportError, error) {
	var errs []ImportError
	checkLanguage := func(entity, id, wanted, actual string) {
		if wanted != actual {
			errs = append(errs, ImportError{
				Entity: entity, ID: id,
				Message: fmt.Sprintf("existing %s belongs to language %q, not %q", entity, actual, wanted),
			})
		}
	}
	if opts.ImportCategories {
		for _, category := range data.Categories {
			existing, err := i.store.GetCategoryBySlug(ctx, category.Slug)
			if err == nil {
				checkLanguage("category", category.Slug,
					importedLanguageCode(category.LanguageCode, defaultLangCode), existing.LanguageCode)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("check category %q language: %w", category.Slug, err)
			}
		}
	}
	if opts.ImportTags {
		for _, tag := range data.Tags {
			existing, err := i.store.GetTagBySlug(ctx, tag.Slug)
			if err == nil {
				checkLanguage("tag", tag.Slug,
					importedLanguageCode(tag.LanguageCode, defaultLangCode), existing.LanguageCode)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("check tag %q language: %w", tag.Slug, err)
			}
		}
	}
	if opts.ImportPages {
		for _, page := range data.Pages {
			existing, err := i.store.GetPageBySlug(ctx, page.Slug)
			if err == nil {
				checkLanguage("page", page.Slug,
					importedLanguageCode(page.LanguageCode, defaultLangCode), existing.LanguageCode)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("check page %q language: %w", page.Slug, err)
			}
		}
	}
	return errs, nil
}

// preflightRelations validates every slug-based reference before dry-run
// counting or transaction writes. The archive format uses slugs for category
// parents, page taxonomy associations, and page-backed menu items; accepting a
// missing reference in preview and discovering it only after writes made the
// three execution modes disagree and could commit partial menus.
func (i *Importer) preflightRelations(ctx context.Context, data *ExportData, opts ImportOptions) ([]ImportError, error) {
	selectedCategories := make(map[string]struct{})
	selectedTags := make(map[string]struct{})
	selectedPages := make(map[string]struct{})
	if opts.ImportCategories {
		for _, category := range data.Categories {
			selectedCategories[category.Slug] = struct{}{}
		}
	}
	if opts.ImportTags {
		for _, tag := range data.Tags {
			selectedTags[tag.Slug] = struct{}{}
		}
	}
	if opts.ImportPages {
		for _, page := range data.Pages {
			selectedPages[page.Slug] = struct{}{}
		}
	}

	categoryExists := make(map[string]bool)
	tagExists := make(map[string]bool)
	pageExists := make(map[string]bool)
	lookupCategory := func(slug string) (bool, error) {
		if exists, cached := categoryExists[slug]; cached {
			return exists, nil
		}
		_, err := i.store.GetCategoryBySlug(ctx, slug)
		exists, err := lookupExists(err)
		if err == nil {
			categoryExists[slug] = exists
		}
		return exists, err
	}
	lookupTag := func(slug string) (bool, error) {
		if exists, cached := tagExists[slug]; cached {
			return exists, nil
		}
		_, err := i.store.GetTagBySlug(ctx, slug)
		exists, err := lookupExists(err)
		if err == nil {
			tagExists[slug] = exists
		}
		return exists, err
	}
	lookupPage := func(slug string) (bool, error) {
		if exists, cached := pageExists[slug]; cached {
			return exists, nil
		}
		_, err := i.store.GetPageBySlug(ctx, slug)
		exists, err := lookupExists(err)
		if err == nil {
			pageExists[slug] = exists
		}
		return exists, err
	}

	var errs []ImportError
	missing := func(entity, id, relation, slug string) {
		errs = append(errs, ImportError{
			Entity: entity, ID: id,
			Message: fmt.Sprintf("referenced %s %q is neither selected for import nor present in the destination", relation, slug),
		})
	}
	if opts.ImportCategories {
		for _, category := range data.Categories {
			if category.ParentSlug == "" {
				continue
			}
			if _, selected := selectedCategories[category.ParentSlug]; selected {
				continue
			}
			exists, err := lookupCategory(category.ParentSlug)
			if err != nil {
				return nil, fmt.Errorf("check category parent %q: %w", category.ParentSlug, err)
			}
			if !exists {
				missing("category", category.Slug, "parent category", category.ParentSlug)
			}
		}
	}
	if opts.ImportPages {
		for _, page := range data.Pages {
			for _, slug := range page.Categories {
				if _, selected := selectedCategories[slug]; selected {
					continue
				}
				exists, err := lookupCategory(slug)
				if err != nil {
					return nil, fmt.Errorf("check page %q category %q: %w", page.Slug, slug, err)
				}
				if !exists {
					missing("page", page.Slug, "category", slug)
				}
			}
			for _, slug := range page.Tags {
				if _, selected := selectedTags[slug]; selected {
					continue
				}
				exists, err := lookupTag(slug)
				if err != nil {
					return nil, fmt.Errorf("check page %q tag %q: %w", page.Slug, slug, err)
				}
				if !exists {
					missing("page", page.Slug, "tag", slug)
				}
			}
		}
	}
	if opts.ImportMenus {
		var validateItems func(menuSlug string, items []ExportMenuItem) error
		validateItems = func(menuSlug string, items []ExportMenuItem) error {
			for _, item := range items {
				if item.PageSlug != "" {
					if _, selected := selectedPages[item.PageSlug]; !selected {
						exists, err := lookupPage(item.PageSlug)
						if err != nil {
							return fmt.Errorf("check menu %q page %q: %w", menuSlug, item.PageSlug, err)
						}
						if !exists {
							missing("menu", menuSlug, "page", item.PageSlug)
						}
					}
				}
				if err := validateItems(menuSlug, item.Children); err != nil {
					return err
				}
			}
			return nil
		}
		for _, menu := range data.Menus {
			if err := validateItems(menu.Slug, menu.Items); err != nil {
				return nil, err
			}
		}
	}
	return errs, nil
}

// previewLanguageContract accepts an archive when at least one selectable
// conflict strategy can satisfy the destination contract. The preview page is
// where the operator chooses that strategy, so validating it with hard-coded
// ConflictSkip would hide the form for imports that Rename or Overwrite can
// safely resolve.
func (i *Importer) previewLanguageContract(
	ctx context.Context,
	data *ExportData,
	opts ImportOptions,
) (importLanguagePlan, []ImportError, error) {
	var fallbackPlan importLanguagePlan
	var fallbackErrors []ImportError
	var fallbackErr error
	// Preview precedes the options form. Accept it when any selectable
	// conflict strategy and language-import choice is valid; the submitted
	// Import call then enforces the exact chosen combination. This keeps an
	// irrelevant legacy language list from hiding the form needed to uncheck
	// "Import languages".
	for _, importLanguages := range []bool{opts.ImportLanguages, false} {
		for _, strategy := range []ConflictStrategy{ConflictRename, ConflictOverwrite, ConflictSkip} {
			candidate := opts
			candidate.ImportLanguages = importLanguages
			candidate.ConflictStrategy = strategy
			plan, contractErrors, err := i.preflightLanguageContract(ctx, data, candidate)
			if err == nil && len(contractErrors) == 0 {
				return plan, nil, nil
			}
			if fallbackErr == nil && len(fallbackErrors) == 0 {
				fallbackPlan, fallbackErrors, fallbackErr = plan, contractErrors, err
			}
		}
	}
	return fallbackPlan, fallbackErrors, fallbackErr
}

// validateSlugsAgainstLanguagePrefixes refuses an archive whose page routes
// the destination's languages would swallow.
//
// known holds the codes that route once this import settles, archive languages
// included, so an archive carrying both a language "eng" and a page "eng"
// contradicts itself and is caught here rather than importing a page no URL
// reaches. This runs in preflight, before anything is written, so the report
// names every offending slug at once instead of failing on the first.
func validateSlugsAgainstLanguagePrefixes(
	data *ExportData, opts ImportOptions, known map[string]struct{},
) []ImportError {
	if !opts.ImportPages || len(known) == 0 {
		return nil
	}
	var errs []ImportError
	for _, page := range data.Pages {
		if _, shadowed := known[page.Slug]; !shadowed {
			continue
		}
		errs = append(errs, ImportError{
			Entity: "page",
			ID:     page.Slug,
			Message: fmt.Sprintf("page slug %q is the URL prefix of active language %q; "+
				"the imported page would be unreachable", page.Slug, page.Slug),
		})
	}
	return errs
}

func validateArchiveSlugOwnership(data *ExportData, opts ImportOptions, defaultCode string) []ImportError {
	var errs []ImportError
	check := func(entity string, values map[string]int64, ids map[int64]string, slug string, id int64) {
		if previousSlug, exists := ids[id]; exists {
			errs = append(errs, ImportError{
				Entity: entity,
				ID:     strconv.FormatInt(id, 10),
				Message: fmt.Sprintf("duplicate archive %s ID %d belongs to slugs %q and %q",
					entity, id, previousSlug, slug),
			})
		} else {
			ids[id] = slug
		}
		if previousID, exists := values[slug]; exists {
			errs = append(errs, ImportError{
				Entity: entity,
				ID:     slug,
				Message: fmt.Sprintf("duplicate archive %s slug %q belongs to source IDs %d and %d",
					entity, slug, previousID, id),
			})
			return
		}
		values[slug] = id
	}
	if opts.ImportCategories {
		seen := make(map[string]int64, len(data.Categories))
		ids := make(map[int64]string, len(data.Categories))
		for _, entity := range data.Categories {
			check("category", seen, ids, entity.Slug, entity.ID)
		}
	}
	if opts.ImportTags {
		seen := make(map[string]int64, len(data.Tags))
		ids := make(map[int64]string, len(data.Tags))
		for _, entity := range data.Tags {
			check("tag", seen, ids, entity.Slug, entity.ID)
		}
	}
	if opts.ImportPages {
		seen := make(map[string]int64, len(data.Pages))
		ids := make(map[int64]string, len(data.Pages))
		for _, entity := range data.Pages {
			check("page", seen, ids, entity.Slug, entity.ID)
		}
	}
	if opts.ImportForms {
		seen := make(map[string]int64, len(data.Forms))
		ids := make(map[int64]string, len(data.Forms))
		for _, entity := range data.Forms {
			identity := importedLanguageCode(entity.LanguageCode, defaultCode) + "/" + entity.Slug
			check("form", seen, ids, identity, entity.ID)
		}
	}
	checkUnique := func(entity string, values []string) {
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			if _, exists := seen[value]; exists {
				errs = append(errs, ImportError{
					Entity:  entity,
					ID:      value,
					Message: fmt.Sprintf("duplicate archive %s identity %q", entity, value),
				})
				continue
			}
			seen[value] = struct{}{}
		}
	}
	if opts.ImportLanguages {
		values := make([]string, 0, len(data.Languages))
		for _, entity := range data.Languages {
			values = append(values, entity.Code)
		}
		checkUnique("language", values)
	}
	if opts.ImportUsers {
		values := make([]string, 0, len(data.Users))
		for _, entity := range data.Users {
			values = append(values, entity.Email)
		}
		checkUnique("user", values)
	}
	if opts.ImportMedia {
		values := make([]string, 0, len(data.Media))
		for _, entity := range data.Media {
			values = append(values, entity.UUID)
		}
		checkUnique("media", values)
	}
	if opts.ImportMenus {
		values := make([]string, 0, len(data.Menus))
		ids := make(map[int64]string, len(data.Menus))
		for _, entity := range data.Menus {
			identity := importedLanguageCode(entity.LanguageCode, defaultCode) + "/" + entity.Slug
			values = append(values, identity)
			if previous, exists := ids[entity.ID]; exists {
				errs = append(errs, ImportError{
					Entity: "menu", ID: strconv.FormatInt(entity.ID, 10),
					Message: fmt.Sprintf("duplicate archive menu ID %d belongs to identities %q and %q",
						entity.ID, previous, identity),
				})
			} else {
				ids[entity.ID] = identity
			}
		}
		checkUnique("menu", values)
	}
	return errs
}

func validateLanguageReferencesAgainst(
	known map[string]struct{},
	data *ExportData,
	opts ImportOptions,
	defaultCode string,
) []ImportError {
	var errs []ImportError
	check := func(entity, id, code string) {
		code = importedLanguageCode(code, defaultCode)
		if _, ok := known[code]; ok {
			return
		}
		errs = append(errs, ImportError{Entity: entity, ID: id, Message: fmt.Sprintf("unknown language %q", code)})
	}
	if opts.ImportCategories {
		for _, entity := range data.Categories {
			check("category", entity.Slug, entity.LanguageCode)
		}
	}
	if opts.ImportTags {
		for _, entity := range data.Tags {
			check("tag", entity.Slug, entity.LanguageCode)
		}
	}
	if opts.ImportMedia {
		for _, entity := range data.Media {
			check("media", entity.UUID, entity.LanguageCode)
		}
	}
	if opts.ImportPages {
		for _, entity := range data.Pages {
			check("page", entity.Slug, entity.LanguageCode)
		}
	}
	if opts.ImportMenus {
		for _, entity := range data.Menus {
			check("menu", entity.Slug, entity.LanguageCode)
		}
	}
	if opts.ImportForms {
		for _, entity := range data.Forms {
			check("form", entity.Slug, entity.LanguageCode)
		}
	}
	return errs
}

func (i *Importer) validateLanguageReferences(
	ctx context.Context,
	queries *store.Queries,
	data *ExportData,
	opts ImportOptions,
	defaultCode string,
) []ImportError {
	languages, err := queries.ListLanguages(ctx)
	if err != nil {
		return []ImportError{{Entity: "language", Message: fmt.Sprintf("could not list destination languages: %v", err)}}
	}
	known := make(map[string]struct{}, len(languages))
	for _, language := range languages {
		if language.IsActive && util.IsValidLangCode(language.Code) &&
			!util.IsReservedLanguageCode(language.Code) {
			known[language.Code] = struct{}{}
		}
	}

	var errs []ImportError
	check := func(entity, id, code string) {
		code = importedLanguageCode(code, defaultCode)
		if _, ok := known[code]; ok {
			return
		}
		errs = append(errs, ImportError{
			Entity:  entity,
			ID:      id,
			Message: fmt.Sprintf("unknown language %q", code),
		})
	}

	if opts.ImportCategories {
		for _, category := range data.Categories {
			check("category", category.Slug, category.LanguageCode)
		}
	}
	if opts.ImportTags {
		for _, tag := range data.Tags {
			check("tag", tag.Slug, tag.LanguageCode)
		}
	}
	if opts.ImportMedia {
		for _, medium := range data.Media {
			check("media", medium.UUID, medium.LanguageCode)
		}
	}
	if opts.ImportPages {
		for _, page := range data.Pages {
			check("page", page.Slug, page.LanguageCode)
		}
	}
	if opts.ImportMenus {
		for _, menu := range data.Menus {
			check("menu", menu.Slug, menu.LanguageCode)
		}
	}
	if opts.ImportForms {
		for _, form := range data.Forms {
			check("form", form.Slug, form.LanguageCode)
		}
	}

	return errs
}

// ValidateFile validates an import file and returns information about its contents.
func (i *Importer) ValidateFile(ctx context.Context, filePath string) (*ValidationResult, error) {
	f, err := os.Open(filePath)
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
	mediaDeselectedData := normalizedTransferMediaIdentities(data, false)
	data = normalizedTransferMediaIdentities(data, true)
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
	result.Entities["translations"] = translationEdgeCount(data, DefaultImportOptions())

	// Run option-neutral structural validation first. Media metadata is optional
	// whenever the archive also contains non-media content, but it may be ignored
	// only when a complete media-deselected preview (including destination media
	// references) is actually importable.
	validationErrors := i.validate(data, false, false)
	if len(validationErrors) > 0 {
		result.Valid = false
		result.Errors = validationErrors
	}

	var destinationMedia destinationMediaIdentityIndex
	destinationMediaLoaded := false
	loadDestinationMedia := func() error {
		if destinationMediaLoaded {
			return nil
		}
		if i.store == nil {
			return errors.New("media validation requires a destination store")
		}
		var err error
		destinationMedia, err = loadDestinationMediaIdentityIndex(ctx, i.store)
		if err != nil {
			return fmt.Errorf("index destination media identities: %w", err)
		}
		destinationMediaLoaded = true
		return nil
	}

	type previewCandidate struct {
		plan   importLanguagePlan
		errors []ImportError
		valid  bool
	}
	evaluateCandidate := func(candidateData *ExportData, opts ImportOptions, candidateErrors []ImportError) (previewCandidate, error) {
		candidate := previewCandidate{errors: append([]ImportError(nil), candidateErrors...)}
		plan, contractErrors, contractErr := i.previewLanguageContract(ctx, candidateData, opts)
		candidate.plan = plan
		if contractErr != nil {
			candidate.errors = append(candidate.errors, ImportError{Entity: "language", Message: contractErr.Error()})
		} else {
			candidate.errors = append(candidate.errors, contractErrors...)
		}
		if importNeedsMediaIdentityResolution(candidateData, opts) {
			if err := loadDestinationMedia(); err != nil {
				return candidate, err
			}
			for _, validationErr := range []error{
				validateImportedMediaIdentities(candidateData, opts, destinationMedia),
				validateImportedPageMediaReferences(candidateData, opts, destinationMedia),
				validateImportedContentMediaURLs(candidateData, opts, destinationMedia),
			} {
				if validationErr != nil {
					candidate.errors = append(candidate.errors, ImportError{Entity: "media", Message: validationErr.Error()})
				}
			}
		}
		candidate.valid = len(candidate.errors) == 0
		return candidate, nil
	}

	previewOpts := DefaultImportOptions()
	selectedCandidate, err := evaluateCandidate(data, previewOpts, validateTransferMediaArchive(data.Media))
	if err != nil {
		return nil, err
	}
	fallbackCandidate := previewCandidate{}
	if hasNonMediaImportPayload(mediaDeselectedData) {
		fallbackOpts := previewOpts
		fallbackOpts.ImportMedia = false
		fallbackOpts.ImportMediaFiles = false
		fallbackCandidate, err = evaluateCandidate(mediaDeselectedData, fallbackOpts, nil)
		if err != nil {
			return nil, err
		}
	}

	plan := selectedCandidate.plan
	if !selectedCandidate.valid {
		if fallbackCandidate.valid {
			plan = fallbackCandidate.plan
		} else {
			result.Valid = false
			result.Errors = append(result.Errors, selectedCandidate.errors...)
			result.Errors = append(result.Errors, fallbackCandidate.errors...)
		}
	}

	// Check for conflicts (entities that already exist)
	// Languages
	for _, lang := range data.Languages {
		exists, err := i.store.LanguageCodeExists(ctx, lang.Code)
		if err != nil {
			return nil, fmt.Errorf("check language %q conflict: %w", lang.Code, err)
		}
		if exists != 0 {
			result.Conflicts["languages"] = append(result.Conflicts["languages"], lang.Code)
		}
	}

	// Users
	for _, user := range data.Users {
		_, err := i.store.GetUserByEmail(ctx, user.Email)
		exists, err := lookupExists(err)
		if err != nil {
			return nil, fmt.Errorf("check user %q conflict: %w", user.Email, err)
		}
		if exists {
			result.Conflicts["users"] = append(result.Conflicts["users"], user.Email)
		}
	}

	// Categories
	for _, cat := range data.Categories {
		_, err := i.store.GetCategoryBySlug(ctx, cat.Slug)
		exists, err := lookupExists(err)
		if err != nil {
			return nil, fmt.Errorf("check category %q conflict: %w", cat.Slug, err)
		}
		if exists {
			result.Conflicts["categories"] = append(result.Conflicts["categories"], cat.Slug)
		}
	}

	// Tags
	for _, tag := range data.Tags {
		_, err := i.store.GetTagBySlug(ctx, tag.Slug)
		exists, err := lookupExists(err)
		if err != nil {
			return nil, fmt.Errorf("check tag %q conflict: %w", tag.Slug, err)
		}
		if exists {
			result.Conflicts["tags"] = append(result.Conflicts["tags"], tag.Slug)
		}
	}

	// Pages
	for _, page := range data.Pages {
		_, err := i.store.GetPageBySlug(ctx, page.Slug)
		exists, err := lookupExists(err)
		if err != nil {
			return nil, fmt.Errorf("check page %q conflict: %w", page.Slug, err)
		}
		if exists {
			result.Conflicts["pages"] = append(result.Conflicts["pages"], page.Slug)
		}
	}

	// Media
	if len(data.Media) > 0 {
		if err := loadDestinationMedia(); err != nil {
			return nil, err
		}
	}
	for _, media := range data.Media {
		_, exists, err := destinationMedia.exact(media.UUID)
		if err != nil {
			// A valid media-deselected candidate may intentionally coexist with
			// unusable optional media metadata. Keep the identity visible in the
			// conflict summary; the selected-media candidate already carries the
			// actionable collision error when no safe fallback exists.
			result.Conflicts["media"] = append(result.Conflicts["media"], media.UUID)
			continue
		}
		if exists {
			result.Conflicts["media"] = append(result.Conflicts["media"], media.UUID)
		}
	}

	// Menus
	for _, menu := range data.Menus {
		languageCode := importedLanguageCode(menu.LanguageCode, plan.defaultCode)
		_, err := i.store.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
			Slug: menu.Slug, LanguageCode: languageCode,
		})
		exists, err := lookupExists(err)
		if err != nil {
			return nil, fmt.Errorf("check menu %q/%q conflict: %w", languageCode, menu.Slug, err)
		}
		if exists {
			result.Conflicts["menus"] = append(result.Conflicts["menus"], menu.Slug)
		}
	}

	// Forms
	for _, form := range data.Forms {
		languageCode := importedLanguageCode(form.LanguageCode, plan.defaultCode)
		_, err := i.store.GetFormBySlugAndLanguage(ctx, store.GetFormBySlugAndLanguageParams{
			Slug: form.Slug, LanguageCode: languageCode,
		})
		exists, err := lookupExists(err)
		if err != nil {
			return nil, fmt.Errorf("check form %q/%q conflict: %w", languageCode, form.Slug, err)
		}
		if exists {
			result.Conflicts["forms"] = append(result.Conflicts["forms"], form.Slug)
		}
	}

	// Config
	for key := range data.Config {
		_, err := i.store.GetConfigByKey(ctx, key)
		exists, err := lookupExists(err)
		if err != nil {
			return nil, fmt.Errorf("check config %q conflict: %w", key, err)
		}
		if exists {
			result.Conflicts["config"] = append(result.Conflicts["config"], key)
		}
	}

	return result, nil
}

func hasNonMediaImportPayload(data *ExportData) bool {
	return data != nil && (len(data.Languages) > 0 || len(data.Users) > 0 ||
		len(data.Categories) > 0 || len(data.Tags) > 0 || len(data.Pages) > 0 ||
		len(data.Menus) > 0 || len(data.Forms) > 0 || len(data.Config) > 0)
}

func lookupExists(err error) (bool, error) {
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

// countEntity increments the appropriate counter based on existence and conflict strategy.
func countEntity(result *ImportResult, strategy ConflictStrategy, exists bool, category string) {
	if exists {
		switch strategy {
		case ConflictSkip:
			result.IncrementSkipped(category)
		case ConflictOverwrite:
			result.IncrementUpdated(category)
		case ConflictRename:
			if category == "languages" || category == "users" || category == "media" || category == "config" {
				result.IncrementSkipped(category)
			} else {
				result.IncrementCreated(category)
			}
		}
	} else {
		result.IncrementCreated(category)
	}
}

// countEntities counts entities that would be imported (for dry run).
// It checks existing entities to properly categorize as created, updated, or skipped.
func (i *Importer) countEntities(
	ctx context.Context,
	data *ExportData,
	opts ImportOptions,
	defaultLanguageCode string,
	result *ImportResult,
) error {
	countLookup := func(category, id string, lookupErr error) error {
		exists, lookupErr := lookupExists(lookupErr)
		if lookupErr != nil {
			return fmt.Errorf("check %s %q: %w", category, id, lookupErr)
		}
		countEntity(result, opts.ConflictStrategy, exists, category)
		return nil
	}

	if opts.ImportLanguages {
		for _, lang := range data.Languages {
			exists, lookupErr := i.store.LanguageCodeExists(ctx, lang.Code)
			if lookupErr != nil {
				return fmt.Errorf("check language %q: %w", lang.Code, lookupErr)
			}
			countEntity(result, opts.ConflictStrategy, exists != 0, "languages")
		}
	}
	if opts.ImportUsers {
		for _, user := range data.Users {
			_, err := i.store.GetUserByEmail(ctx, user.Email)
			if err := countLookup("users", user.Email, err); err != nil {
				return err
			}
		}
	}
	if opts.ImportCategories {
		for _, cat := range data.Categories {
			_, err := i.store.GetCategoryBySlug(ctx, cat.Slug)
			if err := countLookup("categories", cat.Slug, err); err != nil {
				return err
			}
		}
	}
	if opts.ImportTags {
		for _, tag := range data.Tags {
			_, err := i.store.GetTagBySlug(ctx, tag.Slug)
			if err := countLookup("tags", tag.Slug, err); err != nil {
				return err
			}
		}
	}
	if opts.ImportPages {
		for _, page := range data.Pages {
			_, err := i.store.GetPageBySlug(ctx, page.Slug)
			if err := countLookup("pages", page.Slug, err); err != nil {
				return err
			}
		}
	}
	if opts.ImportMedia {
		for _, media := range data.Media {
			_, err := i.store.GetMediaByUUID(ctx, media.UUID)
			if err := countLookup("media", media.UUID, err); err != nil {
				return err
			}
		}
	}
	if opts.ImportMenus {
		for _, menu := range data.Menus {
			languageCode := importedLanguageCode(menu.LanguageCode, defaultLanguageCode)
			_, err := i.store.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
				Slug: menu.Slug, LanguageCode: languageCode,
			})
			if err := countLookup("menus", languageCode+"/"+menu.Slug, err); err != nil {
				return err
			}
		}
	}
	if opts.ImportForms {
		for _, form := range data.Forms {
			languageCode := importedLanguageCode(form.LanguageCode, defaultLanguageCode)
			_, err := i.store.GetFormBySlugAndLanguage(ctx, store.GetFormBySlugAndLanguageParams{
				Slug: form.Slug, LanguageCode: languageCode,
			})
			if err := countLookup("forms", languageCode+"/"+form.Slug, err); err != nil {
				return err
			}
		}
	}
	if opts.ImportConfig {
		for key := range data.Config {
			_, err := i.store.GetConfigByKey(ctx, key)
			if err := countLookup("config", key, err); err != nil {
				return err
			}
		}
	}

	return nil
}

// Import methods for each entity type

func (i *Importer) importLanguages(ctx context.Context, queries *store.Queries, languages []ExportLanguage, opts ImportOptions, result *ImportResult) error {
	now := time.Now()
	defaultCode := ""

	for _, lang := range languages {
		if lang.IsDefault {
			defaultCode = lang.Code
		}
		// Check if language exists
		exists, err := queries.LanguageCodeExists(ctx, lang.Code)
		if err != nil {
			result.AddError("language", lang.Code, err.Error())
			return err
		}

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
					return err
				}
				_, err = queries.UpdateLanguage(ctx, store.UpdateLanguageParams{
					ID:         existing.ID,
					Code:       lang.Code,
					Name:       lang.Name,
					NativeName: lang.NativeName,
					IsDefault:  false,
					IsActive:   lang.IsActive,
					Direction:  lang.Direction,
					Position:   lang.Position,
					UpdatedAt:  now,
				})
				if err != nil {
					result.AddError("language", lang.Code, err.Error())
					return err
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
			IsDefault:  false,
			IsActive:   lang.IsActive,
			Direction:  lang.Direction,
			Position:   lang.Position,
			CreatedAt:  now,
			UpdatedAt:  now,
		})
		if err != nil {
			result.AddError("language", lang.Code, err.Error())
			return err
		}

		result.GetIDMap("languages")[int64(len(result.GetIDMap("languages"))+1)] = created.ID
		result.IncrementCreated("languages")
	}

	defaultLang, err := queries.GetLanguageByCode(ctx, defaultCode)
	if err != nil {
		result.AddError("language", defaultCode, err.Error())
		return err
	}
	if !defaultLang.IsActive {
		err := fmt.Errorf("archive default language %q is inactive in the destination", defaultCode)
		result.AddError("language", defaultCode, err.Error())
		return err
	}
	if err := queries.ClearDefaultLanguage(ctx); err != nil {
		result.AddError("language", defaultCode, err.Error())
		return err
	}
	if err := queries.SetDefaultLanguage(ctx, store.SetDefaultLanguageParams{UpdatedAt: now, ID: defaultLang.ID}); err != nil {
		result.AddError("language", defaultCode, err.Error())
		return err
	}
	resolved, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		result.AddError("language", defaultCode, err.Error())
		return err
	}
	if resolved.ID != defaultLang.ID || !resolved.IsActive {
		err := fmt.Errorf("archive default language %q was not applied atomically", defaultCode)
		result.AddError("language", defaultCode, err.Error())
		return err
	}

	return nil
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
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			result.AddError("user", user.Email, fmt.Sprintf("failed to check for existing user: %v", err))
			continue
		}

		// Create new user with random password (they'll need to reset it)
		randomPassword, err := generateRandomPassword()
		if err != nil {
			result.AddError("user", user.Email, "failed to generate random password")
			continue
		}
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

func (i *Importer) importCategories(ctx context.Context, queries *store.Queries, categories []ExportCategory, defaultLangCode string, opts ImportOptions, result *ImportResult) error {
	now := time.Now()

	// First pass: create all categories without parent relationships
	categoryOldToNew := make(map[int64]int64) // maps export ID to new ID
	sourceSlugToID := make(map[string]int64)
	finalSlugByOldID := make(map[int64]string)
	mutableCategoryIDs := make(map[int64]struct{})
	selectedSourceSlugs := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		selectedSourceSlugs[category.Slug] = struct{}{}
	}

	for _, cat := range categories {
		sourceSlug := cat.Slug
		languageCode := importedLanguageCode(cat.LanguageCode, defaultLangCode)
		// Check if category exists
		existing, err := queries.GetCategoryBySlug(ctx, cat.Slug)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			result.AddError("category", cat.Slug, fmt.Sprintf("failed to check for existing category: %v", err))
			continue
		}

		if err == nil {
			// Category exists
			switch opts.ConflictStrategy {
			case ConflictSkip:
				sourceSlugToID[sourceSlug] = existing.ID
				categoryOldToNew[cat.ID] = existing.ID
				result.IncrementSkipped("categories")
				continue
			case ConflictOverwrite:
				_, err = queries.UpdateCategory(ctx, store.UpdateCategoryParams{
					ID:           existing.ID,
					Name:         cat.Name,
					Slug:         cat.Slug,
					Description:  toNullString(cat.Description),
					ParentID:     sql.NullInt64{}, // Will update in second pass
					Position:     cat.Position,
					LanguageCode: languageCode,
					UpdatedAt:    now,
				})
				if err != nil {
					result.AddError("category", cat.Slug, err.Error())
					continue
				}
				sourceSlugToID[sourceSlug] = existing.ID
				categoryOldToNew[cat.ID] = existing.ID
				finalSlugByOldID[cat.ID] = cat.Slug
				mutableCategoryIDs[cat.ID] = struct{}{}
				result.IncrementUpdated("categories")
				continue
			case ConflictRename:
				cat.Slug = i.generateUniqueSlug(ctx, queries, cat.Slug, "category")
			}
		}

		// Create new category
		created, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
			Name:         cat.Name,
			Slug:         cat.Slug,
			Description:  toNullString(cat.Description),
			ParentID:     sql.NullInt64{}, // Will update in second pass
			Position:     cat.Position,
			LanguageCode: languageCode,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			result.AddError("category", cat.Slug, err.Error())
			continue
		}

		sourceSlugToID[sourceSlug] = created.ID
		categoryOldToNew[cat.ID] = created.ID
		finalSlugByOldID[cat.ID] = cat.Slug
		mutableCategoryIDs[cat.ID] = struct{}{}
		result.IncrementCreated("categories")
	}

	// Second pass: update parent relationships
	for _, cat := range categories {
		if cat.ParentSlug == "" {
			continue
		}
		if _, ok := mutableCategoryIDs[cat.ID]; !ok {
			continue
		}

		newID, ok := categoryOldToNew[cat.ID]
		if !ok {
			continue
		}

		parentID, ok := sourceSlugToID[cat.ParentSlug]
		if !ok {
			if _, selected := selectedSourceSlugs[cat.ParentSlug]; selected {
				err := fmt.Errorf("imported parent category %q was not created", cat.ParentSlug)
				result.AddError("category", cat.Slug, err.Error())
				return err
			}
			// Try to find parent by slug in database
			parent, err := queries.GetCategoryBySlug(ctx, cat.ParentSlug)
			if err != nil {
				err = fmt.Errorf("resolve parent category %q for %q: %w", cat.ParentSlug, cat.Slug, err)
				result.AddError("category", cat.Slug, err.Error())
				return err
			}
			parentID = parent.ID
		}

		_, err := queries.UpdateCategory(ctx, store.UpdateCategoryParams{
			ID:           newID,
			Name:         cat.Name,
			Slug:         finalSlugByOldID[cat.ID],
			Description:  toNullString(cat.Description),
			ParentID:     sql.NullInt64{Int64: parentID, Valid: true},
			Position:     cat.Position,
			LanguageCode: importedLanguageCode(cat.LanguageCode, defaultLangCode),
			UpdatedAt:    now,
		})
		if err != nil {
			err = fmt.Errorf("update parent of category %q: %w", cat.Slug, err)
			result.AddError("category", cat.Slug, err.Error())
			return err
		}
	}

	// Store mapping for use in page import
	for oldID, newID := range categoryOldToNew {
		result.GetIDMap("categories")[oldID] = newID
	}
	return nil
}

func (i *Importer) importTags(ctx context.Context, queries *store.Queries, tags []ExportTag, defaultLangCode string, opts ImportOptions, result *ImportResult) {
	now := time.Now()

	for _, tag := range tags {
		languageCode := importedLanguageCode(tag.LanguageCode, defaultLangCode)
		// Check if tag exists
		existing, err := queries.GetTagBySlug(ctx, tag.Slug)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			result.AddError("tag", tag.Slug, fmt.Sprintf("failed to check for existing tag: %v", err))
			continue
		}

		if err == nil {
			// Tag exists
			switch opts.ConflictStrategy {
			case ConflictSkip:
				result.GetIDMap("tags")[tag.ID] = existing.ID
				result.IncrementSkipped("tags")
				continue
			case ConflictOverwrite:
				_, err = queries.UpdateTag(ctx, store.UpdateTagParams{
					ID:           existing.ID,
					Name:         tag.Name,
					Slug:         tag.Slug,
					LanguageCode: languageCode,
					UpdatedAt:    now,
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
			Name:         tag.Name,
			Slug:         tag.Slug,
			LanguageCode: languageCode,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			result.AddError("tag", tag.Slug, err.Error())
			continue
		}

		result.GetIDMap("tags")[tag.ID] = created.ID
		result.IncrementCreated("tags")
	}
}

func (i *Importer) importMedia(ctx context.Context, queries *store.Queries, media []ExportMedia, userMap map[string]int64, defaultLangCode string, opts ImportOptions, result *ImportResult) error {
	now := time.Now()

	destinationMedia, err := loadDestinationMediaIdentityIndex(ctx, queries)
	if err != nil {
		return fmt.Errorf("index destination media identities: %w", err)
	}

	// Resolve conflicts before creating folder dependencies. Skipped media must
	// not leave archive-only folders behind, and an operational lookup failure
	// must abort the transaction instead of being mistaken for a missing row.
	existingByUUID := make(map[string]store.Medium, len(media))
	folderMedia := make([]ExportMedia, 0, len(media))
	for _, medium := range media {
		existing, exists, err := destinationMedia.exact(medium.UUID)
		switch {
		case err != nil:
			return fmt.Errorf("check existing media %q: %w", medium.UUID, err)
		case exists:
			existingByUUID[medium.UUID] = existing
			if opts.ConflictStrategy == ConflictOverwrite {
				folderMedia = append(folderMedia, medium)
			}
		default:
			folderMedia = append(folderMedia, medium)
		}
	}

	// Build folder path to ID map only for media that will be created or
	// overwritten.
	folderMap, err := i.buildOrCreateFolders(ctx, queries, folderMedia)
	if err != nil {
		return fmt.Errorf("build media folder map: %w", err)
	}

	for _, m := range media {
		// Use language code from import data or default
		langCode := importedLanguageCode(m.LanguageCode, defaultLangCode)

		// Resolve every mutable field before conflict handling so overwrite
		// replaces the full archived identity, not only its presentation fields.
		uploaderID := int64(1)
		if m.UploadedBy != "" {
			if id, ok := userMap[m.UploadedBy]; ok {
				uploaderID = id
			}
		}
		folderID := sql.NullInt64{}
		if m.FolderPath != "" {
			if fID, ok := folderMap[m.FolderPath]; ok {
				folderID = sql.NullInt64{Int64: fID, Valid: true}
			}
		}

		if existing, exists := existingByUUID[m.UUID]; exists {
			// Media exists
			switch opts.ConflictStrategy {
			case ConflictSkip:
				result.GetIDMap("media")[int64(len(result.GetIDMap("media"))+1)] = existing.ID
				result.IncrementSkipped("media")
				continue
			case ConflictOverwrite:
				_, err = queries.UpdateMediaForImport(ctx, store.UpdateMediaForImportParams{
					ID:           existing.ID,
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
					UpdatedAt:    now,
				})
				if err != nil {
					result.AddError("media", m.UUID, err.Error())
					continue
				}
				if err := replaceMediaVariants(ctx, queries, existing.ID, m.Variants, now); err != nil {
					result.AddError("media", m.UUID, err.Error())
					return err
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
		if err := replaceMediaVariants(ctx, queries, created.ID, m.Variants, now); err != nil {
			result.AddError("media", m.UUID, err.Error())
			return err
		}

		result.GetIDMap("media")[int64(len(result.GetIDMap("media"))+1)] = created.ID
		result.IncrementCreated("media")
	}
	return nil
}

func replaceMediaVariants(
	ctx context.Context,
	queries *store.Queries,
	mediaID int64,
	variants []ExportVariant,
	createdAt time.Time,
) error {
	if err := queries.DeleteMediaVariants(ctx, mediaID); err != nil {
		return fmt.Errorf("delete existing media variants: %w", err)
	}
	for _, variant := range variants {
		if _, err := queries.CreateMediaVariant(ctx, store.CreateMediaVariantParams{
			MediaID:   mediaID,
			Type:      variant.Type,
			Width:     variant.Width,
			Height:    variant.Height,
			Size:      variant.Size,
			CreatedAt: createdAt,
		}); err != nil {
			return fmt.Errorf("create %q media variant: %w", variant.Type, err)
		}
	}
	return nil
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
) error {
	now := time.Now()

	pageOldToNew := make(map[int64]int64) // maps export ID to new ID

	for _, page := range pages {
		// Check if page exists
		existing, existsErr := queries.GetPageBySlug(ctx, page.Slug)
		if existsErr != nil && !errors.Is(existsErr, sql.ErrNoRows) {
			result.AddError("page", page.Slug, fmt.Sprintf("failed to check for existing page: %v", existsErr))
			continue
		}
		pageExists := existsErr == nil

		var pageID int64
		shouldCreate := false
		createdPage := false
		updatedPage := false

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
				// Update categories and tags
				if err := queries.ClearPageCategories(ctx, pageID); err != nil {
					result.AddError("page", page.Slug, fmt.Sprintf("clear categories: %v", err))
					return err
				}
				if err := queries.ClearPageTags(ctx, pageID); err != nil {
					result.AddError("page", page.Slug, fmt.Sprintf("clear tags: %v", err))
					return err
				}
				updatedPage = true
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
			createdPage = true
		}

		// Add categories
		for _, catSlug := range page.Categories {
			catID, ok := categoryMap[catSlug]
			if !ok {
				err := fmt.Errorf("referenced category %q is unavailable", catSlug)
				result.AddError("page", page.Slug, err.Error())
				return err
			}
			if err := queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
				PageID:     pageID,
				CategoryID: catID,
			}); err != nil {
				result.AddError("page", page.Slug, fmt.Sprintf("add category %q: %v", catSlug, err))
				return err
			}
		}

		// Add tags
		for _, tagSlug := range page.Tags {
			tagID, ok := tagMap[tagSlug]
			if !ok {
				err := fmt.Errorf("referenced tag %q is unavailable", tagSlug)
				result.AddError("page", page.Slug, err.Error())
				return err
			}
			if err := queries.AddTagToPage(ctx, store.AddTagToPageParams{
				PageID: pageID,
				TagID:  tagID,
			}); err != nil {
				result.AddError("page", page.Slug, fmt.Sprintf("add tag %q: %v", tagSlug, err))
				return err
			}
		}
		pageOldToNew[page.ID] = pageID
		if createdPage {
			result.IncrementCreated("pages")
		}
		if updatedPage {
			result.IncrementUpdated("pages")
		}
	}

	// Store mapping for use later
	for oldID, newID := range pageOldToNew {
		result.GetIDMap("pages")[oldID] = newID
	}
	return nil
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
		VideoUrl:        page.VideoURL,
		VideoTitle:      page.VideoTitle,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		return 0, err
	}

	return created.ID, nil
}

func (i *Importer) importMenus(ctx context.Context, queries *store.Queries, menus []ExportMenu, pageMap map[string]int64, defaultLangCode string, opts ImportOptions, result *ImportResult) error {
	now := time.Now()

	for _, menu := range menus {
		languageCode := importedLanguageCode(menu.LanguageCode, defaultLangCode)
		// Check if menu exists
		existing, existsErr := queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
			Slug: menu.Slug, LanguageCode: languageCode,
		})
		if existsErr != nil && !errors.Is(existsErr, sql.ErrNoRows) {
			result.AddError("menu", menu.Slug, fmt.Sprintf("failed to check for existing menu: %v", existsErr))
			return existsErr
		}
		menuExists := existsErr == nil

		var menuID int64
		shouldCreate := false
		createdMenu := false
		updatedMenu := false

		if menuExists {
			// Menu exists - handle based on conflict strategy
			switch opts.ConflictStrategy {
			case ConflictSkip:
				result.IncrementSkipped("menus")
				continue
			case ConflictOverwrite:
				updated, updateErr := queries.UpdateMenu(ctx, store.UpdateMenuParams{
					ID:           existing.ID,
					Name:         menu.Name,
					Slug:         menu.Slug,
					LanguageCode: languageCode,
					UpdatedAt:    now,
				})
				if updateErr != nil {
					result.AddError("menu", menu.Slug, updateErr.Error())
					return updateErr
				}
				menuID = updated.ID

				// Delete existing menu items
				if err := queries.DeleteMenuItems(ctx, menuID); err != nil {
					result.AddError("menu", menu.Slug, fmt.Sprintf("failed to replace menu items: %v", err))
					return err
				}
				updatedMenu = true
			case ConflictRename:
				menu.Slug, existsErr = i.generateUniqueLanguageSlug(ctx, queries, menu.Slug, languageCode, "menu")
				if existsErr != nil {
					result.AddError("menu", menu.Slug, existsErr.Error())
					return existsErr
				}
				shouldCreate = true
			}
		} else {
			shouldCreate = true
		}

		if shouldCreate {
			created, createErr := queries.CreateMenu(ctx, store.CreateMenuParams{
				Name:         menu.Name,
				Slug:         menu.Slug,
				LanguageCode: languageCode,
				CreatedAt:    now,
				UpdatedAt:    now,
			})
			if createErr != nil {
				result.AddError("menu", menu.Slug, createErr.Error())
				return createErr
			}
			menuID = created.ID
			createdMenu = true
		}

		// Import menu items
		if err := i.importMenuItems(ctx, queries, menuID, menu.Items, pageMap, sql.NullInt64{}, now); err != nil {
			result.AddError("menu", menu.Slug, fmt.Sprintf("failed to import menu items: %v", err))
			return err
		}
		if createdMenu {
			result.IncrementCreated("menus")
		} else if updatedMenu {
			result.IncrementUpdated("menus")
		}
	}
	return nil
}

func (i *Importer) importMenuItems(ctx context.Context, queries *store.Queries, menuID int64, items []ExportMenuItem, pageMap map[string]int64, parentID sql.NullInt64, now time.Time) error {
	for _, item := range items {
		// Get page ID if linked
		pageID := sql.NullInt64{}
		if item.PageSlug != "" {
			id, ok := pageMap[item.PageSlug]
			if !ok {
				return fmt.Errorf("referenced page %q was not imported", item.PageSlug)
			}
			pageID = sql.NullInt64{Int64: id, Valid: true}
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
		langCode := importedLanguageCode(form.LanguageCode, defaultLangCode)

		// Check if form exists
		existing, existsErr := queries.GetFormBySlugAndLanguage(ctx, store.GetFormBySlugAndLanguageParams{
			Slug: form.Slug, LanguageCode: langCode,
		})
		if existsErr != nil && !errors.Is(existsErr, sql.ErrNoRows) {
			result.AddError("form", form.Slug, fmt.Sprintf("failed to check for existing form: %v", existsErr))
			continue
		}
		formExists := existsErr == nil

		var formID int64
		formLangCode := langCode
		shouldCreate := false

		if formExists {
			formLangCode = langCode
			// Form exists - handle based on conflict strategy
			switch opts.ConflictStrategy {
			case ConflictSkip:
				result.GetIDMap("forms")[form.ID] = existing.ID
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
					LanguageCode:   langCode,
					UpdatedAt:      now,
				})
				if updateErr != nil {
					result.AddError("form", form.Slug, updateErr.Error())
					continue
				}
				formID = updated.ID
				result.GetIDMap("forms")[form.ID] = updated.ID

				// Delete existing form fields
				_ = queries.DeleteFormFields(ctx, formID)

				result.IncrementUpdated("forms")
			case ConflictRename:
				form.Slug, existsErr = i.generateUniqueLanguageSlug(ctx, queries, form.Slug, langCode, "form")
				if existsErr != nil {
					result.AddError("form", form.Slug, existsErr.Error())
					continue
				}
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
			result.GetIDMap("forms")[form.ID] = created.ID
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

func (i *Importer) importConfig(
	ctx context.Context,
	queries *store.Queries,
	config map[string]string,
	userMap map[string]int64,
	defaultLangCode string,
	opts ImportOptions,
	result *ImportResult,
) {
	now := time.Now()

	// Get a default user ID for the updated_by field
	updatedBy := int64(1) // Default to first user
	for _, id := range userMap {
		updatedBy = id
		break
	}

	for key, value := range config {
		_, lookupErr := queries.GetConfigByKey(ctx, key)
		exists, lookupErr := lookupExists(lookupErr)
		if lookupErr != nil {
			result.AddError("config", key, fmt.Sprintf("failed to check for existing config: %v", lookupErr))
			continue
		}
		if exists && (opts.ConflictStrategy == ConflictSkip || opts.ConflictStrategy == ConflictRename) {
			result.IncrementSkipped("config")
			continue
		}
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
		if exists {
			result.IncrementUpdated("config")
		} else {
			result.IncrementCreated("config")
		}
	}
}

type exportedTranslationSet struct {
	entityType  string
	mapKey      string
	sourceOldID int64
	targets     map[string]int64
	languages   map[int64]string
}

func buildExportedTranslationSets(data *ExportData, defaultLangCode string, opts ImportOptions) []exportedTranslationSet {
	sets := make([]exportedTranslationSet, 0, len(data.Pages)+len(data.Categories)+len(data.Tags)+len(data.Forms))
	appendSets := func(entityType, mapKey string, translations map[int64]map[string]int64, languages map[int64]string) {
		ids := make([]int64, 0, len(translations))
		for id := range translations {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
		for _, id := range ids {
			sets = append(sets, exportedTranslationSet{
				entityType: entityType, mapKey: mapKey, sourceOldID: id,
				targets: translations[id], languages: languages,
			})
		}
	}
	if opts.ImportPages {
		translations := make(map[int64]map[string]int64, len(data.Pages))
		languages := make(map[int64]string, len(data.Pages))
		for _, entity := range data.Pages {
			translations[entity.ID] = entity.Translations
			languages[entity.ID] = importedLanguageCode(entity.LanguageCode, defaultLangCode)
		}
		appendSets("page", "pages", translations, languages)
	}
	if opts.ImportCategories {
		translations := make(map[int64]map[string]int64, len(data.Categories))
		languages := make(map[int64]string, len(data.Categories))
		for _, entity := range data.Categories {
			translations[entity.ID] = entity.Translations
			languages[entity.ID] = importedLanguageCode(entity.LanguageCode, defaultLangCode)
		}
		appendSets("category", "categories", translations, languages)
	}
	if opts.ImportTags {
		translations := make(map[int64]map[string]int64, len(data.Tags))
		languages := make(map[int64]string, len(data.Tags))
		for _, entity := range data.Tags {
			translations[entity.ID] = entity.Translations
			languages[entity.ID] = importedLanguageCode(entity.LanguageCode, defaultLangCode)
		}
		appendSets("tag", "tags", translations, languages)
	}
	if opts.ImportForms {
		translations := make(map[int64]map[string]int64, len(data.Forms))
		languages := make(map[int64]string, len(data.Forms))
		for _, entity := range data.Forms {
			translations[entity.ID] = entity.Translations
			languages[entity.ID] = importedLanguageCode(entity.LanguageCode, defaultLangCode)
		}
		appendSets("form", "forms", translations, languages)
	}
	return sets
}

func validateTranslationGraph(
	data *ExportData,
	opts ImportOptions,
	defaultLangCode string,
	knownLanguages map[string]struct{},
) []ImportError {
	var errs []ImportError
	sets := buildExportedTranslationSets(data, defaultLangCode, opts)
	for _, set := range sets {
		sourceLanguage := set.languages[set.sourceOldID]
		languageCodes := make([]string, 0, len(set.targets))
		for languageCode := range set.targets {
			languageCodes = append(languageCodes, languageCode)
		}
		sort.Strings(languageCodes)
		for _, languageCode := range languageCodes {
			targetID := set.targets[languageCode]
			if targetID == set.sourceOldID {
				errs = append(errs, ImportError{Entity: set.entityType, ID: strconv.FormatInt(set.sourceOldID, 10),
					Message: "translation cannot target its source entity"})
				continue
			}
			if languageCode == sourceLanguage {
				errs = append(errs, ImportError{Entity: set.entityType, ID: strconv.FormatInt(set.sourceOldID, 10),
					Message: fmt.Sprintf("translation cannot target the source language %q", sourceLanguage)})
				continue
			}
			if _, ok := knownLanguages[languageCode]; !ok {
				errs = append(errs, ImportError{Entity: set.entityType, ID: strconv.FormatInt(set.sourceOldID, 10),
					Message: fmt.Sprintf("translation uses unknown language %q", languageCode)})
				continue
			}
			targetLanguage, ok := set.languages[targetID]
			if !ok {
				errs = append(errs, ImportError{Entity: set.entityType, ID: strconv.FormatInt(set.sourceOldID, 10),
					Message: fmt.Sprintf("references missing translation target %d", targetID)})
				continue
			}
			if targetLanguage != languageCode {
				errs = append(errs, ImportError{Entity: set.entityType, ID: strconv.FormatInt(set.sourceOldID, 10),
					Message: fmt.Sprintf("translation target %d has language %q, not %q", targetID, targetLanguage, languageCode)})
			}
		}
	}

	// A translation component represents one logical entity and may contain at
	// most one member for each language. Per-edge validation alone misses
	// malformed stars/chains such as EN→FR→EN.
	type componentGraph struct {
		languages map[int64]string
		edges     map[int64]map[int64]struct{}
	}
	graphs := make(map[string]*componentGraph)
	for _, set := range sets {
		graph := graphs[set.entityType]
		if graph == nil {
			graph = &componentGraph{languages: set.languages, edges: make(map[int64]map[int64]struct{})}
			graphs[set.entityType] = graph
		}
		if graph.edges[set.sourceOldID] == nil {
			graph.edges[set.sourceOldID] = make(map[int64]struct{})
		}
		sourceLanguage := graph.languages[set.sourceOldID]
		for languageCode, targetID := range set.targets {
			targetLanguage, exists := graph.languages[targetID]
			if !exists || targetID == set.sourceOldID || languageCode == sourceLanguage || targetLanguage != languageCode {
				continue
			}
			if graph.edges[targetID] == nil {
				graph.edges[targetID] = make(map[int64]struct{})
			}
			graph.edges[set.sourceOldID][targetID] = struct{}{}
			graph.edges[targetID][set.sourceOldID] = struct{}{}
		}
	}
	entityTypes := make([]string, 0, len(graphs))
	for entityType := range graphs {
		entityTypes = append(entityTypes, entityType)
	}
	sort.Strings(entityTypes)
	for _, entityType := range entityTypes {
		graph := graphs[entityType]
		ids := make([]int64, 0, len(graph.edges))
		for id := range graph.edges {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
		visited := make(map[int64]struct{}, len(ids))
		for _, rootID := range ids {
			if _, seen := visited[rootID]; seen {
				continue
			}
			stack := []int64{rootID}
			languageOwners := make(map[string]int64)
			for len(stack) > 0 {
				id := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if _, seen := visited[id]; seen {
					continue
				}
				visited[id] = struct{}{}
				languageCode := graph.languages[id]
				if ownerID, duplicate := languageOwners[languageCode]; duplicate && languageCode != "" {
					errs = append(errs, ImportError{
						Entity: entityType,
						ID:     strconv.FormatInt(id, 10),
						Message: fmt.Sprintf(
							"translation component contains multiple %s entities for language %q (%d and %d)",
							entityType, languageCode, ownerID, id,
						),
					})
				} else {
					languageOwners[languageCode] = id
				}
				neighbors := make([]int64, 0, len(graph.edges[id]))
				for neighborID := range graph.edges[id] {
					neighbors = append(neighbors, neighborID)
				}
				sort.Slice(neighbors, func(a, b int) bool { return neighbors[a] > neighbors[b] })
				stack = append(stack, neighbors...)
			}
		}
	}
	return errs
}

func translationEntityLanguage(ctx context.Context, queries *store.Queries, entityType string, id int64) (string, error) {
	switch entityType {
	case "page":
		entity, err := queries.GetPageByID(ctx, id)
		return entity.LanguageCode, err
	case "category":
		entity, err := queries.GetCategoryByID(ctx, id)
		return entity.LanguageCode, err
	case "tag":
		entity, err := queries.GetTagByID(ctx, id)
		return entity.LanguageCode, err
	case "form":
		entity, err := queries.GetFormByID(ctx, id)
		return entity.LanguageCode, err
	default:
		return "", fmt.Errorf("unsupported translation entity type %q", entityType)
	}
}

func validateTranslationComponentMerge(
	ctx context.Context,
	queries *store.Queries,
	entityType string,
	sourceID, targetID int64,
) error {
	owners := make(map[string]int64)
	seenEntities := make(map[int64]struct{})
	addEntity := func(id int64, languageCode string) error {
		if _, seen := seenEntities[id]; seen {
			return nil
		}
		seenEntities[id] = struct{}{}
		if ownerID, exists := owners[languageCode]; exists && ownerID != id {
			return fmt.Errorf(
				"joining translation components would create multiple %s entities for language %q (%d and %d)",
				entityType, languageCode, ownerID, id,
			)
		}
		owners[languageCode] = id
		return nil
	}
	addComponent := func(rootID int64) error {
		languageCode, err := translationEntityLanguage(ctx, queries, entityType, rootID)
		if err != nil {
			return err
		}
		if err := addEntity(rootID, languageCode); err != nil {
			return err
		}
		members, err := queries.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{
			SourceEntityID: rootID,
			EntityType:     entityType,
		})
		if err != nil {
			return err
		}
		for _, member := range members {
			if err := addEntity(member.EntityID, member.LanguageCode); err != nil {
				return err
			}
		}
		return nil
	}
	if err := addComponent(sourceID); err != nil {
		return err
	}
	return addComponent(targetID)
}

func translationEntitySlugs(data *ExportData) map[string]map[int64]string {
	slugs := map[string]map[int64]string{
		"page": {}, "category": {}, "tag": {}, "form": {},
	}
	for _, entity := range data.Pages {
		slugs["page"][entity.ID] = entity.Slug
	}
	for _, entity := range data.Categories {
		slugs["category"][entity.ID] = entity.Slug
	}
	for _, entity := range data.Tags {
		slugs["tag"][entity.ID] = entity.Slug
	}
	for _, entity := range data.Forms {
		slugs["form"][entity.ID] = entity.Slug
	}
	return slugs
}

func lookupExistingTranslationEntity(
	ctx context.Context,
	queries *store.Queries,
	entityType, slug, languageCode string,
) (int64, error) {
	switch entityType {
	case "page":
		entity, err := queries.GetPageBySlug(ctx, slug)
		return entity.ID, err
	case "category":
		entity, err := queries.GetCategoryBySlug(ctx, slug)
		return entity.ID, err
	case "tag":
		entity, err := queries.GetTagBySlug(ctx, slug)
		return entity.ID, err
	case "form":
		entity, err := queries.GetFormBySlugAndLanguage(ctx, store.GetFormBySlugAndLanguageParams{
			Slug: slug, LanguageCode: languageCode,
		})
		return entity.ID, err
	default:
		return 0, fmt.Errorf("unsupported translation entity type %q", entityType)
	}
}

func (i *Importer) validateExistingTranslationComponentMerges(
	ctx context.Context,
	data *ExportData,
	opts ImportOptions,
	defaultCode string,
) ([]ImportError, error) {
	sets := buildExportedTranslationSets(data, defaultCode, opts)
	slugs := translationEntitySlugs(data)
	type archiveGraph struct {
		languages map[int64]string
		edges     map[int64]map[int64]struct{}
	}
	graphs := make(map[string]*archiveGraph)
	for _, set := range sets {
		graph := graphs[set.entityType]
		if graph == nil {
			graph = &archiveGraph{languages: set.languages, edges: make(map[int64]map[int64]struct{})}
			graphs[set.entityType] = graph
		}
		if graph.edges[set.sourceOldID] == nil {
			graph.edges[set.sourceOldID] = make(map[int64]struct{})
		}
		for _, targetOldID := range set.targets {
			if _, exists := graph.languages[targetOldID]; !exists {
				continue
			}
			if graph.edges[targetOldID] == nil {
				graph.edges[targetOldID] = make(map[int64]struct{})
			}
			graph.edges[set.sourceOldID][targetOldID] = struct{}{}
			graph.edges[targetOldID][set.sourceOldID] = struct{}{}
		}
	}

	var errs []ImportError
	entityTypes := make([]string, 0, len(graphs))
	for entityType := range graphs {
		entityTypes = append(entityTypes, entityType)
	}
	sort.Strings(entityTypes)
	for _, entityType := range entityTypes {
		graph := graphs[entityType]
		oldIDs := make([]int64, 0, len(graph.edges))
		for oldID := range graph.edges {
			oldIDs = append(oldIDs, oldID)
		}
		sort.Slice(oldIDs, func(a, b int) bool { return oldIDs[a] < oldIDs[b] })
		visited := make(map[int64]struct{}, len(oldIDs))
		for _, rootOldID := range oldIDs {
			if _, seen := visited[rootOldID]; seen {
				continue
			}
			stack := []int64{rootOldID}
			component := make([]int64, 0)
			hasEdge := false
			for len(stack) > 0 {
				oldID := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if _, seen := visited[oldID]; seen {
					continue
				}
				visited[oldID] = struct{}{}
				component = append(component, oldID)
				if len(graph.edges[oldID]) > 0 {
					hasEdge = true
				}
				for neighborID := range graph.edges[oldID] {
					stack = append(stack, neighborID)
				}
			}
			if !hasEdge {
				continue
			}
			sort.Slice(component, func(a, b int) bool { return component[a] < component[b] })
			languageOwners := make(map[string]string)
			seenIdentities := make(map[string]struct{})
			var componentErr error
			addOwner := func(identity, languageCode string) {
				if componentErr != nil {
					return
				}
				if _, seen := seenIdentities[identity]; seen {
					return
				}
				seenIdentities[identity] = struct{}{}
				if owner, exists := languageOwners[languageCode]; exists && owner != identity {
					componentErr = fmt.Errorf(
						"joining translation components would create multiple %s entities for language %q (%s and %s)",
						entityType, languageCode, owner, identity,
					)
					return
				}
				languageOwners[languageCode] = identity
			}
			for _, oldID := range component {
				existingID, lookupErr := lookupExistingTranslationEntity(
					ctx, i.store, entityType, slugs[entityType][oldID], graph.languages[oldID],
				)
				if errors.Is(lookupErr, sql.ErrNoRows) {
					addOwner("archive:"+strconv.FormatInt(oldID, 10), graph.languages[oldID])
					continue
				}
				if lookupErr != nil {
					return nil, fmt.Errorf("resolve existing %s %d: %w", entityType, oldID, lookupErr)
				}
				actualLanguage, languageErr := translationEntityLanguage(ctx, i.store, entityType, existingID)
				if languageErr != nil {
					return nil, fmt.Errorf("resolve existing %s %d language: %w", entityType, existingID, languageErr)
				}
				addOwner("database:"+strconv.FormatInt(existingID, 10), actualLanguage)
				members, membersErr := i.store.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{
					SourceEntityID: existingID,
					EntityType:     entityType,
				})
				if membersErr != nil {
					return nil, fmt.Errorf("load existing %s translation component for %d: %w", entityType, existingID, membersErr)
				}
				for _, member := range members {
					addOwner("database:"+strconv.FormatInt(member.EntityID, 10), member.LanguageCode)
				}
			}
			if componentErr != nil {
				errs = append(errs, ImportError{
					Entity:  entityType,
					ID:      strconv.FormatInt(rootOldID, 10),
					Message: componentErr.Error(),
				})
			}
		}
	}
	return errs, nil
}

func (i *Importer) countTranslationEdges(
	ctx context.Context,
	data *ExportData,
	opts ImportOptions,
	defaultCode string,
	result *ImportResult,
) error {
	slugs := translationEntitySlugs(data)

	for _, set := range buildExportedTranslationSets(data, defaultCode, opts) {
		var sourceID int64
		if opts.ConflictStrategy == ConflictSkip {
			slug := slugs[set.entityType][set.sourceOldID]
			switch set.entityType {
			case "page":
				entity, err := i.store.GetPageBySlug(ctx, slug)
				if err == nil {
					sourceID = entity.ID
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			case "category":
				entity, err := i.store.GetCategoryBySlug(ctx, slug)
				if err == nil {
					sourceID = entity.ID
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			case "tag":
				entity, err := i.store.GetTagBySlug(ctx, slug)
				if err == nil {
					sourceID = entity.ID
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			case "form":
				entity, err := i.store.GetFormBySlugAndLanguage(ctx, store.GetFormBySlugAndLanguageParams{
					Slug: slug, LanguageCode: set.languages[set.sourceOldID],
				})
				if err == nil {
					sourceID = entity.ID
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
		}

		for languageCode := range set.targets {
			if sourceID == 0 {
				result.IncrementCreated("translations")
				continue
			}
			language, err := i.store.GetLanguageByCode(ctx, languageCode)
			if err != nil {
				return err
			}
			exists, err := i.store.TranslationExists(ctx, store.TranslationExistsParams{
				EntityType: set.entityType, EntityID: sourceID, LanguageID: language.ID,
			})
			if err != nil {
				return err
			}
			if exists != 0 {
				result.IncrementSkipped("translations")
			} else {
				result.IncrementCreated("translations")
			}
		}
	}
	return nil
}

func translationEdgeCount(data *ExportData, opts ImportOptions) int {
	count := 0
	for _, set := range buildExportedTranslationSets(data, "", opts) {
		count += len(set.targets)
	}
	return count
}

func (i *Importer) importTranslations(
	ctx context.Context,
	queries *store.Queries,
	data *ExportData,
	defaultLangCode string,
	opts ImportOptions,
	result *ImportResult,
) error {
	sets := buildExportedTranslationSets(data, defaultLangCode, opts)

	now := time.Now()
	if opts.ConflictStrategy == ConflictOverwrite {
		cleaned := make(map[string]map[int64]struct{})
		for _, set := range sets {
			newIDs := result.GetIDMap(set.mapKey)
			sourceID, ok := newIDs[set.sourceOldID]
			if !ok {
				if len(set.targets) == 0 {
					continue
				}
				return fmt.Errorf("%s %d has translations but was not imported", set.entityType, set.sourceOldID)
			}
			expectedLanguage := set.languages[set.sourceOldID]
			actualLanguage, err := translationEntityLanguage(ctx, queries, set.entityType, sourceID)
			if err != nil {
				return fmt.Errorf("resolve imported %s %d: %w", set.entityType, sourceID, err)
			}
			if actualLanguage != expectedLanguage {
				return fmt.Errorf("imported %s %d belongs to language %q, not %q",
					set.entityType, sourceID, actualLanguage, expectedLanguage)
			}
			ids := cleaned[set.entityType]
			if ids == nil {
				ids = make(map[int64]struct{})
				cleaned[set.entityType] = ids
			}
			if _, ok := ids[sourceID]; ok {
				continue
			}
			if err := queries.DeleteTranslationsRelatedToEntity(ctx, store.DeleteTranslationsRelatedToEntityParams{
				EntityType:    set.entityType,
				EntityID:      sourceID,
				TranslationID: sourceID,
			}); err != nil {
				return fmt.Errorf("clear existing %s translation graph for %d: %w", set.entityType, sourceID, err)
			}
			ids[sourceID] = struct{}{}
		}
	}

	for _, set := range sets {
		newIDs := result.GetIDMap(set.mapKey)
		sourceID, ok := newIDs[set.sourceOldID]
		if !ok {
			if len(set.targets) == 0 {
				continue
			}
			return fmt.Errorf("%s %d has translations but was not imported", set.entityType, set.sourceOldID)
		}
		expectedSourceLanguage := set.languages[set.sourceOldID]
		actualSourceLanguage, err := translationEntityLanguage(ctx, queries, set.entityType, sourceID)
		if err != nil {
			return fmt.Errorf("resolve imported %s %d: %w", set.entityType, sourceID, err)
		}
		if actualSourceLanguage != expectedSourceLanguage {
			return fmt.Errorf("imported %s %d belongs to language %q, not %q",
				set.entityType, sourceID, actualSourceLanguage, expectedSourceLanguage)
		}

		languageCodes := make([]string, 0, len(set.targets))
		for languageCode := range set.targets {
			languageCodes = append(languageCodes, languageCode)
		}
		sort.Strings(languageCodes)
		for _, languageCode := range languageCodes {
			targetOldID := set.targets[languageCode]
			targetLanguage, ok := set.languages[targetOldID]
			if !ok {
				return fmt.Errorf("%s %d references missing translation target %d", set.entityType, set.sourceOldID, targetOldID)
			}
			if targetLanguage != languageCode {
				return fmt.Errorf(
					"%s %d translation target %d has language %q, not %q",
					set.entityType, set.sourceOldID, targetOldID, targetLanguage, languageCode,
				)
			}
			targetID, ok := newIDs[targetOldID]
			if !ok {
				return fmt.Errorf("%s translation target %d was not imported", set.entityType, targetOldID)
			}
			actualTargetLanguage, err := translationEntityLanguage(ctx, queries, set.entityType, targetID)
			if err != nil {
				return fmt.Errorf("resolve imported %s translation target %d: %w", set.entityType, targetID, err)
			}
			if actualTargetLanguage != languageCode {
				return fmt.Errorf("imported %s translation target %d belongs to language %q, not %q",
					set.entityType, targetID, actualTargetLanguage, languageCode)
			}
			language, err := queries.GetLanguageByCode(ctx, languageCode)
			if err != nil {
				return fmt.Errorf("resolve translation language %q: %w", languageCode, err)
			}

			exists, err := queries.TranslationExists(ctx, store.TranslationExistsParams{
				EntityType: set.entityType,
				EntityID:   sourceID,
				LanguageID: language.ID,
			})
			if err != nil {
				return err
			}
			if exists != 0 {
				result.IncrementSkipped("translations")
				continue
			}
			if err := validateTranslationComponentMerge(ctx, queries, set.entityType, sourceID, targetID); err != nil {
				return err
			}

			if _, err := queries.CreateTranslation(ctx, store.CreateTranslationParams{
				EntityType:    set.entityType,
				EntityID:      sourceID,
				LanguageID:    language.ID,
				TranslationID: targetID,
				CreatedAt:     now,
			}); err != nil {
				return err
			}
			result.IncrementCreated("translations")
		}
	}

	return nil
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
		media, err := loadDestinationMediaIdentityIndex(ctx, queries)
		if err != nil {
			return nil, err
		}
		return media.exactLookupMap(), nil
	case entityPage:
		pages, err := listAllPagesForLookup(ctx, queries)
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

	for folderPath := range paths {
		// Check if folder exists by building/finding path
		parts := strings.Split(folderPath, "/")
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
			count, e := queries.FormSlugExists(ctx, slug)
			exists = count > 0
			err = e
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

func (i *Importer) generateUniqueLanguageSlug(
	ctx context.Context,
	queries *store.Queries,
	baseSlug string,
	languageCode string,
	entityType string,
) (string, error) {
	slug := baseSlug
	for counter := 1; ; counter++ {
		var exists int64
		var err error
		switch entityType {
		case "menu":
			exists, err = queries.MenuSlugExistsForLanguage(ctx, store.MenuSlugExistsForLanguageParams{
				Slug: slug, LanguageCode: languageCode,
			})
		case "form":
			exists, err = queries.FormSlugExistsForLanguage(ctx, store.FormSlugExistsForLanguageParams{
				Slug: slug, LanguageCode: languageCode,
			})
		default:
			return slug, nil
		}
		if err != nil {
			return "", fmt.Errorf("failed to check for an available %s slug: %w", entityType, err)
		}
		if exists == 0 {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", baseSlug, counter+1)
	}
}

// generateRandomPassword generates a random password for imported users.
func generateRandomPassword() (string, error) {
	// Generate a random 16-character password
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
	const passwordLength = 16
	result := make([]byte, passwordLength)
	randomBytes := make([]byte, passwordLength)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	for i := range result {
		result[i] = chars[int(randomBytes[i])%len(chars)]
	}
	return string(result), nil
}

// toNullString converts a string to sql.NullString.
func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
