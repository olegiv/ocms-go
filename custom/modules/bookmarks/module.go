// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package bookmarks provides a custom bookmarks module for oCMS.
// It demonstrates how to create a custom module in the custom/modules/ directory
// with database migrations, admin UI, public routes, template functions, hooks,
// and i18n translations.
package bookmarks

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"html/template"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// Module implements the module.Module interface for bookmarks.
type Module struct {
	module.BaseModule
	ctx *module.Context
}

// New creates a new instance of the bookmarks module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"bookmarks",
			"1.0.0",
			"Custom bookmarks module for saving and organizing links",
		),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx
	m.ctx.Logger.Info("Bookmarks module initialized")

	m.registerHooks()

	return nil
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("Bookmarks module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(r chi.Router) {
	r.Get("/bookmarks", m.handlePublicList)
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/bookmarks", m.handleAdminList)
	r.Post("/bookmarks", m.handleCreate)
	r.Post("/bookmarks/{id}/toggle", m.handleToggleFavorite)
	r.Delete("/bookmarks/{id}", m.handleDelete)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"bookmarkCount": func() int {
			count, err := m.countBookmarks()
			if err != nil {
				return 0
			}
			return count
		},
		"bookmarkFavorites": func() []Bookmark {
			items, err := m.listFavorites()
			if err != nil {
				return nil
			}
			return items
		},
	}
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/bookmarks"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "Bookmarks"
}

// TranslationsFS returns the embedded filesystem containing module translations.
func (m *Module) TranslationsFS() embed.FS {
	return localesFS
}

// Migrations returns database migrations for the module.
func (m *Module) Migrations() []module.Migration {
	return []module.Migration{
		{
			Version:     1,
			Description: "Create bookmarks table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS bookmarks (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						title TEXT NOT NULL,
						url TEXT NOT NULL,
						description TEXT DEFAULT '',
						is_favorite BOOLEAN NOT NULL DEFAULT 0,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					)
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS bookmarks`)
				return err
			},
		},
	}
}

// registerHooks registers hook handlers for the module.
func (m *Module) registerHooks() {
	// Log when pages are saved - a custom module could use this
	// to auto-bookmark pages, send notifications, etc.
	m.ctx.Hooks.Register(module.HookPageAfterSave, module.HookHandler{
		Name:     "bookmarks_page_saved",
		Module:   m.Name(),
		Priority: 20,
		Fn: func(ctx context.Context, data any) (any, error) {
			m.ctx.Logger.Debug("Bookmarks module: page saved hook triggered")
			// In a real module, you could auto-bookmark the saved page:
			//   pageData := data.(*PageSaveData)
			//   m.createBookmark(pageData.Title, pageData.URL, "Auto-bookmarked")
			return data, nil
		},
	})
}

// countBookmarks returns the total number of bookmarks.
func (m *Module) countBookmarks() (int, error) {
	var count int
	err := m.ctx.DB.QueryRow("SELECT COUNT(*) FROM bookmarks").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting bookmarks: %w", err)
	}
	return count, nil
}

// listFavorites returns all favorite bookmarks.
func (m *Module) listFavorites() ([]Bookmark, error) {
	rows, err := m.ctx.DB.Query(`
		SELECT id, title, url, description, is_favorite, created_at
		FROM bookmarks
		WHERE is_favorite = 1
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing favorites: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanBookmarks(rows)
}
