// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"errors"
	"fmt"
	"testing"
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
