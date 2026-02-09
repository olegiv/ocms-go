// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package bookmarks

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
)

// Bookmark represents a saved bookmark.
type Bookmark struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Description string    `json:"description"`
	IsFavorite  bool      `json:"is_favorite"`
	CreatedAt   time.Time `json:"created_at"`
}

// handlePublicList handles GET /bookmarks - public route showing all bookmarks.
func (m *Module) handlePublicList(w http.ResponseWriter, _ *http.Request) {
	items, err := m.listBookmarks()
	if err != nil {
		m.ctx.Logger.Error("failed to list bookmarks", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"bookmarks": items,
		"total":     len(items),
	})
}

// handleAdminList handles GET /admin/bookmarks - admin dashboard.
func (m *Module) handleAdminList(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	items, err := m.listBookmarks()
	if err != nil {
		m.ctx.Logger.Error("failed to list bookmarks", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Bookmarks []Bookmark
		Version   string
	}{
		Bookmarks: items,
		Version:   m.Version(),
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_bookmarks", render.TemplateData{
		Title: i18n.T(lang, "bookmarks.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "bookmarks.title"), URL: "/admin/bookmarks", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleCreate handles POST /admin/bookmarks - creates a new bookmark.
func (m *Module) handleCreate(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.FormValue("title"))
	bookmarkURL := strings.TrimSpace(r.FormValue("url"))
	description := strings.TrimSpace(r.FormValue("description"))
	isFavorite := r.FormValue("is_favorite") == "on"

	if title == "" {
		http.Error(w, "Title is required", http.StatusBadRequest)
		return
	}
	if bookmarkURL == "" {
		http.Error(w, "URL is required", http.StatusBadRequest)
		return
	}

	bookmark, err := m.createBookmark(title, bookmarkURL, description, isFavorite)
	if err != nil {
		m.ctx.Logger.Error("failed to create bookmark", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bookmark)
		return
	}

	http.Redirect(w, r, "/admin/bookmarks", http.StatusSeeOther)
}

// handleToggleFavorite handles POST /admin/bookmarks/{id}/toggle - toggles favorite status.
func (m *Module) handleToggleFavorite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	if err := m.toggleFavorite(id); err != nil {
		m.ctx.Logger.Error("failed to toggle favorite", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/bookmarks", http.StatusSeeOther)
}

// handleDelete handles DELETE /admin/bookmarks/{id} - deletes a bookmark.
func (m *Module) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	if err := m.deleteBookmark(id); err != nil {
		m.ctx.Logger.Error("failed to delete bookmark", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Database operations

func (m *Module) listBookmarks() ([]Bookmark, error) {
	rows, err := m.ctx.DB.Query(`
		SELECT id, title, url, description, is_favorite, created_at
		FROM bookmarks
		ORDER BY is_favorite DESC, created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing bookmarks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanBookmarks(rows)
}

func (m *Module) createBookmark(title, bookmarkURL, description string, isFavorite bool) (*Bookmark, error) {
	result, err := m.ctx.DB.Exec(`
		INSERT INTO bookmarks (title, url, description, is_favorite, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, title, bookmarkURL, description, isFavorite, time.Now())
	if err != nil {
		return nil, fmt.Errorf("creating bookmark: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("getting last insert id: %w", err)
	}

	return &Bookmark{
		ID:          id,
		Title:       title,
		URL:         bookmarkURL,
		Description: description,
		IsFavorite:  isFavorite,
		CreatedAt:   time.Now(),
	}, nil
}

func (m *Module) toggleFavorite(id int64) error {
	result, err := m.ctx.DB.Exec(`
		UPDATE bookmarks SET is_favorite = NOT is_favorite WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("toggling favorite: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}

func (m *Module) deleteBookmark(id int64) error {
	result, err := m.ctx.DB.Exec(`DELETE FROM bookmarks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting bookmark: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// scanBookmarks scans rows into a slice of Bookmark.
func scanBookmarks(rows *sql.Rows) ([]Bookmark, error) {
	var items []Bookmark
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.ID, &b.Title, &b.URL, &b.Description, &b.IsFavorite, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning bookmark: %w", err)
		}
		items = append(items, b)
	}
	return items, rows.Err()
}
