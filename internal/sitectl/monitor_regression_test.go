// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// writeHook installs the alert hook with mode and returns its path.
func writeHook(t *testing.T, env *testEnv, mode os.FileMode) string {
	t.Helper()
	hook := filepath.Join(env.ConfDir, "alert")
	writeFiles(t, env.ConfDir, map[string]string{"alert": "#!/bin/sh\n"})
	if err := os.Chmod(hook, mode); err != nil {
		t.Fatal(err)
	}
	return hook
}

func runCheck(env *testEnv, pick func() ([]Site, error)) (string, error) {
	env.out.Reset()
	err := env.Check(context.Background(), pick)
	return env.out.String(), err
}

// hookCalls returns the recorded calls of hook whose arguments start with
// prefix.
func hookCalls(env *testEnv, hook, prefix string) []string {
	var calls []string
	for _, call := range env.host.calls {
		if rest, ok := strings.CutPrefix(call, hook+" "); ok && strings.HasPrefix(rest, prefix) {
			calls = append(calls, rest)
		}
	}
	return calls
}

func TestCheckReportsAUnitStateItCannotRead(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	hook := writeHook(t, env, 0o755)
	busDown := false
	env.Exec = func(ctx context.Context, c Cmd) error {
		if busDown && c.String() == "systemctl is-active app@a" {
			_, _ = fmt.Fprintln(c.Stderr, "Failed to connect to bus")
			return errors.New("exit status 1")
		}
		return env.host.exec(ctx, c)
	}

	busDown = true
	out, err := runCheck(env, sitesOf(site))
	if !errors.Is(err, ErrSitesDown) || !strings.Contains(out, "DOWN") || !strings.Contains(out, "cannot read its unit state") || !strings.Contains(out, "Failed to connect to bus") {
		t.Fatalf("unreadable state: err = %v, output:\n%s", err, out)
	}
	if out, err := runCheck(env, sitesOf(site)); !errors.Is(err, ErrSitesDown) || out != "" {
		t.Errorf("second run: err = %v, output %q; want down and quiet", err, out)
	}
	busDown = false
	if out, err := runCheck(env, sitesOf(site)); err != nil || !strings.Contains(out, "recovered") {
		t.Errorf("readable again: err = %v, output %q", err, out)
	}

	// An outage that began before the fault is still the same outage.
	health.set(false)
	if out, _ := runCheck(env, sitesOf(site)); !strings.Contains(out, "restarting (1/3)") {
		t.Fatalf("outage did not begin:\n%s", out)
	}
	busDown = true
	if out, err := runCheck(env, sitesOf(site)); !errors.Is(err, ErrSitesDown) || out != "" {
		t.Errorf("fault during an outage: err = %v, output %q; want down and quiet", err, out)
	}
	busDown = false
	env.host.set("app@a", "active")
	if out, _ := runCheck(env, sitesOf(site)); strings.Contains(out, "DOWN") || !strings.Contains(out, "restarting (2/3)") {
		t.Errorf("the fault reset the outage:\n%s", out)
	}
	if got := hookCalls(env, hook, "a DOWN"); len(got) != 2 {
		t.Errorf("hook DOWN calls = %v, want one per outage", got)
	}

	env.host.set("app@a", "bogus")
	if out, err := runCheck(env, sitesOf(site)); !errors.Is(err, ErrSitesDown) || !strings.HasSuffix(err.Error(), ": a") {
		t.Errorf("unexpected state text: err = %v, output %q", err, out)
	}
}

func TestCheckForgetsAnOutageOnlyWhenTheUnitStops(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	health.set(false)
	if out, err := runCheck(env, sitesOf(site)); !errors.Is(err, ErrSitesDown) || !strings.Contains(out, "DOWN") {
		t.Fatalf("outage: %v\n%s", err, out)
	}
	env.host.set("app@a", "deactivating")
	if out, err := runCheck(env, sitesOf(site)); !errors.Is(err, ErrSitesDown) || out != "" {
		t.Errorf("deactivating: err = %v, output %q; want the outage kept, quietly", err, out)
	}
	env.host.set("app@a", "inactive")
	if out, err := runCheck(env, sitesOf(site)); err != nil || out != "" {
		t.Errorf("stopped: err = %v, output %q; want quiet", err, out)
	}
	env.host.set("app@a", "active")
	if out, _ := runCheck(env, sitesOf(site)); !strings.Contains(out, "DOWN") {
		t.Errorf("an outage after a stop is a new one:\n%s", out)
	}
}

func TestStatusFailsWhileASiteIsDown(t *testing.T) {
	env := newTestEnv(t)
	up, _ := env.addSite(t, "up")
	down, health := env.addSite(t, "down")
	stopped, _ := env.addSite(t, "stopped")
	env.host.set("app@stopped", "inactive")

	if err := env.List(context.Background(), []Site{up, stopped}); err != nil {
		t.Errorf("a stopped site failed status: %v", err)
	}
	health.set(false)
	env.out.Reset()
	err := env.List(context.Background(), []Site{up, down, stopped})
	if !errors.Is(err, ErrSitesDown) || !strings.HasSuffix(err.Error(), ": down") {
		t.Errorf("err = %v, want only the down site", err)
	}
	if !strings.Contains(env.out.String(), "down: not answering /health: HTTP 503") {
		t.Errorf("status did not say why:\n%s", env.out.String())
	}
}

func TestCommandsForEverySiteNeedARegistry(t *testing.T) {
	env := newTestEnv(t)
	hook := writeHook(t, env, 0o755)
	pickAll := func() ([]Site, error) { return env.Pick(nil, true) }

	out, err := runCheck(env, pickAll)
	if !errors.Is(err, ErrReported) || !strings.Contains(out, "MONITOR FAILED") || !strings.Contains(out, "init") {
		t.Fatalf("no registry: err = %v, output:\n%s", err, out)
	}
	if out, err := runCheck(env, pickAll); !errors.Is(err, ErrReported) || out != "" {
		t.Errorf("second run: err = %v, output %q; want failed and quiet", err, out)
	}
	if got := hookCalls(env, hook, ServerSite+" MONITOR FAILED "); len(got) != 1 {
		t.Errorf("hook calls = %v, want one", got)
	}
	if err := env.Backup(context.Background(), pickAll); err == nil {
		t.Error("backup --all without a registry succeeded")
	}
	if got := hookCalls(env, hook, ServerSite+" BACKUP FAILED "); len(got) != 1 {
		t.Errorf("hook calls = %v, want one", got)
	}

	if err := env.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(env.registry()); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("init made no readable registry: %v", err)
	}
	if out, err := runCheck(env, pickAll); err != nil || !strings.Contains(out, "recovered") {
		t.Errorf("empty registry: err = %v, output %q; want the failure over", err, out)
	}
	if out, err := runCheck(env, pickAll); err != nil || out != "" {
		t.Errorf("empty registry: err = %v, output %q; want quiet", err, out)
	}
	env.addSite(t, "a")
	if err := env.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sites, err := pickAll(); err != nil || len(sites) != 1 {
		t.Errorf("init replaced the registry: %v, %v", sites, err)
	}
}

func TestCheckReportsItsOwnFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, env *testEnv) // runs after one good check
		once  bool                             // the state remembers the report
	}{
		{"malformed registry", func(t *testing.T, env *testEnv) {
			writeFiles(t, env.ConfDir, map[string]string{"sites.conf": "a /x\n"})
		}, true},
		{"state others can write", func(t *testing.T, env *testEnv) {
			if err := os.Chmod(env.StateDir, 0o777); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"state that cannot be read", func(t *testing.T, env *testEnv) {
			if err := os.Remove(env.stateFile()); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(env.stateFile(), 0o700); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"state that cannot be saved", func(t *testing.T, env *testEnv) {
			skipAsRoot(t)
			if err := os.Chmod(env.StateDir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(env.StateDir, 0o700) })
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.addSite(t, "a")
			hook := writeHook(t, env, 0o755)
			pickAll := func() ([]Site, error) { return env.Pick(nil, true) }
			if _, err := runCheck(env, pickAll); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, env)
			for run := 1; run <= 2; run++ {
				out, err := runCheck(env, pickAll)
				if !errors.Is(err, ErrReported) {
					t.Fatalf("run %d: err = %v, want a reported failure", run, err)
				}
				if quiet := tc.once && run == 2; quiet != (out == "") {
					t.Errorf("run %d: output %q", run, out)
				}
			}
			want := 2
			if tc.once {
				want = 1
			}
			if got := hookCalls(env, hook, ServerSite+" MONITOR FAILED "); len(got) != want {
				t.Errorf("hook calls = %v, want %d", got, want)
			}
		})
	}
}

func TestCheckSetsAsideStateItCannotParse(t *testing.T) {
	for _, content := range []string{"{not json", "null", `{"outages":null}`} {
		t.Run(content, func(t *testing.T) {
			env := newTestEnv(t)
			site, health := env.addSite(t, "a")
			hook := writeHook(t, env, 0o755)
			writeFiles(t, env.StateDir, map[string]string{"monitor.json": content})
			if err := os.Chmod(env.StateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			health.set(false)
			out, err := runCheck(env, sitesOf(site))
			if !errors.Is(err, ErrSitesDown) || !strings.Contains(out, "DOWN") {
				t.Fatalf("err = %v, output:\n%s", err, out)
			}
			corrupt := content == "{not json"
			if got := hookCalls(env, hook, ServerSite+" MONITOR FAILED "); (len(got) == 1) != corrupt {
				t.Errorf("hook calls = %v", got)
			}
			if _, err := os.Stat(env.stateFile() + ".corrupt"); (err == nil) != corrupt {
				t.Errorf("set aside: %v", err)
			}
		})
	}
}

func TestAlertsTheHookDoesNotTakeAreRetried(t *testing.T) {
	env := newTestEnv(t)
	a, healthA := env.addSite(t, "a")
	b, healthB := env.addSite(t, "b")
	hook := writeHook(t, env, 0o644) // there, but it cannot run
	healthA.set(false)
	healthB.set(false)

	out, err := runCheck(env, sitesOf(a, b))
	if !errors.Is(err, ErrSitesDown) || strings.Count(out, "DOWN") != 2 || !strings.Contains(out, "not an executable file") {
		t.Fatalf("err = %v, output:\n%s", err, out)
	}
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, _ := runCheck(env, sitesOf(a, b)); strings.Contains(out, "DOWN") {
		t.Errorf("the retry printed the alerts again:\n%s", out)
	}
	got := hookCalls(env, hook, "")
	if len(got) != 2 || !strings.HasPrefix(got[0], "a DOWN") || !strings.HasPrefix(got[1], "b DOWN") {
		t.Errorf("hook calls = %v, want both outages in order", got)
	}
	if _, err := runCheck(env, sitesOf(a, b)); !errors.Is(err, ErrSitesDown) || len(hookCalls(env, hook, "")) != 2 {
		t.Errorf("delivered alerts were sent again: %v", hookCalls(env, hook, ""))
	}
}

func TestTheAlertHookHasATimeLimit(t *testing.T) {
	env := newTestEnv(t)
	a, healthA := env.addSite(t, "a")
	b, healthB := env.addSite(t, "b")
	hook := writeHook(t, env, 0o755)
	writeFiles(t, env.ConfDir, map[string]string{"alert": "#!/bin/sh\nexec sleep 30\n"})
	defer func(old time.Duration) { hookTimeout = old }(hookTimeout)
	hookTimeout = 200 * time.Millisecond
	tries := 0
	env.Exec = func(ctx context.Context, c Cmd) error {
		if c.Name == hook {
			tries++
			return RunCmd(ctx, c)
		}
		return env.host.exec(ctx, c)
	}
	healthA.set(false)
	healthB.set(false)

	start := time.Now()
	out, err := runCheck(env, sitesOf(a, b))
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("a hanging hook held the check for %s", took)
	}
	if !errors.Is(err, ErrSitesDown) || !strings.Contains(out, "next check tries again") {
		t.Errorf("err = %v, output:\n%s", err, out)
	}
	if tries != 1 {
		t.Errorf("the hook was tried %d times in one run, want 1", tries)
	}
}

func TestOnlyOneCheckRunsAtATime(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	health.set(false)
	unlock, err := env.lockMonitor(false)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := runCheck(env, sitesOf(site)); err != nil || out != "" {
		t.Errorf("while another check runs: err = %v, output %q; want it to leave", err, out)
	}
	unlock()
	if out, _ := runCheck(env, sitesOf(site)); !strings.Contains(out, "DOWN") {
		t.Errorf("after the other check:\n%s", out)
	}
}

func TestCheckStopsLookingWhenItsTimeRunsOut(t *testing.T) {
	env := newTestEnv(t)
	slow, health := env.addSite(t, "slow")
	later, _ := env.addSite(t, "later")
	health.onProbe = func() { time.Sleep(time.Second) }
	defer func(old time.Duration) { checkBudget = old }(checkBudget)
	checkBudget = 100 * time.Millisecond

	out, err := runCheck(env, sitesOf(slow, later))
	if !errors.Is(err, ErrReported) || !strings.Contains(out, "ran out of time") {
		t.Fatalf("err = %v, output:\n%s", err, out)
	}
	if strings.Contains(out, " DOWN ") || slices.Contains(env.host.systemctlCalls(), "restart app@slow") {
		t.Errorf("the deadline was taken for an outage:\n%s", out)
	}
}

func TestBackupAlertsWaitForTheNextCheck(t *testing.T) {
	env, site, _ := backupEnv(t)
	hook := filepath.Join(env.ConfDir, "alert")
	if err := os.Chmod(hook, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(site.Dir, env.DB.Default)); err != nil {
		t.Fatal(err)
	}
	if err := env.Backup(context.Background(), sitesOf(site)); err == nil {
		t.Fatal("backup without a database succeeded")
	}
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runCheck(env, sitesOf(site)); err != nil {
		t.Fatal(err)
	}
	if got := hookCalls(env, hook, "a BACKUP FAILED "); len(got) != 1 {
		t.Errorf("hook calls = %v, want the backup failure once", hookCalls(env, hook, ""))
	}
}
