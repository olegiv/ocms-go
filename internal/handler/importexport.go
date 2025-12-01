package handler

import (
	"database/sql"
	"fmt"
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
