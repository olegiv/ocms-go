// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/olegiv/ocms-go/internal/module"
)

// TestShutdownWaitsWithinItsShare checks how Shutdown waits for the module's
// background work: work that finishes within bgShutdownWait is waited for,
// with GeoIP still open for its lookup; stuck work is reported and left
// behind, and its lookup after GeoIP closed gets no country instead of
// crashing. Shutdown used to wait for the work without a limit (review of the
// 2026-10-17 audit fixes).
//
// Bug state: wait with m.bgWG.Wait() in Shutdown, or close GeoIP before
// waiting, and this fails.
func TestShutdownWaitsWithinItsShare(t *testing.T) {
	for _, tc := range []struct {
		name        string
		work        time.Duration // 0: until released after Shutdown
		wantCountry string
	}{
		{"finishing work", time.Second, "GB"},
		{"stuck work", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := New()
				if err := m.geoIP.Init("../../internal/geoip/testdata/country-test.mmdb"); err != nil {
					t.Fatal(err)
				}
				release, country := make(chan struct{}), make(chan string, 1)
				m.bgWG.Go(func() {
					if tc.work > 0 {
						time.Sleep(tc.work)
					} else {
						<-release
					}
					country <- m.geoIP.LookupCountry("81.2.69.142")
				})

				start := time.Now()
				err := m.Shutdown()
				elapsed := time.Since(start)
				close(release)
				if got := <-country; got != tc.wantCountry {
					t.Errorf("the work's lookup got %q, want %q", got, tc.wantCountry)
				}
				if elapsed > bgShutdownWait {
					t.Errorf("Shutdown took %v, want at most bgShutdownWait (%v)", elapsed, bgShutdownWait)
				}
				if stuck := tc.work == 0; stuck != (err != nil && strings.Contains(err.Error(), "left behind")) {
					t.Errorf("Shutdown returned %v; stuck work: %t", err, stuck)
				}
			})
		})
	}
}

// TestShutdownStopsAndWaitsForCronJobs checks that Shutdown cancels a running
// cron job's context and waits for the job to return. Cron jobs ran on
// context.Background() and Shutdown did not wait for them, so an aggregation
// running at shutdown wrote on into a closing database (review of the
// 2026-10-17 audit fixes).
//
// Bug state: derive the job's context from context.Background() in
// addCronJob, or drop the cron waiter in Shutdown, and this fails.
func TestShutdownStopsAndWaitsForCronJobs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New()
		m.ctx = &module.Context{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		m.bgCtx, m.bgCancel = context.WithCancel(context.Background())
		m.cron = cron.New()
		started := make(chan struct{})
		var runs atomic.Int32
		var finished atomic.Bool
		m.addCronJob("@every 1s", time.Hour, func(ctx context.Context) error {
			if runs.Add(1) == 1 {
				close(started)
			}
			<-ctx.Done()
			time.Sleep(100 * time.Millisecond) // the job winds down
			finished.Store(true)
			return nil
		}, "probe", "Probe job", "probe job failed")
		m.cron.Start()
		<-started

		if err := m.Shutdown(); err != nil {
			t.Errorf("Shutdown returned %v, want the cron job waited for", err)
		}
		if !finished.Load() {
			t.Error("Shutdown returned while the cron job was still running")
		}
	})
}
