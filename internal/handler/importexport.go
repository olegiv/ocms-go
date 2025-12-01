package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/middleware"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
	"ocms-go/internal/transfer"
)

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
	user := middleware.GetUser(r)

	data := ExportFormData{
		PageStatuses: []string{"all", "published", "draft"},
	}

	if err := h.renderer.Render(w, r, "admin/export", render.TemplateData{
		Title: "Export Content",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Export", URL: "/admin/export", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Export handles POST /admin/export - generates and downloads the export.
func (h *ImportExportHandler) Export(w http.ResponseWriter, r *http.Request) {
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

	// Generate filename with current date
	filename := fmt.Sprintf("ocms-export-%s.json", time.Now().Format("2006-01-02"))

	// Set response headers for file download
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	// Export directly to response writer
	if err := exporter.ExportToWriter(r.Context(), opts, w); err != nil {
		h.logger.Error("export failed", "error", err)
		http.Error(w, "Export failed", http.StatusInternalServerError)
		return
	}
}

// ImportFormData holds data for the import form template.
type ImportFormData struct {
	ConflictStrategies []ConflictStrategyOption
	ValidationResult   *transfer.ValidationResult
	ImportResult       *transfer.ImportResult
	UploadedData       *transfer.ExportData
}

// ConflictStrategyOption represents a conflict strategy for the dropdown.
type ConflictStrategyOption struct {
	Value       string
	Label       string
	Description string
}

// ImportForm handles GET /admin/import - displays the import form.
func (h *ImportExportHandler) ImportForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	data := ImportFormData{
		ConflictStrategies: []ConflictStrategyOption{
			{Value: "skip", Label: "Skip Existing", Description: "Skip items that already exist"},
			{Value: "overwrite", Label: "Overwrite", Description: "Update existing items with imported data"},
			{Value: "rename", Label: "Rename", Description: "Create with new slug if exists"},
		},
	}

	if err := h.renderer.Render(w, r, "admin/import", render.TemplateData{
		Title: "Import Content",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Import", URL: "/admin/import", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// ImportValidate handles POST /admin/import/validate - validates the uploaded file.
func (h *ImportExportHandler) ImportValidate(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	// Parse multipart form (max 50MB)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		h.renderImportError(w, r, user, "Failed to parse form: "+err.Error())
		return
	}

	// Get uploaded file
	file, header, err := r.FormFile("import_file")
	if err != nil {
		h.renderImportError(w, r, user, "Please select a file to import")
		return
	}
	defer file.Close()

	// Check file extension
	if header.Filename[len(header.Filename)-5:] != ".json" {
		h.renderImportError(w, r, user, "Only JSON files are supported")
		return
	}

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		h.renderImportError(w, r, user, "Failed to read file: "+err.Error())
		return
	}

	// Parse JSON
	var exportData transfer.ExportData
	if err := json.Unmarshal(content, &exportData); err != nil {
		h.renderImportError(w, r, user, "Invalid JSON format: "+err.Error())
		return
	}

	// Create importer and validate
	importer := transfer.NewImporter(h.queries, h.db, h.logger)
	validationResult, err := importer.ValidateData(r.Context(), &exportData)
	if err != nil {
		h.renderImportError(w, r, user, "Validation failed: "+err.Error())
		return
	}

	// Store validated data in session for the actual import
	jsonData, _ := json.Marshal(exportData)
	h.sessionManager.Put(r.Context(), "import_data", string(jsonData))

	data := ImportFormData{
		ConflictStrategies: []ConflictStrategyOption{
			{Value: "skip", Label: "Skip Existing", Description: "Skip items that already exist"},
			{Value: "overwrite", Label: "Overwrite", Description: "Update existing items with imported data"},
			{Value: "rename", Label: "Rename", Description: "Create with new slug if exists"},
		},
		ValidationResult: validationResult,
		UploadedData:     &exportData,
	}

	if err := h.renderer.Render(w, r, "admin/import", render.TemplateData{
		Title: "Import Content",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Import", URL: "/admin/import", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Import handles POST /admin/import - performs the actual import.
func (h *ImportExportHandler) Import(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if err := r.ParseForm(); err != nil {
		h.renderImportError(w, r, user, "Invalid form data")
		return
	}

	// Get stored data from session
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

	// Build import options from form
	opts := transfer.ImportOptions{
		DryRun:           r.FormValue("dry_run") == "on",
		ConflictStrategy: transfer.ConflictStrategy(r.FormValue("conflict_strategy")),
		ImportUsers:      r.FormValue("import_users") == "on",
		ImportPages:      r.FormValue("import_pages") == "on",
		ImportCategories: r.FormValue("import_categories") == "on",
		ImportTags:       r.FormValue("import_tags") == "on",
		ImportMedia:      r.FormValue("import_media") == "on",
		ImportMenus:      r.FormValue("import_menus") == "on",
		ImportForms:      r.FormValue("import_forms") == "on",
		ImportConfig:     r.FormValue("import_config") == "on",
		ImportLanguages:  r.FormValue("import_languages") == "on",
	}

	// Default conflict strategy
	if opts.ConflictStrategy == "" {
		opts.ConflictStrategy = transfer.ConflictSkip
	}

	// Create importer and perform import
	importer := transfer.NewImporter(h.queries, h.db, h.logger)
	result, err := importer.Import(r.Context(), &exportData, opts)
	if err != nil {
		h.logger.Error("import failed", "error", err)
		h.renderImportError(w, r, user, "Import failed: "+err.Error())
		return
	}

	// Clear session data
	h.sessionManager.Remove(r.Context(), "import_data")

	data := ImportFormData{
		ConflictStrategies: []ConflictStrategyOption{
			{Value: "skip", Label: "Skip Existing", Description: "Skip items that already exist"},
			{Value: "overwrite", Label: "Overwrite", Description: "Update existing items with imported data"},
			{Value: "rename", Label: "Rename", Description: "Create with new slug if exists"},
		},
		ImportResult: result,
	}

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

	if err := h.renderer.Render(w, r, "admin/import", render.TemplateData{
		Title: "Import Content",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Import", URL: "/admin/import", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// renderImportError renders the import form with an error message.
func (h *ImportExportHandler) renderImportError(w http.ResponseWriter, r *http.Request, user interface{}, errMsg string) {
	h.sessionManager.Put(r.Context(), "flash_error", errMsg)

	data := ImportFormData{
		ConflictStrategies: []ConflictStrategyOption{
			{Value: "skip", Label: "Skip Existing", Description: "Skip items that already exist"},
			{Value: "overwrite", Label: "Overwrite", Description: "Update existing items with imported data"},
			{Value: "rename", Label: "Rename", Description: "Create with new slug if exists"},
		},
	}

	if err := h.renderer.Render(w, r, "admin/import", render.TemplateData{
		Title: "Import Content",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Import", URL: "/admin/import", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
