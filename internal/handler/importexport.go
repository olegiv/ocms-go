// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/transfer"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

const maxImportUploadBytes int64 = 100 << 20 // 100 MB

// ImportExportHandler handles import/export routes.
type ImportExportHandler struct {
	db             *sql.DB
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	logger         *slog.Logger
}

// NewImportExportHandler creates a new ImportExportHandler.
func NewImportExportHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *ImportExportHandler {
	return &ImportExportHandler{
		db:             db,
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		logger:         slog.Default(),
	}
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
	exporter := transfer.NewExporter(h.queries, h.logger)

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
	importer := transfer.NewImporter(h.queries, h.db, h.logger)

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
			h.renderImportError(w, r, user, "Failed to extract data from zip: "+err.Error())
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

	// Store validated data in session for the actual import
	// For zip files, we store base64 encoded bytes; for JSON, we store the parsed data
	if isZipFile {
		h.sessionManager.Put(r.Context(), "import_zip_data", base64.StdEncoding.EncodeToString(content))
		h.sessionManager.Put(r.Context(), "import_is_zip", true)
	} else {
		jsonData, _ := json.Marshal(exportData)
		h.sessionManager.Put(r.Context(), "import_data", string(jsonData))
		h.sessionManager.Put(r.Context(), "import_is_zip", false)
	}

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

	// Create importer
	importer := transfer.NewImporter(h.queries, h.db, h.logger)

	var result *transfer.ImportResult
	var err error

	if isZip {
		// Get stored zip data from session (base64 encoded)
		zipDataB64 := h.sessionManager.GetString(r.Context(), "import_zip_data")
		if zipDataB64 == "" {
			h.renderImportError(w, r, user, "No import data found. Please upload a file first.")
			return
		}

		// Decode base64
		zipData, err := base64.StdEncoding.DecodeString(zipDataB64)
		if err != nil {
			h.renderImportError(w, r, user, "Failed to decode stored data")
			return
		}

		// Perform zip import
		result, err = importer.ImportFromZipBytes(r.Context(), zipData, opts)
		if err != nil {
			h.logger.Error("zip import failed", "error", err)
			h.renderImportError(w, r, user, "Import failed: "+err.Error())
			return
		}

		// Clear session data
		h.sessionManager.Remove(r.Context(), "import_zip_data")
	} else {
		// Get stored JSON data from session
		jsonData := h.sessionManager.GetString(r.Context(), "import_data")
		if jsonData == "" {
			h.renderImportError(w, r, user, "No import data found. Please upload a file first.")
			return
		}

		// Parse the stored data
		var exportData transfer.ExportData
		if err := json.Unmarshal([]byte(jsonData), &exportData); err != nil {
			h.renderImportError(w, r, user, "Failed to parse stored data")
			return
		}

		// Perform JSON import
		result, err = importer.Import(r.Context(), &exportData, opts)
		if err != nil {
			h.logger.Error("import failed", "error", err)
			h.renderImportError(w, r, user, "Import failed: "+err.Error())
			return
		}

		// Clear session data
		h.sessionManager.Remove(r.Context(), "import_data")
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

// renderImportError renders the import form with an error message.
func (h *ImportExportHandler) renderImportError(w http.ResponseWriter, r *http.Request, user interface{}, errMsg string) {
	h.sessionManager.Put(r.Context(), "flash_error", errMsg)
	h.renderImportPage(w, r, user, ImportFormData{
		ConflictStrategies: defaultConflictStrategies(),
	})
}
