// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// handleListSources handles GET /admin/migrator - displays available sources.
func (m *Module) handleListSources(w http.ResponseWriter, r *http.Request) {
	lang := m.ctx.Render.GetAdminLang(r)

	sources := ListSources()
	var viewSources []MigratorSourceView
	for _, src := range sources {
		viewSources = append(viewSources, MigratorSourceView{
			Name:        src.Name(),
			DisplayName: src.DisplayName(),
			Description: src.Description(),
		})
	}

	viewData := MigratorListViewData{
		Sources: viewSources,
	}

	pc := m.ctx.Render.BuildPageContext(r, i18n.T(lang, "migrator.title"), []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
		{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
		{Label: i18n.T(lang, "migrator.title"), URL: "/admin/migrator", Active: true},
	})
	render.Templ(w, r, MigratorListPage(pc, viewData))
}

// sessionKeyMigratorConfig is the session key for storing migrator config between requests.
const sessionKeyMigratorConfig = "migrator_config"

// handleSourceForm handles GET /admin/migrator/{source} - displays source import form.
func (m *Module) handleSourceForm(w http.ResponseWriter, r *http.Request) {
	lang := m.ctx.Render.GetAdminLang(r)
	sourceName := chi.URLParam(r, "source")

	source, ok := GetSource(sourceName)
	if !ok || source == nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.error_source_not_found"), "error")
		http.Redirect(w, r, "/admin/migrator", http.StatusSeeOther)
		return
	}

	// Get imported item counts for this source. A read failure is logged rather
	// than dropped: nil counts render as "no imported content" and hide the
	// delete form, so the operator would silently lose the only undo path for
	// an import that did happen.
	importedCounts, err := m.getImportedCounts(r.Context(), sourceName)
	if err != nil {
		m.ctx.Logger.Error("failed to read imported counts", "source", sourceName, "error", err)
	}

	// Rendering the current job here means a page refresh — or a second admin's
	// browser — picks up an in-flight import and starts polling immediately.
	job, err := m.latestJob(r.Context(), sourceName)
	if err != nil {
		m.ctx.Logger.Error("failed to read import job", "source", sourceName, "error", err)
	}

	config := make(map[string]string)
	if savedConfig := m.ctx.Render.PopSessionData(r, sessionKeyMigratorConfig); savedConfig != nil {
		config = savedConfig
	} else {
		applySafeDefaults(config, source.ConfigFields())
	}

	viewData := MigratorSourceFormViewData{
		SourceName:     sourceName,
		DisplayName:    source.DisplayName(),
		Description:    source.Description(),
		ConfigFields:   source.ConfigFields(),
		Config:         config,
		ImportedCounts: importedCounts,
		Job:            job,
	}

	pc := m.ctx.Render.BuildPageContext(r, i18n.T(lang, "migrator.import_from", source.DisplayName()), []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
		{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
		{Label: i18n.T(lang, "migrator.title"), URL: "/admin/migrator"},
		{Label: source.DisplayName(), URL: "/admin/migrator/" + sourceName, Active: true},
	})
	render.Templ(w, r, MigratorSourceFormPage(pc, viewData))
}

// applySafeDefaults applies non-sensitive defaults to config.
// Password defaults are intentionally skipped to avoid rendering secrets in the UI.
func applySafeDefaults(config map[string]string, fields []ConfigField) {
	for _, field := range fields {
		if field.Default == "" || field.Type == "password" {
			continue
		}
		config[field.Name] = field.Default
	}
}

// withoutSecrets copies config minus every password-typed field.
//
// The form config is round-tripped through the session so a failed connection
// test does not clear what the admin typed. The session store is SQLite-backed
// and gob-encoded, not encrypted, so persisting the source database password
// there would write it in plaintext into the CMS database — and it would then
// be echoed back into the rendered form. Secrets are re-entered instead.
func withoutSecrets(config map[string]string, fields []ConfigField) map[string]string {
	safe := make(map[string]string, len(config))
	secret := make(map[string]bool, len(fields))
	for _, field := range fields {
		if field.Type == "password" {
			secret[field.Name] = true
		}
	}
	for name, value := range config {
		if secret[name] {
			continue
		}
		safe[name] = value
	}
	return safe
}

// sourceRequestContext holds common data extracted from migrator handler requests.
type sourceRequestContext struct {
	User       *store.User
	Lang       string
	SourceName string
	Source     Source
}

// getSourceContext validates user auth and source, returning false if validation failed (response already sent).
func (m *Module) getSourceContext(w http.ResponseWriter, r *http.Request) (sourceRequestContext, bool) {
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return sourceRequestContext{}, false
	}

	lang := m.ctx.Render.GetAdminLang(r)
	sourceName := chi.URLParam(r, "source")

	source, ok := GetSource(sourceName)
	if !ok || source == nil {
		m.ctx.Render.SetFlash(r, i18n.T(lang, "migrator.error_source_not_found"), "error")
		http.Redirect(w, r, "/admin/migrator", http.StatusSeeOther)
		return sourceRequestContext{}, false
	}

	return sourceRequestContext{
		User:       user,
		Lang:       lang,
		SourceName: sourceName,
		Source:     source,
	}, true
}

// collectSourceConfig collects config values from form based on source's config fields.
func collectSourceConfig(r *http.Request, source Source) map[string]string {
	cfg := make(map[string]string)
	for _, field := range source.ConfigFields() {
		cfg[field.Name] = r.FormValue(field.Name)
	}
	return cfg
}

// parseSourceForm validates context, parses form, and collects config.
// Returns nil context if validation failed (response already written).
func (m *Module) parseSourceForm(w http.ResponseWriter, r *http.Request) (*sourceRequestContext, map[string]string) {
	ctx, ok := m.getSourceContext(w, r)
	if !ok {
		return nil, nil
	}
	if err := r.ParseForm(); err != nil {
		m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.error_parse_form"), "error")
		http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
		return nil, nil
	}
	return &ctx, collectSourceConfig(r, ctx.Source)
}

// handleTestConnection handles POST /admin/migrator/{source}/test - tests connection.
func (m *Module) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	ctx, cfg := m.parseSourceForm(w, r)
	if ctx == nil {
		return
	}

	// Test connection
	if err := ctx.Source.TestConnection(cfg); err != nil {
		m.ctx.Logger.Error("connection test failed", "source", ctx.SourceName, "error", err)
		// Save config to session so form values are preserved
		m.ctx.Render.SetSessionData(r, sessionKeyMigratorConfig, withoutSecrets(cfg, ctx.Source.ConfigFields()))
		m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.error_connection")+": "+err.Error(), "error")
		http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
		return
	}

	// Save config on success too, so user can proceed with import
	m.ctx.Render.SetSessionData(r, sessionKeyMigratorConfig, withoutSecrets(cfg, ctx.Source.ConfigFields()))
	m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.success_connection"), "success")
	http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
}

// parseImportOptions maps the import form checkboxes onto ImportOptions.
//
// This is the single source of truth for the form-key to field mapping;
// TestImportOptionsHaveFormCheckboxes derives the expected keys from the struct
// and fails if the two drift apart.
func parseImportOptions(r *http.Request) ImportOptions {
	on := func(key string) bool { return r.FormValue(key) == "on" }
	return ImportOptions{
		ImportTags:       on("import_tags"),
		ImportCategories: on("import_categories"),
		ImportMedia:      on("import_media"),
		ImportPosts:      on("import_posts"),
		ImportPages:      on("import_pages"),
		ImportMenus:      on("import_menus"),
		ImportUsers:      on("import_users"),
		SkipExisting:     on("skip_existing"),
	}
}

// jobRun carries everything the detached import goroutine needs.
//
// Every value is copied out of the *http.Request before the goroutine starts:
// the request may be recycled once the handler returns, so the goroutine must
// never touch it.
type jobRun struct {
	ID         int64
	SourceName string
	Source     Source
	Cfg        map[string]string
	Opts       ImportOptions
	UserID     int64
	ClientIP   string
	RequestURL string
}

// handleImport handles POST /admin/migrator/{source}/import.
//
// It starts a detached background job and redirects immediately. The import is
// deliberately not run inline: the router installs a 30s request timeout
// (cmd/ocms/main.go), and since that timeout replaces r.Context(), an inline
// import of any real site would be cancelled part-way through and answered with
// a 503 while still holding half-written content.
func (m *Module) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx, cfg := m.parseSourceForm(w, r)
	if ctx == nil {
		return
	}
	opts := parseImportOptions(r)

	jobID, err := m.startJob(r.Context(), ctx.SourceName, ctx.User.Email, ctx.User.ID, opts)
	if err != nil {
		if errors.Is(err, errImportRunning) {
			m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.error_import_running"), "error")
		} else {
			m.ctx.Logger.Error("failed to start import job", "source", ctx.SourceName, "error", err)
			m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.error_import")+": "+err.Error(), "error")
		}
		m.ctx.Render.SetSessionData(r, sessionKeyMigratorConfig, withoutSecrets(cfg, ctx.Source.ConfigFields()))
		http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
		return
	}

	m.ctx.Logger.Info("starting import",
		"source", ctx.SourceName,
		"job_id", jobID,
		"user", ctx.User.Email,
		"import_tags", opts.ImportTags,
		"import_categories", opts.ImportCategories,
		"import_media", opts.ImportMedia,
		"import_posts", opts.ImportPosts,
		"import_pages", opts.ImportPages,
		"import_menus", opts.ImportMenus,
		"import_users", opts.ImportUsers,
		"skip_existing", opts.SkipExisting,
	)

	// WithoutCancel keeps request-scoped values while dropping both the
	// cancellation and the router's 30s deadline.
	jobCtx, cancelJob := context.WithTimeout(context.WithoutCancel(r.Context()), importJobTimeout)

	run := jobRun{
		ID:         jobID,
		SourceName: ctx.SourceName,
		Source:     ctx.Source,
		Cfg:        cfg,
		Opts:       opts,
		UserID:     ctx.User.ID,
		ClientIP:   middleware.GetClientIP(r),
		RequestURL: middleware.GetRequestURL(r),
	}

	m.jobsMu.Lock()
	m.cancel[jobID] = cancelJob
	m.jobsMu.Unlock()
	m.jobsWG.Add(1)
	go m.runImportJob(jobCtx, run)

	// The config round-trips through the session so the form stays filled in,
	// and the status panel on the form page picks the job up on arrival.
	m.ctx.Render.SetSessionData(r, sessionKeyMigratorConfig, withoutSecrets(cfg, ctx.Source.ConfigFields()))
	m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.import_started"), "success")
	http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
}

// runImportJob executes a source import on a detached context and records the
// terminal state. It owns the job row from startJob until it writes a terminal
// status.
//
// The goroutine cannot set a flash message — there is no request or session
// here — so completion surfaces through the status fragment and the event log.
func (m *Module) runImportJob(ctx context.Context, run jobRun) {
	defer m.jobsWG.Done()
	defer func() {
		m.jobsMu.Lock()
		cancel := m.cancel[run.ID]
		delete(m.cancel, run.ID)
		delete(m.live, run.SourceName)
		m.jobsMu.Unlock()
		// Release the 6h timer and the retained request-scoped context values.
		// Only Shutdown ever called these, so every completed import used to
		// pin its whole context chain until the timeout expired.
		if cancel != nil {
			cancel()
		}
	}()

	// chi's Recoverer middleware does not reach this goroutine, so without an
	// explicit recover a panic inside any source would take down the server.
	defer func() {
		if rec := recover(); rec != nil {
			m.ctx.Logger.Error("import panicked", "source", run.SourceName, "job_id", run.ID, "panic", rec)
			// nil is correct here: Import never returned, so there is no
			// result. finishJob preserves the counters already flushed to the
			// row rather than overwriting them with empty maps.
			m.finalizeJob(ctx, run, JobFailed, nil, fmt.Errorf("import panicked: %v", rec))
		}
	}()

	// Backstop for the flush goroutine. The explicit stopFlush() below handles
	// the normal path, but a panic in Import would otherwise skip it and leave
	// the ticker running until the job context expires — up to six hours.
	// stopFlush is idempotent, so calling it twice is safe.
	var stopFlush func()
	defer func() {
		if stopFlush != nil {
			stopFlush()
		}
	}()

	progress := newJobProgress()
	m.jobsMu.Lock()
	m.live[run.SourceName] = progress
	m.jobsMu.Unlock()

	stopFlush = m.startProgressFlush(ctx, run.ID, progress)

	result, err := run.Source.Import(ctx, m.ctx.DB, run.Cfg, run.Opts, m)
	stopFlush()

	status := deriveJobStatus(err, ctx.Err(), result)
	switch status {
	case JobFailed:
		m.ctx.Logger.Error("import failed", "source", run.SourceName, "job_id", run.ID, "error", err)
	case JobPartial:
		m.ctx.Logger.Warn("import completed with errors",
			"source", run.SourceName, "job_id", run.ID,
			"errors", len(result.Errors), "imported", result.TotalImported())
	case JobRunning, JobCompleted, JobInterrupted:
		// No extra logging; finalizeJob records the outcome.
	}

	m.finalizeJob(ctx, run, status, result, err)
}

// deriveJobStatus maps an import outcome onto a terminal job status.
//
// Two mistakes are baked into the ordering here, both of which shipped:
//
//   - Testing `err != nil` before the context made JobInterrupted unreachable
//     for any source that correctly propagates context.Canceled, so a routine
//     restart mid-import was reported as a hard "Failed — context canceled".
//   - Sources report per-item failures on the result, never as the returned
//     error, so deriving status from the error alone reported an import in
//     which every single item failed as a clean "Completed".
func deriveJobStatus(err, ctxErr error, result *ImportResult) JobStatus {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return JobInterrupted
	case err != nil:
		return JobFailed
	case ctxErr != nil:
		return JobInterrupted
	case result != nil && result.HasErrors():
		return JobPartial
	default:
		return JobCompleted
	}
}

// startProgressFlush persists progress on a ticker and returns a stop function.
//
// The goroutine joins jobsWG so Shutdown cannot return while it is mid-write.
// Its writes use a detached context, so cancellation alone would not stop it
// racing the database being closed. Registering here is safe: the caller
// (runImportJob) is already counted in the group, so Wait cannot have returned.
func (m *Module) startProgressFlush(ctx context.Context, jobID int64, progress *jobProgress) func() {
	done := make(chan struct{})
	var once sync.Once

	m.jobsWG.Add(1)
	go func() {
		defer m.jobsWG.Done()
		ticker := time.NewTicker(progressFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				snap, changed := progress.snapshot()
				if !changed {
					continue
				}
				// Progress writes use a detached context so a cancelled import
				// can still record how far it got.
				flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishJobTimeout)
				if err := m.flushJobProgress(flushCtx, jobID, snap); err != nil {
					m.ctx.Logger.Warn("failed to flush import progress", "job_id", jobID, "error", err)
				}
				cancel()
			}
		}
	}()

	return func() { once.Do(func() { close(done) }) }
}

// finalizeJob writes the terminal row, invalidates caches and records the audit
// event.
//
// The terminal write uses a context derived with WithoutCancel: by this point
// the job context may already be cancelled, and reusing it would fail the
// UPDATE and leave the row stuck in "running" forever.
func (m *Module) finalizeJob(ctx context.Context, run jobRun, status JobStatus, result *ImportResult, fatal error) {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishJobTimeout)
	defer cancel()

	if err := m.finishJob(finishCtx, run.ID, status, result, fatal); err != nil {
		m.ctx.Logger.Error("failed to record import job result", "job_id", run.ID, "error", err)
	}

	if result != nil {
		for i, errMsg := range result.Errors {
			m.ctx.Logger.Error("import error", "source", run.SourceName, "index", i, "message", errMsg)
		}
		// Notices are expected outcomes, not failures — logging them at error
		// level is what made a healthy import look broken in the first place.
		for i, notice := range result.Notices {
			m.ctx.Logger.Info("import notice", "source", run.SourceName, "index", i, "message", notice)
		}
		m.ctx.Logger.Info("import completed",
			"source", run.SourceName,
			"job_id", run.ID,
			"status", string(status),
			"imported", result.TotalImported(),
			"skipped", result.TotalSkipped(),
			"errors", len(result.Errors),
			"notices", len(result.Notices),
		)
	}

	m.invalidateCaches(run.Opts)

	if m.ctx.Events != nil {
		metadata := map[string]any{"source": run.SourceName, "status": string(status)}
		if result != nil {
			for entityType, count := range result.Counters() {
				metadata[string(entityType)+"_imported"] = count
			}
			metadata["skipped"] = result.TotalSkipped()
			metadata["errors"] = len(result.Errors)
			metadata["notices"] = len(result.Notices)
		}
		userID := run.UserID
		// This is the only durable record that the import happened; a failure
		// to write it must be visible in the log, not swallowed.
		if err := m.ctx.Events.LogMigratorEvent(finishCtx, "info",
			"Content imported from "+run.SourceName, &userID, run.ClientIP, run.RequestURL, metadata); err != nil {
			m.ctx.Logger.Error("failed to record import audit event",
				"source", run.SourceName, "job_id", run.ID, "error", err)
		}
	}
}

// invalidateCaches drops the caches a bulk content write invalidates.
//
// Nil-safe: module.Context.Cache is unset in tests and in any embedder that
// does not wire it.
func (m *Module) invalidateCaches(opts ImportOptions) {
	if m.ctx == nil || m.ctx.Cache == nil {
		return
	}
	// InvalidateContent covers both the page cache and the sitemap.
	m.ctx.Cache.InvalidateContent()
	if opts.ImportMenus {
		m.ctx.Cache.InvalidateMenus()
	}
}

// ReportProgress records a progress sample published by a running source.
// It implements types.ProgressReporter.
func (m *Module) ReportProgress(_ context.Context, p Progress) {
	m.jobsMu.Lock()
	progress := m.live[p.Source]
	m.jobsMu.Unlock()
	if progress != nil {
		progress.report(p)
	}
}

// handleJobStatus handles GET /admin/migrator/{source}/status and returns the
// htmx status fragment.
func (m *Module) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	ctx, ok := m.getSourceContext(w, r)
	if !ok {
		return
	}

	job, err := m.latestJob(r.Context(), ctx.SourceName)
	if err != nil {
		m.ctx.Logger.Error("failed to read import job", "source", ctx.SourceName, "error", err)
	}

	counts, countsErr := m.getImportedCounts(r.Context(), ctx.SourceName)
	if countsErr != nil {
		m.ctx.Logger.Error("failed to read imported counts", "source", ctx.SourceName, "error", countsErr)
	}

	// Deliberately not Render.BuildPageContext: that pops the flash out of the
	// session, and this fragment never renders @Alert. A poll every two seconds
	// would swallow every flash the operator was meant to see, including ones
	// set in their other tabs. The fragment only needs translations.
	pc := &adminviews.PageContext{AdminLang: ctx.Lang}

	// Always 200 with a fragment: htmx treats 204 as "no swap", which would
	// strand a stale running card on screen forever.
	w.Header().Set("Cache-Control", "no-store")
	// On a counts read failure the out-of-band swap is suppressed: swapping in
	// nil counts would replace the Imported Content card with "nothing
	// imported" and remove the delete form, destroying the only undo path for
	// an import that did happen.
	render.Templ(w, r, MigratorJobStatusResponse(
		pc, buildJobStatusView(ctx.SourceName, job, err), counts, countsErr == nil))
}

// buildJobStatusView assembles the status fragment's view data.
//
// readErr is the job lookup's error. A failed lookup keeps polling on purpose:
// the state is unknown rather than idle, so stopping would strand the panel on
// a blank card after a single transient error, with no way back except F5.
func buildJobStatusView(sourceName string, job *ImportJob, readErr error) MigratorJobStatusViewData {
	data := MigratorJobStatusViewData{
		SourceName: sourceName,
		StatusURL:  "/admin/migrator/" + sourceName + "/status",
		Job:        job,
		ReadFailed: readErr != nil,
	}
	if readErr != nil {
		data.Polling = true
		return data
	}
	if job != nil {
		data.Stale = job.IsStale(time.Now())
		data.Polling = job.Status == JobRunning && !data.Stale
	}
	return data
}

// handleDeleteImported handles POST /admin/migrator/{source}/delete - deletes all imported content.
func (m *Module) handleDeleteImported(w http.ResponseWriter, r *http.Request) {
	ctx, ok := m.getSourceContext(w, r)
	if !ok {
		return
	}

	m.ctx.Logger.Info("deleting imported content", "source", ctx.SourceName, "user", ctx.User.Email)

	// Delete all imported items for this source
	deleted, err := m.deleteImportedItems(r.Context(), ctx.SourceName)
	if err != nil {
		m.ctx.Logger.Error("delete failed", "source", ctx.SourceName, "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.error_delete")+": "+err.Error(), "error")
		http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
		return
	}

	m.ctx.Logger.Info("deleted imported content", "source", ctx.SourceName, "deleted", deleted)

	// Log event for audit trail
	if m.ctx.Events != nil {
		metadata := map[string]any{"source": ctx.SourceName}
		for entityType, count := range deleted {
			metadata[entityType+"_deleted"] = count
		}
		clientIP := middleware.GetClientIP(r)
		// As above: the only durable record that content was deleted.
		if err := m.ctx.Events.LogMigratorEvent(r.Context(), "info",
			"Imported content deleted from "+ctx.SourceName, &ctx.User.ID, clientIP,
			middleware.GetRequestURL(r), metadata); err != nil {
			m.ctx.Logger.Error("failed to record delete audit event",
				"source", ctx.SourceName, "error", err)
		}
	}

	m.invalidateCaches(ImportOptions{ImportMenus: true})

	msg := i18n.T(ctx.Lang, "migrator.success_delete_summary", formatEntityCounts(ctx.Lang, deleted))
	m.ctx.Render.SetFlash(r, msg, "success")
	http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
}

// formatEntityCounts renders per-entity counts as a translated, comma-separated
// list such as "3 posts, 1 menu".
//
// The counts are composed in Go rather than passed as positional %d arguments
// to one format string: i18n.T is a bare fmt.Sprintf, so every new entity type
// would otherwise have to be threaded through every locale's format string in
// lockstep, and a single mismatch renders as "%!d(MISSING)" in production.
func formatEntityCounts(lang string, counts map[string]int) string {
	var parts []string
	for _, entityType := range types.AllEntityTypes {
		count := counts[string(entityType)]
		if count == 0 {
			continue
		}
		label := i18n.T(lang, "migrator.imported_"+string(entityType))
		parts = append(parts, fmt.Sprintf("%d %s", count, label))
	}
	if len(parts) == 0 {
		return i18n.T(lang, "migrator.no_items")
	}
	return strings.Join(parts, ", ")
}

// TrackImportedItem records an imported item for later deletion.
//
// It doubles as the live-progress spine: every source already calls this once
// per imported entity, so counting here gives any source a progress display
// without the source having to opt in.
func (m *Module) TrackImportedItem(ctx context.Context, source, entityType string, entityID int64) error {
	m.jobsMu.Lock()
	progress := m.live[source]
	m.jobsMu.Unlock()
	if progress != nil {
		progress.addItem(types.EntityType(entityType))
	}

	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT INTO migrator_imported_items (source, entity_type, entity_id, created_at)
		VALUES (?, ?, ?, ?)
	`, source, entityType, entityID, time.Now())
	return err
}

// getImportedCounts returns counts of imported items by entity type for a source.
func (m *Module) getImportedCounts(ctx context.Context, source string) (map[string]int, error) {
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT entity_type, COUNT(*) as cnt
		FROM migrator_imported_items
		WHERE source = ?
		GROUP BY entity_type
	`, source)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	counts := make(map[string]int)
	for rows.Next() {
		var entityType string
		var count int
		if err := rows.Scan(&entityType, &count); err != nil {
			return nil, err
		}
		counts[entityType] = count
	}
	return counts, rows.Err()
}

// getImportedItems returns all imported item IDs of a given type for a source.
func (m *Module) getImportedItems(ctx context.Context, source, entityType string) ([]int64, error) {
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT entity_id FROM migrator_imported_items
		WHERE source = ? AND entity_type = ?
	`, source, entityType)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// entityDeleter binds an entity type to the store call that removes one row.
//
// Exactly one of del and cascadesFrom is set: cascadesFrom marks a type whose
// rows are removed by a database cascade rather than by an explicit delete.
type entityDeleter struct {
	entityType   types.EntityType
	del          func(ctx context.Context, q *store.Queries, id int64) error
	cascadesFrom types.EntityType
}

// deleters returns every trackable entity type in dependency-safe deletion
// order. The order is dictated by the schema's foreign keys:
//
//   - menu_items.menu_id cascades from menus, so items are removed first to
//     keep their counts accurate
//   - pages.author_id is ON DELETE RESTRICT, so pages and posts must be gone
//     before their authors
//   - page_aliases.page_id is ON DELETE CASCADE, so aliases need no delete of
//     their own — they vanish with their page
//   - categories.parent_id is ON DELETE SET NULL, so category order is free
//
// TestDeleteImportedItemsCoversAllEntityTypes fails if a type in
// types.AllEntityTypes has neither a deleter nor a cascade source.
func (m *Module) deleters() []entityDeleter {
	deletePage := func(ctx context.Context, q *store.Queries, id int64) error {
		if err := q.ClearPageTags(ctx, id); err != nil {
			return err
		}
		if err := q.ClearPageCategories(ctx, id); err != nil {
			return err
		}
		return q.DeletePage(ctx, id)
	}

	return []entityDeleter{
		{entityType: types.EntityMenuItem, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeleteMenuItem(ctx, id)
		}},
		{entityType: types.EntityMenu, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeleteMenu(ctx, id)
		}},
		{entityType: types.EntityAlias, cascadesFrom: types.EntityPage},
		{entityType: types.EntityPost, del: deletePage},
		{entityType: types.EntityPage, del: deletePage},
		{entityType: types.EntityTag, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeleteTag(ctx, id)
		}},
		{entityType: types.EntityCategory, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeleteCategory(ctx, id)
		}},
		{entityType: types.EntityMedia, del: m.deleteImportedMedia},
		{entityType: types.EntityUser, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeleteUser(ctx, id)
		}},
	}
}

// deleteImportedMedia removes one media row, its variants and its files on disk.
func (m *Module) deleteImportedMedia(ctx context.Context, q *store.Queries, id int64) error {
	if media, err := q.GetMediaByID(ctx, id); err == nil {
		m.deleteMediaFiles(media.Uuid)
		if err := q.DeleteMediaVariants(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete media variants", "id", id, "error", err)
		}
	}
	return q.DeleteMedia(ctx, id)
}

// deleteImportedItems deletes all content imported from a source.
//
// The returned counts are "tracked items of this type that were removed". A
// cascade can delete a child before its own turn comes — a nested menu item, for
// instance — and that still counts, which is what an admin wants to read.
func (m *Module) deleteImportedItems(ctx context.Context, source string) (map[string]int, error) {
	queries := store.New(m.ctx.DB)
	deleted := make(map[string]int)

	for _, d := range m.deleters() {
		ids, err := m.getImportedItems(ctx, source, string(d.entityType))
		if err != nil {
			return nil, err
		}
		if d.del == nil {
			// Removed by a database cascade from its parent entity.
			deleted[string(d.entityType)] = len(ids)
			continue
		}
		for _, id := range ids {
			if err := d.del(ctx, queries, id); err != nil {
				m.ctx.Logger.Warn("failed to delete imported item",
					"type", string(d.entityType), "id", id, "error", err)
				continue
			}
			deleted[string(d.entityType)]++
		}
	}

	// Clear tracking table for this source
	if _, err := m.ctx.DB.ExecContext(ctx, `DELETE FROM migrator_imported_items WHERE source = ?`, source); err != nil {
		return deleted, err
	}

	return deleted, nil
}

// deleteMediaFiles removes media files from disk.
//
// The UUID is validated before it reaches os.RemoveAll. Media UUIDs are
// normally server-generated, but internal/transfer/importer.go carries the UUID
// over from an uploaded archive, so "the value came from our own database" is
// not a strong enough guarantee for a recursive delete. Each path is then
// joined properly and confirmed to sit inside the uploads root.
func (m *Module) deleteMediaFiles(mediaUUID string) {
	if !isMediaUUID(mediaUUID) {
		m.ctx.Logger.Warn("refusing to delete media directory for malformed uuid", "uuid", mediaUUID)
		return
	}

	uploadDir := os.Getenv("OCMS_UPLOADS_DIR")
	if uploadDir == "" {
		uploadDir = "./uploads"
	}
	variants := []string{"originals", "thumbnail", "grid", "small", "medium", "large"}

	for _, variant := range variants {
		dir := filepath.Join(uploadDir, variant, mediaUUID)
		if err := util.ValidatePathWithinBase(uploadDir, dir); err != nil {
			m.ctx.Logger.Warn("refusing to delete path outside uploads root", "dir", dir, "error", err)
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			m.ctx.Logger.Warn("failed to delete media directory", "dir", dir, "error", err)
		}
	}
}

// isMediaUUID reports whether s is a canonical hyphenated UUID. Deliberately
// stricter than uuid.Parse, which also accepts unhyphenated and URN forms.
func isMediaUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
