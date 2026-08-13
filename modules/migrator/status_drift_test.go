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
