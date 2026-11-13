// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeFiles(t *testing.T, base string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackupWritesPrivateVerifiedCopiesAndExpiresOldOnes(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	createDB(t, filepath.Join(site.Dir, env.DBPath))
	writeFiles(t, site.Dir, map[string]string{
		"uploads/2026/photo.jpg":      "jpeg",
		"custom/themes/mine/base.css": "body{}",
		"backups/app_old.db.gz":       "old",
		"backups/uploads_old.tar.gz":  "old",
		"backups/notes.txt":           "keep me",
	})
	old := env.now.Add(-backupRetention - time.Hour)
	for _, name := range []string{"app_old.db.gz", "uploads_old.tar.gz", "notes.txt"} {
		if err := os.Chtimes(filepath.Join(site.Dir, "backups", name), old, old); err != nil {
			t.Fatal(err)
		}
	}

	if err := env.Backup(context.Background(), []Site{site}); err != nil {
		t.Fatal(err)
	}

	backups := filepath.Join(site.Dir, "backups")
	if info, _ := os.Stat(backups); info.Mode().Perm() != 0o700 {
		t.Errorf("backups/ has mode %o, want 700", info.Mode().Perm())
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
		if info, _ := entry.Info(); entry.Name() != "notes.txt" && info.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %o, want 600", entry.Name(), info.Mode().Perm())
		}
	}
	want := []string{"app_20261113_100000.db.gz", "custom_20261113_100000.tar.gz", "notes.txt", "uploads_20261113_100000.tar.gz"}
	if !slices.Equal(names, want) {
		t.Fatalf("backups/ holds %v, want %v", names, want)
	}

	// The database copy is a complete, readable database.
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := gunzipFile(filepath.Join(backups, want[0]), restored); err != nil {
		t.Fatal(err)
	}
	if err := checkIntegrity(context.Background(), restored); err != nil {
		t.Error(err)
	}
	if got := queryInt(t, restored, "SELECT count(*) FROM pages"); got != 1 {
		t.Errorf("restored database has %d pages, want 1", got)
	}
}

func TestSnapshotIsConsistentAndScrubRemovesCredentials(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	createDB(t, filepath.Join(site.Dir, env.DBPath))

	var packed bytes.Buffer
	env.Stdout = &packed
	if err := env.Snapshot(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(site.Dir, "backups", ".snapshot-*")); len(leftovers) > 0 {
		t.Errorf("snapshot left %v on the server", leftovers)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s.db.gz"), packed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(dir, "s.db")
	if err := gunzipFile(filepath.Join(dir, "s.db.gz"), local); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, local, "SELECT count(*) FROM sessions"); got != 1 {
		t.Fatalf("snapshot has %d sessions, want the production row", got)
	}

	if err := env.Scrub(context.Background(), local); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, local, "SELECT count(*) FROM sessions"); got != 0 {
		t.Errorf("scrubbed database still has %d sessions", got)
	}
	if got := queryInt(t, local, "SELECT count(*) FROM pages"); got != 1 {
		t.Errorf("scrub removed content: %d pages", got)
	}
	// The row must be gone from the file, not only from the table.
	if raw := readFile(t, local); strings.Contains(raw, "secret-session-token") {
		t.Error("the deleted session token is still in the database file")
	}
}

func TestThemesFollowLocalSymlinksAndReplaceTheOldSet(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	writeFiles(t, site.Dir, map[string]string{"custom/themes/old/theme.json": "{}"})

	// A site repository may link a shared theme into custom/themes.
	local := t.TempDir()
	writeFiles(t, local, map[string]string{
		"shared/mine/theme.json":       `{"name":"mine"}`,
		"shared/mine/static/js/app.js": "js",
		"themes/README.md":             "x",
	})
	if err := os.Symlink(filepath.Join(local, "shared", "mine"), filepath.Join(local, "themes", "mine")); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := writeTarGz(&archive, filepath.Join(local, "themes"), ".", true); err != nil {
		t.Fatal(err)
	}
	if err := env.InstallThemes(context.Background(), site, &archive); err != nil {
		t.Fatal(err)
	}

	themes := filepath.Join(site.Dir, "custom", "themes")
	if got := readFile(t, filepath.Join(themes, "mine", "static", "js", "app.js")); got != "js" {
		t.Errorf("linked theme file = %q", got)
	}
	if info, err := os.Lstat(filepath.Join(themes, "mine")); err != nil || !info.IsDir() {
		t.Errorf("the linked theme did not arrive as a real directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(themes, "old")); err == nil {
		t.Error("a theme deleted locally is still on the server")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(site.Dir, "custom", ".themes-*")); len(leftovers) > 0 {
		t.Errorf("left behind %v", leftovers)
	}
}

func TestThemesArchiveCannotEscapeOrBreakTheOldSet(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	writeFiles(t, site.Dir, map[string]string{"custom/themes/old/theme.json": "{}"})

	entries := map[string]tar.Header{
		"path outside the themes": {Name: "../../escaped.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
		"symlink":                 {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
	}
	for name, header := range entries {
		var archive bytes.Buffer
		zw := gzip.NewWriter(&archive)
		tw := tar.NewWriter(zw)
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tw.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
		}
		_ = tw.Close()
		_ = zw.Close()
		if err := env.InstallThemes(context.Background(), site, &archive); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := env.InstallThemes(context.Background(), site, strings.NewReader("not a gzip stream")); err == nil {
		t.Error("garbage accepted as themes")
	}
	if _, err := os.Stat(filepath.Join(site.Dir, "custom", "themes", "old", "theme.json")); err != nil {
		t.Errorf("a rejected upload damaged the installed themes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(site.Dir, "escaped.txt")); err == nil {
		t.Error("an archive entry escaped the themes directory")
	}
}

func TestCheckRestartsWithACapAndAlertsOncePerOutage(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	stopped, _ := env.addSite(t, "stopped")
	env.host.set("app@stopped", "inactive")
	hook := filepath.Join(env.ConfDir, "alert")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sites := []Site{site, stopped}
	check := func() (output string, err error) {
		env.out.Reset()
		err = env.Check(context.Background(), sites)
		return env.out.String(), err
	}
	count := func(prefix string) int {
		n := 0
		for _, call := range env.host.calls {
			if strings.HasPrefix(call, prefix) {
				n++
			}
		}
		return n
	}

	if out, err := check(); err != nil || out != "" {
		t.Fatalf("healthy run: %v, output %q; want silence", err, out)
	}

	health.set(false)
	for run := 1; run <= maxRestarts+2; run++ {
		out, err := check()
		if !errors.Is(err, ErrSitesDown) || !strings.HasSuffix(err.Error(), ": a") {
			t.Fatalf("run %d: err = %v, want a down", run, err)
		}
		if strings.Contains(err.Error(), "stopped") {
			t.Errorf("run %d: a stopped site was reported down", run)
		}
		switch {
		case run == 1 && !strings.Contains(out, "DOWN"):
			t.Errorf("first run did not report the outage:\n%s", out)
		case run == maxRestarts+1 && !strings.Contains(out, "NEEDS ATTENTION"):
			t.Errorf("run %d did not say the restarts ran out:\n%s", run, out)
		case run == maxRestarts+2 && out != "":
			t.Errorf("run %d repeated itself:\n%s", run, out)
		}
	}
	if got := count("systemctl restart app@a"); got != maxRestarts {
		t.Errorf("%d restarts, want %d", got, maxRestarts)
	}
	if got := count(hook + " a DOWN"); got != 1 {
		t.Errorf("the alert hook got %d DOWN calls, want 1", got)
	}

	health.set(true)
	if out, err := check(); err != nil || !strings.Contains(out, "recovered") {
		t.Fatalf("recovery run: %v, output %q", err, out)
	}
	if out, err := check(); err != nil || out != "" {
		t.Fatalf("run after recovery: %v, output %q; want silence", err, out)
	}

	// A crash loop and a failed unit are down, and restarting adds nothing.
	restarts := count("systemctl restart app@a")
	for _, state := range []string{"activating", "failed"} {
		env.host.set("app@a", state)
		if _, err := check(); err == nil {
			t.Errorf("a unit that is %s was not reported down", state)
		}
		env.host.set("app@a", "active")
		if _, err := check(); err != nil {
			t.Fatal(err)
		}
	}
	if got := count("systemctl restart app@a"); got != restarts {
		t.Errorf("restarted a crash-looping or failed unit %d times", got-restarts)
	}
}

func TestCheckRefusesStateOthersCanWrite(t *testing.T) {
	env := newTestEnv(t)
	if err := os.MkdirAll(env.StateDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(env.StateDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := env.Check(context.Background(), nil); err == nil {
		t.Error("a world-writable state directory was accepted")
	}
}

// sshCalls returns the recorded ssh command lines.
func sshCalls(env *testEnv) []string {
	var calls []string
	for _, call := range env.host.calls {
		if strings.HasPrefix(call, "ssh ") {
			calls = append(calls, call)
		}
	}
	return calls
}

func TestDeploySendsTheDigestOfTheBinaryItUploads(t *testing.T) {
	env := newTestEnv(t)
	dir := t.TempDir()
	data, sum := binaryFixture("v1")
	binary := filepath.Join(dir, "app-linux-amd64")
	if err := os.WriteFile(binary, data, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{"themes/mine/theme.json": "{}"})

	var uploaded []byte
	exec := env.Exec
	env.Exec = func(ctx context.Context, c Cmd) error {
		if c.Name == "ssh" && slices.Contains(c.Args, "install") {
			uploaded, _ = io.ReadAll(c.Stdin)
		}
		return exec(ctx, c)
	}
	o := DeployOpts{Host: "root@server", Sites: []string{"a", "b"}, Binary: binary, Build: []string{"make", "build-linux-amd64"}, Themes: true, ThemesDir: filepath.Join(dir, "themes")}
	if err := env.Deploy(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	ctl := env.ctl()
	if env.host.calls[0] != "make build-linux-amd64" {
		t.Errorf("first command = %q, want the build", env.host.calls[0])
	}
	want := []string{
		"ssh -C root@server " + ctl + " themes a",
		"ssh -C root@server " + ctl + " themes b",
		"ssh -C root@server " + ctl + " install --sha256 " + sum + " a b",
	}
	if got := sshCalls(env); !slices.Equal(got, want) {
		t.Errorf("ssh calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !bytes.Equal(uploaded, data) {
		t.Error("the binary on ssh's standard input is not the one that was hashed")
	}
}

func TestDeployVariants(t *testing.T) {
	env := newTestEnv(t)
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"themes/mine/theme.json": "{}", "placeholders/.gitkeep": "", "placeholders/README.md": "x"})
	ctl := env.ctl()
	base := DeployOpts{Host: "prod", Binary: filepath.Join(dir, "missing"), ThemesDir: filepath.Join(dir, "themes")}

	themesOnly := base
	themesOnly.Sites, themesOnly.ThemesOnly = []string{"a"}, true
	if err := env.Deploy(context.Background(), themesOnly); err != nil {
		t.Fatal(err)
	}
	want := []string{"ssh -C prod " + ctl + " themes a", "ssh -C prod " + ctl + " restart a"}
	if got := sshCalls(env); !slices.Equal(got, want) {
		t.Errorf("themes-only ssh calls = %v, want %v", got, want)
	}

	// A dry run prints the plan and runs nothing, even before the first build.
	env.host.calls = nil
	dry := base
	dry.All, dry.DryRun, dry.Build, dry.HealthTimeout = true, true, []string{"make", "x"}, time.Minute
	if err := env.Deploy(context.Background(), dry); err != nil {
		t.Fatal(err)
	}
	if len(env.host.calls) != 0 {
		t.Errorf("a dry run executed %v", env.host.calls)
	}
	for _, line := range []string{"[dry-run] make x", "install --sha256 " + strings.Repeat("0", 64) + " --health-timeout=1m0s --all < " + dry.Binary} {
		if !strings.Contains(env.out.String(), line) {
			t.Errorf("dry run output lacks %q:\n%s", line, env.out.String())
		}
	}

	for name, bad := range map[string]DeployOpts{
		"missing binary":        {Host: "prod", Sites: []string{"a"}, Binary: base.Binary},
		"option as host":        {Host: "-oProxyCommand=evil", Sites: []string{"a"}, ThemesOnly: true, ThemesDir: base.ThemesDir},
		"shell text in a site":  {Host: "prod", Sites: []string{"a;reboot"}, ThemesOnly: true, ThemesDir: base.ThemesDir},
		"sites and --all":       {Host: "prod", Sites: []string{"a"}, All: true, Binary: base.Binary},
		"themes without a site": {Host: "prod", All: true, Themes: true, ThemesDir: base.ThemesDir},
		"no themes directory":   {Host: "prod", Sites: []string{"a"}, ThemesOnly: true, ThemesDir: filepath.Join(dir, "empty")},
		"placeholders only":     {Host: "prod", Sites: []string{"a"}, ThemesOnly: true, ThemesDir: filepath.Join(dir, "placeholders")},
	} {
		env.host.calls = nil
		if err := env.Deploy(context.Background(), bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if got := sshCalls(env); len(got) != 0 {
			t.Errorf("%s: reached the server: %v", name, got)
		}
	}
}

func TestPullRefusesWhileTheLocalServerRuns(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a") // its test server stands in for a local dev server
	o := PullOpts{Host: "prod", Site: "a", DB: true, LocalDB: filepath.Join(t.TempDir(), "app.db"), LocalPort: site.Port, Yes: true}
	err := env.Pull(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(site.Port)) {
		t.Fatalf("Pull = %v, want a refusal naming the port", err)
	}
	if got := sshCalls(env); len(got) != 0 {
		t.Errorf("reached the server: %v", got)
	}

	o.LocalPort, o.Yes, o.Confirm = 0, false, strings.NewReader("n\n")
	if err := env.Pull(context.Background(), o); err == nil || len(sshCalls(env)) != 0 {
		t.Errorf("Pull went ahead after the answer n: %v", err)
	}
}

// The package is meant to be lifted out for other projects, and every
// external command must be visible to dry runs and tests.
func TestSitectlIsApplicationAgnostic(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files: %v", err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if strings.HasPrefix(path, "github.com/olegiv/ocms-go") {
				t.Errorf("%s imports %s: sitectl must not depend on the application", name, path)
			}
			if path == "os/exec" && name != "app.go" {
				t.Errorf("%s imports os/exec: run commands through App.Exec (RunCmd in app.go)", name)
			}
		}
	}
}

func TestLogsReadTheSiteLogFileOrTheJournal(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	if err := env.Logs(context.Background(), site, 100, true, false); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, site.Dir, map[string]string{"logs/app.log": "line\n"})
	if err := env.Logs(context.Background(), site, 5, false, false); err != nil {
		t.Fatal(err)
	}
	want := []string{"journalctl -u app@a -n 100 --no-pager -f", "tail -n 5 " + filepath.Join(site.Dir, "logs", "app.log")}
	if !slices.Equal(env.host.calls, want) {
		t.Errorf("log commands = %v, want %v", env.host.calls, want)
	}
	if err := env.Logs(context.Background(), site, 5, false, true); err == nil {
		t.Error("a missing error log was not reported")
	}
}
