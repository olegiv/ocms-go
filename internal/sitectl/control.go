// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	probeTimeout  = 5 * time.Second
	probeInterval = 2 * time.Second
)

func (a *App) systemctl(ctx context.Context, args ...string) error {
	return a.run(ctx, "systemctl", args...)
}

// State returns what `systemctl is-active` prints for the site's unit:
// active, activating, failed, inactive, ...
func (a *App) State(ctx context.Context, site Site) string {
	state, _ := a.output(ctx, "systemctl", "is-active", a.unit(site.ID))
	if state == "" {
		return "unknown"
	}
	return state
}

// Probe asks the site for path on its loopback port; nil means HTTP 200.
func (a *App) Probe(ctx context.Context, site Site, path string) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", site.Port, path), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// waitHealthy polls the site until it answers or HealthTimeout passes.
func (a *App) waitHealthy(ctx context.Context, site Site) error {
	deadline := a.Now().Add(a.HealthTimeout)
	for {
		err := a.Probe(ctx, site, a.HealthPath)
		if err == nil {
			return nil
		}
		if !a.Now().Before(deadline) {
			return fmt.Errorf("not healthy after %s: %w", a.HealthTimeout, err)
		}
		// systemd gave up on the unit: waiting longer changes nothing.
		if state := a.State(ctx, site); state == "failed" || state == "inactive" {
			return fmt.Errorf("unit is %s: %w", state, err)
		}
		if err := sleep(ctx, probeInterval); err != nil {
			return err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Start starts the sites' units.
func (a *App) Start(ctx context.Context, sites []Site) error {
	return a.each(sites, func(site Site) error { return a.systemctl(ctx, "start", a.unit(site.ID)) })
}

// Stop stops the sites' units. systemd waits for the graceful drain
// (TimeoutStopSec in the unit).
func (a *App) Stop(ctx context.Context, sites []Site) error {
	return a.each(sites, func(site Site) error { return a.systemctl(ctx, "stop", a.unit(site.ID)) })
}

// each runs fn for every site and reports every failure, so one broken site
// does not hide the others.
func (a *App) each(sites []Site, fn func(Site) error) error {
	var errs []error
	for _, site := range sites {
		if err := fn(site); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", site.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Restart stops the sites, starts them and requires each to be healthy.
//
// Every site is stopped before any is started, as the deploy scripts always
// did. A site that fails is reported and the others carry on. A site that
// did not stop is not started: its old process may still hold the port and
// answer the health check. A healthy site is asked once more after a pause,
// because systemd restarts a binary that exits, so one that dies right after
// its first answer would otherwise pass (on 2026-10-23 every instance
// crash-looped on a missing setting while the deploy reported success).
func (a *App) Restart(ctx context.Context, sites []Site) error {
	var failed []string
	fail := func(site Site, format string, args ...any) {
		a.logf("  %-24s FAILED: %s", site.ID, fmt.Sprintf(format, args...))
		failed = append(failed, site.ID)
	}

	var stopped, started, healthy []Site
	for _, site := range sites {
		if err := a.systemctl(ctx, "stop", a.unit(site.ID)); err != nil {
			fail(site, "did not stop, left as it is: %v", err)
			continue
		}
		stopped = append(stopped, site)
	}
	for _, site := range stopped {
		if err := a.systemctl(ctx, "start", a.unit(site.ID)); err != nil {
			fail(site, "did not start: %v", err)
			continue
		}
		started = append(started, site)
	}
	for _, site := range started {
		if err := a.waitHealthy(ctx, site); err != nil {
			fail(site, "%v", err)
			a.showLogTail(ctx, site)
			continue
		}
		healthy = append(healthy, site)
	}
	if len(healthy) > 0 {
		if err := sleep(ctx, a.Settle); err != nil {
			return err
		}
	}
	for _, site := range healthy {
		if state := a.State(ctx, site); state != "active" {
			fail(site, "unit is %s after it answered: it keeps exiting", state)
			a.showLogTail(ctx, site)
		} else if err := a.Probe(ctx, site, a.HealthPath); err != nil {
			fail(site, "stopped answering after it started: %v", err)
			a.showLogTail(ctx, site)
		} else {
			a.logf("  %-24s OK", site.ID)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed: %s (see `%s logs <site>`)", strings.Join(failed, ", "), filepath.Base(a.ctl()))
	}
	return nil
}

// Health checks one site's health endpoint.
func (a *App) Health(ctx context.Context, site Site) error {
	if err := a.Probe(ctx, site, a.HealthPath); err != nil {
		return fmt.Errorf("%s: %w", site.ID, err)
	}
	a.logf("%s: OK", site.ID)
	return nil
}

// List prints the sites with their unit state and health.
func (a *App) List(ctx context.Context, sites []Site) error {
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SITE\tUSER\tPORT\tUNIT\tHEALTH\tDIRECTORY")
	for _, site := range sites {
		health := "ok"
		if err := a.Probe(ctx, site, a.HealthPath); err != nil {
			health = "down"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", site.ID, site.User, site.Port, a.State(ctx, site), health, site.Dir)
	}
	return w.Flush()
}

// Logs shows the last lines of the site's log: its log file when the unit
// writes one, the journal otherwise. errorsOnly shows the application's
// error log.
func (a *App) Logs(ctx context.Context, site Site, lines int, follow, errorsOnly bool) error {
	file := filepath.Join(site.Dir, "logs", a.Name+".log")
	if errorsOnly {
		file = filepath.Join(site.Dir, a.ErrorLog)
	}
	c := Cmd{Name: "tail", Args: []string{"-n", strconv.Itoa(lines), file}, Stdout: a.Stdout, Stderr: a.Stderr}
	if _, err := os.Stat(file); err != nil {
		if errorsOnly {
			return fmt.Errorf("no error log at %s", file)
		}
		c.Name, c.Args = "journalctl", []string{"-u", a.unit(site.ID), "-n", strconv.Itoa(lines), "--no-pager"}
	}
	if follow {
		c.Args = append(c.Args, "-f")
	}
	return a.Exec(ctx, c)
}

// showLogTail prints why a site failed, as far as its log says.
func (a *App) showLogTail(ctx context.Context, site Site) {
	if err := a.Logs(ctx, site, 5, false, false); err != nil {
		a.logf("  (no log: %v)", err)
	}
}
