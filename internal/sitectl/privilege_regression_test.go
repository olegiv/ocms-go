// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// pull's rsync reads on the server through Send: one sending rsync, as the
// site user, and nothing else.
func TestSendRunsOnlyAReadingRsync(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	reading := []string{"--server", "--sender", "-rtze.iLsfxCIvu", "--checksum", ".", site.Dir + "/uploads/"}
	if err := env.Send(context.Background(), site, reading, nil); err != nil {
		t.Fatal(err)
	}
	if want := "rsync " + strings.Join(reading, " "); !slices.Equal(env.host.calls, []string{want}) {
		t.Errorf("calls = %v, want %s", env.host.calls, want)
	}
	for name, args := range map[string][]string{
		"receiver":      {"--server", "-rtz", ".", site.Dir + "/uploads/"},
		"not a server":  {"-rtz", "/etc/", "/tmp/x/"},
		"remove source": {"--server", "--sender", "--remove-source-files", "-rtz", ".", site.Dir + "/uploads/"},
		"nothing":       {},
	} {
		env.host.calls = nil
		if err := env.Send(context.Background(), site, args, nil); err == nil || len(env.host.calls) != 0 {
			t.Errorf("%s: err = %v, calls %v", name, err, env.host.calls)
		}
	}
}

// Every site operation drops to the site user; a site user that is root
// would make that drop a no-op, so it is refused when the site is added and
// whenever a registered site is operated on.
func TestSitesNeverRunAsRoot(t *testing.T) {
	env := newTestEnv(t)
	if err := os.MkdirAll(filepath.Join(env.SitesBase, "example.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := env.AddSite(context.Background(), SiteSpec{Domain: "example.com", User: "root", Group: currentGroup(t)})
	if err == nil || !strings.Contains(err.Error(), "has uid 0") {
		t.Errorf("AddSite with root: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(env.SitesBase, "example.com")); len(entries) != 0 {
		t.Errorf("AddSite with root created %v", entries)
	}

	// A registry entry from before this check.
	site := Site{ID: "old", Dir: filepath.Join(env.SitesBase, "old"), User: "root", Port: 8090}
	if err := env.register(site); err != nil {
		t.Fatal(err)
	}
	createDB(t, filepath.Join(site.Dir, env.DB.Default))
	env.host.calls = nil
	checks := map[string]error{
		"backup":       env.Backup(context.Background(), sitesOf(site)),
		"snapshot":     env.Snapshot(context.Background(), site),
		"themes":       env.InstallThemes(context.Background(), site, &bytes.Buffer{}),
		"site paths":   env.SitePaths(context.Background(), site),
		"logs":         env.Logs(context.Background(), site, 5, false, true),
		"pull's rsync": env.Send(context.Background(), site, []string{"--server", "--sender", ".", site.Dir + "/"}, nil),
	}
	for name, err := range checks {
		if err == nil || !strings.Contains(err.Error(), "which has uid 0") {
			t.Errorf("%s as root: %v", name, err)
		}
	}
	if slices.ContainsFunc(env.host.calls, func(call string) bool { return strings.HasPrefix(call, "rsync") }) {
		t.Errorf("ran %v", env.host.calls)
	}
	if backups, _ := filepath.Glob(filepath.Join(site.Dir, "backups", "*.gz")); len(backups) != 0 {
		t.Errorf("backed up as root: %v", backups)
	}
}

// rollback --to and install --staged take only a release that is installed:
// a name is a path below releases/, and root follows it.
func TestRollbackOnlyToAnInstalledRelease(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	if err := env.install(t, "v1", site); err != nil {
		t.Fatal(err)
	}
	current := env.Current()
	// A binary beside releases/, where "../evil" leads.
	writeFiles(t, env.Root, map[string]string{"evil/app": "\x7fELFevil", "releases/.upload-x/app": "\x7fELFpartial"})
	env.host.calls = nil
	for _, name := range []string{"../evil", "nonexistent", ".upload-x", "x/../../evil"} {
		if err := env.Rollback(context.Background(), name, []Site{site}); err == nil {
			t.Errorf("rollback --to %s succeeded", name)
		}
		if err := env.Activate(context.Background(), name, []Site{site}); err == nil {
			t.Errorf("activating %s succeeded", name)
		}
	}
	if env.Current() != current || len(env.host.systemctlCalls()) != 0 {
		t.Errorf("current %s (was %s), systemctl %v", env.Current(), current, env.host.systemctlCalls())
	}
	if _, err := env.PlaceStaged(strings.Repeat("ab", 32)); err == nil {
		t.Error("placed a staged binary nobody uploaded")
	}
}

// The first install over a binary the shell scripts left adopts it as a
// release and names it as the way back when the new one fails.
func TestFirstInstallOffersTheLegacyBinaryBack(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	if err := os.MkdirAll(env.binDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.binary(), []byte("legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := env.now.Add(-time.Hour)
	if err := os.Chtimes(env.binary(), legacy, legacy); err != nil {
		t.Fatal(err)
	}
	health.set(false)
	if err := env.install(t, "v1", site); err == nil {
		t.Fatal("install of an unhealthy release succeeded")
	}
	want := "rollback <site>... --to " + legacy.UTC().Format(releaseStamp) + "-legacy"
	if !strings.Contains(env.out.String(), want) {
		t.Errorf("output does not offer %q:\n%s", want, env.out.String())
	}
	health.set(true)
	if err := env.Rollback(context.Background(), "", []Site{site}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, env.binary()); got != "legacy" {
		t.Errorf("after rollback bin/app runs %q", got)
	}
}

// deploy --themes keeps the binary first (Stage), then makes it a release
// by its digest (PlaceStaged) and switches to it (Activate). A binary that
// was staged but never installed is no release: rollback and pruning never
// see it.
func TestStagedBinaryIsNoReleaseUntilInstalled(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	if err := env.install(t, "v1", site); err != nil {
		t.Fatal(err)
	}
	first := env.Current()
	stage := func(content string) string {
		t.Helper()
		env.now = env.now.Add(time.Minute)
		data, sum := binaryFixture(content)
		if err := env.Stage(bytes.NewReader(data), sum); err != nil {
			t.Fatal(err)
		}
		return sum
	}

	// A deploy --themes whose themes failed after staging, then a plain
	// deploy: rollback must offer v1, the last release that ran.
	env.host.calls = nil
	stage("abandoned")
	if releases, _ := env.Releases(); len(releases) != 1 || env.Current() != first || len(env.host.systemctlCalls()) != 0 {
		t.Fatalf("staging made releases %v, current %s, systemctl %v", releases, env.Current(), env.host.systemctlCalls())
	}
	env.now = env.now.Add(time.Minute)
	if err := env.install(t, "v2", site); err != nil {
		t.Fatal(err)
	}
	if err := env.Rollback(context.Background(), "", []Site{site}); err != nil {
		t.Fatal(err)
	}
	if env.Current() != first {
		t.Errorf("rollback went to %s, want %s", env.Current(), first)
	}

	sum := stage("v3")
	release, err := env.PlaceStaged(strings.ToUpper(sum))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Activate(context.Background(), release, []Site{site}); err != nil {
		t.Fatal(err)
	}
	if env.Current() != release || readFile(t, env.binary()) != "\x7fELFv3" {
		t.Errorf("current %s, want %s", env.Current(), release)
	}
	if _, err := env.PlaceStaged(sum); err == nil {
		t.Error("the staged binary was placed twice")
	}
	data, sum := binaryFixture("v4")
	if err := env.Stage(bytes.NewReader([]byte("\x7fELFdamaged")), sum); err == nil {
		t.Error("a damaged upload was staged")
	}
	if err := env.Stage(bytes.NewReader(data), sum); err != nil {
		t.Fatal(err)
	}
	if staged, _ := filepath.Glob(filepath.Join(env.releasesDir(), ".staged-*")); len(staged) != 1 {
		t.Errorf("staged binaries %v, want only the newest", staged)
	}
}
