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
	"time"
)

const (
	maxRestarts = 3  // per outage; the count resets once the site is healthy
	maxPending  = 20 // undelivered alerts kept for the next check: the newest
	// ServerSite is the site the alert hook is given for a failure of the
	// tool itself (the registry, the monitor's state) rather than a site's.
	// No site ID starts with an underscore.
	ServerSite = "_server"
)

// Variables for tests.
var (
	// checkBudget bounds a check's look at the sites: cron starts one every
	// five minutes, and a run still holding the lock makes the next one
	// skip. Alerts get their own time.
	checkBudget = 3 * time.Minute
	// hookTimeout bounds one call of the alert hook.
	hookTimeout = 30 * time.Second
)

var (
	// ErrSitesDown is what Check and List return while a site is down. The
	// outage was already printed, so the caller exits non-zero without
	// printing it again: cron would otherwise mail on every run.
	ErrSitesDown = errors.New("sites are down")
	// ErrReported marks a failure that was already printed and passed to the
	// alert hook; like ErrSitesDown, it is not printed again.
	ErrReported = errors.New("already reported")

	errCheckRunning = errors.New("another check is running")
	// errNoHook is a delivery without a hook to deliver to: what the run
	// prints is all there is (check's output reaches cron mail).
	errNoHook = errors.New("no alert hook")
)

// outage is what the monitor remembers about a site that is down.
type outage struct {
	Restarts  int    `json:"restarts"`
	GaveUp    bool   `json:"gave_up"`
	LastError string `json:"last_error,omitempty"` // why the last restart failed
}

// hookCall is one event for the alert hook.
type hookCall struct {
	Site    string `json:"site"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Mailed  bool   `json:"mailed,omitempty"` // printed where cron mails it (by a check)
}

// monitorState is what the checks remember between runs.
type monitorState struct {
	Outages    map[string]outage `json:"outages"`
	Failed     string            `json:"failed,omitempty"`      // the check's own failure, already reported
	HookFailed string            `json:"hook_failed,omitempty"` // why the hook fails, already reported
	Pending    []hookCall        `json:"pending,omitempty"`     // alerts the hook has not taken, oldest first
}

// Check is the cron monitor. pick selects the sites; for every one it looks
// at the unit state and the readiness endpoint (lookAt):
//
//	active, ready         healthy; a previous outage is reported as over
//	active, not ready     down; restarted, at most maxRestarts times per outage
//	activating            down: it keeps exiting and systemd keeps restarting it
//	failed                down; not restarted, it may have been stopped on purpose
//	state unreadable      down; what systemctl said is the reason
//	inactive              stopped; a previous outage is forgotten
//	deactivating, ...     changing; judged on the next run
//
// It prints, and calls the alert hook, only when a site goes down, when its
// restarts run out and when it recovers, so cron mail stays quiet during a
// long outage. A failure of the check itself is reported the same way, once,
// as site ServerSite. Without a hook it prints the alerts backups left for
// it; with one that keeps failing it says so once. It returns ErrSitesDown
// while any site is down.
func (a *App) Check(ctx context.Context, pick func() ([]Site, error)) error {
	s := &sender{a: a, mailed: true}
	unlock, err := a.lockMonitor(false)
	if errors.Is(err, errCheckRunning) {
		return nil // that run reports what it finds, and checkBudget ends it
	}
	if err != nil {
		return a.checkFailed(ctx, nil, s, err)
	}
	defer unlock()
	st, err := a.loadMonitor(true)
	if err != nil {
		return a.checkFailed(ctx, nil, s, err)
	}
	s.retry(ctx, st.Pending)
	down, err := a.checkSites(ctx, pick, st, s)
	if err != nil {
		err = a.checkFailed(ctx, st, s, err)
	} else if st.Failed != "" {
		st.Failed = ""
		s.send(ctx, a.serverEvent("recovered", "the check runs again"))
	}
	st.Pending = a.trimPending(s.pending)
	a.reportHook(st, s)
	if saveErr := a.saveMonitor(st); saveErr != nil {
		return a.checkFailed(ctx, nil, s, saveErr)
	}
	if err != nil {
		return err
	}
	if len(down) > 0 {
		return fmt.Errorf("%w: %s", ErrSitesDown, strings.Join(down, ", "))
	}
	return nil
}

// checkSites looks at the sites pick selects, records what it finds in st
// and returns the sites that are down.
func (a *App) checkSites(ctx context.Context, pick func() ([]Site, error), st *monitorState, s *sender) ([]string, error) {
	sites, err := pick()
	if err != nil {
		return nil, err
	}
	lookCtx, cancel := context.WithTimeout(ctx, checkBudget)
	defer cancel()
	var down []string
	for i, site := range sites {
		l := a.lookAt(lookCtx, site, a.ReadyPath)
		if lookCtx.Err() != nil {
			// The deadline may be what this look saw: keep what is known
			// about this site and the rest.
			for _, rest := range sites[i:] {
				if _, ok := st.Outages[rest.ID]; ok {
					down = append(down, rest.ID)
				}
			}
			if ctx.Err() != nil {
				return down, ctx.Err()
			}
			return down, fmt.Errorf("the check ran out of time (%s) at %s", checkBudget, site.ID)
		}
		out, wasDown := st.Outages[site.ID]
		switch l.cond {
		case siteStopped:
			delete(st.Outages, site.ID)
			continue
		case siteChanging:
			if wasDown {
				down = append(down, site.ID)
			}
			continue
		case siteUp:
			if wasDown {
				s.send(ctx, a.siteEvent(site, "recovered", "healthy again"))
				delete(st.Outages, site.ID)
			}
			continue
		}
		if !wasDown {
			s.send(ctx, a.siteEvent(site, "DOWN", l.reason))
		}
		if l.restart && out.Restarts < maxRestarts {
			out.Restarts++
			a.logf("%s: restarting (%d/%d)", site.ID, out.Restarts, maxRestarts)
			out.LastError = ""
			if err := a.systemctl(lookCtx, "restart", a.unit(site.ID)); err != nil {
				a.logf("%s: %v", site.ID, err)
				out.LastError = err.Error()
			}
		} else if l.restart && !out.GaveUp {
			out.GaveUp = true
			message := fmt.Sprintf("still down after %d restart attempts", maxRestarts)
			if out.LastError != "" {
				message += "; the last one failed: " + out.LastError
			}
			s.send(ctx, a.siteEvent(site, "NEEDS ATTENTION", message))
		}
		st.Outages[site.ID] = out
		down = append(down, site.ID)
	}
	return down, nil
}

// checkFailed reports a failure of the check itself as site ServerSite: once
// while st remembers it, on every run when there is no state to remember it
// in.
func (a *App) checkFailed(ctx context.Context, st *monitorState, s *sender, cause error) error {
	if st == nil || st.Failed != cause.Error() {
		s.send(ctx, a.serverEvent("MONITOR FAILED", cause.Error()))
		if st != nil {
			st.Failed = cause.Error()
		}
	}
	return fmt.Errorf("%w: %w", ErrReported, cause)
}

// sender passes events to the alert hook <ConfDir>/alert, called as:
// alert <site> <status> <message>. Once the hook fails, the run's later
// events wait for the next check without trying it, so a hook that hangs
// costs one hookTimeout per run.
type sender struct {
	a         *App
	mailed    bool       // what this run prints reaches cron mail (check, not backup)
	failed    error      // why the hook failed in this run
	delivered bool       // the hook took an event in this run
	noHook    bool       // there was no hook to try
	pending   []hookCall // events the hook has not taken
}

// send prints the event and passes it to the hook. An event the hook does
// not take, or that has no hook to go to, waits for the next check, unless
// this run's output already reaches cron mail and there is no hook.
func (s *sender) send(ctx context.Context, c hookCall) {
	s.a.announce(c)
	c.Mailed = s.mailed
	switch err := s.deliver(ctx, c); {
	case errors.Is(err, errNoHook) && !s.mailed:
		s.pending = append(s.pending, c)
		s.a.logf("  no alert hook: the next check prints this for cron mail")
	case err != nil && !errors.Is(err, errNoHook):
		s.a.logf("  alert hook: %v; the next check tries again", err)
	}
}

// retry, in a check, passes the events earlier runs left to the hook again.
// An event not yet printed where cron mails it (a backup's) is printed now,
// whatever the hook does; one already printed is not printed again.
func (s *sender) retry(ctx context.Context, calls []hookCall) {
	for _, c := range calls {
		err := s.deliver(ctx, c)
		if err == nil || c.Mailed {
			continue
		}
		s.a.announce(c)
		if !errors.Is(err, errNoHook) {
			s.pending[len(s.pending)-1].Mailed = true
		}
	}
}

// deliver passes c to the hook. What a failing hook does not take waits in
// pending; errNoHook leaves c to the caller.
func (s *sender) deliver(ctx context.Context, c hookCall) error {
	if s.failed != nil {
		s.pending = append(s.pending, c)
		return s.failed
	}
	err := s.a.callHook(ctx, c)
	switch {
	case err == nil:
		s.delivered = true
	case errors.Is(err, errNoHook):
		s.noHook = true
	default:
		s.failed = err
		s.pending = append(s.pending, c)
	}
	return err
}

// reportHook says once, for cron mail, that the hook fails and how many
// alerts wait for it, and once that it delivers again; the tries in between
// are quiet. A removed hook ends the failure without a word.
func (a *App) reportHook(st *monitorState, s *sender) {
	switch {
	case s.failed != nil && st.HookFailed != s.failed.Error():
		st.HookFailed = s.failed.Error()
		a.logf("alert hook: %v; %d alerts wait for it", s.failed, len(st.Pending))
	case s.failed == nil && s.delivered && st.HookFailed != "":
		st.HookFailed = ""
		a.logf("alert hook: delivers again")
	case s.failed == nil && s.noHook:
		st.HookFailed = ""
	}
}

func (a *App) announce(c hookCall) { a.logf("%s %s", c.Status, c.Message) }

// callHook runs the hook for one event: errNoHook without one, and a
// failed delivery when it is there but cannot run. The hook gets its own
// process group, all of it ended at hookTimeout: a hung curl it started
// must not outlive it.
func (a *App) callHook(ctx context.Context, c hookCall) error {
	hook := filepath.Join(a.ConfDir, "alert")
	info, err := os.Stat(hook)
	if errors.Is(err, os.ErrNotExist) {
		return errNoHook
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("%s is not an executable file", hook)
	}
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	if err := a.Exec(ctx, Cmd{Name: hook, Args: []string{c.Site, c.Status, c.Message}, Stdout: a.Stdout, Stderr: a.Stderr, Group: true}); err != nil {
		return fmt.Errorf("%s: %w", hook, err)
	}
	return nil
}

func (a *App) siteEvent(site Site, status, message string) hookCall {
	return hookCall{Site: site.ID, Status: status, Message: fmt.Sprintf("%s on %s (port %d): %s", a.unit(site.ID), hostname(), site.Port, message)}
}

func (a *App) serverEvent(status, message string) hookCall {
	return hookCall{Site: ServerSite, Status: status, Message: fmt.Sprintf("%s on %s: %s", filepath.Base(a.ctl()), hostname(), message)}
}

func hostname() string {
	host, err := os.Hostname()
	if err != nil {
		return "this server"
	}
	return host
}

// trimPending keeps maxPending undelivered events. A site's newest event is
// where it stands now, so the older events of a site go first, oldest
// first; only then the oldest of the rest. The hook then never hears that a
// site went down without hearing that it recovered.
func (a *App) trimPending(calls []hookCall) []hookCall {
	n := len(calls) - maxPending
	if n <= 0 {
		return calls
	}
	a.logf("alert hook: %d undelivered alerts dropped", n)
	newest := map[string]int{} // site -> index of its newest event
	for i, c := range calls {
		newest[c.Site] = i
	}
	dropped := map[int]bool{}
	for i, c := range calls {
		if n > 0 && newest[c.Site] != i {
			dropped[i] = true
			n--
		}
	}
	for i := range calls {
		if n > 0 && !dropped[i] {
			dropped[i] = true
			n--
		}
	}
	kept := make([]hookCall, 0, maxPending)
	for i, c := range calls {
		if !dropped[i] {
			kept = append(kept, c)
		}
	}
	return kept
}

// keepAlerts hands events the hook did not take to the next check.
func (a *App) keepAlerts(calls []hookCall) error {
	unlock, err := a.lockMonitor(true)
	if err != nil {
		return err
	}
	defer unlock()
	st, err := a.loadMonitor(false)
	if err != nil {
		return err
	}
	st.Pending = a.trimPending(append(st.Pending, calls...))
	return a.saveMonitor(st)
}

// stateDir makes the monitor state directory. It must belong to the caller
// alone: state any local user could write would let them silence a site's
// alerts and restarts.
func (a *App) stateDir() error {
	if err := os.MkdirAll(a.StateDir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(a.StateDir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s must be a directory only %d can write", a.StateDir, os.Geteuid())
	}
	return nil
}

// lockMonitor takes <StateDir>/monitor.lock, so two runs cannot overwrite
// each other's state. Without wait it returns errCheckRunning while another
// run holds the lock.
func (a *App) lockMonitor(wait bool) (unlock func(), err error) {
	if err := a.stateDir(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(a.StateDir, "monitor.lock"), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errCheckRunning
		}
		return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
	}
	return func() { _ = f.Close() }, nil // closing the file releases the lock
}

func (a *App) stateFile() string { return filepath.Join(a.StateDir, "monitor.json") }

// loadMonitor reads the monitor state. A file that does not parse is moved
// to monitor.json.corrupt, replacing an earlier one, and reported: what it
// held is forgotten, so outages already reported are reported again.
func (a *App) loadMonitor(mailed bool) (*monitorState, error) {
	st := &monitorState{}
	data, err := os.ReadFile(a.stateFile())
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if parseErr := json.Unmarshal(data, st); parseErr != nil {
			aside := a.stateFile() + ".corrupt"
			if err := os.Rename(a.stateFile(), aside); err != nil {
				return nil, fmt.Errorf("%s does not parse (%v) and cannot be moved aside: %w", a.stateFile(), parseErr, err)
			}
			notice := a.serverEvent("MONITOR FAILED", fmt.Sprintf("%s did not parse (%v); moved to %s, so outages already reported will be reported again", a.stateFile(), parseErr, aside))
			a.announce(notice)
			notice.Mailed = mailed // a check printed it for cron mail already
			st = &monitorState{Pending: []hookCall{notice}}
		}
	}
	if st.Outages == nil { // a new file, or one that holds null
		st.Outages = map[string]outage{}
	}
	return st, nil
}

func (a *App) saveMonitor(st *monitorState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeFileAtomic(a.stateFile(), data, 0o600)
}
