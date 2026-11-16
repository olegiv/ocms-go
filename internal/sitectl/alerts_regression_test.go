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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Cron writes a backup's output to a log nobody reads. Without an alert
// hook, its failures wait for the next check, whose output cron mails.
func TestBackupFailuresWithoutAHookReachTheNextCheck(t *testing.T) {
	env, site, _ := backupEnv(t)
	if err := os.Remove(filepath.Join(env.ConfDir, "alert")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(site.Dir, env.DB.Default)); err != nil {
		t.Fatal(err)
	}
	env.out.Reset()
	if err := env.Backup(context.Background(), sitesOf(site)); err == nil {
		t.Fatal("backup without a database succeeded")
	}
	if !strings.Contains(env.out.String(), "the next check prints this") {
		t.Errorf("backup output:\n%s", env.out)
	}
	out, err := runCheck(env, sitesOf(site))
	if err != nil || !strings.Contains(out, "BACKUP FAILED") || !strings.Contains(out, "database") {
		t.Errorf("check = %v, output:\n%s", err, out)
	}
	if out, _ := runCheck(env, sitesOf(site)); out != "" {
		t.Errorf("the next check printed again:\n%s", out)
	}
}

// A hook that keeps failing is said once, with the alerts earlier runs left
// for it, and once more when it delivers again; the retries are quiet.
func TestAFailingHookIsReportedOnce(t *testing.T) {
	env, site, _ := backupEnv(t)
	hook := filepath.Join(env.ConfDir, "alert")
	if err := os.Chmod(hook, 0o644); err != nil { // there, but it cannot run
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(site.Dir, env.DB.Default)); err != nil {
		t.Fatal(err)
	}
	if err := env.Backup(context.Background(), sitesOf(site)); err == nil {
		t.Fatal("backup without a database succeeded")
	}
	out, err := runCheck(env, sitesOf(site))
	if err != nil || !strings.Contains(out, "not an executable file; 1 alerts wait for it") || !strings.Contains(out, "BACKUP FAILED") {
		t.Fatalf("check = %v, output:\n%s", err, out)
	}
	if out, _ := runCheck(env, sitesOf(site)); out != "" {
		t.Errorf("the retry was not quiet:\n%s", out)
	}
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, _ := runCheck(env, sitesOf(site)); !strings.Contains(out, "delivers again") {
		t.Errorf("recovery of the hook not said:\n%s", out)
	}
	if got := hookCalls(env, hook, site.ID+" BACKUP FAILED"); len(got) != 1 {
		t.Errorf("hook calls = %v", got)
	}
}

// After the restarts run out, the alert says why the last one failed rather
// than counting restarts that never happened as tried.
func TestNeedsAttentionSaysWhyRestartsFailed(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	hook := writeHook(t, env, 0o755)
	health.set(false)
	env.host.fail["systemctl restart app@a"] = true
	for range maxRestarts + 1 {
		if _, err := runCheck(env, sitesOf(site)); !errors.Is(err, ErrSitesDown) {
			t.Fatal(err)
		}
	}
	got := hookCalls(env, hook, "a NEEDS ATTENTION")
	if len(got) != 1 || !strings.Contains(got[0], "the last one failed: systemctl restart app@a") {
		t.Errorf("hook calls = %v", got)
	}
}

// Trimming the undelivered alerts drops "recovered" first, oldest first,
// then the oldest of the rest.
func TestTrimmingKeepsTheAlertsThatMatter(t *testing.T) {
	env := newTestEnv(t)
	var calls []hookCall
	for i := range maxPending + 3 {
		status := "DOWN"
		if i%5 == 0 {
			status = "recovered"
		}
		calls = append(calls, hookCall{Site: strconv.Itoa(i), Status: status})
	}
	kept := env.trimPending(calls)
	if len(kept) != maxPending {
		t.Fatalf("kept %d, want %d", len(kept), maxPending)
	}
	for _, dropped := range []string{"0", "5", "10"} { // the three oldest recovered
		if slices.ContainsFunc(kept, func(c hookCall) bool { return c.Site == dropped }) {
			t.Errorf("kept recovered event %s; kept %v", dropped, kept)
		}
	}
	if kept[0].Site != "1" || kept[len(kept)-1].Site != strconv.Itoa(maxPending+2) {
		t.Errorf("order changed: %v", kept)
	}
}

// The hook's time limit ends what the hook started too: a curl it left
// hanging would pile up, one per five-minute check.
func TestTheAlertHookTimeLimitEndsItsChildren(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	hook := writeHook(t, env, 0o755)
	pidFile := filepath.Join(t.TempDir(), "child")
	writeFiles(t, env.ConfDir, map[string]string{"alert": fmt.Sprintf("#!/bin/sh\nsleep 30 &\necho $! > %s\nwait\n", pidFile)})
	// Long enough for a loaded machine to start the child and record it.
	defer func(old time.Duration) { hookTimeout = old }(hookTimeout)
	hookTimeout = 2 * time.Second
	env.Exec = func(ctx context.Context, c Cmd) error {
		if c.Name == hook {
			return RunCmd(ctx, c)
		}
		return env.host.exec(ctx, c)
	}
	health.set(false)
	start := time.Now()
	if _, err := runCheck(env, sitesOf(site)); !errors.Is(err, ErrSitesDown) {
		t.Fatal(err)
	}
	// A child left holding the hook's output would hold the check too.
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("the hook's child held the check for %s", took)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	for deadline := time.Now().Add(3 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the hook's child %d outlived its time limit", pid)
		}
	}
}

// logs reads only a regular file: a FIFO the site put there would hang it.
func TestLogsReadOnlyRegularFiles(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	writeFiles(t, site.Dir, map[string]string{".env": "APP_ERROR_LOG=./logs/errors.log\n", "logs/.keep": ""})
	if err := syscall.Mkfifo(filepath.Join(site.Dir, "logs", "errors.log"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := env.Logs(context.Background(), site, 5, false, true)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("logs of a FIFO: %v", err)
	}
	if slices.ContainsFunc(env.host.calls, func(call string) bool { return strings.HasPrefix(call, "tail") }) {
		t.Errorf("ran %v", env.host.calls)
	}
}
