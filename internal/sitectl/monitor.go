// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maxRestarts = 3 // per outage; the count resets once the site is healthy

// ErrSitesDown is what Check returns while a site is down. The outage was
// already reported when it began, so the caller exits non-zero without
// printing: cron would otherwise mail on every run.
var ErrSitesDown = errors.New("sites are down")

// outage is what the monitor remembers about a site that is down.
type outage struct {
	Restarts int  `json:"restarts"`
	GaveUp   bool `json:"gave_up"`
}

// Check is the cron monitor. For every site it looks at the unit state and
// the readiness endpoint:
//
//	active, ready      healthy; a previous outage is reported as over
//	active, not ready  down; restarted, at most maxRestarts times per outage
//	activating         down: it keeps exiting and systemd keeps restarting it
//	failed             down; not restarted, it may have been stopped on purpose
//	anything else      stopped; skipped
//
// It prints, and calls the alert hook, only when a site goes down, when its
// restarts run out and when it recovers, so cron mail stays quiet during a
// long outage. It returns ErrSitesDown while any site is down.
func (a *App) Check(ctx context.Context, sites []Site) error {
	state, err := a.loadOutages()
	if err != nil {
		return err
	}
	var down []string
	for _, site := range sites {
		reason, restart := "", false
		switch a.State(ctx, site) {
		case "active":
			if err := a.Probe(ctx, site, a.ReadyPath); err != nil {
				reason, restart = fmt.Sprintf("not ready: %v", err), true
			}
		case "activating":
			reason = "systemd keeps restarting it: it exits at startup"
		case "failed":
			reason = "unit failed; not restarted, it may have been stopped on purpose"
		default:
			delete(state, site.ID)
			continue
		}

		out, wasDown := state[site.ID]
		if reason == "" {
			if wasDown {
				a.alert(ctx, site, "recovered", "healthy again")
				delete(state, site.ID)
			}
			continue
		}
		if !wasDown {
			a.alert(ctx, site, "DOWN", reason)
		}
		if restart && out.Restarts < maxRestarts {
			out.Restarts++
			a.logf("%s: restarting (%d/%d)", site.ID, out.Restarts, maxRestarts)
			if err := a.systemctl(ctx, "restart", a.unit(site.ID)); err != nil {
				a.logf("%s: %v", site.ID, err)
			}
		} else if restart && !out.GaveUp {
			out.GaveUp = true
			a.alert(ctx, site, "NEEDS ATTENTION", fmt.Sprintf("still down after %d restarts", maxRestarts))
		}
		state[site.ID] = out
		down = append(down, site.ID)
	}
	if err := a.saveOutages(state); err != nil {
		return err
	}
	if len(down) > 0 {
		return fmt.Errorf("%w: %s", ErrSitesDown, strings.Join(down, ", "))
	}
	return nil
}

// alert prints the event and passes it to the optional hook
// <ConfDir>/alert, called as: alert <site> <status> <message>.
func (a *App) alert(ctx context.Context, site Site, status, message string) {
	host, _ := os.Hostname()
	message = fmt.Sprintf("%s on %s (port %d): %s", a.unit(site.ID), host, site.Port, message)
	a.logf("%s %s", status, message)
	hook := filepath.Join(a.ConfDir, "alert")
	if info, err := os.Stat(hook); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return
	}
	if err := a.run(ctx, hook, site.ID, status, message); err != nil {
		a.logf("alert hook: %v", err)
	}
}

func (a *App) outagesFile() string { return filepath.Join(a.StateDir, "outages.json") }

// loadOutages reads the monitor state. The directory must belong to the
// caller alone: state any local user could write would let them silence a
// site's alerts and restarts.
func (a *App) loadOutages() (map[string]outage, error) {
	if err := os.MkdirAll(a.StateDir, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(a.StateDir)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("%s must be a directory only %d can write", a.StateDir, os.Geteuid())
	}
	state := map[string]outage{}
	data, err := os.ReadFile(a.outagesFile())
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return map[string]outage{}, nil // unreadable state is an empty one
	}
	return state, nil
}

func (a *App) saveOutages(state map[string]outage) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeFileAtomic(a.outagesFile(), data, 0o600)
}
