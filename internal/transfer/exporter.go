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
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// Exporter handles exporting CMS content to JSON format.
type Exporter struct {
	store      *store.Queries
	logger     *slog.Logger
	uploadDir  string
	createFile func(string) (io.WriteCloser, error)

	// afterExportData runs once the export data has been read and before any
	// archive bytes are staged. Tests use it to cancel the request mid-build,
	// which is the only way to reach the checks that refuse to keep staging —
	// or to deliver — an archive whose caller has already been answered.
	afterExportData func()
}

type mediaExportBudget struct {
	files int
	total uint64
}

func (b *mediaExportBudget) reserve(size int64) error {
	if size < 0 {
		return fmt.Errorf("negative media file size %d", size)
	}
	if size > maxZipMediaFileUncompressedBytes {
		return fmt.Errorf("media file exceeds max size (%d bytes)", maxZipMediaFileUncompressedBytes)
	}
	if b.files >= maxZipMediaFiles {
		return fmt.Errorf("archive contains too many media files (%d > %d)", b.files+1, maxZipMediaFiles)
	}
	nextTotal := b.total + uint64(size)
	if nextTotal < b.total || nextTotal > uint64(maxZipMediaTotalUncompressedBytes) {
		return fmt.Errorf("total media size exceeds max size (%d bytes)", maxZipMediaTotalUncompressedBytes)
	}
	b.files++
	b.total = nextTotal
	return nil
}

// NewExporter creates a new Exporter instance.
func NewExporter(queries *store.Queries, logger *slog.Logger) *Exporter {
	return &Exporter{
		store:     queries,
		logger:    logger,
		uploadDir: "./uploads",
		createFile: func(path string) (io.WriteCloser, error) {
			return os.Create(path)
		},
	}
}

// SetUploadDir sets the upload directory for media files.
func (e *Exporter) SetUploadDir(dir string) {
	e.uploadDir = dir
}

// Export generates an ExportData structure based on the provided options.
func (e *Exporter) Export(ctx context.Context, opts ExportOptions) (*ExportData, error) {
	data := &ExportData{
		Version:    ExportVersion,
		ExportedAt: time.Now().UTC(),
		Site:       ExportSite{},
	}

	// Export site configuration
	if opts.IncludeConfig {
		if err := e.exportConfig(ctx, data); err != nil {
			e.logger.Warn("failed to export config", "error", err)
		}
	}

	// Export languages
	if opts.IncludeLanguages {
		if err := e.exportLanguages(ctx, data); err != nil {
			e.logger.Warn("failed to export languages", "error", err)
		}
	}

	// Build lookup maps for reference resolution
	userMap := make(map[int64]string)
	var err error
	if opts.IncludePages || opts.IncludeMedia {
		userMap, err = e.buildIDLookupMap(ctx, exportUser)
		if err != nil {
			return nil, fmt.Errorf("build user reference map: %w", err)
		}
	}

	categoryMap := make(map[int64]string)
	if opts.IncludeCategories {
		categoryMap, err = e.buildIDLookupMap(ctx, exportCategory)
		if err != nil {
			e.logger.Warn("failed to build category map", "error", err)
		}
	}

	mediaMap := make(map[int64]ExportMediaRef)
	if opts.IncludePages {
		mediaMap, err = e.buildMediaMap(ctx)
		if err != nil {
			return nil, fmt.Errorf("build page media reference map: %w", err)
		}
	}

	// Export users
	if opts.IncludeUsers {
		if err := e.exportUsers(ctx, data); err != nil {
			e.logger.Warn("failed to export users", "error", err)
		}
	}

	// Export categories
	if opts.IncludeCategories {
		if err := e.exportCategories(ctx, data, categoryMap); err != nil {
			e.logger.Warn("failed to export categories", "error", err)
		}
	}

	// Export tags
	if opts.IncludeTags {
		if err := e.exportTags(ctx, data); err != nil {
			e.logger.Warn("failed to export tags", "error", err)
		}
	}

	// Export media
	if opts.IncludeMedia {
		if err := e.exportMedia(ctx, data, userMap); err != nil {
			return nil, fmt.Errorf("export media: %w", err)
		}
	}

	// Export pages
	if opts.IncludePages {
		if err := e.exportPages(ctx, data, opts, userMap, mediaMap); err != nil {
			return nil, fmt.Errorf("export pages: %w", err)
		}
	}

	// Export menus
	if opts.IncludeMenus {
		strictPageReferences := !opts.IncludePages
		pageMap := make(map[int64]string, len(data.Pages))
		for _, page := range data.Pages {
			pageMap[page.ID] = page.Slug
		}
		// A menu-only archive does not contain page entities, but its page-backed
		// items can still target pages that already exist at the destination. Keep
		// those references as slugs without adding the pages themselves. When pages
		// are selected, continue using only the exported subset so status filtering
		// cannot leave the archive with references to deliberately omitted pages.
		if strictPageReferences {
			pageMap, err = e.buildIDLookupMap(ctx, exportPage)
			if err != nil {
				return nil, fmt.Errorf("build menu page reference map: %w", err)
			}
		}
		if err := e.exportMenus(ctx, data, pageMap, strictPageReferences); err != nil {
			return nil, fmt.Errorf("export menus: %w", err)
		}
	}

	// Export forms
	if opts.IncludeForms {
		if err := e.exportForms(ctx, data, opts.IncludeSubmissions); err != nil {
			e.logger.Warn("failed to export forms", "error", err)
		}
	}
	if err := validateExportedContentMediaIdentities(data); err != nil {
		return nil, fmt.Errorf("validate exported media references: %w", err)
	}

	return data, nil
}

func validateExportedContentMediaIdentities(data *ExportData) error {
	carried := make(map[string]string, len(data.Media))
	for _, medium := range data.Media {
		if !imaging.IsCanonicalMediaUUID(medium.UUID) {
			return fmt.Errorf("media metadata has invalid media UUID %q", medium.UUID)
		}
		logicalUUID := strings.ToLower(medium.UUID)
		if existing, ok := carried[logicalUUID]; ok && existing != medium.UUID {
			return fmt.Errorf("media UUID %q duplicates logical UUID %q", medium.UUID, existing)
		}
		carried[logicalUUID] = medium.UUID
	}
	spellings := make(map[string]string)
	recordUUID := func(label, mediaUUID string) error {
		if !imaging.IsCanonicalMediaUUID(mediaUUID) {
			return fmt.Errorf("%s has invalid media UUID %q", label, mediaUUID)
		}
		logicalUUID := strings.ToLower(mediaUUID)
		if _, normalizedByImport := carried[logicalUUID]; normalizedByImport {
			return nil
		}
		if existing, ok := spellings[logicalUUID]; ok && existing != mediaUUID {
			return fmt.Errorf("%s media UUID %q conflicts by case with referenced UUID %q", label, mediaUUID, existing)
		}
		spellings[logicalUUID] = mediaUUID
		return nil
	}
	validateValue := func(label, value string) error {
		mediaUUIDs, err := knownMediaUUIDs(value)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		for _, mediaUUID := range mediaUUIDs {
			if err := recordUUID(label, mediaUUID); err != nil {
				return err
			}
		}
		return nil
	}

	for _, page := range data.Pages {
		if page.FeaturedImage != nil {
			if err := recordUUID(fmt.Sprintf("page %q featured image", page.Slug), page.FeaturedImage.UUID); err != nil {
				return err
			}
		}
		if page.SEO != nil && page.SEO.OgImage != nil {
			if err := recordUUID(fmt.Sprintf("page %q OG image", page.Slug), page.SEO.OgImage.UUID); err != nil {
				return err
			}
		}
		values := []struct {
			label string
			value string
		}{
			{fmt.Sprintf("page %q body", page.Slug), page.Body},
			{fmt.Sprintf("page %q video URL", page.Slug), page.VideoURL},
		}
		if page.SEO != nil {
			values = append(values, struct {
				label string
				value string
			}{fmt.Sprintf("page %q canonical URL", page.Slug), page.SEO.CanonicalURL})
		}
		for _, value := range values {
			if err := validateValue(value.label, value.value); err != nil {
				return err
			}
		}
	}
	for _, category := range data.Categories {
		if err := validateValue(fmt.Sprintf("category %q description", category.Slug), category.Description); err != nil {
			return err
		}
	}
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
	for _, form := range data.Forms {
		values := []struct {
			label string
			value string
		}{
			{fmt.Sprintf("form %q description", form.Slug), form.Description},
			{fmt.Sprintf("form %q success message", form.Slug), form.SuccessMessage},
		}
		for _, field := range form.Fields {
			values = append(values,
				struct {
					label string
					value string
				}{fmt.Sprintf("form %q field %q placeholder", form.Slug, field.Name), field.Placeholder},
				struct {
					label string
					value string
				}{fmt.Sprintf("form %q field %q help text", form.Slug, field.Name), field.HelpText},
				struct {
					label string
					value string
				}{fmt.Sprintf("form %q field %q options", form.Slug, field.Name), field.Options},
				struct {
					label string
					value string
				}{fmt.Sprintf("form %q field %q validation", form.Slug, field.Name), field.Validation},
			)
		}
		for _, submission := range form.Submissions {
			values = append(values, struct {
				label string
				value string
			}{fmt.Sprintf("form %q submission", form.Slug), submission.Data})
		}
		for _, value := range values {
			if err := validateValue(value.label, value.value); err != nil {
				return err
			}
		}
	}
	configKeys := make([]string, 0, len(data.Config))
	for key := range data.Config {
		configKeys = append(configKeys, key)
	}
	sort.Strings(configKeys)
	for _, key := range configKeys {
		if err := validateValue(fmt.Sprintf("config %q", key), data.Config[key]); err != nil {
			return err
		}
	}
	return nil
}

// ExportToWriter writes the export as JSON to the provided writer.
func (e *Exporter) ExportToWriter(ctx context.Context, opts ExportOptions, w io.Writer) error {
	data, err := e.Export(ctx, opts)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// ExportToFile writes the export as JSON to a file.
func (e *Exporter) ExportToFile(ctx context.Context, opts ExportOptions, path string) error {
	f, err := e.openExportFile(path)
	if err != nil {
		return err
	}
	writeErr := e.ExportToWriter(ctx, opts, f)
	closeErr := f.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close export file: %w", closeErr)
	}
	return errors.Join(writeErr, closeErr)
}

// ExportWithMedia creates a zip archive containing export.json and media files.
// The complete archive is staged and closed before a byte reaches w, so a
// missing later original or central-directory failure cannot leave an HTTP
// response containing a partial archive.
func (e *Exporter) ExportWithMedia(ctx context.Context, opts ExportOptions, w io.Writer) error {
	staged, err := os.CreateTemp("", "ocms-media-export-*.zip")
	if err != nil {
		return fmt.Errorf("create staged media export: %w", err)
	}
	stagedPath := staged.Name()
	defer func() { _ = os.Remove(stagedPath) }()

	writeErr := e.writeMediaArchive(ctx, opts, staged)
	closeErr := staged.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close staged media export: %w", closeErr)
	}
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	stagedInfo, err := os.Stat(stagedPath)
	if err != nil {
		return fmt.Errorf("inspect completed media export: %w", err)
	}
	if err := validateZipArchiveSize(stagedInfo.Size()); err != nil {
		return err
	}

	// Staging can outlast the request. Once the server's timeout has fired, the
	// handler's ResponseWriter has already carried a 503 to the client, and
	// copying the archive into it now would append archive bytes to that
	// response. The archive is complete and correct — it just has no reader.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("media export canceled before delivery: %w", err)
	}

	// #nosec G304 -- stagedPath is created by os.CreateTemp above and never
	// accepts caller-controlled path components.
	complete, err := os.Open(stagedPath)
	if err != nil {
		return fmt.Errorf("reopen completed media export: %w", err)
	}
	_, copyErr := copyArchiveWithContext(ctx, w, complete)
	if copyErr != nil {
		copyErr = fmt.Errorf("copy staged media export: %w", copyErr)
	}
	readCloseErr := complete.Close()
	if readCloseErr != nil {
		readCloseErr = fmt.Errorf("close completed media export: %w", readCloseErr)
	}
	return errors.Join(copyErr, readCloseErr)
}

// archiveCopyChunkBytes bounds how much of a completed archive is written
// between cancellation checks. Large enough that the checks cost nothing on a
// healthy transfer, small enough that a slow client cannot hold the writer for
// long past the deadline.
const archiveCopyChunkBytes = 256 * 1024

// copyArchiveWithContext writes src to dst in chunks, stopping at the first
// chunk boundary after ctx is done.
//
// A single io.Copy cannot be interrupted. Delivering a large archive to a slow
// client can outlast the request deadline mid-copy, and every byte written
// after that goes through a ResponseWriter the timeout middleware has already
// finished with — appending archive data to the 503 it sent. Checking once
// before the copy only covers archives that were already late when they
// started.
func copyArchiveWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	var written int64
	buffer := make([]byte, archiveCopyChunkBytes)
	for {
		if err := ctx.Err(); err != nil {
			return written, fmt.Errorf("media export canceled after %d bytes: %w", written, err)
		}
		read, readErr := src.Read(buffer)
		if read > 0 {
			out, writeErr := dst.Write(buffer[:read])
			written += int64(out)
			if writeErr != nil {
				return written, writeErr
			}
			if out != read {
				return written, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func (e *Exporter) writeMediaArchive(ctx context.Context, opts ExportOptions, w io.Writer) (resultErr error) {
	if opts.IncludeMediaFiles && !opts.IncludeMedia {
		return errors.New("media files cannot be exported without media metadata")
	}
	// Generate export data
	data, err := e.Export(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to generate export: %w", err)
	}
	if e.afterExportData != nil {
		e.afterExportData()
	}

	// Create zip writer
	zipWriter := zip.NewWriter(w)
	defer func() {
		if err := zipWriter.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close zip archive: %w", err))
		}
	}()

	// Add media files if requested
	if opts.IncludeMediaFiles && len(data.Media) > 0 {
		budget := &mediaExportBudget{}
		for i := range data.Media {
			// A large library can take longer than the request allows. Copying
			// file after file into an archive nobody will read wastes the disk
			// and delays the staged file's cleanup, so stop at the first item
			// once the caller is gone.
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("media export canceled: %w", err)
			}
			mediaItem := &data.Media[i]
			if err := e.addMediaToZip(zipWriter, mediaItem, budget); err != nil {
				return fmt.Errorf("add media %q to zip: %w", mediaItem.UUID, err)
			}
		}
	}

	jsonPayload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode export.json: %w", err)
	}
	jsonPayload = append(jsonPayload, '\n')
	if len(jsonPayload) > maxZipExportJSONUncompressedBytes {
		return fmt.Errorf("export.json exceeds max size (%d bytes)", maxZipExportJSONUncompressedBytes)
	}

	// Write export.json only after its complete encoded size has been proven.
	jsonWriter, err := zipWriter.Create("export.json")
	if err != nil {
		return fmt.Errorf("failed to create export.json in zip: %w", err)
	}
	if written, err := jsonWriter.Write(jsonPayload); err != nil {
		return fmt.Errorf("failed to write export.json: %w", err)
	} else if written != len(jsonPayload) {
		return fmt.Errorf("failed to write export.json: %w", io.ErrShortWrite)
	}

	return nil
}

// ExportWithMediaToFile creates a zip archive file containing export.json and media files.
func (e *Exporter) ExportWithMediaToFile(ctx context.Context, opts ExportOptions, path string) error {
	dir := filepath.Dir(path)
	staged, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create staged export file: %w", err)
	}
	stagedPath := staged.Name()
	defer func() { _ = os.Remove(stagedPath) }()
	writeErr := e.ExportWithMedia(ctx, opts, staged)
	closeErr := staged.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close export file: %w", closeErr)
	}
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(stagedPath, path); err != nil {
		return fmt.Errorf("publish media export: %w", err)
	}
	return nil
}

func (e *Exporter) openExportFile(filePath string) (io.WriteCloser, error) {
	if e.createFile != nil {
		return e.createFile(filePath)
	}
	return os.Create(filePath)
}

// addMediaToZip adds a media file and its variants to the zip archive.
func (e *Exporter) addMediaToZip(zipWriter *zip.Writer, media *ExportMedia, budget *mediaExportBudget) error {
	if !imaging.IsCanonicalMediaUUID(media.UUID) {
		return fmt.Errorf("invalid canonical media UUID %q", media.UUID)
	}
	if err := validateZipPathSegment(media.Filename); err != nil {
		return fmt.Errorf("invalid media filename %q: %w", media.Filename, err)
	}
	// Add original file
	originalPath := filepath.Join(e.uploadDir, "originals", media.UUID, media.Filename)
	zipPath := path.Join("media", "originals", media.UUID, media.Filename)

	if err := e.addFileToZip(zipWriter, originalPath, zipPath, media.Size, media, budget); err != nil {
		return fmt.Errorf("failed to add original: %w", err)
	}
	media.FilePath = zipPath

	// Add variants
	for i := range media.Variants {
		variant := &media.Variants[i]
		if _, supported := model.ImageVariants[variant.Type]; !supported {
			return fmt.Errorf("unsupported media variant type %q", variant.Type)
		}
		variantPath := filepath.Join(e.uploadDir, variant.Type, media.UUID, media.Filename)
		variantZipPath := path.Join("media", variant.Type, media.UUID, media.Filename)

		if err := preflightMediaFile(variantPath, variant.Size); err != nil {
			return fmt.Errorf("preflight %s variant: %w", variant.Type, err)
		}
		var imageMetadata *ExportMedia
		if isProcessableImageMimeType(media.MimeType) {
			imageMetadata = &ExportMedia{
				MimeType: media.MimeType,
				Width:    &variant.Width,
				Height:   &variant.Height,
			}
		}
		if err := e.addFileToZip(zipWriter, variantPath, variantZipPath, variant.Size, imageMetadata, budget); err != nil {
			// Once CreateHeader succeeds the entry exists. Any later error must
			// abort the staged archive rather than emit an undeclared partial file.
			return fmt.Errorf("failed to add %s variant: %w", variant.Type, err)
		}
		variant.FilePath = variantZipPath
	}

	return nil
}

// addFileToZip adds a single file to the zip archive.
func (e *Exporter) addFileToZip(
	zipWriter *zip.Writer,
	srcPath, zipPath string,
	expectedSize int64,
	imageMetadata *ExportMedia,
	budget *mediaExportBudget,
) (resultErr error) {
	// Open source file
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %w", srcPath, err)
	}
	defer func() {
		if err := srcFile.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close source file %s: %w", srcPath, err))
		}
	}()

	// Get file info for header
	info, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source file %s is not regular", srcPath)
	}
	if info.Size() != expectedSize {
		return fmt.Errorf("source file size %d does not match metadata size %d", info.Size(), expectedSize)
	}
	if err := budget.reserve(expectedSize); err != nil {
		return err
	}

	var source io.Reader = srcFile
	if imageMetadata != nil && isProcessableImageMimeType(imageMetadata.MimeType) {
		// Validate and publish the exact same bounded bytes. Reopening or rewinding
		// the source would leave a race where the checked inode contents could be
		// replaced before ZIP publication.
		payload, err := io.ReadAll(io.LimitReader(srcFile, int64(maxZipMediaFileUncompressedBytes)+1))
		if err != nil {
			return fmt.Errorf("failed to read image source: %w", err)
		}
		if int64(len(payload)) != expectedSize {
			return fmt.Errorf("source file changed while exporting: read %d bytes, expected %d", len(payload), expectedSize)
		}
		width, height, detectedMimeType, err := imaging.ValidateImage(bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("validate image source: %w", err)
		}
		if detectedMimeType != imageMetadata.MimeType {
			return fmt.Errorf("image MIME type %q does not match metadata MIME type %q",
				detectedMimeType, imageMetadata.MimeType)
		}
		if imageMetadata.Width != nil && int64(width) != *imageMetadata.Width {
			return fmt.Errorf("image width %d does not match metadata width %d", width, *imageMetadata.Width)
		}
		if imageMetadata.Height != nil && int64(height) != *imageMetadata.Height {
			return fmt.Errorf("image height %d does not match metadata height %d", height, *imageMetadata.Height)
		}
		source = bytes.NewReader(payload)
	}

	// Create header
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return fmt.Errorf("failed to create file header: %w", err)
	}
	header.Name = zipPath
	header.Method = zip.Deflate

	// Create file in zip
	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("failed to create zip entry: %w", err)
	}

	// Copy file content
	written, err := io.Copy(writer, io.LimitReader(source, int64(maxZipMediaFileUncompressedBytes)+1))
	if err != nil {
		return fmt.Errorf("failed to write file content: %w", err)
	}
	if written != expectedSize {
		return fmt.Errorf("source file changed while exporting: wrote %d bytes, expected %d", written, expectedSize)
	}

	return nil
}

func isProcessableImageMimeType(mimeType string) bool {
	switch mimeType {
	case model.MimeTypeJPEG, model.MimeTypePNG, model.MimeTypeGIF, model.MimeTypeWebP:
		return true
	default:
		return false
	}
}

func processableImageMimeTypeForFilename(filename string) (string, bool) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".jpg", ".jpeg":
		return model.MimeTypeJPEG, true
	case ".png":
		return model.MimeTypePNG, true
	case ".gif":
		return model.MimeTypeGIF, true
	case ".webp":
		return model.MimeTypeWebP, true
	default:
		return "", false
	}
}

func preflightMediaFile(filePath string, expectedSize int64) (resultErr error) {
	// #nosec G304 -- filePath is assembled from the configured uploads root,
	// a canonical media UUID, a fixed storage directory, and a validated filename.
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("file is not regular")
	}
	if info.Size() != expectedSize {
		return fmt.Errorf("file size %d does not match metadata size %d", info.Size(), expectedSize)
	}
	if expectedSize < 0 || expectedSize > maxZipMediaFileUncompressedBytes {
		return fmt.Errorf("file size %d is outside archive limits", expectedSize)
	}
	return nil
}

// exportConfig exports site configuration.
func (e *Exporter) exportConfig(ctx context.Context, data *ExportData) error {
	configs, err := e.store.ListConfig(ctx)
	if err != nil {
		return err
	}

	data.Config = make(map[string]string)
	for _, cfg := range configs {
		data.Config[cfg.Key] = cfg.Value

		// Populate site info
		switch cfg.Key {
		case "site_name":
			data.Site.Name = cfg.Value
		case "site_description":
			data.Site.Description = cfg.Value
		case "site_url":
			data.Site.URL = cfg.Value
		}
	}

	return nil
}

// exportLanguages exports all languages.
func (e *Exporter) exportLanguages(ctx context.Context, data *ExportData) error {
	languages, err := e.store.ListLanguages(ctx)
	if err != nil {
		return err
	}

	data.Languages = make([]ExportLanguage, 0, len(languages))
	for _, lang := range languages {
		data.Languages = append(data.Languages, ExportLanguage{
			Code:       lang.Code,
			Name:       lang.Name,
			NativeName: lang.NativeName,
			IsDefault:  lang.IsDefault,
			IsActive:   lang.IsActive,
			Direction:  lang.Direction,
			Position:   lang.Position,
		})
	}

	return nil
}

// exportUsers exports all users (without passwords).
func (e *Exporter) exportUsers(ctx context.Context, data *ExportData) error {
	// Use a reasonable limit for users
	users, err := e.store.ListUsers(ctx, store.ListUsersParams{
		Limit:  1000,
		Offset: 0,
	})
	if err != nil {
		return err
	}

	data.Users = make([]ExportUser, 0, len(users))
	for _, user := range users {
		data.Users = append(data.Users, ExportUser{
			Email:     user.Email,
			Name:      user.Name,
			Role:      user.Role,
			CreatedAt: user.CreatedAt,
		})
	}

	return nil
}

// exportCategories exports all categories with hierarchy.
func (e *Exporter) exportCategories(ctx context.Context, data *ExportData, categoryMap map[int64]string) error {
	categories, err := e.store.ListCategories(ctx)
	if err != nil {
		return err
	}

	data.Categories = make([]ExportCategory, 0, len(categories))
	for _, cat := range categories {
		exportCat := ExportCategory{
			ID:           cat.ID,
			Name:         cat.Name,
			Slug:         cat.Slug,
			Description:  nullStringToString(cat.Description),
			Position:     cat.Position,
			LanguageCode: cat.LanguageCode,
			CreatedAt:    cat.CreatedAt,
			UpdatedAt:    cat.UpdatedAt,
		}

		// Resolve parent category slug
		if cat.ParentID.Valid {
			if parentSlug, ok := categoryMap[cat.ParentID.Int64]; ok {
				exportCat.ParentSlug = parentSlug
			}
		}

		// Get translations for this category
		translations, err := e.store.GetTranslationsForEntity(ctx, store.GetTranslationsForEntityParams{
			EntityType: "category",
			EntityID:   cat.ID,
		})
		if err == nil && len(translations) > 0 {
			exportCat.Translations = make(map[string]int64)
			for _, t := range translations {
				exportCat.Translations[t.LanguageCode] = t.TranslationID
			}
		}

		data.Categories = append(data.Categories, exportCat)
	}

	return nil
}

// exportTags exports all tags.
func (e *Exporter) exportTags(ctx context.Context, data *ExportData) error {
	tags, err := e.store.ListAllTags(ctx)
	if err != nil {
		return err
	}

	data.Tags = make([]ExportTag, 0, len(tags))
	for _, tag := range tags {
		exportTag := ExportTag{
			ID:           tag.ID,
			Name:         tag.Name,
			Slug:         tag.Slug,
			LanguageCode: tag.LanguageCode,
			CreatedAt:    tag.CreatedAt,
			UpdatedAt:    tag.UpdatedAt,
		}

		// Get translations for this tag
		translations, err := e.store.GetTranslationsForEntity(ctx, store.GetTranslationsForEntityParams{
			EntityType: "tag",
			EntityID:   tag.ID,
		})
		if err == nil && len(translations) > 0 {
			exportTag.Translations = make(map[string]int64)
			for _, t := range translations {
				exportTag.Translations[t.LanguageCode] = t.TranslationID
			}
		}

		data.Tags = append(data.Tags, exportTag)
	}

	return nil
}

// exportMedia exports all media metadata.
func (e *Exporter) exportMedia(ctx context.Context, data *ExportData, userMap map[int64]string) error {
	// Build folder path map
	folderPaths, err := e.buildFolderPathMap(ctx)
	if err != nil {
		return fmt.Errorf("build media folder paths: %w", err)
	}

	media, err := listAllMediaForLookup(ctx, e.store)
	if err != nil {
		return err
	}
	if err := validateExportMediaIdentities(media); err != nil {
		return err
	}

	data.Media = make([]ExportMedia, 0, len(media))
	for _, m := range media {
		exportMedia := ExportMedia{
			UUID:         m.Uuid,
			Filename:     m.Filename,
			MimeType:     m.MimeType,
			Size:         m.Size,
			Alt:          nullStringToString(m.Alt),
			Caption:      nullStringToString(m.Caption),
			UploadedBy:   userMap[m.UploadedBy],
			LanguageCode: m.LanguageCode,
			CreatedAt:    m.CreatedAt,
		}

		if m.Width.Valid {
			exportMedia.Width = new(m.Width.Int64)
		}
		if m.Height.Valid {
			exportMedia.Height = new(m.Height.Int64)
		}

		if m.FolderID.Valid {
			exportMedia.FolderPath = folderPaths[m.FolderID.Int64]
		}

		// Get variants
		variants, err := e.store.GetMediaVariants(ctx, m.ID)
		if err != nil {
			return fmt.Errorf("read variants for media %q: %w", m.Uuid, err)
		}
		if len(variants) > 0 {
			exportMedia.Variants = make([]ExportVariant, 0, len(variants))
			for _, v := range variants {
				exportMedia.Variants = append(exportMedia.Variants, ExportVariant{
					Type:   v.Type,
					Width:  v.Width,
					Height: v.Height,
					Size:   v.Size,
				})
			}
		}
		if validationErrors := validateTransferMediaMetadata(exportMedia); len(validationErrors) > 0 {
			return fmt.Errorf("media %q is not exportable: %s", m.Uuid, strings.Join(validationErrors, "; "))
		}

		data.Media = append(data.Media, exportMedia)
	}

	return nil
}

// exportPages exports pages based on options.
func (e *Exporter) exportPages(
	ctx context.Context,
	data *ExportData,
	opts ExportOptions,
	userMap map[int64]string,
	mediaMap map[int64]ExportMediaRef,
) error {
	var pages []store.Page
	var err error

	// Fetch pages based on status filter
	switch opts.PageStatus {
	case "published":
		pages, err = e.store.ListPublishedPages(ctx, store.ListPublishedPagesParams{
			Limit:  100000,
			Offset: 0,
		})
	case "draft":
		pages, err = e.store.ListPagesByStatus(ctx, store.ListPagesByStatusParams{
			Status: "draft",
			Limit:  100000,
			Offset: 0,
		})
	default: // "all"
		pages, err = e.store.ListPages(ctx, store.ListPagesParams{
			Limit:  100000,
			Offset: 0,
		})
	}
	if err != nil {
		return err
	}

	includedPageIDs := make(map[int64]struct{}, len(pages))
	for _, page := range pages {
		includedPageIDs[page.ID] = struct{}{}
	}
	data.Pages = make([]ExportPage, 0, len(pages))
	for _, page := range pages {
		exportPage := ExportPage{
			ID:          page.ID,
			Title:       page.Title,
			Slug:        page.Slug,
			Body:        page.Body,
			Status:      page.Status,
			AuthorEmail: userMap[page.AuthorID],
			CreatedAt:   page.CreatedAt,
			UpdatedAt:   page.UpdatedAt,
		}

		// Handle published_at
		if page.PublishedAt.Valid {
			exportPage.PublishedAt = new(page.PublishedAt.Time)
		}

		// Handle scheduled_at
		if page.ScheduledAt.Valid {
			exportPage.ScheduledAt = new(page.ScheduledAt.Time)
		}

		// Handle video URL
		exportPage.VideoURL = page.VideoUrl
		exportPage.VideoTitle = page.VideoTitle

		// Handle language
		exportPage.LanguageCode = page.LanguageCode

		// Get categories for page
		categories, err := e.store.GetCategoriesForPage(ctx, page.ID)
		if err == nil && len(categories) > 0 {
			exportPage.Categories = make([]string, 0, len(categories))
			for _, cat := range categories {
				exportPage.Categories = append(exportPage.Categories, cat.Slug)
			}
		}

		// Get tags for page
		tags, err := e.store.GetTagsForPage(ctx, page.ID)
		if err == nil && len(tags) > 0 {
			exportPage.Tags = make([]string, 0, len(tags))
			for _, tag := range tags {
				exportPage.Tags = append(exportPage.Tags, tag.Slug)
			}
		}

		// Handle SEO metadata
		if page.MetaTitle != "" || page.MetaDescription != "" || page.MetaKeywords != "" ||
			page.NoIndex != 0 || page.NoFollow != 0 || page.CanonicalUrl != "" || page.OgImageID.Valid {
			exportPage.SEO = &ExportPageSEO{
				MetaTitle:       page.MetaTitle,
				MetaDescription: page.MetaDescription,
				MetaKeywords:    page.MetaKeywords,
				NoIndex:         page.NoIndex != 0,
				NoFollow:        page.NoFollow != 0,
				CanonicalURL:    page.CanonicalUrl,
			}

			// Handle OG image
			if page.OgImageID.Valid {
				if ref, ok := mediaMap[page.OgImageID.Int64]; ok {
					exportPage.SEO.OgImage = &ref
				}
			}
		}

		// Handle featured image
		if page.FeaturedImageID.Valid {
			if ref, ok := mediaMap[page.FeaturedImageID.Int64]; ok {
				exportPage.FeaturedImage = &ref
			}
		}

		// Get translations for this page
		translations, err := e.store.GetTranslationsForEntity(ctx, store.GetTranslationsForEntityParams{
			EntityType: "page",
			EntityID:   page.ID,
		})
		if err == nil && len(translations) > 0 {
			exportPage.Translations = make(map[string]int64)
			for _, t := range translations {
				if _, included := includedPageIDs[t.TranslationID]; !included {
					continue
				}
				exportPage.Translations[t.LanguageCode] = t.TranslationID
			}
		}

		data.Pages = append(data.Pages, exportPage)
	}

	return nil
}

// exportMenus exports all menus with their items.
func (e *Exporter) exportMenus(
	ctx context.Context,
	data *ExportData,
	pageMap map[int64]string,
	strictPageReferences bool,
) error {
	menus, err := e.store.ListMenus(ctx)
	if err != nil {
		return err
	}

	data.Menus = make([]ExportMenu, 0, len(menus))
	for _, menu := range menus {
		exportMenu := ExportMenu{
			ID:           menu.ID,
			Name:         menu.Name,
			Slug:         menu.Slug,
			LanguageCode: menu.LanguageCode,
			CreatedAt:    menu.CreatedAt,
			UpdatedAt:    menu.UpdatedAt,
		}

		items, err := e.store.ListMenuItems(ctx, menu.ID)
		if err != nil {
			return fmt.Errorf("list items for menu %q: %w", menu.Slug, err)
		}
		if len(items) != 0 {
			tree, treeErr := buildMenuItemTree(menu.ID, items)
			if treeErr != nil {
				return fmt.Errorf("validate items for menu %q: %w", menu.Slug, treeErr)
			}
			exportMenu.Items = make([]ExportMenuItem, 0, len(tree.roots))
			for _, item := range tree.roots {
				exportItem, include, exportErr := e.exportMenuItem(
					item, tree.children, pageMap, strictPageReferences)
				if exportErr != nil {
					return fmt.Errorf("export item %d for menu %q: %w", item.ID, menu.Slug, exportErr)
				}
				if include {
					exportMenu.Items = append(exportMenu.Items, exportItem)
				}
			}
		}

		data.Menus = append(data.Menus, exportMenu)
	}

	return nil
}

// exportMenuItem exports a menu item recursively (for nested menus).
func (e *Exporter) exportMenuItem(
	item store.MenuItem,
	childrenByParent map[int64][]store.MenuItem,
	pageMap map[int64]string,
	strictPageReferences bool,
) (ExportMenuItem, bool, error) {
	exportItem := ExportMenuItem{
		ID:       item.ID,
		Title:    item.Title,
		URL:      nullStringToString(item.Url),
		Target:   nullStringToString(item.Target),
		CSSClass: nullStringToString(item.CssClass),
		IsActive: item.IsActive,
		Position: item.Position,
	}

	// Resolve page slug
	if item.PageID.Valid {
		pageSlug, included := pageMap[item.PageID.Int64]
		if !included {
			if strictPageReferences {
				return ExportMenuItem{}, false, fmt.Errorf(
					"menu item %d references missing page %d", item.ID, item.PageID.Int64)
			}
			if exportItem.URL == "" {
				return ExportMenuItem{}, false, nil
			}
		}
		exportItem.PageSlug = pageSlug
	}

	children := childrenByParent[item.ID]
	if len(children) > 0 {
		exportItem.Children = make([]ExportMenuItem, 0, len(children))
		for _, child := range children {
			exportedChild, include, exportErr := e.exportMenuItem(
				child, childrenByParent, pageMap, strictPageReferences)
			if exportErr != nil {
				return ExportMenuItem{}, false, exportErr
			}
			if include {
				exportItem.Children = append(exportItem.Children, exportedChild)
			}
		}
	}

	return exportItem, true, nil
}

type menuItemTree struct {
	roots    []store.MenuItem
	children map[int64][]store.MenuItem
}

// buildMenuItemTree validates that every row belongs to one acyclic menu tree
// before export. This prevents cross-menu parent references and disconnected
// cycles from being silently relocated or omitted.
func buildMenuItemTree(menuID int64, items []store.MenuItem) (menuItemTree, error) {
	tree := menuItemTree{children: make(map[int64][]store.MenuItem)}
	itemsByID := make(map[int64]store.MenuItem, len(items))
	for _, item := range items {
		if item.MenuID != menuID {
			return menuItemTree{}, fmt.Errorf(
				"menu item %d belongs to menu %d, not menu %d", item.ID, item.MenuID, menuID)
		}
		if _, duplicate := itemsByID[item.ID]; duplicate {
			return menuItemTree{}, fmt.Errorf("duplicate menu item ID %d", item.ID)
		}
		itemsByID[item.ID] = item
	}

	for _, item := range items {
		if !item.ParentID.Valid {
			tree.roots = append(tree.roots, item)
			continue
		}
		parentID := item.ParentID.Int64
		if _, found := itemsByID[parentID]; !found {
			return menuItemTree{}, fmt.Errorf(
				"menu item %d references missing or foreign parent %d", item.ID, parentID)
		}
		tree.children[parentID] = append(tree.children[parentID], item)
	}

	states := make(map[int64]uint8, len(items))
	var visit func(int64) error
	visit = func(itemID int64) error {
		switch states[itemID] {
		case 1:
			return fmt.Errorf("menu item hierarchy contains a cycle at item %d", itemID)
		case 2:
			return nil
		}
		states[itemID] = 1
		for _, child := range tree.children[itemID] {
			if err := visit(child.ID); err != nil {
				return err
			}
		}
		states[itemID] = 2
		return nil
	}
	for itemID := range itemsByID {
		if err := visit(itemID); err != nil {
			return menuItemTree{}, err
		}
	}

	sortMenuItems := func(siblings []store.MenuItem) {
		sort.Slice(siblings, func(left, right int) bool {
			if siblings[left].Position != siblings[right].Position {
				return siblings[left].Position < siblings[right].Position
			}
			return siblings[left].ID < siblings[right].ID
		})
	}
	sortMenuItems(tree.roots)
	for parentID := range tree.children {
		sortMenuItems(tree.children[parentID])
	}

	accounted := 0
	var countTree func([]store.MenuItem)
	countTree = func(siblings []store.MenuItem) {
		for _, item := range siblings {
			accounted++
			countTree(tree.children[item.ID])
		}
	}
	countTree(tree.roots)
	if accounted != len(items) {
		return menuItemTree{}, fmt.Errorf(
			"menu item hierarchy accounts for %d of %d rows", accounted, len(items))
	}

	return tree, nil
}

// exportForms exports all forms.
func (e *Exporter) exportForms(ctx context.Context, data *ExportData, includeSubmissions bool) error {
	forms, err := e.store.ListForms(ctx, store.ListFormsParams{
		Limit:  10000,
		Offset: 0,
	})
	if err != nil {
		return err
	}

	data.Forms = make([]ExportForm, 0, len(forms))
	for _, form := range forms {
		exportForm := ExportForm{
			ID:             form.ID,
			Name:           form.Name,
			Slug:           form.Slug,
			Title:          form.Title,
			Description:    nullStringToString(form.Description),
			SuccessMessage: nullStringToString(form.SuccessMessage),
			EmailTo:        nullStringToString(form.EmailTo),
			IsActive:       form.IsActive,
			LanguageCode:   form.LanguageCode,
			CreatedAt:      form.CreatedAt,
			UpdatedAt:      form.UpdatedAt,
		}

		// Get form fields
		fields, err := e.store.GetFormFields(ctx, form.ID)
		if err == nil && len(fields) > 0 {
			exportForm.Fields = make([]ExportFormField, 0, len(fields))
			for _, field := range fields {
				exportForm.Fields = append(exportForm.Fields, ExportFormField{
					Type:        field.Type,
					Name:        field.Name,
					Label:       field.Label,
					Placeholder: nullStringToString(field.Placeholder),
					HelpText:    nullStringToString(field.HelpText),
					Options:     nullStringToString(field.Options),
					Validation:  nullStringToString(field.Validation),
					IsRequired:  field.IsRequired,
					Position:    field.Position,
				})
			}
		}

		// Get submissions if requested
		if includeSubmissions {
			submissions, err := e.store.GetFormSubmissions(ctx, store.GetFormSubmissionsParams{
				FormID: form.ID,
				Limit:  100000,
				Offset: 0,
			})
			if err == nil && len(submissions) > 0 {
				exportForm.Submissions = make([]ExportFormSubmission, 0, len(submissions))
				for _, sub := range submissions {
					exportForm.Submissions = append(exportForm.Submissions, ExportFormSubmission{
						Data:      sub.Data,
						IPAddress: nullStringToString(sub.IpAddress),
						UserAgent: nullStringToString(sub.UserAgent),
						IsRead:    sub.IsRead,
						CreatedAt: sub.CreatedAt,
					})
				}
			}
		}

		translations, err := e.store.GetTranslationsForEntity(ctx, store.GetTranslationsForEntityParams{
			EntityType: "form",
			EntityID:   form.ID,
		})
		if err == nil && len(translations) > 0 {
			exportForm.Translations = make(map[string]int64, len(translations))
			for _, translation := range translations {
				exportForm.Translations[translation.LanguageCode] = translation.TranslationID
			}
		}

		data.Forms = append(data.Forms, exportForm)
	}

	return nil
}

// Helper methods for building lookup maps

// buildIDToStringMap is a generic helper that builds a map from ID to string value.
func buildIDToStringMap[T any](items []T, idFn func(T) int64, valueFn func(T) string) map[int64]string {
	m := make(map[int64]string, len(items))
	for _, item := range items {
		m[idFn(item)] = valueFn(item)
	}
	return m
}

// exportEntityType defines the type of entity for building export lookup maps.
type exportEntityType int

const (
	exportUser exportEntityType = iota
	exportCategory
	exportLanguage
	exportPage
)

// buildIDLookupMap builds an ID-to-string lookup map for the specified entity type.
func (e *Exporter) buildIDLookupMap(ctx context.Context, entityType exportEntityType) (map[int64]string, error) {
	switch entityType {
	case exportUser:
		users, err := e.store.ListUsers(ctx, store.ListUsersParams{Limit: 10000, Offset: 0})
		if err != nil {
			return nil, err
		}
		return buildIDToStringMap(users, func(u store.User) int64 { return u.ID }, func(u store.User) string { return u.Email }), nil
	case exportCategory:
		categories, err := e.store.ListCategories(ctx)
		if err != nil {
			return nil, err
		}
		return buildIDToStringMap(categories, func(c store.Category) int64 { return c.ID }, func(c store.Category) string { return c.Slug }), nil
	case exportLanguage:
		languages, err := e.store.ListLanguages(ctx)
		if err != nil {
			return nil, err
		}
		return buildIDToStringMap(languages, func(l store.Language) int64 { return l.ID }, func(l store.Language) string { return l.Code }), nil
	case exportPage:
		pages, err := listAllPagesForLookup(ctx, e.store)
		if err != nil {
			return nil, err
		}
		return buildIDToStringMap(pages, func(p store.Page) int64 { return p.ID }, func(p store.Page) string { return p.Slug }), nil
	default:
		return make(map[int64]string), nil
	}
}

// buildMediaMap creates a map of media ID to media reference.
func (e *Exporter) buildMediaMap(ctx context.Context) (map[int64]ExportMediaRef, error) {
	media, err := listAllMediaForLookup(ctx, e.store)
	if err != nil {
		return nil, err
	}
	mediaMap := make(map[int64]ExportMediaRef, len(media))
	for _, m := range media {
		mediaMap[m.ID] = ExportMediaRef{UUID: m.Uuid, Filename: m.Filename}
	}
	return mediaMap, nil
}

// buildFolderPathMap creates a map of folder ID to full path.
func (e *Exporter) buildFolderPathMap(ctx context.Context) (map[int64]string, error) {
	folders, err := e.store.ListMediaFolders(ctx)
	if err != nil {
		return nil, err
	}

	// Build folder name map first
	folderNames := make(map[int64]string, len(folders))
	parentIDs := make(map[int64]int64, len(folders))
	for _, folder := range folders {
		if err := validateZipPathSegment(folder.Name); err != nil {
			return nil, fmt.Errorf("media folder %d has invalid name %q: %w", folder.ID, folder.Name, err)
		}
		folderNames[folder.ID] = folder.Name
		if folder.ParentID.Valid {
			parentIDs[folder.ID] = folder.ParentID.Int64
		}
	}

	folderPaths := make(map[int64]string, len(folders))
	states := make(map[int64]uint8, len(folders))
	var resolve func(int64) (string, error)
	resolve = func(id int64) (string, error) {
		if resolved, ok := folderPaths[id]; ok {
			return resolved, nil
		}
		switch states[id] {
		case 1:
			return "", fmt.Errorf("media folder hierarchy contains a cycle at folder %d", id)
		case 2:
			return folderPaths[id], nil
		}
		name, exists := folderNames[id]
		if !exists {
			return "", fmt.Errorf("media folder hierarchy references missing folder %d", id)
		}
		states[id] = 1
		resolved := name
		if parentID, hasParent := parentIDs[id]; hasParent {
			parentPath, err := resolve(parentID)
			if err != nil {
				return "", err
			}
			resolved = parentPath + "/" + name
		}
		states[id] = 2
		folderPaths[id] = resolved
		return resolved, nil
	}
	for _, folder := range folders {
		if _, err := resolve(folder.ID); err != nil {
			return nil, err
		}
	}

	return folderPaths, nil
}

// buildFolderPath recursively builds the full folder path.
func buildFolderPath(id int64, names map[int64]string, parents map[int64]int64) string {
	name := names[id]
	if parentID, ok := parents[id]; ok {
		return buildFolderPath(parentID, names, parents) + "/" + name
	}
	return name
}

// nullStringToString converts sql.NullString to string.
func nullStringToString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}
