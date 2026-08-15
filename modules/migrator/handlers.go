// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
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
	importedCounts, pendingMediaCleanup, err := m.getImportedContentState(r.Context(), sourceName)
	if err != nil {
		m.ctx.Logger.Error("failed to read imported counts", "source", sourceName, "error", err)
	}

	// Rendering the current job here means a page refresh — or a second admin's
	// browser — picks up an in-flight import and starts polling immediately.
	job, jobErr := m.latestJob(r.Context(), sourceName)
	if jobErr != nil {
		m.ctx.Logger.Error("failed to read import job", "source", sourceName, "error", jobErr)
	}

	config := make(map[string]string)
	if savedConfig := m.ctx.Render.PopSessionData(r, sessionKeyMigratorConfig); savedConfig != nil {
		config = savedConfig
	} else {
		applySafeDefaults(config, source.ConfigFields())
	}

	viewData := MigratorSourceFormViewData{
		SourceName:          sourceName,
		DisplayName:         source.DisplayName(),
		Description:         source.Description(),
		ConfigFields:        source.ConfigFields(),
		Config:              config,
		ImportedCounts:      importedCounts,
		PendingMediaCleanup: pendingMediaCleanup,
		Job:                 job,
		JobReadErr:          jobErr,
		SupportedOptions:    types.SupportedImportOptionSet(source),
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

	var err error
	if tester, ok := ctx.Source.(types.ContextConnectionTester); ok {
		err = tester.TestConnectionContext(r.Context(), cfg)
	} else {
		// Compatibility path for third-party sources compiled against the
		// original interface. Built-in sources implement the context-aware
		// capability above.
		err = ctx.Source.TestConnection(cfg)
	}
	if err != nil {
		if r.Context().Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			m.ctx.Logger.Info("connection test stopped with request context",
				"source", ctx.SourceName, "error", err)
			return
		}
		m.ctx.Logger.Error("connection test failed", "source", ctx.SourceName, "error", err)
		// Save config to session so form values are preserved
		m.ctx.Render.SetSessionData(r, sessionKeyMigratorConfig, withoutSecrets(cfg, ctx.Source.ConfigFields()))
		m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.error_connection")+": "+err.Error(), "error")
		http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
		return
	}
	if r.Context().Err() != nil {
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
	// Options the source ignores are cleared rather than recorded. The form no
	// longer offers them, so this only bites a hand-crafted POST — but the
	// options blob is persisted on the job row and read back by the admin UI,
	// and ImportMenus drives cache invalidation, so recording one the source
	// never read would misreport what the run was asked to do.
	opts := types.MaskUnsupportedImportOptions(ctx.Source, parseImportOptions(r))

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
	// progress is captured by the recover defer below, so it must be declared
	// before it. It is assigned once the accumulator exists.
	var progress *jobProgress

	defer func() {
		if rec := recover(); rec != nil {
			m.ctx.Logger.Error("import panicked", "source", run.SourceName, "job_id", run.ID, "panic", rec)
			m.flushPendingProgress(ctx, run.ID, progress, "panic")
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

	progress = newJobProgress()
	m.jobsMu.Lock()
	m.live[run.SourceName] = progress
	m.jobsMu.Unlock()

	stopFlush = m.startProgressFlush(ctx, run.ID, progress)

	result, err := run.Source.Import(ctx, m.ctx.DB, run.Cfg, run.Opts, m)
	stopFlush()
	if result == nil {
		// No result means finishJob keeps the counters already in the database,
		// and stopping the ticker does not persist what the accumulator holds.
		// A source that imported rows and then failed would report zero, or a
		// count up to a second stale, inviting a rerun that imports them twice.
		m.flushPendingProgress(ctx, run.ID, progress, "failure")
	}

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

// flushPendingProgress persists whatever the accumulator holds right now.
//
// finishJob keeps the counters already in the database when a run produces no
// result, and the flush ticker only fires once a second, so work done inside
// the first interval or since the last tick lives nowhere else. Both paths
// that finalize a run without a result — a panic and a plain failure — go
// through here so the recorded count matches the rows that were imported.
//
// The context is detached: the job's own context is often already canceled by
// the time this runs, and the point is to record work that survived it.
func (m *Module) flushPendingProgress(ctx context.Context, jobID int64, progress *jobProgress, reason string) {
	if progress == nil {
		return
	}
	snap := progress.forceSnapshot()
	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishJobTimeout)
	defer cancel()
	if err := m.flushJobProgress(flushCtx, jobID, snap); err != nil {
		m.ctx.Logger.Warn("failed to flush progress before finalizing",
			"job_id", jobID, "reason", reason, "error", err)
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
	stopped := make(chan struct{})
	var once sync.Once

	m.jobsWG.Add(1)
	go func() {
		defer m.jobsWG.Done()
		defer close(stopped)
		ticker := time.NewTicker(progressFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				snap, revision, changed := progress.pendingSnapshot()
				// Progress writes use a detached context so a cancelled import
				// can still record how far it got.
				flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishJobTimeout)
				var err error
				if changed {
					err = m.flushJobProgress(flushCtx, jobID, snap)
					if err == nil {
						progress.acknowledge(revision)
					}
				} else {
					// Still touch updated_at. A stage that publishes no
					// progress for two minutes — a long index rebuild, a slow
					// streaming query — otherwise looks orphaned to a second
					// process sharing the database, which would reap the row
					// and start a competing import for the same source while
					// this goroutine is still writing.
					err = m.touchJobHeartbeat(flushCtx, jobID)
				}
				if err != nil {
					m.ctx.Logger.Warn("failed to flush import progress", "job_id", jobID, "error", err)
				}
				cancel()
			}
		}
	}()

	return func() {
		once.Do(func() { close(done) })
		// Join this specific ticker rather than jobsWG: runImportJob itself is
		// in that WaitGroup, so waiting for the whole group here would deadlock.
		// The join prevents an older in-flight snapshot from overwriting panic
		// recovery's forced snapshot or racing terminal finalization.
		<-stopped
	}
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

	var finishErr error
	for attempt := 1; attempt <= finishJobAttempts; attempt++ {
		if finishErr = m.finishJob(finishCtx, run.ID, status, result, fatal); finishErr == nil {
			break
		}
		if attempt < finishJobAttempts {
			time.Sleep(finishJobRetryDelay * time.Duration(attempt))
		}
	}
	if finishErr != nil {
		m.ctx.Logger.Error("failed to record import job result", "job_id", run.ID, "error", finishErr)
		// runImportJob is about to drop this job from m.live, leaving a row that
		// still reads "running" and still carries this process's run ID. Nothing
		// here reaps a row it owns, so the source would refuse every later
		// import until a restart. A fresh context because finishCtx may be the
		// very thing that expired.
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), finishJobTimeout)
		if releaseErr := m.releaseJobOwnership(releaseCtx, run.ID); releaseErr != nil {
			m.ctx.Logger.Error("failed to release stuck import job",
				"job_id", run.ID, "source", run.SourceName, "error", releaseErr)
		} else {
			m.ctx.Logger.Warn("released stuck import job for stale reaping",
				"job_id", run.ID, "source", run.SourceName)
		}
		releaseCancel()
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
	if m.ctx == nil {
		return
	}
	if m.ctx.Cache != nil {
		// InvalidateContent covers both the page cache and the sitemap.
		m.ctx.Cache.InvalidateContent()
		if opts.ImportMenus {
			m.ctx.Cache.InvalidateMenus()
		}
	}
	if m.ctx.RedirectCacheInvalidator != nil {
		m.ctx.RedirectCacheInvalidator.InvalidateCache()
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

	counts, pendingMediaCleanup, countsErr := m.getImportedContentState(r.Context(), ctx.SourceName)
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
		pc, buildJobStatusView(ctx.SourceName, job, err), counts,
		pendingMediaCleanup, countsErr == nil))
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

	// Pending filesystem cleanup is independent of whether this request can
	// delete more database rows. Retry it before either job-state guard returns,
	// so a live import or a transient job lookup failure cannot strand durable
	// cleanup work until an otherwise-successful delete is attempted later.
	m.retryQueuedMediaCleanup(r.Context(), ctx.SourceName)

	// Refuse while an import is in flight. The status poll renders the delete
	// form as soon as the running job tracks its first item, so without this an
	// admin can delete rows the goroutine is still using: it keeps its in-memory
	// user, taxonomy and media IDs, so later stages hit foreign-key failures or
	// leave only the tail of the import tracked for a future delete.
	//
	// This check exists for the error message. The guarantee is in
	// deleteImportedItems, which re-checks inside the transaction that
	// serializes against startJob — a read here can always be overtaken by an
	// import starting a moment later.
	if job, err := m.latestJob(r.Context(), ctx.SourceName); err != nil {
		m.ctx.Logger.Error("failed to read import job before delete", "source", ctx.SourceName, "error", err)
		m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.error_delete"), "error")
		http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
		return
	} else if job != nil && job.Status == JobRunning && !job.IsStale(time.Now()) {
		m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.error_delete_while_running"), "error")
		http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
		return
	}

	m.ctx.Logger.Info("deleting imported content", "source", ctx.SourceName, "user", ctx.User.Email)

	// Delete all imported items for this source
	deleted, err := m.deleteImportedItemsAfterCleanupRetry(r.Context(), ctx.SourceName)
	var cleanupPending *mediaCleanupPendingError
	var rowsPending *importedRowsPendingError
	if err != nil {
		// An import that started between the check above and the transaction.
		if errors.Is(err, errImportRunning) {
			m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.error_delete_while_running"), "error")
			http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
			return
		}
		hasCleanupPending := errors.As(err, &cleanupPending)
		hasRowsPending := errors.As(err, &rowsPending)
		if hasCleanupPending || hasRowsPending {
			if hasRowsPending {
				m.ctx.Logger.Warn("some imported database rows remain tracked for retry",
					"source", ctx.SourceName, "count", rowsPending.count, "error", err)
			}
			if hasCleanupPending {
				m.ctx.Logger.Warn("imported rows deleted but media file cleanup remains queued",
					"source", ctx.SourceName, "error", err)
			}
		} else {
			m.ctx.Logger.Error("delete failed", "source", ctx.SourceName, "error", err)
			m.ctx.Render.SetFlash(r, i18n.T(ctx.Lang, "migrator.error_delete")+": "+err.Error(), "error")
			http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
			return
		}
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
	flashType := "success"
	if cleanupPending != nil {
		msg += " " + i18n.T(ctx.Lang, "migrator.warning_media_cleanup_pending")
		flashType = "warning"
	}
	if rowsPending != nil {
		msg += " " + i18n.T(ctx.Lang, "migrator.warning_delete_partial")
		flashType = "warning"
	}
	m.ctx.Render.SetFlash(r, msg, flashType)
	http.Redirect(w, r, "/admin/migrator/"+ctx.SourceName, http.StatusSeeOther)
}

type importedRowsPendingError struct {
	count int
}

func (e *importedRowsPendingError) Error() string {
	return fmt.Sprintf("%d imported database item(s) remain tracked for retry", e.count)
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
	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT INTO migrator_imported_items (source, entity_type, entity_id, created_at)
		VALUES (?, ?, ?, ?)
	`, source, entityType, entityID, time.Now())
	if err != nil {
		return err
	}

	m.jobsMu.Lock()
	progress := m.live[source]
	m.jobsMu.Unlock()
	if progress != nil {
		progress.addItem(types.EntityType(entityType))
	}
	return nil
}

// getImportedCounts returns counts of imported items by entity type for a source.
func (m *Module) getImportedCounts(ctx context.Context, source string) (map[string]int, error) {
	counts, _, err := m.getImportedContentState(ctx, source)
	return counts, err
}

// getImportedContentState returns tracked entity counts together with the
// durable media-cleanup backlog. Queue rows are included in the media count so
// deletion remains available, while the separate value lets the UI explain
// that those items are pending filesystem work rather than live media rows.
func (m *Module) getImportedContentState(ctx context.Context, source string) (map[string]int, int, error) {
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT entity_type, COUNT(*) as cnt
		FROM migrator_imported_items
		WHERE source = ?
		GROUP BY entity_type
	`, source)
	if err != nil {
		return nil, 0, err
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
			return nil, 0, err
		}
		counts[entityType] = count
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}

	var pendingMedia int
	if err := m.ctx.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM migrator_media_cleanup_queue WHERE source = ?
	`, source).Scan(&pendingMedia); err != nil {
		return nil, 0, err
	}
	if pendingMedia > 0 {
		counts[string(types.EntityMedia)] += pendingMedia
	}
	return counts, pendingMedia, nil
}

// getImportedItems returns all imported item IDs of a given type for a source.
//
// db is a store.DBTX so the delete path can read through its own transaction:
// reading on another connection would see the pre-transaction tracking rows and
// defeat the point of doing the delete atomically.
func (m *Module) getImportedItems(ctx context.Context, db store.DBTX, source, entityType string) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `
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
//
// beforeDelete, when set, runs immediately before del. It returns keep=true to
// leave the row in place, and may first adjust rows that the delete would
// otherwise take with it through a cascade.
//
// Both cases exist because a cascade out of an imported row can reach content
// the import does not own, which would break the module's promise that original
// content is untouched.
type entityDeleter struct {
	entityType   types.EntityType
	del          func(ctx context.Context, q *store.Queries, id int64) error
	cascadesFrom types.EntityType
	beforeDelete func(ctx context.Context, q *store.Queries, id int64) (keep bool, err error)
}

// keepIfReferenced turns a reference count into a beforeDelete hook.
func keepIfReferenced(count func(ctx context.Context, q *store.Queries, id int64) (int64, error)) func(
	ctx context.Context, q *store.Queries, id int64) (bool, error) {

	return func(ctx context.Context, q *store.Queries, id int64) (bool, error) {
		refs, err := count(ctx, q, id)
		return refs > 0, err
	}
}

// canonicalDetachedPageURL returns the same canonical public path used by the
// menu service for a page-backed item. Default-language pages are unprefixed;
// active, routable non-default languages own their prefix. A missing,
// inactive, or unsafe language fails closed so deletion never leaves behind a
// link that resolves in another language's namespace.
func canonicalDetachedPageURL(ctx context.Context, q *store.Queries, page store.Page) (string, bool) {
	if !util.IsValidSlug(page.Slug) {
		return "", false
	}
	defaultLanguage, err := q.GetDefaultLanguage(ctx)
	if err != nil {
		return "", false
	}
	language, err := q.GetLanguageByCode(ctx, page.LanguageCode)
	if err != nil || !language.IsActive {
		return "", false
	}
	if !util.IsValidLangCode(language.Code) || util.IsReservedLanguageCode(language.Code) {
		return "", false
	}
	if language.ID == defaultLanguage.ID && language.Code == defaultLanguage.Code {
		return "/" + page.Slug, true
	}
	return "/" + language.Code + "/" + page.Slug, true
}

// importedEntityRouteCandidates returns every concrete path that may have been
// persisted for an imported frontend entity: unprefixed while its language was
// the default, and language-prefixed while it was not. Comparing both exact
// paths preserves a failed navigation dependency even if the administrator
// changed the default language after the import. It also avoids guessing the
// current default and falsely retaining unrelated entities when defaults are
// temporarily ambiguous.
func importedEntityRouteCandidates(
	ctx context.Context,
	q *store.Queries,
	entityType types.EntityType,
	id int64,
) ([]string, error) {
	var slug, languageCode, routePrefix string
	switch entityType {
	case types.EntityPage, types.EntityPost:
		page, err := q.GetPageByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		slug, languageCode = page.Slug, page.LanguageCode
	case types.EntityTag:
		tag, err := q.GetTagByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		slug, languageCode, routePrefix = tag.Slug, tag.LanguageCode, "/tag"
	case types.EntityCategory:
		category, err := q.GetCategoryByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		slug, languageCode, routePrefix = category.Slug, category.LanguageCode, "/category"
	default:
		return nil, nil
	}
	if !util.IsValidSlug(slug) {
		return nil, nil
	}
	basePath := routePrefix + "/" + slug
	paths := []string{basePath}
	if util.IsValidLangCode(languageCode) && !util.IsReservedLanguageCode(languageCode) {
		paths = append(paths, "/"+languageCode+basePath)
	}
	return paths, nil
}

// deleters returns every trackable entity type in dependency-safe deletion
// order. The order is dictated by the schema's foreign keys:
//
//   - menu_items.menu_id cascades from menus, so items are removed first to
//     keep their counts accurate
//   - pages.author_id is ON DELETE RESTRICT, so pages and posts must be gone
//     before their authors
//   - aliases are deleted before pages so they remain independently undoable
//     even when a page is deliberately preserved or its deletion fails
//   - categories.parent_id is ON DELETE SET NULL, so category order is free
//
// Tags and categories additionally declare a reference count. They are the only
// entity types an *original* oCMS page can start using after the import: by the
// time their turn comes every imported page is already deleted, so any join row
// left behind belongs to a page this import does not own. Deleting the row then
// cascades that association away — page_tags.tag_id and page_categories.
// category_id are both ON DELETE CASCADE — which would break the module's
// documented promise that original content is untouched.
//
// TestDeleteImportedItemsCoversAllEntityTypes fails if a type in
// types.AllEntityTypes has neither a deleter nor a cascade source.
//
// collectMediaUUID receives the UUID of every media row about to be removed, so
// its files can be deleted after the transaction commits rather than during it.
// It may be nil.
func (m *Module) deleters(collectMediaUUID func(uuid string)) []entityDeleter {
	deletePage := func(ctx context.Context, q *store.Queries, id int64) error {
		if err := q.ClearPageTags(ctx, id); err != nil {
			return err
		}
		if err := q.ClearPageCategories(ctx, id); err != nil {
			return err
		}
		return q.DeletePage(ctx, id)
	}

	// The UUID is queued only after the row is gone. Queuing it up front meant a
	// failed DeleteMedia kept the media row — correctly, and retryably — while
	// its files were deleted anyway once the transaction committed, leaving a
	// row pointing at nothing. A lookup failure is propagated rather than
	// swallowed, because deleting a row whose UUID we never read would orphan
	// its files with nothing left to find them by.
	deleteMedia := func(ctx context.Context, q *store.Queries, id int64) error {
		media, err := q.GetMediaByID(ctx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Already gone; nothing to delete and no files to schedule.
				return nil
			}
			return fmt.Errorf("failed to read media %d before deleting it: %w", id, err)
		}
		if err := q.DeleteMediaVariants(ctx, id); err != nil {
			m.ctx.Logger.Warn("failed to delete media variants", "id", id, "error", err)
		}
		if err := q.DeleteMedia(ctx, id); err != nil {
			return err
		}
		if collectMediaUUID != nil {
			collectMediaUUID(media.Uuid)
		}
		return nil
	}

	// menu_items.page_id is ON DELETE SET NULL and a page-backed item stores no
	// fallback URL, so deleting an imported page left any administrator-created
	// item pointing at it present but with an empty destination. Tracked menu
	// items are already gone by the time pages are deleted, so everything still
	// pointing at the page belongs to the administrator: those items are given
	// the page's canonical language-aware URL. It will 404 after the page is
	// removed, but a visible, editable link beats one that silently goes
	// nowhere. If no safe canonical URL exists, both the page and the
	// administrator's page-backed item remain untouched and the page stays
	// tracked for a later retry after its language is remediated.
	detachMenuItems := func(ctx context.Context, q *store.Queries, id int64) (bool, error) {
		page, err := q.GetPageByID(ctx, id)
		if err != nil {
			return false, err
		}
		items, err := q.ListMenuItemIDsForPage(ctx, sql.NullInt64{Int64: id, Valid: true})
		if err != nil {
			return false, err
		}
		if len(items) == 0 {
			return false, nil
		}
		pageURL, ok := canonicalDetachedPageURL(ctx, q, page)
		if !ok {
			return false, fmt.Errorf("cannot detach menu items from page %d with unrouteable language %q",
				page.ID, page.LanguageCode)
		}
		for _, itemID := range items {
			if err := q.ConvertMenuItemToURL(ctx, store.ConvertMenuItemToURLParams{
				Url:       sql.NullString{String: pageURL, Valid: true},
				UpdatedAt: time.Now(),
				ID:        itemID,
			}); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	return []entityDeleter{
		{entityType: types.EntityMenuItem, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeleteMenuItem(ctx, id)
		}, beforeDelete: func(ctx context.Context, q *store.Queries, id int64) (bool, error) {
			// menu_items.parent_id is ON DELETE CASCADE, so removing an
			// imported item would take any child with it — including one an
			// administrator hung off it after the import.
			//
			// Children are lifted to the item's own parent rather than the
			// item being preserved: keeping it would leave an imported entry
			// in the site's navigation permanently, which is the content the
			// operator asked to remove. Tracked children are moved too and are
			// then deleted by their own tracking rows, so no ordering
			// assumption is needed.
			item, err := q.GetMenuItemByID(ctx, id)
			if err != nil {
				return false, err
			}
			children, err := q.ListChildMenuItemIDs(ctx, sql.NullInt64{Int64: id, Valid: true})
			if err != nil {
				return false, err
			}
			if len(children) == 0 {
				return false, nil
			}
			return false, q.ReparentMenuItemChildren(ctx, store.ReparentMenuItemChildrenParams{
				ParentID:   item.ParentID,
				UpdatedAt:  time.Now(),
				ParentID_2: sql.NullInt64{Int64: id, Valid: true},
			})
		}},
		{entityType: types.EntityMenu, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeleteMenu(ctx, id)
		}, beforeDelete: func(ctx context.Context, q *store.Queries, id int64) (bool, error) {
			// menu_items.menu_id is ON DELETE CASCADE, so deleting a menu the
			// import created takes every item in it — including ones an
			// administrator added afterwards. The parent_id reparenting hook
			// does not help here: it protects descendants of a deleted item,
			// not root items that merely belong to this menu.
			//
			// Unlike a menu item, an item cannot be moved out of the way —
			// menu_id is NOT NULL — so the menu is kept instead. By this point
			// every tracked item is already deleted, so anything left belongs
			// to the administrator.
			items, err := q.ListMenuItems(ctx, id)
			if err != nil {
				return false, err
			}
			return len(items) > 0, nil
		}},
		{entityType: types.EntityRedirect, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeleteRedirect(ctx, id)
		}},
		{entityType: types.EntityAlias, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeletePageAlias(ctx, id)
		}},
		{entityType: types.EntityPost, del: deletePage, beforeDelete: detachMenuItems},
		{entityType: types.EntityPage, del: deletePage, beforeDelete: detachMenuItems},
		{entityType: types.EntityTag, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeleteTag(ctx, id)
		}, beforeDelete: keepIfReferenced(func(ctx context.Context, q *store.Queries, id int64) (int64, error) {
			return q.CountPagesForTag(ctx, id)
		})},
		{entityType: types.EntityCategory, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeleteCategory(ctx, id)
		}, beforeDelete: func(ctx context.Context, q *store.Queries, id int64) (bool, error) {
			refs, err := q.CountPagesByCategory(ctx, id)
			if err != nil || refs > 0 {
				return refs > 0, err
			}
			children, err := q.ListChildCategories(ctx, sql.NullInt64{Int64: id, Valid: true})
			return len(children) > 0, err
		}},
		{entityType: types.EntityMedia, del: deleteMedia,
			beforeDelete: func(ctx context.Context, q *store.Queries, id int64) (bool, error) {
				// pages.featured_image_id and og_image_id are ON DELETE SET
				// NULL, so deleting imported media silently strips the image
				// from any page still using it. Body URLs have no foreign key,
				// so they must be checked explicitly too or a failed imported
				// page can survive with its embedded files removed.
				refs, err := q.CountPagesUsingMedia(ctx, store.CountPagesUsingMediaParams{
					FeaturedImageID: sql.NullInt64{Int64: id, Valid: true},
					OgImageID:       sql.NullInt64{Int64: id, Valid: true},
				})
				if err != nil || refs > 0 {
					return refs > 0, err
				}
				media, err := q.GetMediaByID(ctx, id)
				if errors.Is(err, sql.ErrNoRows) {
					return false, nil
				}
				if err != nil {
					return false, err
				}
				for _, storageDir := range model.MediaStorageDirs() {
					refs, err = q.CountPagesEmbeddingMediaUUID(ctx, store.CountPagesEmbeddingMediaUUIDParams{
						StorageDir: sql.NullString{String: storageDir, Valid: true},
						MediaUuid:  sql.NullString{String: media.Uuid, Valid: true},
					})
					if err != nil || refs > 0 {
						return refs > 0, err
					}
				}
				return false, nil
			}},
		{entityType: types.EntityUser, del: func(ctx context.Context, q *store.Queries, id int64) error {
			return q.DeleteUser(ctx, id)
		}, beforeDelete: keepIfReferenced(func(ctx context.Context, q *store.Queries, id int64) (int64, error) {
			// pages.author_id and page_versions.changed_by are ON DELETE
			// RESTRICT, and media.uploaded_by carries no action clause, which
			// enforces the same way — so a user who still owns any of them
			// cannot be deleted at all. Two imports sharing an email is the
			// ordinary way to reach this: the second reuses the account the
			// first created, and deleting the first import then hit a foreign
			// key error that was logged and otherwise ignored.
			return q.CountUserOwnedContent(ctx, store.CountUserOwnedContentParams{
				AuthorID:    id,
				ChangedBy:   id,
				UploadedBy:  id,
				CreatedBy:   id,
				CreatedBy_2: id,
			})
		})},
	}
}

// trackedItem identifies one row in migrator_imported_items.
type trackedItem struct {
	entityType string
	id         int64
}

type failedEntityIDs map[types.EntityType]map[int64]struct{}

func (failed failedEntityIDs) add(entityType types.EntityType, id int64) {
	if failed[entityType] == nil {
		failed[entityType] = make(map[int64]struct{})
	}
	failed[entityType][id] = struct{}{}
}

func failedPageIDs(failed failedEntityIDs) []int64 {
	ids := make([]int64, 0, len(failed[types.EntityPost])+len(failed[types.EntityPage]))
	for id := range failed[types.EntityPost] {
		ids = append(ids, id)
	}
	for id := range failed[types.EntityPage] {
		if _, duplicate := failed[types.EntityPost][id]; !duplicate {
			ids = append(ids, id)
		}
	}
	return ids
}

// keptEntityDependsOnFailedItems reports whether the concrete reference that
// made an imported dependency stay belongs to an earlier tracked row whose
// deletion failed. Type-wide propagation is unsafe: one unrelated page failure
// must not keep a taxonomy/media/user row that is shared only by administrator
// content falsely tracked forever.
func keptEntityDependsOnFailedItems(
	ctx context.Context,
	q *store.Queries,
	tx *sql.Tx,
	entityType types.EntityType,
	id int64,
	failed failedEntityIDs,
) (bool, error) {
	exists := func(query string, args ...any) (bool, error) {
		var value int
		if err := tx.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
			return false, err
		}
		return value != 0, nil
	}
	forFailedPage := func(query string) (bool, error) {
		for _, pageID := range failedPageIDs(failed) {
			matched, err := exists(query, pageID, id)
			if err != nil || matched {
				return matched, err
			}
		}
		return false, nil
	}
	forFailedNavigationTarget := func(includePageID bool) (bool, error) {
		if len(failed[types.EntityMenuItem]) == 0 && len(failed[types.EntityRedirect]) == 0 {
			return false, nil
		}
		if includePageID {
			for itemID := range failed[types.EntityMenuItem] {
				matched, err := exists(`SELECT EXISTS(
					SELECT 1 FROM menu_items WHERE id = ? AND page_id = ?
				)`, itemID, id)
				if err != nil || matched {
					return matched, err
				}
			}
		}
		paths, err := importedEntityRouteCandidates(ctx, q, entityType, id)
		if err != nil {
			return false, err
		}
		for itemID := range failed[types.EntityMenuItem] {
			for _, path := range paths {
				matched, err := exists(`SELECT EXISTS(
					SELECT 1 FROM menu_items WHERE id = ? AND url = ?
				)`, itemID, path)
				if err != nil || matched {
					return matched, err
				}
			}
		}
		for redirectID := range failed[types.EntityRedirect] {
			for _, path := range paths {
				matched, err := exists(`SELECT EXISTS(
					SELECT 1 FROM redirects WHERE id = ? AND target_url = ?
				)`, redirectID, path)
				if err != nil || matched {
					return matched, err
				}
			}
		}
		return false, nil
	}

	switch entityType {
	case types.EntityMenu:
		for itemID := range failed[types.EntityMenuItem] {
			matched, err := exists(`SELECT EXISTS(
				SELECT 1 FROM menu_items WHERE id = ? AND menu_id = ?
			)`, itemID, id)
			if err != nil || matched {
				return matched, err
			}
		}
	case types.EntityTag:
		matched, err := forFailedPage(`SELECT EXISTS(
			SELECT 1 FROM page_tags WHERE page_id = ? AND tag_id = ?
		)`)
		if err != nil || matched {
			return matched, err
		}
		return forFailedNavigationTarget(false)
	case types.EntityCategory:
		matched, err := forFailedPage(`SELECT EXISTS(
			SELECT 1 FROM page_categories WHERE page_id = ? AND category_id = ?
		)`)
		if err != nil || matched {
			return matched, err
		}
		for childID := range failed[types.EntityCategory] {
			matched, err := exists(`SELECT EXISTS(
				SELECT 1 FROM categories WHERE id = ? AND parent_id = ?
			)`, childID, id)
			if err != nil || matched {
				return matched, err
			}
		}
		return forFailedNavigationTarget(false)
	case types.EntityMedia:
		for _, pageID := range failedPageIDs(failed) {
			matched, err := exists(`SELECT EXISTS(
				SELECT 1 FROM pages
				WHERE id = ? AND (featured_image_id = ? OR og_image_id = ?)
			)`, pageID, id, id)
			if err != nil || matched {
				return matched, err
			}
		}
		media, err := q.GetMediaByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		for _, pageID := range failedPageIDs(failed) {
			for _, storageDir := range model.MediaStorageDirs() {
				mediaPath := model.MediaURL(storageDir, media.Uuid, "")
				matched, err := exists(`SELECT EXISTS(
					SELECT 1 FROM pages WHERE id = ? AND instr(body, ?) > 0
				)`, pageID, mediaPath)
				if err != nil || matched {
					return matched, err
				}
			}
		}
	case types.EntityUser:
		for _, pageID := range failedPageIDs(failed) {
			matched, err := exists(`SELECT EXISTS(
				SELECT 1 FROM pages WHERE id = ? AND author_id = ?
			)`, pageID, id)
			if err != nil || matched {
				return matched, err
			}
			matched, err = exists(`SELECT EXISTS(
				SELECT 1 FROM page_versions WHERE page_id = ? AND changed_by = ?
			)`, pageID, id)
			if err != nil || matched {
				return matched, err
			}
		}
		for mediaID := range failed[types.EntityMedia] {
			matched, err := exists(`SELECT EXISTS(
				SELECT 1 FROM media WHERE id = ? AND uploaded_by = ?
			)`, mediaID, id)
			if err != nil || matched {
				return matched, err
			}
		}
	case types.EntityPage, types.EntityPost:
		return forFailedNavigationTarget(true)
	}
	return false, nil
}

// orderCategoryIDsChildFirst ensures tracked descendants are deleted before
// their tracked ancestors. A parent is preserved when any child remains after
// that pass, which protects administrator-created descendants without leaking
// an imported parent merely because its own tracked child had not run yet.
func orderCategoryIDsChildFirst(ctx context.Context, queries *store.Queries, ids []int64) ([]int64, error) {
	parents := make(map[int64]int64, len(ids))
	for _, id := range ids {
		category, err := queries.GetCategoryByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if category.ParentID.Valid {
			parents[id] = category.ParentID.Int64
		}
	}
	depths := make(map[int64]int, len(ids))
	var depth func(int64, map[int64]bool) int
	depth = func(id int64, visiting map[int64]bool) int {
		if known, ok := depths[id]; ok {
			return known
		}
		if visiting[id] {
			// A malformed cycle must not recurse forever. Equal depths preserve
			// the stable tracking order; the database delete will then fail
			// safely if its constraints reject the cycle.
			return 0
		}
		visiting[id] = true
		value := 0
		if parentID, trackedParent := parents[id]; trackedParent {
			if _, parentIsTracked := parents[parentID]; parentIsTracked {
				value = depth(parentID, visiting) + 1
			} else {
				for _, candidate := range ids {
					if candidate == parentID {
						value = depth(parentID, visiting) + 1
						break
					}
				}
			}
		}
		delete(visiting, id)
		depths[id] = value
		return value
	}

	ordered := append([]int64(nil), ids...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return depth(ordered[i], map[int64]bool{}) > depth(ordered[j], map[int64]bool{})
	})
	return ordered, nil
}

const entityDeleteSavepoint = "migrator_entity_delete"

func beginEntityDeleteSavepoint(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "SAVEPOINT "+entityDeleteSavepoint)
	return err
}

func releaseEntityDeleteSavepoint(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+entityDeleteSavepoint)
	return err
}

// rollbackEntityDeleteSavepoint reverts both the beforeDelete hook and the
// delete statement. The hook may already have reparented or detached
// administrator-owned rows, and those changes must not survive a failed
// deletion of the imported owner.
func rollbackEntityDeleteSavepoint(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+entityDeleteSavepoint); err != nil {
		return err
	}
	return releaseEntityDeleteSavepoint(ctx, tx)
}

// retainTrackingRows re-inserts tracking rows for items whose delete failed.
//
// The delete clears the source's tracking rows wholesale, which is right for
// everything it removed or deliberately preserved. It was wrong for a failure:
// the row stayed in the database while its only record of being imported was
// discarded, so nothing could find it again — not the Imported Content panel,
// not a retry. Every current blocking foreign key is covered by a reference
// check, so this is the net for the ones that are not: a transient database
// error, or a constraint added later.
func retainTrackingRows(ctx context.Context, tx *sql.Tx, source string, failed []trackedItem) error {
	now := time.Now()
	for _, item := range failed {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO migrator_imported_items (source, entity_type, entity_id, created_at)
			VALUES (?, ?, ?, ?)`, source, item.entityType, item.id, now); err != nil {
			return fmt.Errorf("failed to retain tracking row for %s %d: %w",
				item.entityType, item.id, err)
		}
	}
	return nil
}

// deleteImportedItems deletes all content imported from a source.
//
// The returned counts are "tracked items of this type that were removed". A
// cascade can delete a child before its own turn comes — a nested menu item, for
// instance — and that still counts, which is what an admin wants to read.
//
// The whole delete runs in one transaction, and that is what serializes it
// against a starting import. oCMS opens SQLite with _txlock=immediate, so the
// write lock is taken at BEGIN: this transaction and startJob's cannot
// interleave, which is the only thing that makes the running-job check below
// mean anything. handleDeleteImported's own check is a nicety for the error
// message — on its own it is a read that startJob can slip past, after which
// the delete would strip rows the importer was still using and then clear the
// tracking rows of the job that had just started, leaving its content with no
// undo path.
//
// Holding the write lock for the duration is acceptable here in a way it is not
// for an import: a delete is bounded by what one import created and is a rare,
// deliberate admin action, whereas an import can run for hours.
func (m *Module) deleteImportedItems(ctx context.Context, source string) (map[string]int, error) {
	m.retryQueuedMediaCleanup(ctx, source)
	return m.deleteImportedItemsAfterCleanupRetry(ctx, source)
}

// retryQueuedMediaCleanup retries durable filesystem work independently of a
// new database deletion. It intentionally logs and continues on failure:
// drainMediaCleanup keeps failed rows queued for the next bounded attempt.
func (m *Module) retryQueuedMediaCleanup(ctx context.Context, source string) {
	// A prior delete or import-time compensation may have committed cleanup work
	// that is still waiting on the filesystem. Retry it on every later delete
	// attempt, even when the new database deletion aborts before commit. The retry
	// has its own bounded context because it is independent of both this
	// transaction and a request cancellation; failed work remains durable and the
	// post-commit drain retries it together with any newly queued UUIDs.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupDrainTimeout)
	defer cleanupCancel()
	if err := m.drainMediaCleanup(cleanupCtx, source); err != nil {
		m.ctx.Logger.Warn("media cleanup remains queued after pre-delete retry",
			"source", source, "error", err)
	}
}

// deleteImportedItemsAfterCleanupRetry performs the transactional delete after
// its caller has retried already-queued filesystem work.
func (m *Module) deleteImportedItemsAfterCleanupRetry(ctx context.Context, source string) (map[string]int, error) {
	tx, err := m.ctx.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin delete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// A stale row is not a live import — the same rule ImportJob.IsStale
	// applies — so an abandoned job must not block deletion forever.
	var running int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM migrator_import_jobs
		WHERE source = ? AND status = ?
		  AND (owner_run_id = ? OR updated_at >= ?)`,
		source, string(JobRunning), m.runID, time.Now().Add(-staleJobThreshold)).Scan(&running); err != nil {
		return nil, fmt.Errorf("failed to check for a running import: %w", err)
	}
	if running > 0 {
		return nil, errImportRunning
	}

	// Files are removed after the commit: filesystem deletion cannot be rolled
	// back. UUIDs are first persisted to the durable queue in this transaction,
	// so a crash or removal failure cannot make the work undiscoverable.
	var mediaUUIDs []string
	// Items whose delete failed. Their tracking rows survive so the operator can
	// retry, and so the Imported Content panel keeps showing what is left.
	var failed []trackedItem
	failedEntities := make(failedEntityIDs)
	recordFailure := func(entityType types.EntityType, id int64) {
		failed = append(failed, trackedItem{entityType: string(entityType), id: id})
		failedEntities.add(entityType, id)
	}
	queries := store.New(m.ctx.DB).WithTx(tx)
	deleted := make(map[string]int)

	for _, d := range m.deleters(func(uuid string) { mediaUUIDs = append(mediaUUIDs, uuid) }) {
		ids, err := m.getImportedItems(ctx, tx, source, string(d.entityType))
		if err != nil {
			return nil, err
		}
		if d.del == nil {
			// Removed by a database cascade from its parent entity.
			deleted[string(d.entityType)] = len(ids)
			continue
		}
		if d.entityType == types.EntityCategory {
			ids, err = orderCategoryIDsChildFirst(ctx, queries, ids)
			if err != nil {
				return nil, fmt.Errorf("failed to order imported categories for deletion: %w", err)
			}
		}
		for _, id := range ids {
			if err := beginEntityDeleteSavepoint(ctx, tx); err != nil {
				return nil, fmt.Errorf("failed to begin savepoint for %s %d: %w",
					string(d.entityType), id, err)
			}
			retryable, dependencyErr := keptEntityDependsOnFailedItems(
				ctx, queries, tx, d.entityType, id, failedEntities,
			)
			if dependencyErr != nil {
				if err := rollbackEntityDeleteSavepoint(ctx, tx); err != nil {
					return nil, fmt.Errorf("failed to roll back dependency check for %s %d: %w",
						string(d.entityType), id, errors.Join(dependencyErr, err))
				}
				m.ctx.Logger.Warn("could not identify the failed imported dependency; keeping item tracked",
					"type", string(d.entityType), "id", id, "error", dependencyErr)
				recordFailure(d.entityType, id)
				continue
			}
			if retryable {
				if err := releaseEntityDeleteSavepoint(ctx, tx); err != nil {
					return nil, fmt.Errorf("failed to release savepoint for retryable %s %d: %w",
						string(d.entityType), id, err)
				}
				recordFailure(d.entityType, id)
				continue
			}

			keep, beforeErr := m.preserveShared(ctx, queries, d, id)
			if beforeErr != nil {
				if errors.Is(beforeErr, sql.ErrNoRows) {
					// A cascade or an earlier manual cleanup already removed the
					// tracked row. There is nothing to retry.
					if err := releaseEntityDeleteSavepoint(ctx, tx); err != nil {
						return nil, fmt.Errorf("failed to release savepoint for missing %s %d: %w",
							string(d.entityType), id, err)
					}
					deleted[string(d.entityType)]++
					continue
				}
				if err := rollbackEntityDeleteSavepoint(ctx, tx); err != nil {
					return nil, fmt.Errorf("failed to roll back before-delete work for %s %d: %w",
						string(d.entityType), id, err)
				}
				recordFailure(d.entityType, id)
				continue
			}
			if keep {
				// Deliberately kept, permanently: retrying would keep it too,
				// so its tracking row goes.
				if err := releaseEntityDeleteSavepoint(ctx, tx); err != nil {
					return nil, fmt.Errorf("failed to release savepoint for preserved %s %d: %w",
						string(d.entityType), id, err)
				}
				continue
			}
			if err := d.del(ctx, queries, id); err != nil {
				if rollbackErr := rollbackEntityDeleteSavepoint(ctx, tx); rollbackErr != nil {
					return nil, fmt.Errorf("delete and savepoint rollback failed for %s %d: %w",
						string(d.entityType), id, errors.Join(err, rollbackErr))
				}
				// Keep the tracking row. Clearing it was the more damaging half
				// of this: the row stayed in the database, untracked, while the
				// UI reported a clean delete — so nothing could ever find it
				// again, not even a retry.
				m.ctx.Logger.Warn("failed to delete imported item; keeping it tracked so a retry can find it",
					"type", string(d.entityType), "id", id, "error", err)
				recordFailure(d.entityType, id)
				continue
			}
			if err := releaseEntityDeleteSavepoint(ctx, tx); err != nil {
				return nil, fmt.Errorf("failed to release savepoint for deleted %s %d: %w",
					string(d.entityType), id, err)
			}
			deleted[string(d.entityType)]++
		}
	}

	// Clear tracking for this source, except the items that could not be
	// deleted — those stay tracked so a retry can still find them.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM migrator_imported_items WHERE source = ?`, source); err != nil {
		return nil, fmt.Errorf("failed to clear import tracking rows: %w", err)
	}
	if err := retainTrackingRows(ctx, tx, source, failed); err != nil {
		return nil, err
	}
	if len(failed) > 0 {
		m.ctx.Logger.Warn("some imported items could not be deleted and remain tracked",
			"source", source, "count", len(failed))
	}

	if len(mediaUUIDs) > 0 {
		uploadRoot, err := m.configuredUploadRoot()
		if err != nil {
			return nil, fmt.Errorf("resolve uploads root before delete: %w", err)
		}
		for _, mediaUUID := range mediaUUIDs {
			if err := enqueueMediaCleanup(ctx, tx, mediaCleanupWork{
				source: source, uploadRoot: uploadRoot, mediaUUID: mediaUUID,
			}); err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit delete: %w", err)
	}

	cleanupErr := m.drainMediaCleanup(ctx, source)
	var pendingErr error
	if len(failed) > 0 {
		pendingErr = &importedRowsPendingError{count: len(failed)}
	}
	return deleted, errors.Join(pendingErr, cleanupErr)
}

// preserveShared reports whether a row must be kept because content this import
// does not own still points at it.
//
// A check failure preserves the row and its tracking entry for retry. The two
// outcomes are not symmetric: keeping an imported tag costs the operator one
// row they can delete by hand, while removing a shared one silently strips the
// association from an original page and cannot be undone.
func (m *Module) preserveShared(ctx context.Context, queries *store.Queries, d entityDeleter, id int64) (bool, error) {
	if d.beforeDelete == nil {
		return false, nil
	}
	keep, err := d.beforeDelete(ctx, queries, id)
	if err != nil {
		m.ctx.Logger.Warn("could not prepare an imported item for deletion; keeping it tracked for retry",
			"type", string(d.entityType), "id", id, "error", err)
		return true, err
	}
	if !keep {
		return false, nil
	}
	m.ctx.Logger.Info("keeping imported item still used by content this import did not create",
		"type", string(d.entityType), "id", id)
	return true, nil
}
