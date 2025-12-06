package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
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
	ID        int64
	Level     string
	Category  string
	Message   string
	Metadata  string
	CreatedAt string
	UserName  string
	UserEmail string
}

// EventsListData holds data for the events list template.
type EventsListData struct {
	Events      []EventWithUser
	CurrentPage int
	TotalPages  int
	TotalEvents int64
	HasPrev     bool
	HasNext     bool
	PrevPage    int
	NextPage    int
	Level       string
	Category    string
	Levels      []string
	Categories  []string
}

// List handles GET /admin/events - displays a paginated list of events.
func (h *EventsHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	// Get filter parameters
	level := r.URL.Query().Get("level")
	category := r.URL.Query().Get("category")

	// Get page number from query string
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

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
		slog.Error("failed to count events", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Calculate pagination
	totalPages := int((totalEvents + EventsPerPage - 1) / EventsPerPage)
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

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
			slog.Error("failed to list events", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
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
			slog.Error("failed to list events", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
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
			slog.Error("failed to list events", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		events = convertEventsWithUserByCategory(rows)
	} else {
		rows, err := h.queries.ListEventsWithUser(r.Context(), store.ListEventsWithUserParams{
			Limit:  EventsPerPage,
			Offset: offset,
		})
		if err != nil {
			slog.Error("failed to list events", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		events = convertEventsWithUser(rows)
	}

	data := EventsListData{
		Events:      events,
		CurrentPage: page,
		TotalPages:  totalPages,
		TotalEvents: totalEvents,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		PrevPage:    page - 1,
		NextPage:    page + 1,
		Level:       level,
		Category:    category,
		Levels:      []string{model.EventLevelInfo, model.EventLevelWarning, model.EventLevelError},
		Categories:  []string{model.EventCategoryAuth, model.EventCategoryPage, model.EventCategoryUser, model.EventCategoryConfig, model.EventCategorySystem},
	}

	if err := h.renderer.Render(w, r, "admin/events", render.TemplateData{
		Title: i18n.T(lang, "events.title"),
		User:  user,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Helper functions to convert sqlc rows to EventWithUser
func convertEventsWithUser(rows []store.ListEventsWithUserRow) []EventWithUser {
	events := make([]EventWithUser, len(rows))
	for i, row := range rows {
		events[i] = EventWithUser{
			ID:        row.ID,
			Level:     row.Level,
			Category:  row.Category,
			Message:   row.Message,
			Metadata:  row.Metadata,
			CreatedAt: row.CreatedAt.Format("2006-01-02 15:04:05"),
			UserName:  row.UserName.String,
			UserEmail: row.UserEmail.String,
		}
	}
	return events
}

func convertEventsWithUserByLevel(rows []store.ListEventsWithUserByLevelRow) []EventWithUser {
	events := make([]EventWithUser, len(rows))
	for i, row := range rows {
		events[i] = EventWithUser{
			ID:        row.ID,
			Level:     row.Level,
			Category:  row.Category,
			Message:   row.Message,
			Metadata:  row.Metadata,
			CreatedAt: row.CreatedAt.Format("2006-01-02 15:04:05"),
			UserName:  row.UserName.String,
			UserEmail: row.UserEmail.String,
		}
	}
	return events
}

func convertEventsWithUserByCategory(rows []store.ListEventsWithUserByCategoryRow) []EventWithUser {
	events := make([]EventWithUser, len(rows))
	for i, row := range rows {
		events[i] = EventWithUser{
			ID:        row.ID,
			Level:     row.Level,
			Category:  row.Category,
			Message:   row.Message,
			Metadata:  row.Metadata,
			CreatedAt: row.CreatedAt.Format("2006-01-02 15:04:05"),
			UserName:  row.UserName.String,
			UserEmail: row.UserEmail.String,
		}
	}
	return events
}

func convertEventsWithUserByLevelAndCategory(rows []store.ListEventsWithUserByLevelAndCategoryRow) []EventWithUser {
	events := make([]EventWithUser, len(rows))
	for i, row := range rows {
		events[i] = EventWithUser{
			ID:        row.ID,
			Level:     row.Level,
			Category:  row.Category,
			Message:   row.Message,
			Metadata:  row.Metadata,
			CreatedAt: row.CreatedAt.Format("2006-01-02 15:04:05"),
			UserName:  row.UserName.String,
			UserEmail: row.UserEmail.String,
		}
	}
	return events
}
