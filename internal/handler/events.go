// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// EventsPerPage is the number of events to display per page.
const EventsPerPage = 25

// EventsHandler handles event log viewing routes.
type EventsHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
}

// NewEventsHandler creates a new EventsHandler.
func NewEventsHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *EventsHandler {
	return &EventsHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// EventWithUser represents an event with associated user info.
type EventWithUser struct {
	ID          int64
	Level       string
	Category    string
	Message     string
	Metadata    string
	Details     string // Formatted metadata as readable text
	DetailsLong bool   // True if details exceed display threshold
	CreatedAt   string
	UserName    string
	UserEmail   string
}

// detailsLengthThreshold is the max chars before details are collapsible
const detailsLengthThreshold = 80

// formatMetadata converts JSON metadata to readable text format.
// Example: {"path":"/admin/pages","error":"not found"} -> "path: /admin/pages, error: not found"
func formatMetadata(metadata string) string {
	if metadata == "" || metadata == "{}" {
		return ""
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(metadata), &data); err != nil {
		return metadata // Return as-is if not valid JSON
	}

	if len(data) == 0 {
		return ""
	}

	// Sort keys for consistent output order
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var parts []string
	for _, key := range keys {
		value := data[key]
		var strValue string
		switch v := value.(type) {
		case string:
			strValue = v
		case float64:
			strValue = strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			strValue = strconv.FormatBool(v)
		default:
			// For nested objects, marshal back to JSON
			if b, err := json.Marshal(v); err == nil {
				strValue = string(b)
			}
		}
		parts = append(parts, key+": "+strValue)
	}

	return strings.Join(parts, ", ")
}

// EventsListData holds data for the events list template.
type EventsListData struct {
	Events      []EventWithUser
	TotalEvents int64
	Level       string
	Category    string
	Levels      []string
	Categories  []string
	Pagination  AdminPagination
}

// List handles GET /admin/events - displays a paginated list of events.
func (h *EventsHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	// Get filter parameters
	level := r.URL.Query().Get("level")
	category := r.URL.Query().Get("category")

	page := ParsePageParam(r)

	// Get total event count based on filters
	var totalEvents int64
	var err error

	if level != "" && category != "" {
		totalEvents, err = h.queries.CountEventsByLevelAndCategory(r.Context(), store.CountEventsByLevelAndCategoryParams{
			Level:    level,
			Category: category,
		})
	} else if level != "" {
		totalEvents, err = h.queries.CountEventsByLevel(r.Context(), level)
	} else if category != "" {
		totalEvents, err = h.queries.CountEventsByCategory(r.Context(), category)
	} else {
		totalEvents, err = h.queries.CountEvents(r.Context())
	}

	if err != nil {
		logAndInternalError(w, "failed to count events", "error", err)
		return
	}

	// Normalize page to valid range
	page, _ = NormalizePagination(page, int(totalEvents), EventsPerPage)
	offset := int64((page - 1) * EventsPerPage)

	// Fetch events for current page based on filters
	var events []EventWithUser

	if level != "" && category != "" {
		rows, err := h.queries.ListEventsWithUserByLevelAndCategory(r.Context(), store.ListEventsWithUserByLevelAndCategoryParams{
			Level:    level,
			Category: category,
			Limit:    EventsPerPage,
			Offset:   offset,
		})
		if err != nil {
			logAndInternalError(w, "failed to list events", "error", err)
			return
		}
		events = convertEventsWithUserByLevelAndCategory(rows)
	} else if level != "" {
		rows, err := h.queries.ListEventsWithUserByLevel(r.Context(), store.ListEventsWithUserByLevelParams{
			Level:  level,
			Limit:  EventsPerPage,
			Offset: offset,
		})
		if err != nil {
			logAndInternalError(w, "failed to list events", "error", err)
			return
		}
		events = convertEventsWithUserByLevel(rows)
	} else if category != "" {
		rows, err := h.queries.ListEventsWithUserByCategory(r.Context(), store.ListEventsWithUserByCategoryParams{
			Category: category,
			Limit:    EventsPerPage,
			Offset:   offset,
		})
		if err != nil {
			logAndInternalError(w, "failed to list events", "error", err)
			return
		}
		events = convertEventsWithUserByCategory(rows)
	} else {
		rows, err := h.queries.ListEventsWithUser(r.Context(), store.ListEventsWithUserParams{
			Limit:  EventsPerPage,
			Offset: offset,
		})
		if err != nil {
			logAndInternalError(w, "failed to list events", "error", err)
			return
		}
		events = convertEventsWithUser(rows)
	}

	data := EventsListData{
		Events:      events,
		TotalEvents: totalEvents,
		Level:       level,
		Category:    category,
		Levels:      []string{model.EventLevelInfo, model.EventLevelWarning, model.EventLevelError},
		Categories:  []string{model.EventCategoryAuth, model.EventCategoryPage, model.EventCategoryUser, model.EventCategoryConfig, model.EventCategorySystem},
		Pagination:  BuildAdminPagination(page, int(totalEvents), EventsPerPage, redirectAdminEvents, r.URL.Query()),
	}

	h.renderer.RenderPage(w, r, "admin/events", render.TemplateData{
		Title: i18n.T(lang, "events.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.event_log"), URL: redirectAdminEvents, Active: true},
		},
	})
}

// Helper functions to convert sqlc rows to EventWithUser
func convertEventsWithUser(rows []store.ListEventsWithUserRow) []EventWithUser {
	events := make([]EventWithUser, len(rows))
	for i, row := range rows {
		details := formatMetadata(row.Metadata)
		events[i] = EventWithUser{
			ID:          row.ID,
			Level:       row.Level,
			Category:    row.Category,
			Message:     row.Message,
			Metadata:    row.Metadata,
			Details:     details,
			DetailsLong: len(details) > detailsLengthThreshold,
			CreatedAt:   row.CreatedAt.Format("2006-01-02 15:04:05"),
			UserName:    row.UserName.String,
			UserEmail:   row.UserEmail.String,
		}
	}
	return events
}

func convertEventsWithUserByLevel(rows []store.ListEventsWithUserByLevelRow) []EventWithUser {
	events := make([]EventWithUser, len(rows))
	for i, row := range rows {
		details := formatMetadata(row.Metadata)
		events[i] = EventWithUser{
			ID:          row.ID,
			Level:       row.Level,
			Category:    row.Category,
			Message:     row.Message,
			Metadata:    row.Metadata,
			Details:     details,
			DetailsLong: len(details) > detailsLengthThreshold,
			CreatedAt:   row.CreatedAt.Format("2006-01-02 15:04:05"),
			UserName:    row.UserName.String,
			UserEmail:   row.UserEmail.String,
		}
	}
	return events
}

func convertEventsWithUserByCategory(rows []store.ListEventsWithUserByCategoryRow) []EventWithUser {
	events := make([]EventWithUser, len(rows))
	for i, row := range rows {
		details := formatMetadata(row.Metadata)
		events[i] = EventWithUser{
			ID:          row.ID,
			Level:       row.Level,
			Category:    row.Category,
			Message:     row.Message,
			Metadata:    row.Metadata,
			Details:     details,
			DetailsLong: len(details) > detailsLengthThreshold,
			CreatedAt:   row.CreatedAt.Format("2006-01-02 15:04:05"),
			UserName:    row.UserName.String,
			UserEmail:   row.UserEmail.String,
		}
	}
	return events
}

func convertEventsWithUserByLevelAndCategory(rows []store.ListEventsWithUserByLevelAndCategoryRow) []EventWithUser {
	events := make([]EventWithUser, len(rows))
	for i, row := range rows {
		details := formatMetadata(row.Metadata)
		events[i] = EventWithUser{
			ID:          row.ID,
			Level:       row.Level,
			Category:    row.Category,
			Message:     row.Message,
			Metadata:    row.Metadata,
			Details:     details,
			DetailsLong: len(details) > detailsLengthThreshold,
			CreatedAt:   row.CreatedAt.Format("2006-01-02 15:04:05"),
			UserName:    row.UserName.String,
			UserEmail:   row.UserEmail.String,
		}
	}
	return events
}
