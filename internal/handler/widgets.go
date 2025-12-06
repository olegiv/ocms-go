package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
	"ocms-go/internal/theme"
)

// WidgetsHandler handles widget management routes.
type WidgetsHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	themeManager   *theme.Manager
}

// NewWidgetsHandler creates a new WidgetsHandler.
func NewWidgetsHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, tm *theme.Manager) *WidgetsHandler {
	return &WidgetsHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		themeManager:   tm,
	}
}

// WidgetTypes defines available widget types.
var WidgetTypes = []struct {
	ID          string
	Name        string
	Description string
}{
	{"text", "Text/HTML", "Custom text or HTML content"},
	{"recent_posts", "Recent Posts", "Display recent blog posts"},
	{"categories", "Categories", "Display category list"},
	{"tags", "Tags", "Display tag cloud"},
	{"search", "Search", "Search form widget"},
	{"custom_menu", "Custom Menu", "Display a navigation menu"},
}

// WidgetAreaWithWidgets represents a widget area with its widgets.
type WidgetAreaWithWidgets struct {
	Area    theme.WidgetArea
	Widgets []store.Widget
}

// WidgetsListData holds data for the widgets list template.
type WidgetsListData struct {
	Theme       *theme.Theme
	WidgetAreas []WidgetAreaWithWidgets
	WidgetTypes []struct {
		ID          string
		Name        string
		Description string
	}
	AllThemes []theme.ThemeInfo
}

// List handles GET /admin/widgets - displays widget management page.
func (h *WidgetsHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	// Get active theme
	activeTheme := h.themeManager.GetActiveTheme()
	if activeTheme == nil {
		h.renderer.SetFlash(r, "No active theme found", "error")
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	// Get widgets for this theme
	widgets, err := h.queries.GetAllWidgetsByTheme(r.Context(), activeTheme.Name)
	if err != nil {
		slog.Error("failed to get widgets", "error", err)
		widgets = []store.Widget{}
	}

	// Group widgets by area
	widgetsByArea := make(map[string][]store.Widget)
	for _, w := range widgets {
		widgetsByArea[w.Area] = append(widgetsByArea[w.Area], w)
	}

	// Build widget areas with widgets
	var widgetAreas []WidgetAreaWithWidgets
	for _, area := range activeTheme.Config.WidgetAreas {
		widgetAreas = append(widgetAreas, WidgetAreaWithWidgets{
			Area:    area,
			Widgets: widgetsByArea[area.ID],
		})
	}

	data := WidgetsListData{
		Theme:       activeTheme,
		WidgetAreas: widgetAreas,
		WidgetTypes: WidgetTypes,
		AllThemes:   h.themeManager.ListThemesWithActive(),
	}

	if err := h.renderer.Render(w, r, "admin/widgets_list", render.TemplateData{
		Title: i18n.T(lang, "nav.widgets"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.widgets"), URL: "/admin/widgets", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// CreateWidgetRequest represents the JSON request for creating a widget.
type CreateWidgetRequest struct {
	Theme      string `json:"theme"`
	Area       string `json:"area"`
	WidgetType string `json:"widget_type"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Settings   string `json:"settings"`
}

// Create handles POST /admin/widgets - creates a new widget.
func (h *WidgetsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateWidgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate
	if req.Theme == "" {
		http.Error(w, "Theme is required", http.StatusBadRequest)
		return
	}
	if req.Area == "" {
		http.Error(w, "Area is required", http.StatusBadRequest)
		return
	}
	if req.WidgetType == "" {
		http.Error(w, "Widget type is required", http.StatusBadRequest)
		return
	}

	// Get max position
	maxPosResult, err := h.queries.GetMaxWidgetPosition(r.Context(), store.GetMaxWidgetPositionParams{
		Theme: req.Theme,
		Area:  req.Area,
	})
	var maxPos int64 = -1
	if err != nil {
		slog.Error("failed to get max position", "error", err)
	} else if maxPosResult != nil {
		if v, ok := maxPosResult.(int64); ok {
			maxPos = v
		}
	}

	widget, err := h.queries.CreateWidget(r.Context(), store.CreateWidgetParams{
		Theme:      req.Theme,
		Area:       req.Area,
		WidgetType: req.WidgetType,
		Title:      sql.NullString{String: req.Title, Valid: req.Title != ""},
		Content:    sql.NullString{String: req.Content, Valid: req.Content != ""},
		Settings:   sql.NullString{String: req.Settings, Valid: req.Settings != ""},
		Position:   maxPos + 1,
		IsActive:   1,
	})
	if err != nil {
		slog.Error("failed to create widget", "error", err)
		http.Error(w, "Error creating widget", http.StatusInternalServerError)
		return
	}

	slog.Info("widget created", "widget_id", widget.ID, "theme", req.Theme, "area", req.Area)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"widget":  widget,
	})
}

// UpdateWidgetRequest represents the JSON request for updating a widget.
type UpdateWidgetRequest struct {
	WidgetType string `json:"widget_type"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Settings   string `json:"settings"`
	IsActive   bool   `json:"is_active"`
}

// Update handles PUT /admin/widgets/{id} - updates a widget.
func (h *WidgetsHandler) Update(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid widget ID", http.StatusBadRequest)
		return
	}

	widget, err := h.queries.GetWidget(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Widget not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	var req UpdateWidgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	isActive := int64(0)
	if req.IsActive {
		isActive = 1
	}

	updatedWidget, err := h.queries.UpdateWidget(r.Context(), store.UpdateWidgetParams{
		ID:         id,
		WidgetType: req.WidgetType,
		Title:      sql.NullString{String: req.Title, Valid: req.Title != ""},
		Content:    sql.NullString{String: req.Content, Valid: req.Content != ""},
		Settings:   sql.NullString{String: req.Settings, Valid: req.Settings != ""},
		Position:   widget.Position,
		IsActive:   isActive,
	})
	if err != nil {
		slog.Error("failed to update widget", "error", err, "widget_id", id)
		http.Error(w, "Error updating widget", http.StatusInternalServerError)
		return
	}

	slog.Info("widget updated", "widget_id", id)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"widget":  updatedWidget,
	})
}

// Delete handles DELETE /admin/widgets/{id} - deletes a widget.
func (h *WidgetsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid widget ID", http.StatusBadRequest)
		return
	}

	_, err = h.queries.GetWidget(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Widget not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	err = h.queries.DeleteWidget(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete widget", "error", err, "widget_id", id)
		http.Error(w, "Error deleting widget", http.StatusInternalServerError)
		return
	}

	slog.Info("widget deleted", "widget_id", id, "deleted_by", middleware.GetUserID(r))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// ReorderWidgetsRequest represents the JSON request for reordering widgets.
type ReorderWidgetsRequest struct {
	Widgets []struct {
		ID       int64 `json:"id"`
		Position int64 `json:"position"`
	} `json:"widgets"`
}

// Reorder handles POST /admin/widgets/reorder - reorders widgets.
func (h *WidgetsHandler) Reorder(w http.ResponseWriter, r *http.Request) {
	var req ReorderWidgetsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	for _, item := range req.Widgets {
		err := h.queries.UpdateWidgetPosition(r.Context(), store.UpdateWidgetPositionParams{
			ID:       item.ID,
			Position: item.Position,
		})
		if err != nil {
			slog.Error("failed to update widget position", "error", err, "widget_id", item.ID)
			http.Error(w, "Error reordering widgets", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// GetWidget handles GET /admin/widgets/{id} - gets a widget by ID.
func (h *WidgetsHandler) GetWidget(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid widget ID", http.StatusBadRequest)
		return
	}

	widget, err := h.queries.GetWidget(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Widget not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(widget)
}

// MoveWidgetRequest represents the JSON request for moving a widget to a different area.
type MoveWidgetRequest struct {
	Area string `json:"area"`
}

// MoveWidget handles POST /admin/widgets/{id}/move - moves a widget to a different area.
func (h *WidgetsHandler) MoveWidget(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid widget ID", http.StatusBadRequest)
		return
	}

	widget, err := h.queries.GetWidget(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Widget not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	var req MoveWidgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Area = strings.TrimSpace(req.Area)
	if req.Area == "" {
		http.Error(w, "Area is required", http.StatusBadRequest)
		return
	}

	// Get max position in target area
	maxPosResult, err := h.queries.GetMaxWidgetPosition(r.Context(), store.GetMaxWidgetPositionParams{
		Theme: widget.Theme,
		Area:  req.Area,
	})
	var maxPos int64 = -1
	if err != nil {
		slog.Error("failed to get max position", "error", err)
	} else if maxPosResult != nil {
		if v, ok := maxPosResult.(int64); ok {
			maxPos = v
		}
	}

	// Update widget with new area and position
	updatedWidget, err := h.queries.UpdateWidget(r.Context(), store.UpdateWidgetParams{
		ID:         id,
		WidgetType: widget.WidgetType,
		Title:      widget.Title,
		Content:    widget.Content,
		Settings:   widget.Settings,
		Position:   maxPos + 1,
		IsActive:   widget.IsActive,
	})
	if err != nil {
		slog.Error("failed to move widget", "error", err, "widget_id", id)
		http.Error(w, "Error moving widget", http.StatusInternalServerError)
		return
	}

	slog.Info("widget moved", "widget_id", id, "new_area", req.Area)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"widget":  updatedWidget,
	})
}
