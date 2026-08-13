// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// --- Job lifecycle ---

func TestStartJobRejectsSecondConcurrentImport(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	first, err := m.startJob(ctx, "mock", "admin@example.com", 1, ImportOptions{})
	if err != nil {
		t.Fatalf("startJob() error = %v", err)
	}

	_, err = m.startJob(ctx, "mock", "admin@example.com", 1, ImportOptions{})
	if !errors.Is(err, errImportRunning) {
		t.Fatalf("second startJob() error = %v, want errImportRunning", err)
	}

	var running int
	if err := m.ctx.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM migrator_import_jobs WHERE source = ? AND status = ?`,
		"mock", string(JobRunning)).Scan(&running); err != nil {
		t.Fatalf("failed to count running jobs: %v", err)
	}
	if running != 1 {
		t.Errorf("running jobs = %d, want 1; the rejected start must not have inserted a row", running)
	}

	if err := m.finishJob(ctx, first, JobCompleted, &ImportResult{}, nil); err != nil {
		t.Fatalf("finishJob() error = %v", err)
	}
	if _, err := m.startJob(ctx, "mock", "admin@example.com", 1, ImportOptions{}); err != nil {
		t.Errorf("startJob() after completion error = %v; the unique index must be partial", err)
	}
}

func TestStartJobAllowsDifferentSourcesConcurrently(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	if _, err := m.startJob(ctx, "elefant", "a@example.com", 1, ImportOptions{}); err != nil {
		t.Fatalf("startJob(elefant) error = %v", err)
	}
	if _, err := m.startJob(ctx, "drupal", "a@example.com", 1, ImportOptions{}); err != nil {
		t.Errorf("startJob(drupal) error = %v; sources must not block each other", err)
	}
}

func TestFinishJobPersistsCountersAndErrors(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	jobID, err := m.startJob(ctx, "drupal", "admin@example.com", 1, ImportOptions{ImportPages: true})
	if err != nil {
		t.Fatalf("startJob() error = %v", err)
	}

	result := &ImportResult{PagesImported: 4, MenusImported: 1, UsersSkipped: 2}
	result.AddError("something went wrong with %s", "node 7")

	if err := m.finishJob(ctx, jobID, JobCompleted, result, nil); err != nil {
		t.Fatalf("finishJob() error = %v", err)
	}

	job, err := m.latestJob(ctx, "drupal")
	if err != nil {
		t.Fatalf("latestJob() error = %v", err)
	}
	if job == nil {
		t.Fatal("latestJob() returned nil")
	}
	if job.Status != JobCompleted {
		t.Errorf("Status = %q, want %q", job.Status, JobCompleted)
	}
	if got := job.Count(types.EntityPage); got != 4 {
		t.Errorf("page count = %d, want 4", got)
	}
	if got := job.Count(types.EntityMenu); got != 1 {
		t.Errorf("menu count = %d, want 1", got)
	}
	if len(job.Errors) != 1 || !strings.Contains(job.Errors[0], "node 7") {
		t.Errorf("Errors = %v, want the recorded per-item error", job.Errors)
	}
	if !job.IsTerminal() {
		t.Error("a completed job should be terminal")
	}
	if !job.FinishedAt.Valid {
		t.Error("a completed job should have FinishedAt set")
	}
}

func TestFinishJobRecordsFatalError(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	jobID, err := m.startJob(ctx, "drupal", "admin@example.com", 1, ImportOptions{})
	if err != nil {
		t.Fatalf("startJob() error = %v", err)
	}
	if err := m.finishJob(ctx, jobID, JobFailed, nil, errors.New("connection refused")); err != nil {
		t.Fatalf("finishJob() error = %v", err)
	}

	job, err := m.latestJob(ctx, "drupal")
	if err != nil {
		t.Fatalf("latestJob() error = %v", err)
	}
	if job.Status != JobFailed {
		t.Errorf("Status = %q, want %q", job.Status, JobFailed)
	}
	if !strings.Contains(job.FatalError, "connection refused") {
		t.Errorf("FatalError = %q, want it to carry the cause", job.FatalError)
	}
}

func TestLatestJobReturnsNilWhenNoneExists(t *testing.T) {
	m := testModule(t)
	job, err := m.latestJob(context.Background(), "never-run")
	if err != nil {
		t.Fatalf("latestJob() error = %v", err)
	}
	if job != nil {
		t.Errorf("latestJob() = %v, want nil", job)
	}
}

// TestReapStaleJobsLeavesOwnLiveJobs is the guard against a second process — or
// a restart racing its own goroutine — killing an import that is still running.
func TestReapStaleJobsLeavesOwnLiveJobs(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	jobID, err := m.startJob(ctx, "drupal", "admin@example.com", 1, ImportOptions{})
	if err != nil {
		t.Fatalf("startJob() error = %v", err)
	}
	// Backdate the heartbeat far enough to look stale.
	if _, err := m.ctx.DB.ExecContext(ctx,
		`UPDATE migrator_import_jobs SET updated_at = ? WHERE id = ?`,
		time.Now().Add(-time.Hour), jobID); err != nil {
		t.Fatalf("failed to backdate job: %v", err)
	}

	reaped, err := m.reapStaleJobs(ctx)
	if err != nil {
		t.Fatalf("reapStaleJobs() error = %v", err)
	}
	if reaped != 0 {
		t.Errorf("reaped %d jobs owned by this run; a live sibling import would have been killed", reaped)
	}

	job, _ := m.latestJob(ctx, "drupal")
	if job.Status != JobRunning {
		t.Errorf("Status = %q, want it left %q", job.Status, JobRunning)
	}
}

func TestReapStaleJobsMarksForeignRunningJobsInterrupted(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	// Simulate a row left behind by a previous process.
	if _, err := m.ctx.DB.ExecContext(ctx, `
		INSERT INTO migrator_import_jobs (source, status, owner_run_id, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		"drupal", string(JobRunning), "some-older-process",
		time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatalf("failed to insert orphaned job: %v", err)
	}

	reaped, err := m.reapStaleJobs(ctx)
	if err != nil {
		t.Fatalf("reapStaleJobs() error = %v", err)
	}
	if reaped != 1 {
		t.Errorf("reaped %d jobs, want 1", reaped)
	}

	job, _ := m.latestJob(ctx, "drupal")
	if job.Status != JobInterrupted {
		t.Errorf("Status = %q, want %q", job.Status, JobInterrupted)
	}
}

func TestJobIsStale(t *testing.T) {
	now := time.Now()
	fresh := &ImportJob{Status: JobRunning, UpdatedAt: now.Add(-time.Second)}
	if fresh.IsStale(now) {
		t.Error("a job with a recent heartbeat is not stale")
	}
	stale := &ImportJob{Status: JobRunning, UpdatedAt: now.Add(-staleJobThreshold - time.Second)}
	if !stale.IsStale(now) {
		t.Error("a job past the heartbeat threshold should be stale")
	}
	done := &ImportJob{Status: JobCompleted, UpdatedAt: now.Add(-time.Hour)}
	if done.IsStale(now) {
		t.Error("a terminal job is never stale")
	}
}

func TestTrimJobHistoryBoundsTheTable(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	for i := 0; i < jobHistoryLimit+5; i++ {
		jobID, err := m.startJob(ctx, "drupal", "admin@example.com", 1, ImportOptions{})
		if err != nil {
			t.Fatalf("startJob() error = %v", err)
		}
		if err := m.finishJob(ctx, jobID, JobCompleted, &ImportResult{}, nil); err != nil {
			t.Fatalf("finishJob() error = %v", err)
		}
	}
	// One more start triggers the trim.
	if _, err := m.startJob(ctx, "drupal", "admin@example.com", 1, ImportOptions{}); err != nil {
		t.Fatalf("startJob() error = %v", err)
	}

	var total int
	if err := m.ctx.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM migrator_import_jobs WHERE source = ?`, "drupal").Scan(&total); err != nil {
		t.Fatalf("failed to count jobs: %v", err)
	}
	if total > jobHistoryLimit+1 {
		t.Errorf("job history holds %d rows, want at most %d", total, jobHistoryLimit+1)
	}
}

// --- Progress ---

func TestProgressSnapshotClearsDirtyFlag(t *testing.T) {
	p := newJobProgress()

	if _, changed := p.snapshot(); changed {
		t.Error("a fresh accumulator should report no change")
	}

	p.addItem(types.EntityPage)
	p.report(types.Progress{Phase: types.EntityPage, Processed: 1, Total: 10})

	snap, changed := p.snapshot()
	if !changed {
		t.Fatal("snapshot() should report a change after activity")
	}
	if snap.Counters[types.EntityPage] != 1 {
		t.Errorf("page counter = %d, want 1", snap.Counters[types.EntityPage])
	}
	if snap.Phase != types.EntityPage || snap.Processed != 1 || snap.Total != 10 {
		t.Errorf("snapshot = %+v, want the reported sample", snap)
	}

	// The ticker relies on this to avoid writing once per second while idle.
	if _, changed := p.snapshot(); changed {
		t.Error("snapshot() should report no change when nothing happened since the last call")
	}
}

func TestTrackImportedItemAdvancesLiveProgress(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	progress := newJobProgress()
	m.jobsMu.Lock()
	m.live["drupal"] = progress
	m.jobsMu.Unlock()

	if err := m.TrackImportedItem(ctx, "drupal", string(types.EntityPage), 1); err != nil {
		t.Fatalf("TrackImportedItem() error = %v", err)
	}
	if err := m.TrackImportedItem(ctx, "drupal", string(types.EntityPage), 2); err != nil {
		t.Fatalf("TrackImportedItem() error = %v", err)
	}

	snap, changed := progress.snapshot()
	if !changed || snap.Counters[types.EntityPage] != 2 {
		t.Errorf("live page count = %d, want 2; tracking is the progress spine "+
			"that gives every source a live display for free", snap.Counters[types.EntityPage])
	}

	counts, err := m.getImportedCounts(ctx, "drupal")
	if err != nil {
		t.Fatalf("getImportedCounts() error = %v", err)
	}
	if counts[string(types.EntityPage)] != 2 {
		t.Errorf("persisted page count = %d, want 2", counts[string(types.EntityPage)])
	}
}

func TestTrackImportedItemWithoutLiveJobStillPersists(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	if err := m.TrackImportedItem(ctx, "drupal", string(types.EntityTag), 9); err != nil {
		t.Fatalf("TrackImportedItem() with no live job error = %v", err)
	}
	counts, err := m.getImportedCounts(ctx, "drupal")
	if err != nil {
		t.Fatalf("getImportedCounts() error = %v", err)
	}
	if counts[string(types.EntityTag)] != 1 {
		t.Errorf("tag count = %d, want 1", counts[string(types.EntityTag)])
	}
}

func TestReportProgressWithoutLiveJobIsNoOp(t *testing.T) {
	m := testModule(t)
	// Must not panic when no import is running for the named source.
	m.ReportProgress(context.Background(), Progress{Source: "nobody", Phase: types.EntityPage})
}

// --- Delete path ---

// TestDeleteImportedItemsRespectsForeignKeys runs the delete against a real
// migrated database with foreign keys enabled.
//
// Bug state: move "user" ahead of "page" in deleters() and DeleteUser trips the
// ON DELETE RESTRICT on pages.author_id, so the user is never removed.
func TestDeleteImportedItemsRespectsForeignKeys(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	queries := store.New(m.ctx.DB)
	now := time.Now()

	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "imported@example.com", PasswordHash: "x", Role: "public",
		Name: "Imported", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	page, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Imported", Slug: "imported", Status: "published",
		AuthorID: user.ID, LanguageCode: lang.Code, PageType: "page",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create page: %v", err)
	}
	if _, err := queries.CreatePageAlias(ctx, store.CreatePageAliasParams{
		PageID: page.ID, Alias: "old/url", CreatedAt: now,
	}); err != nil {
		t.Fatalf("failed to create alias: %v", err)
	}

	tag, err := queries.CreateTag(ctx, store.CreateTagParams{
		Name: "Go", Slug: "go", LanguageCode: lang.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}
	if err := queries.AddTagToPage(ctx, store.AddTagToPageParams{PageID: page.ID, TagID: tag.ID}); err != nil {
		t.Fatalf("failed to link tag: %v", err)
	}

	category, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name: "News", Slug: "news", LanguageCode: lang.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create category: %v", err)
	}

	menu, err := queries.CreateMenu(ctx, store.CreateMenuParams{
		Name: "Main", Slug: "main", LanguageCode: lang.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create menu: %v", err)
	}
	item, err := queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
		MenuID: menu.ID, Title: "Home", PageID: sql.NullInt64{Int64: page.ID, Valid: true},
		IsActive: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create menu item: %v", err)
	}

	for _, tracked := range []struct {
		entityType types.EntityType
		id         int64
	}{
		{types.EntityMenuItem, item.ID},
		{types.EntityMenu, menu.ID},
		{types.EntityAlias, page.ID},
		{types.EntityPage, page.ID},
		{types.EntityTag, tag.ID},
		{types.EntityCategory, category.ID},
		{types.EntityUser, user.ID},
	} {
		if err := m.TrackImportedItem(ctx, "drupal", string(tracked.entityType), tracked.id); err != nil {
			t.Fatalf("failed to track %s: %v", tracked.entityType, err)
		}
	}

	deleted, err := m.deleteImportedItems(ctx, "drupal")
	if err != nil {
		t.Fatalf("deleteImportedItems() error = %v", err)
	}

	for _, entityType := range []types.EntityType{
		types.EntityMenuItem, types.EntityMenu, types.EntityPage,
		types.EntityTag, types.EntityCategory, types.EntityUser,
	} {
		if deleted[string(entityType)] != 1 {
			t.Errorf("deleted[%s] = %d, want 1", entityType, deleted[string(entityType)])
		}
	}

	if _, err := queries.GetUserByEmail(ctx, "imported@example.com"); err == nil {
		t.Error("the imported user should be gone; a delete ordered before its pages would have been blocked by the RESTRICT foreign key")
	}
	if _, err := queries.GetPageByID(ctx, page.ID); err == nil {
		t.Error("the imported page should be gone")
	}
	if _, err := queries.GetMenuBySlug(ctx, "main"); err == nil {
		t.Error("the imported menu should be gone")
	}
	if _, err := queries.GetCategoryBySlug(ctx, "news"); err == nil {
		t.Error("the imported category should be gone")
	}

	// Aliases have no deleter: they ride the ON DELETE CASCADE from pages.
	aliases, err := queries.GetAliasesForPage(ctx, page.ID)
	if err != nil {
		t.Fatalf("failed to read aliases: %v", err)
	}
	if len(aliases) != 0 {
		t.Errorf("aliases = %v, want none after the page cascade", aliases)
	}

	counts, err := m.getImportedCounts(ctx, "drupal")
	if err != nil {
		t.Fatalf("getImportedCounts() error = %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("tracking table still holds %v after delete", counts)
	}
}

// --- Handlers ---

func TestParseImportOptionsAllUnchecked(t *testing.T) {
	req := httptest.NewRequest("POST", "/", nil)
	opts := parseImportOptions(req)
	if opts != (ImportOptions{}) {
		t.Errorf("parseImportOptions() with no form values = %+v, want the zero value", opts)
	}
}

// TestFormatEntityCounts covers the summary composition. The i18n catalog is
// not initialized in unit tests, so i18n.T echoes the key back — which is fine
// here: what matters is that each non-zero entity type contributes its own
// count and label, and that zero counts are omitted. Composing the summary in
// Go is what lets a new entity type appear in the flash automatically instead
// of needing a new positional %d in every locale.
func TestFormatEntityCounts(t *testing.T) {
	got := formatEntityCounts("en", map[string]int{
		string(types.EntityPage): 3,
		string(types.EntityMenu): 1,
		string(types.EntityTag):  0,
	})

	if !strings.Contains(got, "3 ") || !strings.Contains(got, string(types.EntityPage)) {
		t.Errorf("formatEntityCounts() = %q, want it to report 3 pages", got)
	}
	if !strings.Contains(got, "1 ") || !strings.Contains(got, string(types.EntityMenu)) {
		t.Errorf("formatEntityCounts() = %q, want it to report 1 menu", got)
	}
	if strings.Contains(got, string(types.EntityTag)) {
		t.Errorf("formatEntityCounts() = %q, want zero counts omitted", got)
	}
	// Menu items sort before pages in AllEntityTypes, and pages before menus is
	// wrong — the order must follow the declared entity order.
	if idx := strings.Index(got, string(types.EntityMenu)); idx > strings.Index(got, string(types.EntityPage)) {
		t.Errorf("formatEntityCounts() = %q, want entity types in AllEntityTypes order", got)
	}

	if empty := formatEntityCounts("en", map[string]int{}); empty == "" {
		t.Error("formatEntityCounts() with no counts should still render a phrase")
	}
}

// TestInvalidateCachesIsNilSafe locks the property that the migrator works
// without a cache manager, which is how every test context and any embedder
// that does not wire one is configured.
func TestInvalidateCachesIsNilSafe(t *testing.T) {
	m := testModule(t)
	if m.ctx.Cache != nil {
		t.Fatal("the test module context should have no cache manager")
	}
	m.invalidateCaches(ImportOptions{ImportMenus: true})
}

func TestModuleImplementsTrackerAndReporter(t *testing.T) {
	var _ types.ImportTracker = (*Module)(nil)
	var _ types.ProgressReporter = (*Module)(nil)
}

// --- Status fragment ---

// renderJobStatus renders the status fragment for a job.
func renderJobStatus(t *testing.T, job *ImportJob) string {
	t.Helper()
	var sb strings.Builder
	data := buildJobStatusView("drupal", job, nil)
	if err := MigratorJobStatus(&adminviews.PageContext{}, data).Render(context.Background(), &sb); err != nil {
		t.Fatalf("failed to render job status: %v", err)
	}
	return sb.String()
}

// TestJobStatusFragmentPollsWhileRunning proves the running fragment carries
// its own polling attributes, which is the whole mechanism — there is no
// JavaScript driving the refresh.
func TestJobStatusFragmentPollsWhileRunning(t *testing.T) {
	markup := renderJobStatus(t, &ImportJob{
		Status: JobRunning, Phase: string(types.EntityPage),
		Processed: 3, Total: 10, UpdatedAt: time.Now(),
		Counters: map[string]int{string(types.EntityPage): 3},
	})

	for _, want := range []string{
		`id="migrator-job-status"`,
		`hx-trigger="every 2s"`,
		`hx-get="/admin/migrator/drupal/status"`,
		`hx-swap="outerHTML"`,
		// Guards session expiry: RequireRole redirects to /login and htmx would
		// otherwise swap the login page into the status card.
		`hx-select="#migrator-job-status"`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("running fragment is missing %s\ngot: %s", want, markup)
		}
	}
}

// TestJobStatusFragmentStopsPollingWhenTerminal is the counterpart: swapping in
// a copy without hx-trigger is what ends the poll loop. A fragment that kept
// polling after completion would hammer the server indefinitely.
func TestJobStatusFragmentStopsPollingWhenTerminal(t *testing.T) {
	for _, status := range []JobStatus{JobCompleted, JobFailed, JobInterrupted} {
		t.Run(string(status), func(t *testing.T) {
			markup := renderJobStatus(t, &ImportJob{
				Status: status, UpdatedAt: time.Now(),
				Counters: map[string]int{string(types.EntityPage): 2},
			})
			if !strings.Contains(markup, `id="migrator-job-status"`) {
				t.Error("terminal fragment must keep the swap target id")
			}
			if strings.Contains(markup, "hx-trigger") {
				t.Errorf("terminal fragment still polls\ngot: %s", markup)
			}
		})
	}
}

// TestJobStatusFragmentStaleRunningStopsPolling covers a job whose process died
// without reaping: the UI must stop spinning rather than poll forever.
func TestJobStatusFragmentStaleRunningStopsPolling(t *testing.T) {
	markup := renderJobStatus(t, &ImportJob{
		Status:    JobRunning,
		UpdatedAt: time.Now().Add(-staleJobThreshold - time.Minute),
	})
	if strings.Contains(markup, "hx-trigger") {
		t.Errorf("a stale running job should stop polling\ngot: %s", markup)
	}
	if !strings.Contains(markup, "job-status-"+string(JobInterrupted)) {
		t.Errorf("a stale running job should render as interrupted\ngot: %s", markup)
	}
}

func TestJobStatusFragmentIdle(t *testing.T) {
	markup := renderJobStatus(t, nil)
	if !strings.Contains(markup, `id="migrator-job-status"`) {
		t.Error("the idle fragment must still carry the swap target id")
	}
	if strings.Contains(markup, "hx-trigger") {
		t.Error("the idle fragment must not poll")
	}
	if !strings.Contains(markup, "job-status-idle") {
		t.Errorf("idle fragment missing its status class\ngot: %s", markup)
	}
}

// TestHandleJobStatusUnauthenticated keeps the polled endpoint behind the same
// auth check as the rest of the module.
func TestHandleJobStatusUnauthenticated(t *testing.T) {
	m := testModule(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/migrator/drupal/status", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("source", "drupal")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()

	m.handleJobStatus(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

// TestAdminRoutesAreGatedToAdmins enforces the decision that the migrator —
// which takes source database credentials and writes core content — is
// admin-only, the same treatment modules/dbmanager gets.
//
// Bug state: register a route outside the RequireAdmin group and an editor
// reaches it.
func TestAdminRoutesAreGatedToAdmins(t *testing.T) {
	m := testModule(t)

	router := chi.NewRouter()
	m.RegisterAdminRoutes(router)

	var routes []string
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, method+" "+route)
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk routes: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("no admin routes were registered")
	}

	editor := store.User{ID: 2, Email: "editor@example.com", Role: "editor"}
	for _, route := range routes {
		parts := strings.SplitN(route, " ", 2)
		method, path := parts[0], parts[1]
		path = strings.ReplaceAll(path, "{source}", "drupal")

		req := httptest.NewRequest(method, path, nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, editor))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code == http.StatusOK {
			t.Errorf("%s is reachable by an editor (status 200); every migrator "+
				"route must sit inside the RequireAdmin group", route)
		}
	}
}

// TestFinishJobKeepsNoticesSeparateFromErrors proves the two message classes
// survive the round trip through the job row independently.
//
// They shared a column before, which is how an import with three informational
// messages and zero failures came to report "3 errors".
func TestFinishJobKeepsNoticesSeparateFromErrors(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	jobID, err := m.startJob(ctx, "drupal", "admin@example.com", 1, ImportOptions{})
	if err != nil {
		t.Fatalf("startJob() error = %v", err)
	}

	result := &ImportResult{PagesImported: 2}
	result.AddNotice("optional table %q not found in source database; related content skipped", "node__body")
	result.AddNotice("%d non-default-language node translations were not imported", 19)

	if err := m.finishJob(ctx, jobID, JobCompleted, result, nil); err != nil {
		t.Fatalf("finishJob() error = %v", err)
	}

	job, err := m.latestJob(ctx, "drupal")
	if err != nil {
		t.Fatalf("latestJob() error = %v", err)
	}
	if len(job.Errors) != 0 {
		t.Errorf("job reports %d errors, want 0: %v", len(job.Errors), job.Errors)
	}
	if len(job.Notices) != 2 {
		t.Errorf("job reports %d notices, want 2: %v", len(job.Notices), job.Notices)
	}
	if !job.HasNotices() {
		t.Error("HasNotices() should be true")
	}
	if job.Status != JobCompleted {
		t.Errorf("Status = %q, want %q — notices must not affect the outcome", job.Status, JobCompleted)
	}
}

// TestMigrationV3AddsNoticesColumn covers the added column and its idempotence,
// since the migration runs against databases that already have the job table.
func TestMigrationV3AddsNoticesColumn(t *testing.T) {
	m := testModule(t)

	var count int
	if err := m.ctx.DB.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('migrator_import_jobs') WHERE name = 'notices'`,
	).Scan(&count); err != nil {
		t.Fatalf("failed to inspect job table: %v", err)
	}
	if count != 1 {
		t.Fatalf("migrator_import_jobs has %d 'notices' columns, want 1", count)
	}

	// Re-running must not fail on a database that already has the column.
	for _, mig := range m.Migrations() {
		if mig.Version == 3 {
			if err := mig.Up(m.ctx.DB); err != nil {
				t.Errorf("re-running migration 3 failed: %v", err)
			}
		}
	}
}

// TestJobStatusFragmentSeparatesNoticesFromErrors keeps the two classes visually
// distinct in the admin UI.
func TestJobStatusFragmentSeparatesNoticesFromErrors(t *testing.T) {
	markup := renderJobStatus(t, &ImportJob{
		Status:    JobCompleted,
		UpdatedAt: time.Now(),
		Counters:  map[string]int{string(types.EntityPage): 2},
		Notices:   []string{"optional table \"node__body\" not found"},
	})

	if !strings.Contains(markup, "job-status-notices") {
		t.Errorf("notices should render in their own block:\n%s", markup)
	}
	if strings.Contains(markup, "job-status-errors") {
		t.Errorf("a job with only notices must not render an errors block:\n%s", markup)
	}
}

// --- Status response: out-of-band refresh and flash safety ---

// TestStatusResponseCarriesImportedContentOOB proves one poll refreshes both
// regions of the page.
//
// The Imported Content card sits outside the polled fragment, so without the
// out-of-band copy a finished import still showed pre-import counts — and on a
// first import, no Delete button at all — until the operator refreshed by hand.
// That was the reported complaint.
func TestStatusResponseCarriesImportedContentOOB(t *testing.T) {
	var sb strings.Builder
	data := buildJobStatusView("drupal", &ImportJob{
		Status: JobCompleted, UpdatedAt: time.Now(),
		Counters: map[string]int{string(types.EntityPage): 2},
	}, nil)
	counts := map[string]int{string(types.EntityPage): 2, string(types.EntityMedia): 42}

	err := MigratorJobStatusResponse(&adminviews.PageContext{}, data, counts, true).
		Render(context.Background(), &sb)
	if err != nil {
		t.Fatalf("failed to render status response: %v", err)
	}
	markup := sb.String()

	if !strings.Contains(markup, `id="migrator-job-status"`) {
		t.Error("status response is missing the job card")
	}
	if !strings.Contains(markup, `id="migrator-imported-content"`) {
		t.Fatalf("status response is missing the imported-content card:\n%s", markup)
	}
	if !strings.Contains(markup, `hx-swap-oob="true"`) {
		t.Errorf("imported-content card is not marked for an out-of-band swap, so it "+
			"would never reach the page:\n%s", markup)
	}
	if !strings.Contains(markup, "42") {
		t.Errorf("imported-content card does not carry the fresh counts:\n%s", markup)
	}
	// The Delete button only exists once something has been imported.
	if !strings.Contains(markup, "delete-imported-form") {
		t.Errorf("imported-content card should offer the delete form once counts exist:\n%s", markup)
	}
}

// TestImportedContentOmitsOOBMarkerOnFullPage keeps the same component reusable
// for the page render, where an out-of-band marker would be wrong.
func TestImportedContentOmitsOOBMarkerOnFullPage(t *testing.T) {
	var sb strings.Builder
	err := MigratorImportedContent(&adminviews.PageContext{}, "drupal",
		map[string]int{string(types.EntityPage): 1}, false).
		Render(context.Background(), &sb)
	if err != nil {
		t.Fatalf("failed to render imported content: %v", err)
	}
	// The attribute must be absent, not empty: htmx treats any present
	// hx-swap-oob as an out-of-band element, so hx-swap-oob="" would make the
	// page's own card try to swap itself away on load.
	if strings.Contains(sb.String(), "hx-swap-oob") {
		t.Errorf("the page copy must carry no hx-swap-oob attribute at all:\n%s", sb.String())
	}
}

// TestRunningCardOffersManualRefresh covers the degraded-mode escape hatch: a
// frozen card and a slow import look identical, so a running card always offers
// a way to check by hand.
func TestRunningCardOffersManualRefresh(t *testing.T) {
	running := renderJobStatus(t, &ImportJob{Status: JobRunning, UpdatedAt: time.Now()})
	if !strings.Contains(running, "/admin/migrator/drupal") {
		t.Errorf("a running card should link back to the form for a manual refresh:\n%s", running)
	}

	done := renderJobStatus(t, &ImportJob{Status: JobCompleted, UpdatedAt: time.Now()})
	if strings.Contains(done, "job-status-refresh") {
		t.Errorf("a finished card does not need a refresh link:\n%s", done)
	}
}

// TestJobStatusPollDoesNotConsumeFlash guards a bug the status endpoint
// introduced: it built a full page context, and BuildPageContext pops the flash
// out of the session. Since the fragment never renders the alert, a poll every
// two seconds silently swallowed messages the operator was meant to see.
//
// This walks the AST rather than the source text, so the explanatory comment in
// handleJobStatus naming BuildPageContext does not count as a call.
func TestJobStatusPollDoesNotConsumeFlash(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(repoRoot(t), "modules/migrator/handlers.go"), nil, 0)
	if err != nil {
		t.Fatalf("failed to parse handlers.go: %v", err)
	}

	var handler *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "handleJobStatus" {
			handler = fn
			break
		}
	}
	if handler == nil {
		t.Fatal("could not locate handleJobStatus; has it been renamed?")
	}

	ast.Inspect(handler, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "BuildPageContext" {
			t.Errorf("%s: handleJobStatus calls BuildPageContext, which pops the session "+
				"flash. The polled fragment does not render @Alert, so every poll would "+
				"discard a pending flash message. Build a minimal PageContext instead.",
				fset.Position(call.Pos()))
		}
		return true
	})
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to determine working directory: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate the repository root")
	return ""
}

// TestDeleteImportedItemsRefusesWhileImportRunning pins the guarantee at the
// layer that can actually hold it.
//
// handleDeleteImported checks for a running job too, but that is a bare read:
// startJob can insert its row a moment later, and the delete would then strip
// content the importer is still using and clear the tracking rows of the job
// that had just begun, leaving its content with no undo path. The check that
// matters lives inside deleteImportedItems' own transaction, which SQLite's
// immediate write lock serializes against startJob's.
//
// Bug state: drop the in-transaction check and this deletes the tracked page
// while the job row says the source is importing.
func TestDeleteImportedItemsRefusesWhileImportRunning(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	queries := store.New(m.ctx.DB)
	now := time.Now()

	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "race@example.com", PasswordHash: "x", Role: "public",
		Name: "Race", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	page, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Imported", Slug: "imported-race", Status: "published",
		AuthorID: user.ID, LanguageCode: lang.Code, PageType: "page",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create page: %v", err)
	}
	if err := m.TrackImportedItem(ctx, "drupal", string(types.EntityPage), page.ID); err != nil {
		t.Fatalf("failed to track page: %v", err)
	}

	// An import claims the source, exactly as a request racing this one would.
	if _, err := m.startJob(ctx, "drupal", "admin@example.com", user.ID, ImportOptions{}); err != nil {
		t.Fatalf("startJob() error = %v", err)
	}

	if _, err := m.deleteImportedItems(ctx, "drupal"); !errors.Is(err, errImportRunning) {
		t.Fatalf("deleteImportedItems() error = %v, want errImportRunning", err)
	}

	if _, err := queries.GetPageByID(ctx, page.ID); err != nil {
		t.Errorf("the imported page was deleted while its import was running: %v", err)
	}
	ids, err := m.getImportedItems(ctx, m.ctx.DB, "drupal", string(types.EntityPage))
	if err != nil {
		t.Fatalf("failed to read tracking rows: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("tracking rows = %d, want 1 — a refused delete must not clear them", len(ids))
	}
}

// TestDeleteImportedItemsKeepsTaxonomyUsedByOriginalPages covers an imported
// tag or category that an administrator later assigns to a page of their own.
//
// page_tags.tag_id and page_categories.category_id are both ON DELETE CASCADE,
// so removing the imported row silently strips that association from the
// original page — breaking the module's documented promise that original oCMS
// content is not touched, and with no way to undo it.
//
// By the time tags and categories are deleted every imported page is already
// gone, so any join row still present belongs to a page this import does not
// own. That is what makes the reference count a reliable test for "shared".
func TestDeleteImportedItemsKeepsTaxonomyUsedByOriginalPages(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	queries := store.New(m.ctx.DB)
	now := time.Now()

	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "owner@example.com", PasswordHash: "x", Role: "admin",
		Name: "Owner", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// An original page, never imported and never tracked.
	original, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Original", Slug: "original", Status: "published",
		AuthorID: author.ID, LanguageCode: lang.Code, PageType: "page",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create original page: %v", err)
	}

	sharedTag, err := queries.CreateTag(ctx, store.CreateTagParams{
		Name: "Shared", Slug: "shared", LanguageCode: lang.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}
	lonelyTag, err := queries.CreateTag(ctx, store.CreateTagParams{
		Name: "Lonely", Slug: "lonely", LanguageCode: lang.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}
	sharedCat, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name: "Shared", Slug: "shared-cat", LanguageCode: lang.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create category: %v", err)
	}

	// The administrator assigns the imported taxonomy to their own page.
	if err := queries.AddTagToPage(ctx, store.AddTagToPageParams{
		PageID: original.ID, TagID: sharedTag.ID,
	}); err != nil {
		t.Fatalf("failed to link tag: %v", err)
	}
	if err := queries.AddCategoryToPage(ctx, store.AddCategoryToPageParams{
		PageID: original.ID, CategoryID: sharedCat.ID,
	}); err != nil {
		t.Fatalf("failed to link category: %v", err)
	}

	for _, tracked := range []struct {
		entityType types.EntityType
		id         int64
	}{
		{types.EntityTag, sharedTag.ID},
		{types.EntityTag, lonelyTag.ID},
		{types.EntityCategory, sharedCat.ID},
	} {
		if err := m.TrackImportedItem(ctx, "drupal", string(tracked.entityType), tracked.id); err != nil {
			t.Fatalf("failed to track %s: %v", tracked.entityType, err)
		}
	}

	if _, err := m.deleteImportedItems(ctx, "drupal"); err != nil {
		t.Fatalf("deleteImportedItems() error = %v", err)
	}

	if _, err := queries.GetTagByID(ctx, sharedTag.ID); err != nil {
		t.Errorf("a tag an original page still uses was deleted: %v", err)
	}
	if _, err := queries.GetCategoryByID(ctx, sharedCat.ID); err != nil {
		t.Errorf("a category an original page still uses was deleted: %v", err)
	}
	if _, err := queries.GetTagByID(ctx, lonelyTag.ID); err == nil {
		t.Error("an unreferenced imported tag survived; only shared rows should be kept")
	}

	tags, err := queries.GetTagsForPage(ctx, original.ID)
	if err != nil {
		t.Fatalf("failed to read original page tags: %v", err)
	}
	if len(tags) != 1 {
		t.Errorf("original page has %d tags, want 1 — deleting the import must not "+
			"cascade its associations away", len(tags))
	}
	cats, err := queries.GetCategoriesForPage(ctx, original.ID)
	if err != nil {
		t.Fatalf("failed to read original page categories: %v", err)
	}
	if len(cats) != 1 {
		t.Errorf("original page has %d categories, want 1", len(cats))
	}
}

// TestDeleteImportedItemsKeepsAdminChildrenAndSharedMedia extends the
// preserve-shared rule to the two remaining cascade paths out of an import.
//
//   - menu_items.parent_id is ON DELETE CASCADE, so deleting an imported menu
//     item takes any child with it, including one an administrator added under
//     it afterwards.
//   - pages.featured_image_id and og_image_id are ON DELETE SET NULL, so
//     deleting imported media silently strips the image from an original page
//     that had started using it.
//
// The imported item's children are lifted to its own parent and the item is then
// deleted: preserving it instead would leave an imported entry in the site's
// navigation permanently, which is the content the operator asked to remove.
func TestDeleteImportedItemsKeepsAdminChildrenAndSharedMedia(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	queries := store.New(m.ctx.DB)
	now := time.Now()

	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}
	author, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "keeper@example.com", PasswordHash: "x", Role: "admin",
		Name: "Keeper", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	sharedMedia, err := queries.CreateMedia(ctx, store.CreateMediaParams{
		Uuid: "11111111-2222-3333-4444-555555555555", Filename: "hero.jpg",
		MimeType: "image/jpeg", Size: 1, UploadedBy: author.ID,
		LanguageCode: lang.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create media: %v", err)
	}
	lonelyMedia, err := queries.CreateMedia(ctx, store.CreateMediaParams{
		Uuid: "66666666-7777-8888-9999-000000000000", Filename: "unused.jpg",
		MimeType: "image/jpeg", Size: 1, UploadedBy: author.ID,
		LanguageCode: lang.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create media: %v", err)
	}

	// An original page adopts the imported media as its featured image.
	original, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title: "Original", Slug: "original-keeps", Status: "published",
		AuthorID: author.ID, LanguageCode: lang.Code, PageType: "page",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create page: %v", err)
	}
	if _, err := m.ctx.DB.ExecContext(ctx,
		`UPDATE pages SET featured_image_id = ? WHERE id = ?`, sharedMedia.ID, original.ID); err != nil {
		t.Fatalf("failed to set featured image: %v", err)
	}

	menu, err := queries.CreateMenu(ctx, store.CreateMenuParams{
		Name: "Main", Slug: "main-keeps", LanguageCode: lang.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create menu: %v", err)
	}
	importedParent, err := queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
		MenuID: menu.ID, Title: "Imported parent", IsActive: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create menu item: %v", err)
	}
	importedChild, err := queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
		MenuID: menu.ID, Title: "Imported child", IsActive: true,
		ParentID:  sql.NullInt64{Int64: importedParent.ID, Valid: true},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create menu item: %v", err)
	}
	// The administrator hangs their own link off the imported parent.
	adminChild, err := queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
		MenuID: menu.ID, Title: "Admin child", IsActive: true,
		ParentID:  sql.NullInt64{Int64: importedParent.ID, Valid: true},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create menu item: %v", err)
	}

	for _, tracked := range []struct {
		entityType types.EntityType
		id         int64
	}{
		{types.EntityMenuItem, importedParent.ID},
		{types.EntityMenuItem, importedChild.ID},
		{types.EntityMedia, sharedMedia.ID},
		{types.EntityMedia, lonelyMedia.ID},
	} {
		if err := m.TrackImportedItem(ctx, "drupal", string(tracked.entityType), tracked.id); err != nil {
			t.Fatalf("failed to track %s: %v", tracked.entityType, err)
		}
	}

	if _, err := m.deleteImportedItems(ctx, "drupal"); err != nil {
		t.Fatalf("deleteImportedItems() error = %v", err)
	}

	survivor, err := queries.GetMenuItemByID(ctx, adminChild.ID)
	if err != nil {
		t.Fatalf("the administrator's menu item was cascade-deleted with its imported parent: %v", err)
	}
	if survivor.ParentID.Valid {
		t.Errorf("the administrator's item is still parented to %d; it should have been "+
			"lifted to the deleted item's own parent", survivor.ParentID.Int64)
	}
	if _, err := queries.GetMenuItemByID(ctx, importedParent.ID); err == nil {
		t.Error("the imported parent survived; leaving it would keep an imported entry " +
			"in the navigation the operator asked to clear")
	}
	if _, err := queries.GetMenuItemByID(ctx, importedChild.ID); err == nil {
		t.Error("the imported child survived; a tracked child must not preserve its tracked parent")
	}

	if _, err := queries.GetMediaByID(ctx, sharedMedia.ID); err != nil {
		t.Errorf("media an original page uses as its featured image was deleted: %v", err)
	}
	if _, err := queries.GetMediaByID(ctx, lonelyMedia.ID); err == nil {
		t.Error("unreferenced imported media survived; only shared rows should be kept")
	}

	page, err := queries.GetPageByID(ctx, original.ID)
	if err != nil {
		t.Fatalf("failed to re-read the original page: %v", err)
	}
	if !page.FeaturedImageID.Valid {
		t.Error("the original page lost its featured image; ON DELETE SET NULL fired")
	}
}
