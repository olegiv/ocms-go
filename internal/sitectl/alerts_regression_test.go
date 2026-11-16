// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

// Trimming keeps each site's newest event, where the site stands now: the
// hook must not hear that a site went down and never that it recovered, nor
// lose a site whose only event is the oldest.
func TestTrimmingKeepsEachSitesNewestEvent(t *testing.T) {
	env := newTestEnv(t)
	calls := []hookCall{{Site: "first", Status: "DOWN"}, {Site: "a", Status: "DOWN"}}
	for i := range maxPending - 2 {
		calls = append(calls, hookCall{Site: "s" + strconv.Itoa(i), Status: "DOWN"})
	}
	calls = append(calls, hookCall{Site: "a", Status: "recovered"}) // maxPending + 1 in all
	kept := env.trimPending(calls)
	has := func(site, status string) bool {
		return slices.ContainsFunc(kept, func(c hookCall) bool { return c.Site == site && c.Status == status })
	}
	if len(kept) != maxPending || !has("first", "DOWN") || !has("a", "recovered") || has("a", "DOWN") {
		t.Errorf("kept %v", kept)
	}
	if kept[0].Site != "first" || kept[len(kept)-1].Status != "recovered" {
		t.Errorf("order changed: %v", kept)
	}
}

// Every failed backup reaches cron mail through the next check, also while
// the hook keeps failing with the same error; what was printed once is not
// printed again.
func TestEveryBackupFailureReachesCronMail(t *testing.T) {
	env, site, _ := backupEnv(t)
	if err := os.Chmod(filepath.Join(env.ConfDir, "alert"), 0o644); err != nil { // fails the same way every time
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(site.Dir, env.DB.Default)); err != nil {
		t.Fatal(err)
	}
	for night := 1; night <= 2; night++ {
		env.now = env.now.Add(24 * time.Hour)
		if err := env.Backup(context.Background(), sitesOf(site)); err == nil {
			t.Fatal("backup without a database succeeded")
		}
		out, err := runCheck(env, sitesOf(site))
		if err != nil || strings.Count(out, "BACKUP FAILED") != 1 {
			t.Errorf("night %d: check = %v, output:\n%s", night, err, out)
		}
	}
	if out, _ := runCheck(env, sitesOf(site)); out != "" {
		t.Errorf("printed again:\n%s", out)
	}

	// A hook removed while it failed ends the failure: the same fault later
	// is reported again.
	if err := os.Remove(filepath.Join(env.ConfDir, "alert")); err != nil {
		t.Fatal(err)
	}
	if _, err := runCheck(env, sitesOf(site)); err != nil {
		t.Fatal(err)
	}
	writeHook(t, env, 0o644)
	if err := env.Backup(context.Background(), sitesOf(site)); err == nil {
		t.Fatal("backup without a database succeeded")
	}
	if out, _ := runCheck(env, sitesOf(site)); !strings.Contains(out, "not an executable file") {
		t.Errorf("the returned fault was not reported:\n%s", out)
	}
}

// A state file that does not parse is reported once, also without a hook.
func TestCorruptStateIsReportedOnce(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	writeFiles(t, env.StateDir, map[string]string{"monitor.json": "{not json"})
	if err := os.Chmod(env.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := runCheck(env, sitesOf(site))
	if err != nil || strings.Count(out, "did not parse") != 1 {
		t.Errorf("check = %v, output:\n%s", err, out)
	}
}

// A child ocmsctl's error is its last "ocmsctl: " line and the lines a
// joined error continues on; without one, a panic's message.
func TestChildErrorKeepsTheWholeError(t *testing.T) {
	for printed, want := range map[string]string{
		"Backing up a\nocmsctl: database: stat x.db: no such file\nuploads: open y: permission denied\n": "database: stat x.db: no such file; uploads: open y: permission denied",
		"panic: nil map\n\ngoroutine 1 [running]:\nmain.main()\n\t/src/main.go:12 +0x1d\n":               "panic: nil map",
		"something else\nlast words\n": "last words",
	} {
		var stderr tailBuffer
		_, _ = stderr.Write([]byte(printed))
		if got := stderr.childError("ocmsctl: "); got != want {
			t.Errorf("childError(%q) = %q, want %q", printed, got, want)
		}
	}
}

// A staged binary that is damaged is said to be, not missing.
func TestPlaceStagedNamesWhatIsWrong(t *testing.T) {
	env := newTestEnv(t)
	data, sum := binaryFixture("v1")
	if err := env.Stage(bytes.NewReader(data), sum); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, env.releasesDir(), map[string]string{".staged-" + sum: "\x7fELFdamaged"})
	if _, err := env.PlaceStaged(sum); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("damaged staged binary: %v", err)
	}
	_, other := binaryFixture("v2")
	if _, err := env.PlaceStaged(other); err == nil || !strings.Contains(err.Error(), "was staged") {
		t.Errorf("missing staged binary: %v", err)
	}
}

// A deploy shows a failed site's log with a time limit: a site could make
// reading it block.
func TestShowingAFailedSitesLogHasATimeLimit(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	writeFiles(t, site.Dir, map[string]string{"logs/app.log": "line\n"})
	env.host.stdout = "append:" + filepath.Join(site.Dir, "logs", "app.log")
	defer func(old time.Duration) { logTailTimeout = old }(logTailTimeout)
	logTailTimeout = 100 * time.Millisecond
	fake := env.Exec
	env.Exec = func(ctx context.Context, c Cmd) error {
		if c.Name == "tail" { // a FIFO swapped in after the check
			<-ctx.Done()
			return ctx.Err()
		}
		return fake(ctx, c)
	}
	done := make(chan struct{})
	go func() { env.showLogTail(context.Background(), site); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("showing the log waited on it")
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

// The cron line of the nightly backup prints one line, outside the log, when
// the backup fails: cron mails it whatever the hook and the monitor's state
// do. A backup that succeeds prints nothing.
func TestCronMailsAFailedBackup(t *testing.T) {
	env := newTestEnv(t)
	if err := env.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(readFile(t, env.CronFile), "\n") {
		if strings.Contains(l, " backup --all") {
			line = l
		}
	}
	fields := strings.Fields(line)
	if len(fields) < 7 {
		t.Fatalf("backup cron line: %q", line)
	}
	command := strings.Join(fields[6:], " ") // past the schedule and the user
	dir := t.TempDir()
	command = strings.ReplaceAll(command, "/var/log/app-backup.log", filepath.Join(dir, "backup.log"))
	for exit, want := range map[int]string{1: env.ctl() + " backup failed; see ", 0: ""} {
		writeFiles(t, filepath.Dir(env.ctl()), map[string]string{filepath.Base(env.ctl()): fmt.Sprintf("#!/bin/sh\necho working\nexit %d\n", exit)})
		if err := os.Chmod(env.ctl(), 0o755); err != nil {
			t.Fatal(err)
		}
		out, _ := exec.Command("/bin/sh", "-c", command).Output()
		if got := string(out); want == "" && got != "" || want != "" && (!strings.HasPrefix(got, want) || strings.Count(got, "\n") != 1) {
			t.Errorf("backup exiting %d: cron would mail %q", exit, got)
		}
	}
}

// A check's own alerts reached cron mail when it printed them: while the
// hook keeps failing, later checks retry them without printing them again.
func TestCheckAlertsArePrintedOnceWhileTheHookFails(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	writeHook(t, env, 0o644) // there, but it cannot run
	health.set(false)
	env.host.fail["systemctl restart app@a"] = true
	if out, _ := runCheck(env, sitesOf(site)); strings.Count(out, "DOWN") != 1 {
		t.Fatalf("first check:\n%s", out)
	}
	if out, _ := runCheck(env, sitesOf(site)); strings.Contains(out, "DOWN") {
		t.Errorf("the retry printed the alert again:\n%s", out)
	}
}
