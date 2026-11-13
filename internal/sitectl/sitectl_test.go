// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHost stands in for the server's systemd and for every other external
// command: it records each call and keeps a unit's state.
type fakeHost struct {
	mu    sync.Mutex
	calls []string
	state map[string]string // unit -> what `systemctl is-active` prints
	fail  map[string]bool   // exact command line -> fails
}

func (h *fakeHost) exec(_ context.Context, c Cmd) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	line := c.String()
	if c.Name != "systemctl" || c.Args[0] != "is-active" {
		h.calls = append(h.calls, line)
	}
	if h.fail[line] {
		return errors.New("exit status 1")
	}
	if c.Name != "systemctl" {
		return nil
	}
	unit := c.Args[len(c.Args)-1]
	switch c.Args[0] {
	case "start", "restart":
		h.state[unit] = "active"
	case "stop", "disable":
		h.state[unit] = "inactive"
	case "is-active":
		state := h.state[unit]
		if state == "" {
			state = "inactive"
		}
		_, _ = fmt.Fprintln(c.Stdout, state)
		if state != "active" {
			return errors.New("exit status 3")
		}
	}
	return nil
}

func (h *fakeHost) set(unit, state string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state[unit] = state
}

// systemctlCalls returns the recorded systemctl calls without the program name.
func (h *fakeHost) systemctlCalls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var calls []string
	for _, call := range h.calls {
		if rest, ok := strings.CutPrefix(call, "systemctl "); ok {
			calls = append(calls, rest)
		}
	}
	return calls
}

type testEnv struct {
	*App
	host *fakeHost
	out  *bytes.Buffer
	now  time.Time
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	env := &testEnv{host: &fakeHost{state: map[string]string{}, fail: map[string]bool{}}, out: &bytes.Buffer{}}
	env.now = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	env.App = &App{
		Name:           "app",
		Root:           filepath.Join(root, "opt"),
		ConfDir:        filepath.Join(root, "etc"),
		SystemdDir:     filepath.Join(root, "systemd"),
		CronFile:       filepath.Join(root, "cron"),
		LogrotateFile:  filepath.Join(root, "logrotate"),
		StateDir:       filepath.Join(root, "run"),
		SitesBase:      filepath.Join(root, "vhosts"),
		DefaultGroup:   currentGroup(t),
		BasePort:       8081,
		HealthPath:     "/health",
		ReadyPath:      "/health/ready",
		DBPath:         "data/app.db",
		ThemesPath:     "custom/themes",
		ThemeFile:      "theme.json",
		ErrorLog:       "logs/error.log",
		Unit:           []byte("[Service]\nExecStart=/opt/app/bin/app\n"),
		EnvTemplate:    "PORT={{.Port}}\nSECRET={{.Secret}}\nOTHER={{.Secret}}\nSEED={{.HexSecret}}\n",
		DropInTemplate: "[Service]\nUser={{.User}}\nGroup={{.Group}}\nWorkingDirectory={{.Dir}}\n",
		ProxyTemplate:  "proxy_pass http://127.0.0.1:{{.Port}};\n",
		ScrubSQL:       `SELECT 'DELETE FROM sessions;' WHERE EXISTS (SELECT 1 FROM sqlite_master WHERE name = 'sessions')`,
		Stdout:         env.out,
		Stderr:         env.out,
		Exec:           env.host.exec,
		HealthTimeout:  0, // one probe decides
		Settle:         0,
		Now:            func() time.Time { return env.now },
	}
	for _, dir := range []string{env.SystemdDir, env.SitesBase} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

func currentUser(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	return u.Username
}

func currentGroup(t *testing.T) string {
	t.Helper()
	group, err := user.LookupGroupId(strconv.Itoa(os.Getgid()))
	if err != nil {
		t.Fatal(err)
	}
	return group.Name
}

// addSite registers a site owned by the current user whose health endpoints
// are served by the returned handler state: set healthy to change the answer.
func (env *testEnv) addSite(t *testing.T, id string) (Site, *healthState) {
	t.Helper()
	health := &healthState{ok: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		health.mu.Lock()
		ok, hook := health.ok, health.onProbe
		health.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		if hook != nil {
			hook()
		}
	}))
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	site := Site{ID: id, Dir: filepath.Join(env.SitesBase, id), User: currentUser(t), Port: port}
	if err := os.MkdirAll(site.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := env.register(site); err != nil {
		t.Fatal(err)
	}
	env.host.set(env.unit(id), "active")
	return site, health
}

type healthState struct {
	mu      sync.Mutex
	ok      bool
	onProbe func()
}

func (h *healthState) set(ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ok = ok
}

// binaryFixture returns a fake Linux executable and its SHA-256.
func binaryFixture(content string) (data []byte, sum string) {
	data = []byte("\x7fELF" + content)
	digest := sha256.Sum256(data)
	return data, hex.EncodeToString(digest[:])
}

func (env *testEnv) install(t *testing.T, content string, sites ...Site) error {
	t.Helper()
	data, sum := binaryFixture(content)
	return env.Install(context.Background(), bytes.NewReader(data), sum, sites)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSitesSkipsDisabledAndRejectsMalformedLines(t *testing.T) {
	env := newTestEnv(t)
	if err := os.MkdirAll(env.ConfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(content string) {
		if err := os.WriteFile(env.registry(), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("# header\n\na_com /srv/a alice 8081\n#old_com /srv/old bob 8082\nb_com /srv/b bob 8083\n")
	sites, err := env.Sites()
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 || sites[0].ID != "a_com" || sites[1].Port != 8083 {
		t.Fatalf("got %v, want a_com and b_com", sites)
	}
	if _, err := env.Pick([]string{"old_com"}, false); err == nil {
		t.Error("a disabled site was picked")
	}

	for _, bad := range []string{"a_com /srv/a alice", "a_com /srv/a alice http", "a;b /srv/a alice 80", "a_com relative alice 80"} {
		write(bad + "\n")
		if _, err := env.Sites(); err == nil {
			t.Errorf("registry line %q was accepted", bad)
		}
	}
}

func TestRegisterAndUnregisterKeepOtherLines(t *testing.T) {
	env := newTestEnv(t)
	for _, site := range []Site{{"a", "/srv/a", "alice", 8081}, {"ab", "/srv/ab", "bob", 8082}} {
		if err := env.register(site); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.unregister("a"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, env.registry())
	if !strings.HasPrefix(got, registryHeader) || !strings.HasSuffix(got, "ab /srv/ab bob 8082\n") || strings.Contains(got, "/srv/a ") {
		t.Errorf("registry after removing a:\n%s", got)
	}
}

func TestInstallVerifiesTheUploadBeforeTouchingAnything(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	if err := env.install(t, "v1", site); err != nil {
		t.Fatal(err)
	}
	before, calls := env.Current(), len(env.host.systemctlCalls())

	data, sum := binaryFixture("v2")
	script := []byte("#!/bin/sh\n")
	scriptSum := sha256.Sum256(script)
	cases := map[string]struct {
		data []byte
		sum  string
	}{
		"truncated upload": {data[:len(data)-1], sum},
		"malformed digest": {data, "abc"},
		"not an ELF file":  {script, hex.EncodeToString(scriptSum[:])},
	}
	for name, c := range cases {
		if err := env.Install(context.Background(), bytes.NewReader(c.data), c.sum, []Site{site}); err == nil {
			t.Errorf("%s: installed", name)
		}
	}
	if env.Current() != before {
		t.Errorf("current release changed from %s to %s", before, env.Current())
	}
	if got := len(env.host.systemctlCalls()); got != calls {
		t.Errorf("a rejected upload ran %d systemctl calls", got-calls)
	}
	releases, err := env.Releases()
	if err != nil || len(releases) != 1 {
		t.Errorf("releases = %v, %v; want only the first", releases, err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(env.releasesDir(), ".upload-*"))
	if len(leftovers) > 0 {
		t.Errorf("rejected uploads left behind: %v", leftovers)
	}
}

func TestInstallAdoptsLegacyBinarySwitchesAndPrunes(t *testing.T) {
	env := newTestEnv(t)
	a, _ := env.addSite(t, "a")
	b, _ := env.addSite(t, "b")

	// The shell scripts left a plain file at bin/app.
	if err := os.MkdirAll(env.binDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.binary(), []byte("legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(env.binary(), env.now.Add(-time.Hour), env.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := env.install(t, "v1", a, b); err != nil {
		t.Fatal(err)
	}
	want := []string{"stop app@a", "stop app@b", "start app@a", "start app@b"}
	if got := env.host.systemctlCalls(); !slices.Equal(got, want) {
		t.Errorf("systemctl calls = %v, want %v", got, want)
	}
	if got := readFile(t, env.binary()); got != "\x7fELFv1" {
		t.Errorf("bin/app runs %q, want the new release", got)
	}
	if info, err := os.Lstat(env.binary()); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("bin/app is not a symlink to a release: %v", err)
	}
	releases, _ := env.Releases()
	if len(releases) != 2 || !strings.HasSuffix(releases[0], "-legacy") || releases[1] != env.Current() {
		t.Fatalf("releases = %v, current %s; want the legacy binary then the new one", releases, env.Current())
	}
	if got := readFile(t, filepath.Join(env.releasesDir(), releases[0], "app")); got != "legacy" {
		t.Errorf("legacy release holds %q", got)
	}

	for i := 2; i <= 5; i++ {
		env.now = env.now.Add(time.Minute)
		if err := env.install(t, "v"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	releases, _ = env.Releases()
	if len(releases) != keepReleases || releases[len(releases)-1] != env.Current() {
		t.Errorf("after five installs: releases %v, current %s; want the newest %d", releases, env.Current(), keepReleases)
	}
}

func TestRestartReportsEachFailureAndCarriesOn(t *testing.T) {
	env := newTestEnv(t)
	stuck, _ := env.addSite(t, "stuck")
	sick, sickHealth := env.addSite(t, "sick")
	fine, _ := env.addSite(t, "fine")
	env.host.fail["systemctl stop app@stuck"] = true
	sickHealth.set(false)

	err := env.Restart(context.Background(), []Site{stuck, sick, fine})
	if err == nil || !strings.Contains(err.Error(), "stuck, sick") || strings.Contains(err.Error(), "fine") {
		t.Fatalf("Restart error = %v; want stuck and sick, not fine", err)
	}
	calls := env.host.systemctlCalls()
	// A site that did not stop may still hold its port: it is not started.
	if slices.Contains(calls, "start app@stuck") {
		t.Error("started a site that did not stop")
	}
	for _, want := range []string{"start app@sick", "start app@fine"} {
		if !slices.Contains(calls, want) {
			t.Errorf("missing %q in %v", want, calls)
		}
	}
	if out := env.out.String(); !strings.Contains(out, "fine") || !strings.Contains(out, "OK") {
		t.Errorf("the healthy site was not reported:\n%s", out)
	}
	// The failure output carries the site's last log lines.
	if !slices.Contains(env.host.calls, "journalctl -u app@sick -n 5 --no-pager") {
		t.Errorf("the unhealthy site's log was not shown: %v", env.host.calls)
	}
}

// A unit that exits right after its first answer is restarted by systemd and
// would pass a single health check.
func TestRestartCatchesASiteThatDiesAfterItsFirstAnswer(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	health.onProbe = func() { env.host.set("app@a", "activating") }

	err := env.Restart(context.Background(), []Site{site})
	if err == nil || !strings.Contains(env.out.String(), "keeps exiting") {
		t.Fatalf("Restart = %v, output:\n%s", err, env.out.String())
	}
}

func TestInstallFailureKeepsTheReleaseAndSaysHowToGoBack(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	if err := env.install(t, "v1", site); err != nil {
		t.Fatal(err)
	}
	first := env.Current()
	env.now = env.now.Add(time.Minute)
	health.set(false)

	if err := env.install(t, "v2", site); err == nil {
		t.Fatal("install of an unhealthy release succeeded")
	}
	if env.Current() == first {
		t.Error("the failed release was rolled back automatically; migrations may already have run")
	}
	if want := "rollback " + first; !strings.Contains(env.out.String(), want) {
		t.Errorf("output does not offer %q:\n%s", want, env.out.String())
	}

	health.set(true)
	if err := env.Rollback(context.Background(), "", []Site{site}); err != nil {
		t.Fatal(err)
	}
	if env.Current() != first || readFile(t, env.binary()) != "\x7fELFv1" {
		t.Errorf("after rollback current = %s, want %s", env.Current(), first)
	}
	if err := env.Rollback(context.Background(), "", nil); err == nil {
		t.Error("rollback past the oldest release succeeded")
	}
}

func TestAddSiteProvisionsAPrivateSite(t *testing.T) {
	env := newTestEnv(t)
	if err := os.MkdirAll(filepath.Join(env.SitesBase, "example.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := SiteSpec{Domain: "example.com", User: currentUser(t)}
	if err := env.AddSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(env.SitesBase, "example.com", "app")
	modes := map[string]os.FileMode{"": 0o750, ".env": 0o600, "backups": 0o700, "data": 0o755, "uploads": 0o755, "logs": 0o755, "custom/themes": 0o755}
	for sub, want := range modes {
		info, err := os.Stat(filepath.Join(dir, sub))
		if err != nil {
			t.Errorf("%s: %v", sub, err)
		} else if info.Mode().Perm() != want {
			t.Errorf("%s has mode %o, want %o", sub, info.Mode().Perm(), want)
		}
	}

	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, filepath.Join(dir, ".env"))), "\n") {
		key, value, _ := strings.Cut(line, "=")
		values[key] = value
	}
	if values["PORT"] != "8081" {
		t.Errorf("PORT = %q, want the base port", values["PORT"])
	}
	if len(values["SECRET"]) < 40 || values["SECRET"] == values["OTHER"] || len(values["SEED"]) != 48 {
		t.Errorf("secrets are not distinct random values: %v", values)
	}

	dropIn := readFile(t, filepath.Join(env.SystemdDir, "app@example_com.service.d", "instance.conf"))
	if !strings.Contains(dropIn, "WorkingDirectory="+dir) || !strings.Contains(dropIn, "User="+spec.User) {
		t.Errorf("drop-in:\n%s", dropIn)
	}
	if site, err := env.Site("example_com"); err != nil || site.Dir != dir || site.Port != 8081 {
		t.Errorf("registry entry = %v, %v", site, err)
	}
	logrotate := readFile(t, env.LogrotateFile)
	if !strings.Contains(logrotate, dir+"/logs/*.log") || !strings.Contains(logrotate, "su "+spec.User+" "+env.DefaultGroup) {
		t.Errorf("logrotate:\n%s", logrotate)
	}
	if !slices.Contains(env.host.systemctlCalls(), "daemon-reload") {
		t.Error("systemd was not reloaded")
	}
	if !strings.Contains(env.out.String(), "proxy_pass http://127.0.0.1:8081;") {
		t.Errorf("no proxy snippet in the output:\n%s", env.out.String())
	}

	// The next site gets the next port; clashes and escapes are refused.
	if err := os.MkdirAll(filepath.Join(env.SitesBase, "second.example.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := env.AddSite(context.Background(), SiteSpec{Domain: "second.example.com", User: spec.User}); err != nil {
		t.Fatal(err)
	}
	if site, _ := env.Site("second_example_com"); site.Port != 8082 {
		t.Errorf("second site got port %d, want 8082", site.Port)
	}
	if err := os.MkdirAll(filepath.Join(env.SitesBase, "third.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]SiteSpec{
		"registered twice":       spec,
		"port in use":            {Domain: "third.com", User: spec.User, Port: 8082},
		"directory outside base": {Domain: "third.com", User: spec.User, Dir: filepath.Join(env.SitesBase, "third.com", "..", "elsewhere")},
		"unknown user":           {Domain: "third.com", User: "no-such-user-here"},
		"bad domain":             {Domain: "Third.com;x", User: spec.User},
	} {
		if err := env.AddSite(context.Background(), bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := os.Stat(filepath.Join(env.SitesBase, "third.com", "app")); err == nil {
		t.Error("a refused site left a directory behind")
	}
}

func TestRemoveSiteLeavesTheData(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	dropIn := filepath.Join(env.SystemdDir, "app@a.service.d")
	if err := os.MkdirAll(dropIn, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := env.RemoveSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Site("a"); err == nil {
		t.Error("site is still registered")
	}
	if _, err := os.Stat(dropIn); err == nil {
		t.Error("drop-in is still there")
	}
	if _, err := os.Stat(site.Dir); err != nil {
		t.Errorf("site data was removed: %v", err)
	}
	if !slices.Contains(env.host.systemctlCalls(), "disable --now app@a") {
		t.Errorf("unit was not disabled: %v", env.host.systemctlCalls())
	}
}

func TestInitInstallsUnitAndKeepsAnExistingCronFile(t *testing.T) {
	env := newTestEnv(t)
	if err := env.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(env.SystemdDir, "app@.service")); got != string(env.Unit) {
		t.Errorf("unit = %q", got)
	}
	cron := readFile(t, env.CronFile)
	if !strings.Contains(cron, env.ctl()+" backup --all") || !strings.Contains(cron, env.ctl()+" check --all") {
		t.Errorf("cron file:\n%s", cron)
	}
	if err := os.WriteFile(env.CronFile, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := env.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, env.CronFile); got != "# mine\n" {
		t.Errorf("init replaced an edited cron file with:\n%s", got)
	}
}

// createDB makes a SQLite database with a sessions table holding one row.
func createDB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		"PRAGMA journal_mode = WAL",
		"CREATE TABLE sessions (token TEXT)",
		"CREATE TABLE pages (title TEXT)",
		"INSERT INTO sessions VALUES ('secret-session-token')",
		"INSERT INTO pages VALUES ('Hello')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func queryInt(t *testing.T, path, query string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// A unit systemd gave up on is reported at once, not after the full wait.
func TestRestartDoesNotWaitForAUnitThatIsGone(t *testing.T) {
	env := newTestEnv(t)
	site, health := env.addSite(t, "a")
	health.set(false)
	health.onProbe = func() { env.host.set("app@a", "failed") }
	env.HealthTimeout = time.Hour

	done := make(chan error, 1)
	go func() { done <- env.Restart(context.Background(), []Site{site}) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(env.out.String(), "unit is failed") {
			t.Errorf("Restart = %v, output:\n%s", err, env.out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Restart kept waiting for a failed unit")
	}
}
