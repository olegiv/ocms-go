// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/security"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/transfer"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

const (
	maxImportUploadBytes            = transfer.MaxZipArchiveBytes
	maxImportMultipartOverheadBytes = 1 << 20 // 1 MiB for boundaries and form fields
	defaultImportExportUploadDir    = "./uploads"
)

func maxImportRequestBytes(fileLimit int64) int64 {
	return fileLimit + maxImportMultipartOverheadBytes
}

// ImportExportHandler handles import/export routes.
type ImportExportHandler struct {
	db             *sql.DB
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	logger         *slog.Logger
	eventService   *service.EventService
	cacheManager   *cache.Manager
	uploadDir      string

	blockSuspiciousMarkup bool
}

// NewImportExportHandler creates a new ImportExportHandler.
func NewImportExportHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, managers ...*cache.Manager) *ImportExportHandler {
	h := &ImportExportHandler{
		db:             db,
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		logger:         slog.Default(),
		eventService:   service.NewEventService(db),
		uploadDir:      defaultImportExportUploadDir,
	}
	if len(managers) > 0 {
		h.cacheManager = managers[0]
	}
	return h
}

// SetUploadDir configures the media storage root used by transfer imports and
// exports. An empty value retains the same default as the transfer package.
func (h *ImportExportHandler) SetUploadDir(dir string) {
	if dir == "" {
		dir = defaultImportExportUploadDir
	}
	h.uploadDir = dir
}

func (h *ImportExportHandler) newExporter() *transfer.Exporter {
	exporter := transfer.NewExporter(h.queries, h.logger)
	exporter.SetUploadDir(h.configuredUploadDir())
	return exporter
}

func (h *ImportExportHandler) newImporter() *transfer.Importer {
	importer := transfer.NewImporter(h.queries, h.db, h.logger)
	importer.SetUploadDir(h.configuredUploadDir())
	return importer
}

func (h *ImportExportHandler) configuredUploadDir() string {
	if h.uploadDir == "" {
		return defaultImportExportUploadDir
	}
	return h.uploadDir
}

// SetBlockSuspiciousMarkup configures whether imports should reject page
// bodies containing suspicious HTML tokens.
func (h *ImportExportHandler) SetBlockSuspiciousMarkup(block bool) {
	h.blockSuspiciousMarkup = block
}

// ExportFormData holds data for the export form template.
type ExportFormData struct {
	PageStatuses []string
}

// ExportForm handles GET /admin/export - displays the export form.
func (h *ImportExportHandler) ExportForm(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionExportData, redirectAdmin) {
		return
	}

	lang := middleware.GetAdminLang(r)

	pc := buildPageContext(r, h.sessionManager, h.renderer,
		i18n.T(lang, "nav.export"),
		exportBreadcrumbs(lang))
	renderTempl(w, r, adminviews.ExportPage(pc, adminviews.ExportViewData{}))
}

// Export handles POST /admin/export - generates and downloads the export.
func (h *ImportExportHandler) Export(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionExportData, redirectAdmin) {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	// Build export options from form
	opts := transfer.ExportOptions{
		IncludeUsers:       r.FormValue("include_users") == "on",
		IncludePages:       r.FormValue("include_pages") == "on",
		IncludeCategories:  r.FormValue("include_categories") == "on",
		IncludeTags:        r.FormValue("include_tags") == "on",
		IncludeMedia:       r.FormValue("include_media") == "on",
		IncludeMediaFiles:  r.FormValue("include_media_files") == "on",
		IncludeMenus:       r.FormValue("include_menus") == "on",
		IncludeForms:       r.FormValue("include_forms") == "on",
		IncludeSubmissions: r.FormValue("include_submissions") == "on",
		IncludeConfig:      r.FormValue("include_config") == "on",
		IncludeLanguages:   r.FormValue("include_languages") == "on",
		PageStatus:         r.FormValue("page_status"),
	}

	// Default page status if not provided
	if opts.PageStatus == "" {
		opts.PageStatus = "all"
	}

	// Create exporter
	exporter := h.newExporter()

	// Check if we need to include media files (creates zip instead of JSON)
	if opts.IncludeMediaFiles && opts.IncludeMedia {
		// Generate zip filename with current date
		filename := fmt.Sprintf("ocms-export-%s.zip", time.Now().Format("2006-01-02"))

		// Set response headers for zip download
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

		// Export with media to zip
		if err := exporter.ExportWithMedia(r.Context(), opts, w); err != nil {
			logAndHTTPError(w, "Export failed", http.StatusInternalServerError, "export with media failed", "error", err)
			return
		}
	} else {
		// Generate JSON filename with current date
		filename := fmt.Sprintf("ocms-export-%s.json", time.Now().Format("2006-01-02"))

		// Set response headers for JSON download
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

		// Export directly to response writer
		if err := exporter.ExportToWriter(r.Context(), opts, w); err != nil {
			logAndHTTPError(w, "Export failed", http.StatusInternalServerError, "export failed", "error", err)
			return
		}
	}
}

// ImportFormData holds data for the import form template.
type ImportFormData struct {
	ConflictStrategies []ConflictStrategyOption
	ValidationResult   *transfer.ValidationResult
	ImportResult       *transfer.ImportResult
	UploadedData       *transfer.ExportData
	IsZipFile          bool // Whether the uploaded file is a zip archive
	HasMediaFiles      bool // Whether the zip contains media files
}

// ConflictStrategyOption represents a conflict strategy for the dropdown.
type ConflictStrategyOption struct {
	Value       string
	Label       string
	Description string
}

// defaultConflictStrategies returns the default conflict resolution strategies.
func defaultConflictStrategies() []ConflictStrategyOption {
	return []ConflictStrategyOption{
		{Value: "skip", Label: "Skip Existing", Description: "Skip items that already exist"},
		{Value: "overwrite", Label: "Overwrite", Description: "Update existing items with imported data"},
		{Value: "rename", Label: "Rename", Description: "Create with new slug if exists"},
	}
}

// importBreadcrumbs returns the standard breadcrumbs for import pages.
func importBreadcrumbs(lang string) []render.Breadcrumb {
	return []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
		{Label: i18n.T(lang, "nav.import"), URL: "/admin/import", Active: true},
	}
}

func readImportFileContent(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("failed to read file: invalid file size limit")
	}

	limited := io.LimitReader(r, maxBytes+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("file is too large (max %d MB)", maxBytes/(1<<20))
	}

	return content, nil
}

// renderImportPage renders the import template with the given data.
func (h *ImportExportHandler) renderImportPage(w http.ResponseWriter, r *http.Request, _ interface{}, data ImportFormData) {
	lang := middleware.GetAdminLang(r)

	viewData := convertImportViewData(data)

	pc := buildPageContext(r, h.sessionManager, h.renderer,
		i18n.T(lang, "nav.import"),
		importBreadcrumbs(lang))
	renderTempl(w, r, adminviews.ImportPage(pc, viewData))
}

// ImportForm handles GET /admin/import - displays the import form.
func (h *ImportExportHandler) ImportForm(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionImportData, redirectAdmin) {
		return
	}

	user := middleware.GetUser(r)
	h.renderImportPage(w, r, user, ImportFormData{
		ConflictStrategies: defaultConflictStrategies(),
	})
}

// ImportValidate handles POST /admin/import/validate - validates the uploaded file.
func (h *ImportExportHandler) ImportValidate(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionImportData, redirectAdmin) {
		return
	}

	user := middleware.GetUser(r)

	// Cap the archive itself at maxImportUploadBytes while reserving bounded
	// space for multipart boundaries and the import option fields. Applying the
	// file limit to the whole request would reject a valid archive at the limit.
	r.Body = http.MaxBytesReader(w, r.Body, maxImportRequestBytes(maxImportUploadBytes))

	// Parse multipart form (max 100MB for zip files with media)
	if err := r.ParseMultipartForm(maxImportUploadBytes); err != nil {
		h.renderImportError(w, r, user, "Failed to parse form: "+err.Error())
		return
	}

	// Get uploaded file
	file, header, err := r.FormFile("import_file")
	if err != nil {
		h.renderImportError(w, r, user, "Please select a file to import")
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > maxImportUploadBytes {
		h.renderImportError(w, r, user, fmt.Sprintf("File is too large (max %d MB)", maxImportUploadBytes/(1<<20)))
		return
	}

	// Check file extension
	filename := strings.ToLower(header.Filename)
	isZipFile := strings.HasSuffix(filename, ".zip")
	isJSONFile := strings.HasSuffix(filename, ".json")

	if !isZipFile && !isJSONFile {
		h.renderImportError(w, r, user, "Only JSON and ZIP files are supported")
		return
	}

	// Read file content with a hard cap to prevent memory exhaustion.
	content, err := readImportFileContent(file, maxImportUploadBytes)
	if err != nil {
		h.renderImportError(w, r, user, err.Error())
		return
	}

	// Create importer
	importer := h.newImporter()

	var validationResult *transfer.ValidationResult
	var exportData transfer.ExportData
	var hasMediaFiles bool

	if isZipFile {
		// Validate zip file
		validationResult, err = importer.ValidateZipBytes(r.Context(), content)
		if err != nil {
			h.renderImportError(w, r, user, "Validation failed: "+err.Error())
			return
		}

		// Check if zip has media files
		if mediaCount, ok := validationResult.Entities["media_files"]; ok {
			hasMediaFiles = mediaCount > 0
		}

		// Extract export data from zip for display (re-read since validation closes readers)
		exportData, err = h.extractExportDataFromZip(content)
		if err != nil {
			h.logger.Warn("import archive could not be read", "error", err)
			h.renderImportError(w, r, user, importZipUnreadableMessage)
			return
		}
	} else {
		// Parse JSON
		if err := json.Unmarshal(content, &exportData); err != nil {
			h.renderImportError(w, r, user, "Invalid JSON format: "+err.Error())
			return
		}

		// Validate JSON data
		validationResult, err = importer.ValidateData(r.Context(), &exportData)
		if err != nil {
			h.renderImportError(w, r, user, "Validation failed: "+err.Error())
			return
		}
	}

	// Audit suspicious page HTML in uploaded import payload. Validation does
	// not block because operator may import non-page entities only.
	if err := h.applyImportPageSecurityPolicy(r, &exportData, "validate", false); err != nil {
		h.renderImportError(w, r, user, "Validation failed: "+err.Error())
		return
	}

	// Stage the validated payload on disk for the actual import; the session
	// row only carries the file path. ZIP uploads keep their raw bytes, JSON
	// uploads keep the parsed data.
	payload := content
	if !isZipFile {
		payload, err = json.Marshal(exportData)
		if err != nil {
			h.logger.Error("failed to encode import payload", "error", err)
			h.renderImportError(w, r, user, "Failed to store the uploaded file for import")
			return
		}
	}
	if err := h.stageImportPayload(r.Context(), payload); err != nil {
		h.logger.Error("failed to stage import payload", "error", err)
		h.renderImportError(w, r, user, "Failed to store the uploaded file for import")
		return
	}
	h.sessionManager.Put(r.Context(), "import_is_zip", isZipFile)

	h.renderImportPage(w, r, user, ImportFormData{
		ConflictStrategies: defaultConflictStrategies(),
		ValidationResult:   validationResult,
		UploadedData:       &exportData,
		IsZipFile:          isZipFile,
		HasMediaFiles:      hasMediaFiles,
	})
}

// extractExportDataFromZip extracts the ExportData from a zip file bytes.
func (h *ImportExportHandler) extractExportDataFromZip(zipData []byte) (transfer.ExportData, error) {
	var data transfer.ExportData

	reader := bytes.NewReader(zipData)
	zipReader, err := zip.NewReader(reader, int64(len(zipData)))
	if err != nil {
		return data, fmt.Errorf("failed to read zip: %w", err)
	}

	for _, f := range zipReader.File {
		if f.Name == "export.json" {
			rc, err := f.Open()
			if err != nil {
				return data, fmt.Errorf("failed to open export.json: %w", err)
			}

			decoder := json.NewDecoder(rc)
			if err := decoder.Decode(&data); err != nil {
				_ = rc.Close()
				return data, fmt.Errorf("failed to decode export.json: %w", err)
			}
			_ = rc.Close()
			return data, nil
		}
	}

	return data, fmt.Errorf("export.json not found in zip")
}

type suspiciousImportPage struct {
	Slug   string
	Tokens []string
}

func findSuspiciousImportPages(data *transfer.ExportData) []suspiciousImportPage {
	if data == nil || len(data.Pages) == 0 {
		return nil
	}

	matches := make([]suspiciousImportPage, 0, len(data.Pages))
	for _, page := range data.Pages {
		tokens := detectSuspiciousPageHTMLTokens(page.Body)
		if len(tokens) == 0 {
			continue
		}
		slug := strings.TrimSpace(page.Slug)
		if slug == "" {
			slug = "(empty-slug)"
		}
		matches = append(matches, suspiciousImportPage{
			Slug:   slug,
			Tokens: tokens,
		})
	}

	return matches
}

func collectSuspiciousImportTokens(matches []suspiciousImportPage) []string {
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(security.SuspiciousPageHTMLTokens))
	tokens := make([]string, 0, len(security.SuspiciousPageHTMLTokens))
	for _, match := range matches {
		for _, token := range match.Tokens {
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			tokens = append(tokens, token)
		}
	}

	return tokens
}

func collectSuspiciousImportSlugs(matches []suspiciousImportPage, limit int) []string {
	if len(matches) == 0 || limit <= 0 {
		return nil
	}

	if len(matches) < limit {
		limit = len(matches)
	}
	slugs := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		slugs = append(slugs, matches[i].Slug)
	}
	return slugs
}

func (h *ImportExportHandler) applyImportPageSecurityPolicy(r *http.Request, data *transfer.ExportData, stage string, enforce bool) error {
	matches := findSuspiciousImportPages(data)
	if len(matches) == 0 {
		return nil
	}

	if h != nil && h.eventService != nil && r != nil {
		_ = h.eventService.LogSecurityEvent(
			r.Context(),
			model.EventLevelWarning,
			"Suspicious page HTML content detected in import payload",
			middleware.GetUserIDPtr(r),
			middleware.GetClientIP(r),
			middleware.GetRequestURL(r),
			map[string]any{
				"stage":        stage,
				"blocked":      enforce && h.blockSuspiciousMarkup,
				"page_count":   len(matches),
				"sample_slugs": collectSuspiciousImportSlugs(matches, 5),
				"tokens":       collectSuspiciousImportTokens(matches),
			},
		)
	}

	if enforce && h.blockSuspiciousMarkup {
		return errors.New("import contains pages with suspicious HTML markup that is blocked by policy")
	}

	return nil
}

// Import handles POST /admin/import - performs the actual import.
func (h *ImportExportHandler) Import(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionImportData, redirectAdmin) {
		return
	}

	user := middleware.GetUser(r)

	if err := r.ParseForm(); err != nil {
		h.renderImportError(w, r, user, "Invalid form data")
		return
	}

	// Check if this is a zip import
	isZip := h.sessionManager.GetBool(r.Context(), "import_is_zip")

	// Build import options from form
	opts := transfer.ImportOptions{
		DryRun:           r.FormValue("dry_run") == "on",
		ConflictStrategy: transfer.ConflictStrategy(r.FormValue("conflict_strategy")),
		ImportUsers:      r.FormValue("import_users") == "on",
		ImportPages:      r.FormValue("import_pages") == "on",
		ImportCategories: r.FormValue("import_categories") == "on",
		ImportTags:       r.FormValue("import_tags") == "on",
		ImportMedia:      r.FormValue("import_media") == "on",
		ImportMediaFiles: r.FormValue("import_media_files") == "on",
		ImportMenus:      r.FormValue("import_menus") == "on",
		ImportForms:      r.FormValue("import_forms") == "on",
		ImportConfig:     r.FormValue("import_config") == "on",
		ImportLanguages:  r.FormValue("import_languages") == "on",
	}

	// Default conflict strategy
	if opts.ConflictStrategy == "" {
		opts.ConflictStrategy = transfer.ConflictSkip
	}
	if opts.ImportMediaFiles && !isZip {
		h.renderImportError(w, r, user, "Import failed: media file import requires a ZIP archive")
		return
	}

	// Create importer
	importer := h.newImporter()

	var result *transfer.ImportResult
	var err error

	payload, staged, err := h.loadStagedImport(r.Context())
	if err != nil {
		h.logger.Error("failed to read staged import payload", "error", err)
		h.renderImportError(w, r, user, "Failed to read the uploaded file. Please upload it again.")
		return
	}
	if !staged {
		h.renderImportError(w, r, user, "No import data found. Please upload a file first.")
		return
	}

	if isZip {
		zipData := payload

		// Enforce page content policy before importer writes pages.
		exportData, err := h.extractExportDataFromZip(zipData)
		if err != nil {
			h.logger.Warn("staged import archive could not be read", "error", err)
			h.renderImportError(w, r, user, importZipUnreadableMessage)
			return
		}
		if err := h.applyImportPageSecurityPolicy(r, &exportData, "import", opts.ImportPages); err != nil {
			h.renderImportError(w, r, user, "Import failed: "+err.Error())
			return
		}

		// Perform zip import
		result, err = importer.ImportFromZipBytes(r.Context(), zipData, opts)
		// Invalidated before the error is examined, not after. A ZIP import can
		// fail once the transaction has already committed — media ownership
		// verification and the uploads-root identity re-check both run after the
		// commit — and that result describes content the database now holds.
		// Returning early from the error branch used to leave those pages, menus
		// and languages behind caches built before the commit, with process-wide
		// i18n state still unaware of the imported languages. The call is a no-op
		// for a nil or dry-run result, so it belongs ahead of every branch.
		h.invalidateCachesAfterImport(r.Context(), result)
		if err != nil {
			h.logger.Error("zip import failed", "error", err)
			h.renderImportError(w, r, user, "Import failed: "+err.Error())
			return
		}

		h.discardStagedImport(r.Context())
	} else {
		// Parse the stored data
		var exportData transfer.ExportData
		if err := json.Unmarshal(payload, &exportData); err != nil {
			h.renderImportError(w, r, user, "Failed to parse stored data")
			return
		}

		if err := h.applyImportPageSecurityPolicy(r, &exportData, "import", opts.ImportPages); err != nil {
			h.renderImportError(w, r, user, "Import failed: "+err.Error())
			return
		}

		// Perform JSON import
		result, err = importer.Import(r.Context(), &exportData, opts)
		h.invalidateCachesAfterImport(r.Context(), result)
		if err != nil {
			h.logger.Error("import failed", "error", err)
			h.renderImportError(w, r, user, "Import failed: "+err.Error())
			return
		}

		h.discardStagedImport(r.Context())
	}

	// Clear common session data
	h.sessionManager.Remove(r.Context(), "import_is_zip")

	// Set flash message based on result
	if result.Success {
		if result.DryRun {
			h.sessionManager.Put(r.Context(), "flash_success", "Dry run completed successfully. No changes were made.")
		} else {
			h.sessionManager.Put(r.Context(), "flash_success", fmt.Sprintf("Import completed: %d created, %d updated, %d skipped",
				result.TotalCreated(), result.TotalUpdated(), result.TotalSkipped()))
		}
	} else {
		h.sessionManager.Put(r.Context(), "flash_error", fmt.Sprintf("Import completed with %d errors", len(result.Errors)))
	}

	h.renderImportPage(w, r, user, ImportFormData{
		ConflictStrategies: defaultConflictStrategies(),
		ImportResult:       result,
	})
}

func (h *ImportExportHandler) invalidateCachesAfterImport(ctx context.Context, result *transfer.ImportResult) {
	if h == nil || result == nil || result.DryRun {
		return
	}
	if h.queries != nil {
		refreshCtx, cancel := detachedLanguageStateContext(ctx)
		defer cancel()
		if err := refreshI18nLanguageState(refreshCtx, h.queries); err != nil {
			h.logger.Error("failed to refresh runtime language state after import", "error", err)
		}
	}
	if h.cacheManager != nil {
		h.cacheManager.ClearAll()
	}
}

// renderImportError renders the import form with an error message.
func (h *ImportExportHandler) renderImportError(w http.ResponseWriter, r *http.Request, user interface{}, errMsg string) {
	h.sessionManager.Put(r.Context(), "flash_error", errMsg)
	h.renderImportPage(w, r, user, ImportFormData{
		ConflictStrategies: defaultConflictStrategies(),
	})
}

// importZipUnreadableMessage is shown instead of the raw archive error, which
// can name server paths; the detail goes to the log.
const importZipUnreadableMessage = "The archive could not be read as an oCMS export. Check the server log for details."

// Staged import payloads live on disk between the upload step and the
// confirmation step. Keeping them in the session row used to store up to
// 100 MiB of upload, inflated by base64, in the sessions table. Staging is
// per host: a multi-instance deployment with shared sessions would see
// "no import data" on the other instance.
const (
	sessionKeyImportPayloadPath = "import_payload_path"
	importStagePrefix           = "ocms-import-"
	importStageMaxAge           = time.Hour
)

// stageImportPayload writes payload to a private temporary file and records
// its path in the session, replacing any earlier staged upload.
func (h *ImportExportHandler) stageImportPayload(ctx context.Context, payload []byte) error {
	h.discardStagedImport(ctx)
	sweepStaleImportPayloads()
	f, err := os.CreateTemp("", importStagePrefix+"*.bin")
	if err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	h.sessionManager.Put(ctx, sessionKeyImportPayloadPath, f.Name())
	return nil
}

// loadStagedImport returns the staged payload for this session. staged is
// false when nothing was uploaded or the file has already been swept.
func (h *ImportExportHandler) loadStagedImport(ctx context.Context) (payload []byte, staged bool, err error) {
	path := h.sessionManager.GetString(ctx, sessionKeyImportPayloadPath)
	if path == "" {
		return nil, false, nil
	}
	if !isStagedImportPath(path) {
		h.logger.Warn("staged import path rejected", "dir", filepath.Dir(path))
		h.sessionManager.Remove(ctx, sessionKeyImportPayloadPath)
		return nil, false, nil
	}
	payload, err = os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		h.sessionManager.Remove(ctx, sessionKeyImportPayloadPath)
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return payload, true, nil
}

// discardStagedImport removes the staged file, if any, and forgets its path.
func (h *ImportExportHandler) discardStagedImport(ctx context.Context) {
	path := h.sessionManager.PopString(ctx, sessionKeyImportPayloadPath)
	if path == "" {
		return
	}
	if !isStagedImportPath(path) {
		h.logger.Warn("staged import path rejected", "dir", filepath.Dir(path))
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		h.logger.Warn("failed to remove staged import payload", "error", err)
	}
}

// isStagedImportPath accepts only files this handler created in the
// temporary directory, so a corrupted session value cannot read or delete
// anything else.
func isStagedImportPath(path string) bool {
	dir, base := filepath.Split(filepath.Clean(path))
	return filepath.Clean(dir) == filepath.Clean(os.TempDir()) && strings.HasPrefix(base, importStagePrefix) && strings.HasSuffix(base, ".bin")
}

// sweepStaleImportPayloads removes staged files older than importStageMaxAge,
// whether or not a session still points at them; loadStagedImport then treats
// the missing file as "nothing uploaded" and asks for a new upload.
func sweepStaleImportPayloads() {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), importStagePrefix+"*.bin"))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-importStageMaxAge)
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(match); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("failed to remove stale import payload", "error", err)
		}
	}
}
