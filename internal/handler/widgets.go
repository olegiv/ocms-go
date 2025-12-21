package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
	"ocms-go/internal/theme"
	"ocms-go/internal/util"
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
	AllThemes []theme.Info
}

// List handles GET /admin/widgets - displays widget management page.
func (h *WidgetsHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	// Get active theme
	activeTheme := h.themeManager.GetActiveTheme()
	if activeTheme == nil {
		flashError(w, r, h.renderer, "/admin", "No active theme found")
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

	h.renderer.RenderPage(w, r, "admin/widgets_list", render.TemplateData{
		Title: i18n.T(lang, "nav.widgets"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.widgets"), URL: "/admin/widgets", Active: true},
		},
	})
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
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate
	if req.Theme == "" {
		writeJSONError(w, http.StatusBadRequest, "Theme is required")
		return
	}
	if req.Area == "" {
		writeJSONError(w, http.StatusBadRequest, "Area is required")
		return
	}
	if req.WidgetType == "" {
		writeJSONError(w, http.StatusBadRequest, "Widget type is required")
		return
	}

	maxPos := h.getMaxWidgetPosition(r, req.Theme, req.Area)

	widget, err := h.queries.CreateWidget(r.Context(), store.CreateWidgetParams{
		Theme:      req.Theme,
		Area:       req.Area,
		WidgetType: req.WidgetType,
		Title:      util.NullStringFromValue(req.Title),
		Content:    util.NullStringFromValue(req.Content),
		Settings:   util.NullStringFromValue(req.Settings),
		Position:   maxPos + 1,
		IsActive:   1,
	})
	if err != nil {
		slog.Error("failed to create widget", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Error creating widget")
		return
	}

	slog.Info("widget created", "widget_id", widget.ID, "theme", req.Theme, "area", req.Area)

	writeJSONSuccess(w, map[string]any{"widget": widget})
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
	id, err := ParseIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid widget ID")
		return
	}

	widget, ok := h.requireWidgetWithJSONError(w, r, id)
	if !ok {
		return
	}

	var req UpdateWidgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	isActive := int64(0)
	if req.IsActive {
		isActive = 1
	}

	updatedWidget, err := h.queries.UpdateWidget(r.Context(), store.UpdateWidgetParams{
		ID:         id,
		WidgetType: req.WidgetType,
		Title:      util.NullStringFromValue(req.Title),
		Content:    util.NullStringFromValue(req.Content),
		Settings:   util.NullStringFromValue(req.Settings),
		Position:   widget.Position,
		IsActive:   isActive,
	})
	if err != nil {
		slog.Error("failed to update widget", "error", err, "widget_id", id)
		writeJSONError(w, http.StatusInternalServerError, "Error updating widget")
		return
	}

	slog.Info("widget updated", "widget_id", id)

	writeJSONSuccess(w, map[string]any{"widget": updatedWidget})
}

// Delete handles DELETE /admin/widgets/{id} - deletes a widget.
func (h *WidgetsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid widget ID")
		return
	}

	if _, ok := h.requireWidgetWithJSONError(w, r, id); !ok {
		return
	}

	if err = h.queries.DeleteWidget(r.Context(), id); err != nil {
		slog.Error("failed to delete widget", "error", err, "widget_id", id)
		writeJSONError(w, http.StatusInternalServerError, "Error deleting widget")
		return
	}

	slog.Info("widget deleted", "widget_id", id, "deleted_by", middleware.GetUserID(r))

	writeJSONSuccess(w, nil)
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
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	for _, item := range req.Widgets {
		if err := h.queries.UpdateWidgetPosition(r.Context(), store.UpdateWidgetPositionParams{
			ID:       item.ID,
			Position: item.Position,
		}); err != nil {
			slog.Error("failed to update widget position", "error", err, "widget_id", item.ID)
			writeJSONError(w, http.StatusInternalServerError, "Error reordering widgets")
			return
		}
	}

	writeJSONSuccess(w, nil)
}

// GetWidget handles GET /admin/widgets/{id} - gets a widget by ID.
func (h *WidgetsHandler) GetWidget(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid widget ID")
		return
	}

	widget, ok := h.requireWidgetWithJSONError(w, r, id)
	if !ok {
		return
	}

	writeJSONSuccess(w, map[string]any{"widget": widget})
}

// MoveWidgetRequest represents the JSON request for moving a widget to a different area.
type MoveWidgetRequest struct {
	Area string `json:"area"`
}

// MoveWidget handles POST /admin/widgets/{id}/move - moves a widget to a different area.
func (h *WidgetsHandler) MoveWidget(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid widget ID")
		return
	}

	widget, ok := h.requireWidgetWithJSONError(w, r, id)
	if !ok {
		return
	}

	var req MoveWidgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	req.Area = strings.TrimSpace(req.Area)
	if req.Area == "" {
		writeJSONError(w, http.StatusBadRequest, "Area is required")
		return
	}

	maxPos := h.getMaxWidgetPosition(r, widget.Theme, req.Area)

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
		writeJSONError(w, http.StatusInternalServerError, "Error moving widget")
		return
	}

	slog.Info("widget moved", "widget_id", id, "new_area", req.Area)

	writeJSONSuccess(w, map[string]any{"widget": updatedWidget})
}

// requireWidgetWithJSONError fetches a widget by ID and returns JSON error on failure.
func (h *WidgetsHandler) requireWidgetWithJSONError(w http.ResponseWriter, r *http.Request, id int64) (store.Widget, bool) {
	widget, err := h.queries.GetWidget(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "Widget not found")
		} else {
			slog.Error("failed to get widget", "error", err, "widget_id", id)
			writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		}
		return store.Widget{}, false
	}
	return widget, true
}

// getMaxWidgetPosition returns the max position for widgets in a theme/area.
func (h *WidgetsHandler) getMaxWidgetPosition(r *http.Request, theme, area string) int64 {
	maxPosResult, err := h.queries.GetMaxWidgetPosition(r.Context(), store.GetMaxWidgetPositionParams{
		Theme: theme,
		Area:  area,
	})
	if err != nil {
		slog.Error("failed to get max position", "error", err)
		return -1
	}
	if maxPosResult != nil {
		if v, ok := maxPosResult.(int64); ok {
			return v
		}
	}
	return -1
}
