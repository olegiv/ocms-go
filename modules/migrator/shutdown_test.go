// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/olegiv/ocms-go/internal/module"
)

// TestShutdownLeavesStuckImportsBehind checks that Shutdown waits for a
// running import only its share of module.ShutdownTimeout, then marks the job
// interrupted and reports it, instead of holding the module shutdowns that
// share that limit (review of the 2026-10-17 audit fixes).
//
// Bug state: wait with m.jobsWG.Wait() in Shutdown and this fails.
func TestShutdownLeavesStuckImportsBehind(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	jobID, err := m.startJob(ctx, "drupal", "tester@example.com", 0, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		m.jobsMu.Lock()
		m.cancel[jobID] = func() {} // the import ignores its cancellation
		m.jobsMu.Unlock()
		m.jobsWG.Go(func() { <-release })

		start := time.Now()
		err := m.Shutdown()
		if elapsed := time.Since(start); elapsed > module.ShutdownTimeout {
			t.Errorf("Shutdown took %v, want at most module.ShutdownTimeout (%v)", elapsed, module.ShutdownTimeout)
		}
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("[%d]", jobID)) || !strings.Contains(err.Error(), "left behind") {
			t.Errorf("Shutdown returned %v, want it to name import %d as left behind", err, jobID)
		}
		close(release)
		synctest.Wait()
	})
	job, err := m.latestJob(ctx, "drupal")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != JobInterrupted {
		t.Errorf("Status = %q after Shutdown, want %q", job.Status, JobInterrupted)
	}
}
