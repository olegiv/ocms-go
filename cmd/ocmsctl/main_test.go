// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"database/sql"
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
	"syscall"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/olegiv/ocms-go/internal/sitectl"
)

// world is a development machine and a server joined by a fake ssh: an ssh
// call on the machine runs the real server command line in this process, so
// the tests cover the arguments and streams that cross the wire.
type world struct {
	local, server *sitectl.App
	out           *bytes.Buffer
	systemctl     []string
	rsync         []string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := t.TempDir()
	w := &world{out: &bytes.Buffer{}}
	euid = func() int { return 0 }
	t.Cleanup(func() { euid = os.Geteuid })

	group, err := user.LookupGroupId(strconv.Itoa(os.Getgid()))
	if err != nil {
		t.Fatal(err)
	}
	w.server = newApp()
	w.server.Root = filepath.Join(root, "opt")
	w.server.ConfDir = filepath.Join(root, "etc")
	w.server.SystemdDir = filepath.Join(root, "systemd")
	w.server.CronFile = filepath.Join(root, "cron")
	w.server.LogrotateFile = filepath.Join(root, "logrotate")
	w.server.StateDir = filepath.Join(root, "run")
	w.server.SitesBase = filepath.Join(root, "vhosts")
	w.server.DefaultGroup = group.Name
	w.server.HealthTimeout, w.server.Settle = 0, 0
	w.server.Stdout, w.server.Stderr = w.out, w.out
	w.server.Exec = func(_ context.Context, c sitectl.Cmd) error {
		if c.Name != "systemctl" {
			return fmt.Errorf("unexpected command on the server: %s", c)
		}
		if c.Args[0] == "is-active" {
			_, _ = fmt.Fprintln(c.Stdout, "active")
			return nil
		}
		w.systemctl = append(w.systemctl, strings.Join(c.Args, " "))
		return nil
	}

	w.local = newApp()
	w.local.Root = w.server.Root // where the server's ocmsctl lives
	w.local.Stdout, w.local.Stderr = w.out, w.out
	w.local.Exec = func(ctx context.Context, c sitectl.Cmd) error {
		switch c.Name {
		case "ssh":
			if !slices.Equal(c.Args[:3], []string{"-C", "root@server", filepath.Join(w.server.Root, "bin", "ocmsctl")}) {
				return fmt.Errorf("unexpected ssh call: %s", c)
			}
			server := *w.server
			server.Stdout = c.Stdout
			return run(ctx, &server, c.Args[3:], c.Stdin)
		case "rsync":
			w.rsync = append(w.rsync, strings.Join(c.Args, " "))
			return nil
		}
		return fmt.Errorf("unexpected command on the machine: %s", c)
	}
	for _, dir := range []string{w.server.SystemdDir, filepath.Join(w.server.SitesBase, "example.com")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// addSite provisions example.com on the server, answering health checks.
func (w *world) addSite(t *testing.T) (dir string) {
	t.Helper()
	health := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(health.Close)
	u, err := url.Parse(health.URL)
	if err != nil {
		t.Fatal(err)
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), w.server, []string{"site", "add", "example.com", me.Username, "--port", u.Port()}, nil); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(w.server.SitesBase, "example.com", "ocms")
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSiteAddWritesAStartableProductionConfig(t *testing.T) {
	w := newWorld(t)
	dir := w.addSite(t)

	env := map[string]string{}
	for _, line := range strings.Split(read(t, filepath.Join(dir, ".env")), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			env[key] = value
		}
	}
	secrets := []string{env["OCMS_SESSION_SECRET"], env["OCMS_MFA_KEY"], env["OCMS_EMBED_PROXY_TOKEN"], env["OCMS_SEED_ADMIN_PASSWORD"]}
	slices.Sort(secrets)
	if len(slices.Compact(secrets)) != 4 || len(secrets[0]) < 32 {
		t.Errorf("the four secrets are not distinct values of 32+ characters: %v", secrets)
	}
	for key, want := range map[string]string{
		"OCMS_ENV":                         "production",
		"OCMS_SERVER_HOST":                 "127.0.0.1",
		"OCMS_DO_SEED":                     "false",
		"OCMS_REQUIRE_SEED_ADMIN_PASSWORD": "true",
		"OCMS_DB_PATH":                     "./data/ocms.db",
		"OCMS_EMBED_ALLOWED_ORIGINS":       "https://example.com",
	} {
		if env[key] != want {
			t.Errorf("%s = %q, want %q", key, env[key], want)
		}
	}

	dropIn := read(t, filepath.Join(w.server.SystemdDir, "ocms@example_com.service.d", "instance.conf"))
	for _, want := range []string{"EnvironmentFile=" + dir + "/.env", "ReadWritePaths=" + dir, "StandardOutput=append:" + dir + "/logs/ocms.log"} {
		if !strings.Contains(dropIn, want) {
			t.Errorf("drop-in lacks %q:\n%s", want, dropIn)
		}
	}
	// nginx variables must survive the template untouched.
	for _, want := range []string{"proxy_pass http://127.0.0.1:" + env["OCMS_SERVER_PORT"] + ";", "proxy_set_header Host $host;", `location ~ /\. {`} {
		if !strings.Contains(w.out.String(), want) {
			t.Errorf("nginx snippet lacks %q:\n%s", want, w.out.String())
		}
	}
}

func TestDeployInstallsOverSSHAndRestartsBehindTheHealthGate(t *testing.T) {
	w := newWorld(t)
	dir := w.addSite(t)
	t.Chdir(t.TempDir())
	for path, content := range map[string]string{"bin/ocms-linux-amd64": "\x7fELFnew build", "custom/themes/mine/theme.json": "{}"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w.systemctl = nil

	// Flags may follow the positional arguments.
	if err := run(context.Background(), w.local, []string{"deploy", "root@server", "example_com", "--no-build", "--themes"}, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out.String())
	}
	if got := read(t, filepath.Join(w.server.Root, "bin", "ocms")); got != "\x7fELFnew build" {
		t.Errorf("the server runs %q", got)
	}
	if got := read(t, filepath.Join(dir, "custom", "themes", "mine", "theme.json")); got != "{}" {
		t.Errorf("theme on the server = %q", got)
	}
	if want := []string{"stop ocms@example_com", "start ocms@example_com"}; !slices.Equal(w.systemctl, want) {
		t.Errorf("systemctl calls = %v, want %v", w.systemctl, want)
	}

	// A corrupted transfer is refused by the server and nothing restarts.
	w.systemctl = nil
	exec := w.local.Exec
	w.local.Exec = func(ctx context.Context, c sitectl.Cmd) error {
		if c.Stdin != nil {
			c.Stdin = strings.NewReader("\x7fELFtampered")
		}
		return exec(ctx, c)
	}
	err := run(context.Background(), w.local, []string{"deploy", "root@server", "example_com", "--no-build"}, nil)
	if err == nil || len(w.systemctl) != 0 {
		t.Errorf("tampered upload: err = %v, systemctl calls %v", err, w.systemctl)
	}
	if got := read(t, filepath.Join(w.server.Root, "bin", "ocms")); got != "\x7fELFnew build" {
		t.Errorf("after a refused upload the server runs %q", got)
	}
}

// The real scrub policy must clear what a development copy must not carry.
func TestPullDeliversAScrubbedSnapshot(t *testing.T) {
	w := newWorld(t)
	dir := w.addSite(t)
	db, err := sql.Open("sqlite", filepath.Join(dir, "data", "ocms.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE sessions (token TEXT)",
		"INSERT INTO sessions VALUES ('live-session')",
		"CREATE TABLE webhooks (url TEXT, secret TEXT, headers TEXT, is_active INTEGER)",
		"INSERT INTO webhooks VALUES ('https://hooks.example.com/x', 'signing-secret', '{\"A\":\"b\"}', 1)",
		"CREATE TABLE pages (title TEXT)",
		"INSERT INTO pages VALUES ('Hello')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir())
	if err := run(context.Background(), w.local, []string{"pull", "root@server", "example_com", "--yes", "--port", "0"}, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out.String())
	}

	local, err := sql.Open("sqlite", "data/ocms.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()
	var sessions, pages, active int
	var hookURL, secret string
	if err := local.QueryRow("SELECT (SELECT count(*) FROM sessions), (SELECT count(*) FROM pages)").Scan(&sessions, &pages); err != nil {
		t.Fatal(err)
	}
	if err := local.QueryRow("SELECT url, secret, is_active FROM webhooks").Scan(&hookURL, &secret, &active); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || hookURL != "" || secret != "" || active != 0 {
		t.Errorf("local copy keeps sessions=%d webhook url=%q secret=%q active=%d", sessions, hookURL, secret, active)
	}
	if pages != 1 {
		t.Errorf("local copy has %d pages, want the content", pages)
	}
	if raw := read(t, "data/ocms.db"); strings.Contains(raw, "signing-secret") || strings.Contains(raw, "live-session") {
		t.Error("scrubbed values are still in the database file")
	}
	if leftovers, _ := filepath.Glob("data/.pull-*"); len(leftovers) > 0 {
		t.Errorf("left behind %v", leftovers)
	}
	want := []string{
		"-az --delete root@server:" + dir + "/uploads/ uploads/",
		"-az root@server:" + dir + "/logs/ logs/",
	}
	if !slices.Equal(w.rsync, want) {
		t.Errorf("rsync calls = %v, want %v", w.rsync, want)
	}
}

func TestServerCommandsNeedRoot(t *testing.T) {
	w := newWorld(t)
	w.addSite(t)
	euid = func() int { return 1000 }
	for _, args := range [][]string{
		{"init"}, {"site", "add", "other.com", "nobody"}, {"site", "remove", "example_com"},
		{"start", "example_com"}, {"stop", "--all"}, {"restart", "example_com"},
		{"install", "--sha256", strings.Repeat("0", 64)}, {"rollback", "--all"}, {"check"},
	} {
		w.systemctl = nil
		err := run(context.Background(), w.server, args, strings.NewReader(""))
		if err == nil || !strings.Contains(err.Error(), "root") || len(w.systemctl) != 0 {
			t.Errorf("%v as a normal user: err = %v, systemctl calls %v", args, err, w.systemctl)
		}
	}
	if err := run(context.Background(), w.server, []string{"frobnicate"}, nil); err == nil {
		t.Error("an unknown command succeeded")
	}
	euid = func() int { return 0 }
	if err := run(context.Background(), w.server, []string{"restart"}, nil); err == nil || len(w.systemctl) != 0 {
		t.Errorf("restart with no site: err = %v, systemctl calls %v", err, w.systemctl)
	}
}

// TestMain lets the test binary stand in for ocmsctl when a root ocmsctl
// re-executes itself as a site user (sitectl.SiteOpCommand).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == sitectl.SiteOpCommand {
		main()
		return
	}
	os.Exit(m.Run())
}

// Root must not write into a site directory itself: a site user who swaps
// backups/ or custom/themes for a symlink would aim root's write anywhere.
func TestSiteOperationsRunAsTheSiteUser(t *testing.T) {
	nobody, err := user.Lookup("nobody")
	if os.Geteuid() != 0 || err != nil {
		t.Skip("needs root and a nobody account")
	}
	uid, _ := strconv.Atoi(nobody.Uid)
	gid, _ := strconv.Atoi(nobody.Gid)

	w := newWorld(t)
	w.server.Exec = sitectl.RunCmd
	base := t.TempDir()
	// The test directories are root's alone by default.
	for _, dir := range []string{filepath.Dir(base), base} {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Install this test binary as the server's ocmsctl (see TestMain).
	w.server.Root = filepath.Join(base, "opt")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(w.server.Root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.server.Root, "bin", "ocmsctl"), program, 0o755); err != nil {
		t.Fatal(err)
	}
	site := filepath.Join(base, "site")
	rootOnly := filepath.Join(base, "root-only")
	for dir, mode := range map[string]os.FileMode{filepath.Join(site, "data"): 0o755, rootOnly: 0o700} {
		if err := os.MkdirAll(dir, mode); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(site, "data", "ocms.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE pages (title TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	err = filepath.Walk(site, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.server.ConfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := fmt.Sprintf("site %s nobody 8081\n", site)
	if err := os.WriteFile(filepath.Join(w.server.ConfDir, "sites.conf"), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run(context.Background(), w.server, []string{"backup", "site"}, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out.String())
	}
	backups, err := filepath.Glob(filepath.Join(site, "backups", "ocms_*.db.gz"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v", backups, err)
	}
	info, err := os.Stat(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if owner := info.Sys().(*syscall.Stat_t).Uid; int(owner) != uid || info.Mode().Perm() != 0o600 {
		t.Errorf("backup is owned by %d with mode %o, want nobody and 600", owner, info.Mode().Perm())
	}

	// The site user redirects backups/ at a directory only root may write.
	if err := os.RemoveAll(filepath.Join(site, "backups")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rootOnly, filepath.Join(site, "backups")); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), w.server, []string{"backup", "site"}, nil); err == nil {
		t.Error("backup through a redirected backups/ succeeded")
	}
	if leaked, _ := os.ReadDir(rootOnly); len(leaked) != 0 {
		t.Errorf("root wrote %d entries where the site user pointed it", len(leaked))
	}
}
