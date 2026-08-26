// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package migrator provides a module for migrating content from other CMS platforms to oCMS.
// It supports multiple source systems through a pluggable importer architecture.
package migrator

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/modules/migrator/sources/drupal"
	"github.com/olegiv/ocms-go/modules/migrator/sources/elefant"
	"github.com/olegiv/ocms-go/modules/migrator/sources/phpnuke"
	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
)

//go:embed locales
var localesFS embed.FS

// shutdownDrainTimeout bounds how long Shutdown waits for in-flight imports to
// notice cancellation before the process moves on.
const (
	shutdownDrainTimeout = 10 * time.Second
	cleanupDrainTimeout  = 30 * time.Second
)

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

	// removeMediaFiles is injectable so durable retry behavior can be tested
	// deterministically without depending on host filesystem permissions.
	removeMediaFiles func(uploadRoot, mediaUUID string) error
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

// AllowedEnvs restricts the migrator to non-production environments. On first
// run the module is auto-inserted as inactive outside this list.
//
// The migrator is a one-shot import tool that dials external databases and
// takes source credentials; a live site does not need it running. Registering
// it active by default is what put an unrestricted migrator on the public
// demo, where the admin routes it registers are reachable with the published
// demo credentials.
//
// This is a default for installs that have no modules row yet — existing
// deployments keep whatever state they already have, so an operator running a
// legitimate one-off import in production is unaffected.
func (m *Module) AllowedEnvs() []string {
	return []string{"development"}
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
	elefantSource := elefant.NewSource()
	elefantSource.SetPublicRouteChecker(ctx.PublicRouteChecker)
	RegisterSource(elefantSource)
	drupalSource := drupal.NewSource()
	drupalSource.SetPublicRouteChecker(ctx.PublicRouteChecker)
	RegisterSource(drupalSource)
	phpnukeSource := phpnuke.NewSource()
	phpnukeSource.SetPublicRouteChecker(ctx.PublicRouteChecker)
	RegisterSource(phpnukeSource)

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupDrainTimeout)
	if err := m.drainMediaCleanup(cleanupCtx, ""); err != nil {
		m.ctx.Logger.Warn("media cleanup remains queued after startup retry", "error", err)
	}
	cleanupCancel()

	if reaped, err := m.reapStaleJobs(context.Background()); err != nil {
		m.ctx.Logger.Warn("failed to reap stale migrator jobs", "error", err)
	} else if reaped > 0 {
		m.ctx.Logger.Info("marked orphaned migrator jobs as interrupted", "count", reaped)
	}

	m.ctx.Logger.Info("Migrator module initialized", "sources", len(sources))
	return nil
}

// CheckActivation refuses runtime activation when production requires a source
// database host allowlist and none is configured.
//
// runPostModulePostureAudits performs the same check, but only at startup. A
// deployment whose migrator was inactive at boot could otherwise be switched on
// from the admin UI with an empty allowlist, and CheckDBHostAllowed treats an
// empty list as unrestricted — so the production policy silently lapsed until
// the next restart.
func (m *Module) CheckActivation(ctx *module.Context) error {
	// Read the registry's context, not m.ctx: this runs before Init, so the
	// module's own context is still nil whenever it was inactive at startup —
	// the exact case this guard is for.
	if ctx == nil || ctx.Config == nil {
		return nil
	}
	cfg := ctx.Config
	if cfg.Env != "production" || !cfg.RequireMigratorAllowedDBHosts {
		return nil
	}
	if strings.TrimSpace(cfg.MigratorAllowedDBHosts) == "" {
		return fmt.Errorf(
			"%s must be configured in production before the migrator can be activated",
			shared.EnvAllowedDBHosts)
	}
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

		// A demo publishes its admin credentials, which turns every route that
		// dials a caller-supplied host into an open outbound connector: the
		// host allowlist matches on hostname only, so an allowed entry covers
		// all of its ports, and pointing a source at the server's own listener
		// parks a request until the driver's handshake times out. Repeat that
		// and a small demo machine runs out of handlers. dbmanager gates its
		// SQL execution the same way.
		r.Group(func(r chi.Router) {
			r.Use(middleware.BlockInDemoMode(middleware.RestrictionImportData))
			r.Post("/migrator/{source}/test", m.handleTestConnection)
			r.Post("/migrator/{source}/import", m.handleImport)
			r.Post("/migrator/{source}/delete", m.handleDeleteImported)
		})
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
							CHECK (status IN ('running','completed','failed','partial','interrupted')),
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
		{
			Version:     4,
			Description: "Record skipped counts on import jobs",
			Up: func(db *sql.DB) error {
				// Skipped counts were computed by every import and then thrown
				// away, so a file the importer declined to handle left no trace
				// anywhere the operator could see it.
				if columnExists(db, "migrator_import_jobs", "skipped") {
					return nil
				}
				_, err := db.Exec(
					`ALTER TABLE migrator_import_jobs ADD COLUMN skipped TEXT NOT NULL DEFAULT '{}'`)
				return err
			},
			Down: func(db *sql.DB) error {
				// As with notices: dropping a column is not portable and the
				// counts are reconstructable by re-running the import.
				return nil
			},
		},
		{
			Version:     5,
			Description: "Allow the partial job status",
			// The v2 CHECK constraint predates the 'partial' status. SQLite
			// cannot alter it, so the helper performs an atomic table rebuild.
			Up: migrateJobsPartialStatus,
			Down: func(db *sql.DB) error {
				// Narrowing the constraint again would reject rows that are
				// already stored as 'partial'.
				return nil
			},
		},
		{
			Version:     6,
			Description: "Create durable media cleanup queue",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS migrator_media_cleanup_queue (
						source TEXT NOT NULL,
						upload_root TEXT NOT NULL,
						media_uuid TEXT NOT NULL,
						attempts INTEGER NOT NULL DEFAULT 0,
						last_error TEXT NOT NULL DEFAULT '',
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						PRIMARY KEY (source, upload_root, media_uuid)
					);
					CREATE INDEX IF NOT EXISTS idx_migrator_media_cleanup_source
						ON migrator_media_cleanup_queue(source);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS migrator_media_cleanup_queue`)
				return err
			},
		},
	}
}

const createPartialJobsTableSQL = `
	CREATE TABLE migrator_import_jobs_new (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		source TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'running'
			CHECK (status IN ('running','completed','failed','partial','interrupted')),
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
		finished_at DATETIME,
		notices TEXT NOT NULL DEFAULT '[]',
		skipped TEXT NOT NULL DEFAULT '{}'
	)`

const copyJobsToPartialTableSQL = `
	INSERT INTO migrator_import_jobs_new
		(id, source, status, phase, processed, total, counters, options, errors,
		 fatal_error, started_by, started_by_email, owner_run_id,
		 started_at, updated_at, finished_at, notices, skipped)
	SELECT id, source, status, phase, processed, total, counters, options, errors,
		 fatal_error, started_by, started_by_email, owner_run_id,
		 started_at, updated_at, finished_at, notices, skipped
	FROM migrator_import_jobs`

type migrationQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func migrationTableDDL(q migrationQueryer, table string) (string, bool, error) {
	var ddl string
	err := q.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
	).Scan(&ddl)
	if err == nil {
		return ddl, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return "", false, err
}

// migrateJobsPartialStatus rebuilds the jobs table inside one transaction.
// It also repairs the two partial states left by the historical multi-statement
// migration: a stale _new table beside an intact old table, or a complete _new
// table after the old table was dropped but before rename/index creation.
func migrateJobsPartialStatus(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin partial-status migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	mainDDL, mainExists, err := migrationTableDDL(tx, "migrator_import_jobs")
	if err != nil {
		return fmt.Errorf("inspect jobs table: %w", err)
	}
	newDDL, newExists, err := migrationTableDDL(tx, "migrator_import_jobs_new")
	if err != nil {
		return fmt.Errorf("inspect temporary jobs table: %w", err)
	}

	switch {
	case mainExists && strings.Contains(mainDDL, "'partial'"):
		if newExists {
			if _, err := tx.Exec(`DROP TABLE migrator_import_jobs_new`); err != nil {
				return fmt.Errorf("drop stale temporary jobs table: %w", err)
			}
		}
	case !mainExists && newExists && strings.Contains(newDDL, "'partial'"):
		if _, err := tx.Exec(`ALTER TABLE migrator_import_jobs_new RENAME TO migrator_import_jobs`); err != nil {
			return fmt.Errorf("recover temporary jobs table: %w", err)
		}
	case !mainExists:
		return errors.New("migrator_import_jobs is missing and no recoverable temporary table exists")
	default:
		if newExists {
			if _, err := tx.Exec(`DROP TABLE migrator_import_jobs_new`); err != nil {
				return fmt.Errorf("drop stale temporary jobs table: %w", err)
			}
		}
		if _, err := tx.Exec(createPartialJobsTableSQL); err != nil {
			return fmt.Errorf("create replacement jobs table: %w", err)
		}
		if _, err := tx.Exec(copyJobsToPartialTableSQL); err != nil {
			return fmt.Errorf("copy jobs into replacement table: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE migrator_import_jobs`); err != nil {
			return fmt.Errorf("drop legacy jobs table: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE migrator_import_jobs_new RENAME TO migrator_import_jobs`); err != nil {
			return fmt.Errorf("activate replacement jobs table: %w", err)
		}
	}

	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_migrator_jobs_one_running
		ON migrator_import_jobs(source) WHERE status = 'running'`); err != nil {
		return fmt.Errorf("create running-job index: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_migrator_jobs_source_started
		ON migrator_import_jobs(source, started_at DESC)`); err != nil {
		return fmt.Errorf("create job-history index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit partial-status migration: %w", err)
	}
	return nil
}

// statusCheckAllows reports whether the jobs table's CHECK constraint already
// permits a status value, so the rebuild is safe to re-run.
func statusCheckAllows(db *sql.DB, status string) bool {
	var ddl string
	err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'migrator_import_jobs'`,
	).Scan(&ddl)
	if err != nil {
		return false
	}
	return strings.Contains(ddl, "'"+status+"'")
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
