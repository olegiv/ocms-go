// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// TestDeriveJobStatus pins the two ordering mistakes that shipped in the
// original status derivation.
//
// Bug state 1: `case err != nil` preceded the context check, so JobInterrupted
// was unreachable for Drupal — which propagates context.Canceled — and a
// routine restart mid-import rendered as red "Failed — context canceled".
//
// Bug state 2: status was derived from the returned error alone. Sources record
// per-item failures on the result, so an import in which every single item
// failed returned (result, nil) and rendered as a green "Completed".
func TestDeriveJobStatus(t *testing.T) {
	failedResult := &ImportResult{}
	failedResult.AddError("every item failed")

	okResult := &ImportResult{PagesImported: 3}

	cases := []struct {
		name   string
		err    error
		ctxErr error
		result *ImportResult
		want   JobStatus
	}{
		{"clean run", nil, nil, okResult, JobCompleted},
		{"nil result", nil, nil, nil, JobCompleted},
		{"hard failure", errors.New("boom"), nil, nil, JobFailed},
		{"cancelled", context.Canceled, nil, okResult, JobInterrupted},
		{"deadline exceeded", context.DeadlineExceeded, nil, okResult, JobInterrupted},
		{"wrapped cancellation", fmt.Errorf("stage: %w", context.Canceled), nil, nil, JobInterrupted},
		{"context cancelled without error", nil, context.Canceled, okResult, JobInterrupted},
		{"per-item failures only", nil, nil, failedResult, JobPartial},
		// A hard error outranks per-item errors: the run did not finish.
		{"hard failure with per-item errors", errors.New("boom"), nil, failedResult, JobFailed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveJobStatus(tc.err, tc.ctxErr, tc.result); got != tc.want {
				t.Errorf("deriveJobStatus(%v, %v, result) = %q, want %q",
					tc.err, tc.ctxErr, got, tc.want)
			}
		})
	}
}

// TestJobPartialIsAKnownStatus keeps the new status wired into everything that
// enumerates statuses — the locale coverage test derives its keys from
// AllJobStatuses, so an unlisted status would silently render an untranslated
// label.
func TestJobPartialIsAKnownStatus(t *testing.T) {
	for _, s := range AllJobStatuses {
		if s == JobPartial {
			return
		}
	}
	t.Error("JobPartial is missing from AllJobStatuses; its label will not be translated")
}

// TestJobStatusViewKeepsPollingOnReadError pins the recovery behaviour of the
// status fragment.
//
// Bug state: a failed job lookup produced a nil Job, which rendered as "no
// import has run" AND set Polling=false. Because the poller swaps itself
// outerHTML, one transient SQLite BUSY replaced the polling element with a
// non-polling one — the panel went blank mid-import and never came back
// without a manual refresh.
func TestJobStatusViewKeepsPollingOnReadError(t *testing.T) {
	data := buildJobStatusView("drupal", nil, errors.New("database is locked"))

	if !data.ReadFailed {
		t.Error("ReadFailed = false; a failed lookup must be distinguishable from an idle source")
	}
	if !data.Polling {
		t.Error("Polling = false after a read error; the fragment would stop polling " +
			"and strand the panel until a manual page reload")
	}

	// A genuinely idle source still must not poll.
	idle := buildJobStatusView("drupal", nil, nil)
	if idle.ReadFailed {
		t.Error("ReadFailed = true for a source that has simply never been imported")
	}
	if idle.Polling {
		t.Error("Polling = true for an idle source; nothing is running to poll for")
	}
}

// TestFinishJobPersistsEveryStatus writes each terminal status through the real
// schema and reads it back.
//
// This is the test that was missing when JobPartial was introduced. The status
// was added to the Go constants but not to the table's CHECK constraint, so
// every partial run failed its terminal UPDATE, the row stayed 'running', and
// the partial unique index then blocked all future imports for that source.
// TestDeriveJobStatus could not catch it: it exercises the pure function, never
// the write. Any future status is covered automatically by iterating
// AllJobStatuses.
func TestFinishJobPersistsEveryStatus(t *testing.T) {
	for _, status := range AllJobStatuses {
		if status == JobRunning {
			continue // not a terminal state; finishJob is never called with it
		}
		t.Run(string(status), func(t *testing.T) {
			m := testModule(t)
			ctx := context.Background()

			jobID, err := m.startJob(ctx, "drupal", "tester@example.com", 0, ImportOptions{})
			if err != nil {
				t.Fatalf("startJob: %v", err)
			}

			result := &ImportResult{PagesImported: 7}
			if err := m.finishJob(ctx, jobID, status, result, nil); err != nil {
				t.Fatalf("finishJob(%s): %v — the schema CHECK constraint most likely "+
					"does not allow this status", status, err)
			}

			job, err := m.latestJob(ctx, "drupal")
			if err != nil {
				t.Fatalf("latestJob: %v", err)
			}
			if job.Status != status {
				t.Errorf("persisted status = %q, want %q; the row was left in a "+
					"non-terminal state and the running-job index will block "+
					"every future import for this source", job.Status, status)
			}
			if job.Counters[string(types.EntityPage)] != 7 {
				t.Errorf("counters were not persisted: %v", job.Counters)
			}
		})
	}
}

// TestFinishJobPreservesCountersWithoutResult covers the panic path.
//
// Bug state: finishJob unconditionally wrote counters built from the result,
// so a nil result (a panic, most often) replaced the counters the flush
// goroutine had already persisted with an empty map. A run that had created
// 4200 pages before panicking reported "0 imported", which invites a re-run
// that duplicates all of them.
//
// The earlier attempt at this fix — hoisting `result` above the recover defer —
// did nothing, because a panic means the assignment never executes.
func TestFinishJobPreservesCountersWithoutResult(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	jobID, err := m.startJob(ctx, "drupal", "tester@example.com", 0, ImportOptions{})
	if err != nil {
		t.Fatalf("startJob: %v", err)
	}

	// Simulate the progress the flush goroutine writes mid-run.
	progress := newJobProgress()
	for range 4200 {
		progress.addItem(types.EntityPage)
	}
	snap := progress.forceSnapshot()
	if err := m.flushJobProgress(ctx, jobID, snap); err != nil {
		t.Fatalf("flushJobProgress: %v", err)
	}

	// Now finish as a panic would: terminal status, no result.
	if err := m.finishJob(ctx, jobID, JobFailed, nil, errors.New("import panicked: boom")); err != nil {
		t.Fatalf("finishJob: %v", err)
	}

	job, err := m.latestJob(ctx, "drupal")
	if err != nil {
		t.Fatalf("latestJob: %v", err)
	}
	if job.Status != JobFailed {
		t.Errorf("status = %q, want %q", job.Status, JobFailed)
	}
	if got := job.Counters[string(types.EntityPage)]; got != 4200 {
		t.Errorf("persisted page count = %d, want 4200; the work already done was "+
			"erased, so the operator sees a failed import that appears to have "+
			"created nothing", got)
	}
	if job.FatalError == "" {
		t.Error("fatal error was not recorded")
	}
}

func TestFailedProgressFlushRemainsRecoverableAfterPanic(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	jobID, err := m.startJob(ctx, "drupal", "tester@example.com", 0, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	progress := newJobProgress()
	for range 17 {
		progress.addItem(types.EntityPage)
	}
	snap, revision, changed := progress.pendingSnapshot()
	if !changed {
		t.Fatal("progress snapshot is unexpectedly clean")
	}
	if _, err := m.ctx.DB.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER fail_progress_flush
		BEFORE UPDATE OF counters ON migrator_import_jobs WHEN OLD.id = %d
		BEGIN SELECT RAISE(FAIL, 'forced progress flush failure'); END`, jobID)); err != nil {
		t.Fatal(err)
	}
	if err := m.flushJobProgress(ctx, jobID, snap); err == nil {
		t.Fatal("flushJobProgress() error = nil, want forced failure")
	}
	// No acknowledgement follows a failed ticker write.
	if _, retryRevision, pending := progress.pendingSnapshot(); !pending || retryRevision != revision {
		t.Fatal("failed progress write was incorrectly marked durable")
	}
	if _, err := m.ctx.DB.ExecContext(ctx, `DROP TRIGGER fail_progress_flush`); err != nil {
		t.Fatal(err)
	}
	// Panic recovery force-copies the full accumulator after joining the ticker.
	if err := m.flushJobProgress(ctx, jobID, progress.forceSnapshot()); err != nil {
		t.Fatal(err)
	}
	if err := m.finishJob(ctx, jobID, JobFailed, nil, errors.New("import panicked: boom")); err != nil {
		t.Fatal(err)
	}
	job, err := m.latestJob(ctx, "drupal")
	if err != nil {
		t.Fatal(err)
	}
	if got := job.Counters[string(types.EntityPage)]; got != 17 {
		t.Fatalf("persisted page count after failed tick and panic = %d, want 17", got)
	}
}

// TestCheckActivationUsesTheRegistryContext covers the activation guard in the
// state it actually runs in.
//
// SetActive calls the guard before Init, so a module that was inactive at
// startup has m.ctx == nil at that moment. The first version of this guard read
// m.ctx and returned "allow" on nil, which made it a no-op in precisely the
// scenario it exists for: activating the migrator in production with no host
// allowlist configured.
func TestCheckActivationUsesTheRegistryContext(t *testing.T) {
	prodNoAllowlist := &module.Context{Config: &config.Config{
		Env:                           "production",
		RequireMigratorAllowedDBHosts: true,
		MigratorAllowedDBHosts:        "",
	}}

	// A brand-new module, exactly as SetActive sees it: Init has not run.
	m := New()
	if m.ctx != nil {
		t.Fatal("precondition failed: a fresh module should have no context")
	}

	if err := m.CheckActivation(prodNoAllowlist); err == nil {
		t.Error("activation was allowed in production with an empty allowlist; " +
			"the guard must read the passed context, not the module's own nil one")
	}

	// Configured allowlist: activation proceeds.
	prodWithAllowlist := &module.Context{Config: &config.Config{
		Env:                           "production",
		RequireMigratorAllowedDBHosts: true,
		MigratorAllowedDBHosts:        "db.internal",
	}}
	if err := m.CheckActivation(prodWithAllowlist); err != nil {
		t.Errorf("activation refused despite a configured allowlist: %v", err)
	}

	// Development is unaffected.
	dev := &module.Context{Config: &config.Config{Env: "development"}}
	if err := m.CheckActivation(dev); err != nil {
		t.Errorf("activation refused outside production: %v", err)
	}
}
