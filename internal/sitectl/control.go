// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"bytes"
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
// active, activating, failed, inactive, ... The command exits non-zero for
// every state but active, so it has failed only when it prints none; its
// complaint then becomes the error instead of going to the terminal, which
// cron would mail on every run.
func (a *App) State(ctx context.Context, site Site) (string, error) {
	var out, stderr bytes.Buffer
	c := Cmd{Name: "systemctl", Args: []string{"is-active", a.unit(site.ID)}, Stdout: &out, Stderr: &stderr}
	err := a.Exec(ctx, c)
	if state := strings.TrimSpace(out.String()); state != "" {
		return state, nil
	}
	if err == nil {
		err = errors.New("printed no state")
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		err = fmt.Errorf("%w: %s", err, msg)
	}
	return "", fmt.Errorf("%s: %w", c, err)
}

// condition is what one look at a site finds.
type condition int

const (
	siteUp       condition = iota // the unit is active and the site answers
	siteDown                      // it should serve and does not, or its unit state cannot be read
	siteStopped                   // inactive: stopped on purpose or never started
	siteChanging                  // stopping or reloading on request: judged on the next look
)

func (c condition) String() string {
	return [...]string{"ok", "down", "stopped", "changing"}[c]
}

// look is one look at a site.
type look struct {
	unit    string // the unit state; "unknown" when it cannot be read
	cond    condition
	reason  string // why the site is down
	restart bool   // the unit runs but the site does not answer: a restart may help
}

// lookAt reads the site's unit state and, while the unit is active, asks the
// site for path. Check and List both judge sites with it, so they agree on
// what down means. A state it does not know is down, not skipped: a monitor
// that skips what it cannot read stays silent while the site is gone.
func (a *App) lookAt(ctx context.Context, site Site, path string) look {
	state, err := a.State(ctx, site)
	switch {
	case err != nil:
		return look{unit: "unknown", cond: siteDown, reason: fmt.Sprintf("cannot read its unit state: %v", err)}
	case state == "active":
		if err := a.Probe(ctx, site, path); err != nil {
			return look{unit: state, cond: siteDown, reason: fmt.Sprintf("not answering %s: %v", path, err), restart: true}
		}
		return look{unit: state, cond: siteUp}
	case state == "activating":
		return look{unit: state, cond: siteDown, reason: "systemd keeps restarting it: it exits at startup"}
	case state == "failed":
		return look{unit: state, cond: siteDown, reason: "unit failed; not restarted, it may have been stopped on purpose"}
	case state == "inactive":
		return look{unit: state, cond: siteStopped}
	case state == "deactivating", state == "reloading", state == "refreshing", state == "maintenance":
		return look{unit: state, cond: siteChanging}
	}
	return look{unit: state, cond: siteDown, reason: fmt.Sprintf("unexpected unit state %q", state)}
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
		if state, err := a.State(ctx, site); err == nil && (state == "failed" || state == "inactive") {
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
// Every site is stopped before any is started. A site that fails is reported
// and the others carry on. A site that did not stop is not started: its old process may still hold the port and
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
		if state, err := a.State(ctx, site); err != nil {
			fail(site, "%v", err)
		} else if state != "active" {
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

// List prints the sites with their unit state and health, then why each
// site that is down is down. It returns ErrSitesDown, after printing, while a
// site is down as Check judges it; a stopped site is not down.
func (a *App) List(ctx context.Context, sites []Site) error {
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SITE\tUSER\tPORT\tUNIT\tHEALTH\tDIRECTORY")
	var down, reasons []string
	for _, site := range sites {
		l := a.lookAt(ctx, site, a.HealthPath)
		if l.cond == siteDown {
			down = append(down, site.ID)
			reasons = append(reasons, site.ID+": "+l.reason)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", site.ID, site.User, site.Port, l.unit, l.cond, site.Dir)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, reason := range reasons {
		a.logf("%s", reason)
	}
	if len(down) > 0 {
		return fmt.Errorf("%w: %s", ErrSitesDown, strings.Join(down, ", "))
	}
	return nil
}

// Logs shows the last lines of the site's log: its log file when the unit
// writes one, the journal otherwise. errorsOnly shows the error log the
// site's configuration names.
func (a *App) Logs(ctx context.Context, site Site, lines int, follow, errorsOnly bool) error {
	var file string
	if errorsOnly {
		var out bytes.Buffer
		if err := a.asSiteUser(ctx, site, "paths", nil, &out); err != nil {
			return err
		}
		paths, err := a.parsePaths(out.String())
		if err != nil {
			return err
		}
		if paths.ErrorLog == "" {
			return fmt.Errorf("%s keeps no error log (%s is not set)", site.ID, a.ErrorLog.Env)
		}
		file = paths.ErrorLog
	} else {
		target, err := a.output(ctx, "systemctl", "show", "-p", "StandardOutput", "--value", a.unit(site.ID))
		if err != nil {
			return fmt.Errorf("log destination for %s: %w", site.ID, err)
		}
		kind, path, _ := strings.Cut(target, ":")
		switch kind {
		case "append", "file", "truncate":
			file = path
		default:
			args := []string{"-u", a.unit(site.ID), "-n", strconv.Itoa(lines), "--no-pager"}
			if follow {
				args = append(args, "-f")
			}
			return a.run(ctx, "journalctl", args...)
		}
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
