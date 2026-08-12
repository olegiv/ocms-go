// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// JobStatus is the lifecycle state of an import job.
type JobStatus string

const (
	JobRunning     JobStatus = "running"
	JobCompleted   JobStatus = "completed"
	JobFailed      JobStatus = "failed"
	JobInterrupted JobStatus = "interrupted"
)

// AllJobStatuses lists every job status, for translation-coverage tests.
var AllJobStatuses = []JobStatus{JobRunning, JobCompleted, JobFailed, JobInterrupted}

const (
	// importJobTimeout bounds a single import run. It is generous because the
	// point of running detached is to escape the 30s router timeout, but it
	// still guarantees a stuck source connection cannot hold a job forever.
	importJobTimeout = 6 * time.Hour

	// progressFlushInterval is how often live progress is written to the job
	// row. Throttling means a 10k-item import costs ~1 write/second rather
	// than one write per item.
	progressFlushInterval = time.Second

	// staleJobThreshold is how long a running job may go without a heartbeat
	// before it is treated as orphaned. It is far above progressFlushInterval
	// so a merely slow stage is never mistaken for a dead process.
	staleJobThreshold = 2 * time.Minute

	// jobHistoryLimit is how many terminal jobs are retained per source.
	jobHistoryLimit = 20

	// finishJobTimeout bounds the terminal write.
	finishJobTimeout = 15 * time.Second
)

// errImportRunning is returned by startJob when the source already has a
// running job. Callers compare with errors.Is.
var errImportRunning = errors.New("migrator: import already running for this source")

// ImportJob is one row of migrator_import_jobs.
type ImportJob struct {
	ID             int64
	Source         string
	Status         JobStatus
	Phase          string
	Processed      int
	Total          int
	Counters       map[string]int
	Errors         []string
	Notices        []string
	FatalError     string
	StartedByEmail string
	StartedAt      time.Time
	UpdatedAt      time.Time
	FinishedAt     sql.NullTime
}

// IsTerminal reports whether the job has finished.
func (j *ImportJob) IsTerminal() bool {
	return j.Status != JobRunning
}

// IsStale reports whether a running job has missed its heartbeat long enough to
// be considered orphaned. The status fragment uses this so the UI stops
// spinning even before the next restart reaps the row.
func (j *ImportJob) IsStale(now time.Time) bool {
	return j.Status == JobRunning && now.Sub(j.UpdatedAt) > staleJobThreshold
}

// HasNotices reports whether the job recorded informational messages.
func (j *ImportJob) HasNotices() bool { return len(j.Notices) > 0 }

// Count returns the imported count for an entity type.
func (j *ImportJob) Count(entityType types.EntityType) int {
	return j.Counters[string(entityType)]
}

// jobProgress accumulates live progress for one running import. It is written
// by the import goroutine — through TrackImportedItem and ReportProgress — and
// read by the flush ticker, so every field is mutex-guarded.
type jobProgress struct {
	mu        sync.Mutex
	counts    map[types.EntityType]int
	phase     types.EntityType
	processed int
	total     int
	dirty     bool
}

// newJobProgress returns an empty progress accumulator.
func newJobProgress() *jobProgress {
	return &jobProgress{counts: make(map[types.EntityType]int)}
}

// progressSnapshot is a point-in-time copy of jobProgress.
type progressSnapshot struct {
	Phase     types.EntityType
	Processed int
	Total     int
	Counters  map[types.EntityType]int
}

// addItem records one imported entity.
func (p *jobProgress) addItem(entityType types.EntityType) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[entityType]++
	p.dirty = true
}

// report records a progress sample published by the source.
func (p *jobProgress) report(sample types.Progress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.phase = sample.Phase
	p.processed = sample.Processed
	p.total = sample.Total
	p.dirty = true
}

// snapshot returns the current progress and whether it changed since the last
// call, so the ticker can skip writing when nothing moved.
func (p *jobProgress) snapshot() (progressSnapshot, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dirty {
		return progressSnapshot{}, false
	}
	p.dirty = false

	counters := make(map[types.EntityType]int, len(p.counts))
	for k, v := range p.counts {
		counters[k] = v
	}
	return progressSnapshot{
		Phase:     p.phase,
		Processed: p.processed,
		Total:     p.total,
		Counters:  counters,
	}, true
}

// startJob inserts a running job row, refusing to start a second import for the
// same source.
//
// The check and the insert run inside one transaction. oCMS opens SQLite with
// _txlock=immediate, so the write lock is taken at BEGIN and the pair is atomic
// even across processes; the partial unique index on (source) WHERE status =
// 'running' is the backstop if that ever changes.
func (m *Module) startJob(ctx context.Context, source, startedByEmail string, userID int64, opts ImportOptions) (int64, error) {
	optionsJSON, err := json.Marshal(opts)
	if err != nil {
		return 0, fmt.Errorf("failed to encode import options: %w", err)
	}

	tx, err := m.ctx.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existing int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM migrator_import_jobs WHERE source = ? AND status = ?`,
		source, string(JobRunning)).Scan(&existing); err != nil {
		return 0, fmt.Errorf("failed to check for a running import: %w", err)
	}
	if existing > 0 {
		return 0, errImportRunning
	}

	now := time.Now()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO migrator_import_jobs
			(source, status, phase, counters, options, errors, started_by, started_by_email, owner_run_id, started_at, updated_at)
		VALUES (?, ?, '', '{}', ?, '[]', ?, ?, ?, ?, ?)`,
		source, string(JobRunning), string(optionsJSON), userID, startedByEmail, m.runID, now, now)
	if err != nil {
		return 0, fmt.Errorf("failed to create import job: %w", err)
	}

	jobID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to read import job id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit import job: %w", err)
	}

	if err := m.trimJobHistory(ctx, source); err != nil {
		m.ctx.Logger.Warn("failed to trim migrator job history", "source", source, "error", err)
	}
	return jobID, nil
}

// latestJob returns the most recent job for a source, or nil when none exists.
func (m *Module) latestJob(ctx context.Context, source string) (*ImportJob, error) {
	row := m.ctx.DB.QueryRowContext(ctx, `
		SELECT id, source, status, phase, processed, total, counters, errors, notices,
		       fatal_error, started_by_email, started_at, updated_at, finished_at
		FROM migrator_import_jobs
		WHERE source = ?
		ORDER BY id DESC
		LIMIT 1`, source)

	var (
		job          ImportJob
		status       string
		countersJSON string
		errorsJSON   string
		noticesJSON  string
	)
	err := row.Scan(&job.ID, &job.Source, &status, &job.Phase, &job.Processed, &job.Total,
		&countersJSON, &errorsJSON, &noticesJSON, &job.FatalError, &job.StartedByEmail,
		&job.StartedAt, &job.UpdatedAt, &job.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read import job: %w", err)
	}

	job.Status = JobStatus(status)
	if err := json.Unmarshal([]byte(countersJSON), &job.Counters); err != nil {
		job.Counters = map[string]int{}
	}
	if err := json.Unmarshal([]byte(errorsJSON), &job.Errors); err != nil {
		job.Errors = nil
	}
	if err := json.Unmarshal([]byte(noticesJSON), &job.Notices); err != nil {
		job.Notices = nil
	}
	return &job, nil
}

// flushJobProgress writes a progress snapshot to the job row. It doubles as the
// job's heartbeat, which is what reapStaleJobs and ImportJob.IsStale key off.
func (m *Module) flushJobProgress(ctx context.Context, jobID int64, snap progressSnapshot) error {
	countersJSON, err := json.Marshal(stringKeyed(snap.Counters))
	if err != nil {
		return fmt.Errorf("failed to encode progress counters: %w", err)
	}

	_, err = m.ctx.DB.ExecContext(ctx, `
		UPDATE migrator_import_jobs
		SET phase = ?, processed = ?, total = ?, counters = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		string(snap.Phase), snap.Processed, snap.Total, string(countersJSON),
		time.Now(), jobID, string(JobRunning))
	if err != nil {
		return fmt.Errorf("failed to update import job progress: %w", err)
	}
	return nil
}

// finishJob records the terminal state of a job.
//
// The result returned by the source is authoritative here: it alone carries the
// skipped counts, which are never tracked item-by-item, so it replaces rather
// than merges with the live tally.
func (m *Module) finishJob(ctx context.Context, jobID int64, status JobStatus, result *ImportResult, fatal error) error {
	counters := map[string]int{}
	var jobErrors, jobNotices []string
	if result != nil {
		counters = stringKeyed(result.Counters())
		jobErrors = result.Errors
		jobNotices = result.Notices
	}

	countersJSON, err := json.Marshal(counters)
	if err != nil {
		return fmt.Errorf("failed to encode import counters: %w", err)
	}
	errorsJSON, err := json.Marshal(jobErrors)
	if err != nil {
		return fmt.Errorf("failed to encode import errors: %w", err)
	}
	noticesJSON, err := json.Marshal(jobNotices)
	if err != nil {
		return fmt.Errorf("failed to encode import notices: %w", err)
	}

	fatalText := ""
	if fatal != nil {
		fatalText = fatal.Error()
	}

	now := time.Now()
	_, err = m.ctx.DB.ExecContext(ctx, `
		UPDATE migrator_import_jobs
		SET status = ?, counters = ?, errors = ?, notices = ?, fatal_error = ?, updated_at = ?, finished_at = ?
		WHERE id = ?`,
		string(status), string(countersJSON), string(errorsJSON), string(noticesJSON),
		fatalText, now, now, jobID)
	if err != nil {
		return fmt.Errorf("failed to finalize import job: %w", err)
	}
	return nil
}

// reapStaleJobs marks orphaned running jobs as interrupted at startup.
//
// A goroutine cannot outlive its process, so a running row owned by a different
// run ID with a stale heartbeat has nothing behind it. The owner_run_id guard
// matters when two processes share one database: without it, a starting process
// would kill its sibling's live import.
func (m *Module) reapStaleJobs(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-staleJobThreshold)
	res, err := m.ctx.DB.ExecContext(ctx, `
		UPDATE migrator_import_jobs
		SET status = ?, fatal_error = 'interrupted by server restart', finished_at = ?, updated_at = ?
		WHERE status = ? AND owner_run_id <> ? AND updated_at < ?`,
		string(JobInterrupted), time.Now(), time.Now(), string(JobRunning), m.runID, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to reap stale import jobs: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return affected, nil
}

// trimJobHistory keeps only the newest terminal jobs per source so the table
// stays bounded.
func (m *Module) trimJobHistory(ctx context.Context, source string) error {
	_, err := m.ctx.DB.ExecContext(ctx, `
		DELETE FROM migrator_import_jobs
		WHERE source = ? AND status <> ? AND id NOT IN (
			SELECT id FROM migrator_import_jobs
			WHERE source = ? AND status <> ?
			ORDER BY id DESC LIMIT ?
		)`, source, string(JobRunning), source, string(JobRunning), jobHistoryLimit)
	if err != nil {
		return fmt.Errorf("failed to trim import job history: %w", err)
	}
	return nil
}

// markJobsInterruptedForRun marks this process's own running jobs as
// interrupted. Called on shutdown so a restart does not leave a phantom.
func (m *Module) markJobsInterruptedForRun(ctx context.Context) {
	if m.ctx == nil || m.ctx.DB == nil {
		return
	}
	now := time.Now()
	if _, err := m.ctx.DB.ExecContext(ctx, `
		UPDATE migrator_import_jobs
		SET status = ?, fatal_error = 'interrupted by server shutdown', finished_at = ?, updated_at = ?
		WHERE status = ? AND owner_run_id = ?`,
		string(JobInterrupted), now, now, string(JobRunning), m.runID); err != nil {
		m.ctx.Logger.Warn("failed to mark running migrator jobs interrupted", "error", err)
	}
}

// stringKeyed converts an entity-type-keyed counter map to string keys for JSON.
func stringKeyed(counters map[types.EntityType]int) map[string]int {
	out := make(map[string]int, len(counters))
	for k, v := range counters {
		out[string(k)] = v
	}
	return out
}
