// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package migrator provides a module for migrating content from other CMS platforms to oCMS.
// It supports multiple source systems through a pluggable importer architecture.
package migrator

import (
	"context"
	"database/sql"
	"embed"
	"html/template"
	"log/slog"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/modules/migrator/sources/drupal"
	"github.com/olegiv/ocms-go/modules/migrator/sources/elefant"
)

//go:embed locales
var localesFS embed.FS

// shutdownDrainTimeout bounds how long Shutdown waits for in-flight imports to
// notice cancellation before the process moves on.
const shutdownDrainTimeout = 10 * time.Second

// Module implements the module.Module interface for the migrator module.
type Module struct {
	module.BaseModule
	ctx *module.Context

	// runID identifies this process, so stale-job recovery can tell its own
	// live jobs apart from ones orphaned by a previous run.
	runID string

	jobsMu sync.Mutex
	live   map[string]*jobProgress      // source -> live progress
	cancel map[int64]context.CancelFunc // job id -> cancel
	jobsWG sync.WaitGroup
}

// New creates a new instance of the migrator module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"migrator",
			"1.1.0",
			"Migrate content from other CMS platforms to oCMS",
		),
		live:   make(map[string]*jobProgress),
		cancel: make(map[int64]context.CancelFunc),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx
	m.runID = uuid.NewString()
	if m.live == nil {
		m.live = make(map[string]*jobProgress)
	}
	if m.cancel == nil {
		m.cancel = make(map[int64]context.CancelFunc)
	}

	// Register available sources
	RegisterSource(elefant.NewSource())
	RegisterSource(drupal.NewSource())

	if reaped, err := m.reapStaleJobs(context.Background()); err != nil {
		m.ctx.Logger.Warn("failed to reap stale migrator jobs", "error", err)
	} else if reaped > 0 {
		m.ctx.Logger.Info("marked orphaned migrator jobs as interrupted", "count", reaped)
	}

	m.ctx.Logger.Info("Migrator module initialized", "sources", len(sources))
	return nil
}

// Shutdown cancels in-flight imports and waits briefly for them to unwind.
func (m *Module) Shutdown() error {
	if m.ctx == nil {
		return nil
	}

	m.jobsMu.Lock()
	for _, cancel := range m.cancel {
		cancel()
	}
	m.jobsMu.Unlock()

	done := make(chan struct{})
	go func() {
		m.jobsWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownDrainTimeout):
		m.ctx.Logger.Warn("timed out waiting for migrator imports to stop")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), finishJobTimeout)
	defer cancel()
	m.markJobsInterruptedForRun(shutdownCtx)

	m.ctx.Logger.Info("Migrator module shutting down")
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes for migrator module
}

// RegisterAdminRoutes registers admin routes for the module.
//
// The migrator takes source database credentials and a filesystem path as
// input and writes directly to core content tables, so its routes are gated to
// admins at the router level as defense in depth beyond the editor-level module
// admin group — the same treatment modules/dbmanager applies.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAdmin())
		r.Get("/migrator", m.handleListSources)
		r.Get("/migrator/{source}", m.handleSourceForm)
		r.Get("/migrator/{source}/status", m.handleJobStatus)
		r.Post("/migrator/{source}/test", m.handleTestConnection)
		r.Post("/migrator/{source}/import", m.handleImport)
		r.Post("/migrator/{source}/delete", m.handleDeleteImported)
	})
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{}
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/migrator"
}

// SidebarLabel returns the i18n key for the admin sidebar label.
func (m *Module) SidebarLabel() string {
	return "nav.migrator"
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
			Description: "Create migrator_imported_items tracking table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS migrator_imported_items (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						source TEXT NOT NULL,
						entity_type TEXT NOT NULL,
						entity_id INTEGER NOT NULL,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					CREATE INDEX IF NOT EXISTS idx_migrator_source ON migrator_imported_items(source);
					CREATE INDEX IF NOT EXISTS idx_migrator_entity ON migrator_imported_items(source, entity_type);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS migrator_imported_items`)
				return err
			},
		},
		{
			Version:     2,
			Description: "Create migrator_import_jobs table for background imports",
			Up: func(db *sql.DB) error {
				// The partial unique index is the concurrency guard: it holds
				// across processes and restarts, which an in-process mutex
				// cannot. Counters are one JSON blob because ImportResult gains
				// fields as sources learn new entity classes, and a blob avoids
				// a schema migration per counter.
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS migrator_import_jobs (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						source TEXT NOT NULL,
						status TEXT NOT NULL DEFAULT 'running'
							CHECK (status IN ('running','completed','failed','interrupted')),
						phase TEXT NOT NULL DEFAULT '',
						processed INTEGER NOT NULL DEFAULT 0,
						total INTEGER NOT NULL DEFAULT 0,
						counters TEXT NOT NULL DEFAULT '{}',
						options TEXT NOT NULL DEFAULT '{}',
						errors TEXT NOT NULL DEFAULT '[]',
						fatal_error TEXT NOT NULL DEFAULT '',
						started_by INTEGER,
						started_by_email TEXT NOT NULL DEFAULT '',
						owner_run_id TEXT NOT NULL DEFAULT '',
						started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						finished_at DATETIME
					);
					CREATE UNIQUE INDEX IF NOT EXISTS idx_migrator_jobs_one_running
						ON migrator_import_jobs(source) WHERE status = 'running';
					CREATE INDEX IF NOT EXISTS idx_migrator_jobs_source_started
						ON migrator_import_jobs(source, started_at DESC);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS migrator_import_jobs`)
				return err
			},
		},
		{
			Version:     3,
			Description: "Separate informational notices from errors on import jobs",
			Up: func(db *sql.DB) error {
				// Notices are things the operator does not need to act on — an
				// optional source table the site does not have, content
				// deliberately out of scope. Storing them alongside errors made
				// a healthy import report failures it had not had.
				if columnExists(db, "migrator_import_jobs", "notices") {
					return nil
				}
				_, err := db.Exec(
					`ALTER TABLE migrator_import_jobs ADD COLUMN notices TEXT NOT NULL DEFAULT '[]'`)
				return err
			},
			Down: func(db *sql.DB) error {
				// SQLite before 3.35 cannot drop a column, and losing the
				// notices is harmless, so this is intentionally a no-op.
				return nil
			},
		},
	}
}

// columnExists reports whether a table already has the named column, so the
// migration is safe to re-run against a database that has it.
func columnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}
