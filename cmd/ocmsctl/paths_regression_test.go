// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"database/sql"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/sitectl"
)

// serverDB gives the site a database holding one page titled title.
func serverDB(t *testing.T, path, title string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE pages(title TEXT); INSERT INTO pages VALUES (?)", title); err != nil {
		t.Fatal(err)
	}
}

func pageTitle(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no database at %s: %v", path, err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var title string
	if err := db.QueryRow("SELECT title FROM pages").Scan(&title); err != nil {
		t.Fatal(err)
	}
	return title
}

func writeTree(t *testing.T, base string, files map[string]string) {
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

// pull without --db or --port uses the database and port this checkout's
// server uses: its environment, then the env file, then the defaults.
func TestPullFollowsTheCheckoutConfiguration(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	busy := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	for _, tc := range []struct {
		name    string
		env     map[string]string // process environment
		files   map[string]string
		args    []string
		want    string // database the snapshot lands in
		failure string // or the error it fails with
	}{
		{name: "port from .env", files: map[string]string{".env": "OCMS_SERVER_PORT=" + busy + "\n"}, failure: "port " + busy},
		{name: "port from the environment", env: map[string]string{"OCMS_SERVER_PORT": busy}, files: map[string]string{".env": "OCMS_SERVER_PORT=1\n"}, failure: "port " + busy},
		{name: "--port 0 skips the check", files: map[string]string{".env": "OCMS_SERVER_PORT=" + busy + "\n"}, args: []string{"--port", "0"}, want: "data/ocms.db"},
		{name: "database from .env", files: map[string]string{".env": "OCMS_DB_PATH=./db/dev.db\n"}, want: "db/dev.db"},
		{name: "environment over .env", env: map[string]string{"OCMS_DB_PATH": "env.db"}, files: map[string]string{".env": "OCMS_DB_PATH=./db/dev.db\n"}, want: "env.db"},
		{name: "--db over both", env: map[string]string{"OCMS_DB_PATH": "env.db"}, args: []string{"--db", "flag.db"}, want: "flag.db"},
		{name: "OCMS_ENV_FILE", env: map[string]string{"OCMS_ENV_FILE": "conf/site.env"}, files: map[string]string{".env": "OCMS_DB_PATH=dot.db\n", "conf/site.env": "OCMS_DB_PATH=named.db\n"}, want: "named.db"},
		{name: "missing OCMS_ENV_FILE", env: map[string]string{"OCMS_ENV_FILE": "conf/missing.env"}, failure: "missing.env"},
		{name: "unreadable .env", files: map[string]string{".env": "OCMS_DB_PATH='unterminated\n"}, failure: "local configuration"},
		{name: "not a port", files: map[string]string{".env": "OCMS_SERVER_PORT=web\n"}, failure: `OCMS_SERVER_PORT="web" is not a port`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			serverDB(t, filepath.Join(w.addSite(t), "data", "ocms.db"), "production")
			t.Chdir(t.TempDir())
			writeTree(t, ".", tc.files)
			for key, value := range tc.env {
				w.environ[key] = value
			}
			args := append([]string{"pull", "root@server", "example_com", "--yes", "--no-uploads", "--no-logs"}, tc.args...)
			err := run(context.Background(), w.local, args, nil)
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("pull = %v, want an error about %s", err, tc.failure)
				}
				if dbs, _ := filepath.Glob("*/*.db"); len(dbs) > 0 {
					t.Errorf("a refused pull wrote %v", dbs)
				}
				return
			}
			if err != nil {
				t.Fatalf("%v\n%s", err, w.out)
			}
			if got := pageTitle(t, tc.want); got != "production" {
				t.Errorf("%s holds %q", tc.want, got)
			}
		})
	}
}

// Each side's paths come from its own configuration: the server's .env as
// the site user reads it, and this checkout's.
func TestPullUsesEachSidesConfiguredPaths(t *testing.T) {
	w := newWorld(t)
	dir := w.addSite(t)
	shared := filepath.Join(filepath.Dir(dir), "shared-custom")
	writeTree(t, dir, map[string]string{".env": "OCMS_UPLOADS_DIR=./media\nOCMS_CUSTOM_DIR=" + shared + "\n"})
	t.Chdir(t.TempDir())
	writeTree(t, ".", map[string]string{".env": "OCMS_UPLOADS_DIR=./local-media\nOCMS_CUSTOM_DIR=./lc\n", "local-media/kept.jpg": "x"})
	// An older pull copied links; the mirror drops them before rsync,
	// which would keep them.
	if err := os.Symlink("../.env", filepath.Join("local-media", "stale.jpg")); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), w.local, []string{"pull", "root@server", "example_com", "--yes", "--no-db", "--themes"}, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	if _, err := os.Lstat(filepath.Join("local-media", "stale.jpg")); !os.IsNotExist(err) {
		t.Errorf("a link in the uploads survived the pull: %v", err)
	}
	if got := read(t, filepath.Join("local-media", "kept.jpg")); got != "x" {
		t.Errorf("the cleanup touched a regular file: %q", got)
	}
	send := "--rsync-path=" + filepath.Join(w.server.Root, "bin", "ocmsctl") + " _send example_com "
	want := []string{
		"-rtz --checksum --delete " + send + "root@server:" + filepath.Join(dir, "media") + "/ " + filepath.Join(cwd, "local-media") + "/",
		"-rtz " + send + "root@server:" + filepath.Join(dir, "logs") + "/ " + filepath.Join(cwd, "logs") + "/",
		"-rtz " + send + "root@server:" + filepath.Join(shared, "themes") + "/ " + filepath.Join(cwd, "lc", "themes") + "/",
	}
	if !slices.Equal(w.rsync, want) {
		t.Errorf("rsync calls:\n%s\nwant:\n%s", strings.Join(w.rsync, "\n"), strings.Join(want, "\n"))
	}

	// A path that could not cross ssh as one word stops the pull, by name.
	w.rsync = nil
	writeTree(t, dir, map[string]string{".env": "OCMS_UPLOADS_DIR='./my media'\n"})
	err = run(context.Background(), w.local, []string{"pull", "root@server", "example_com", "--yes", "--no-db"}, nil)
	if err == nil || !strings.Contains(err.Error(), "OCMS_UPLOADS_DIR") || len(w.rsync) != 0 {
		t.Errorf("unsafe server path: err = %v, rsync %v", err, w.rsync)
	}
	if strings.Contains(err.Error(), "server-setup") {
		t.Errorf("an unsafe path is no reason to update the server: %v", err)
	}
}

// The uploads mirror deletes what production lacks, so it must not be aimed
// at the checkout or at what it keeps.
func TestPullRefusesAnUploadsMirrorOverTheCheckout(t *testing.T) {
	for _, uploads := range []string{".", "..", "./data", "/", "./custom/..", "./linked"} {
		t.Run(uploads, func(t *testing.T) {
			w := newWorld(t)
			w.addSite(t)
			t.Chdir(t.TempDir())
			writeTree(t, ".", map[string]string{".env": "OCMS_UPLOADS_DIR=" + uploads + "\n"})
			if err := os.Symlink(".", "linked"); err != nil { // linked/ is the checkout
				t.Fatal(err)
			}
			contacted := false
			exec := w.local.Exec
			w.local.Exec = func(ctx context.Context, c sitectl.Cmd) error {
				contacted = true
				return exec(ctx, c)
			}
			err := run(context.Background(), w.local, []string{"pull", "root@server", "example_com", "--yes", "--port", "0"}, nil)
			if err == nil || !strings.Contains(err.Error(), "would delete") || contacted {
				t.Errorf("OCMS_UPLOADS_DIR=%s: err = %v, contacted the server: %v", uploads, err, contacted)
			}
		})
	}
}

// installTestCtl makes this test binary the server's ocmsctl (see TestMain).
func installTestCtl(t *testing.T, root string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	writeTree(t, filepath.Join(root, "bin"), map[string]string{"ocmsctl": string(program)})
	if err := os.Chmod(filepath.Join(root, "bin", "ocmsctl"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// realRsync has the machine run rsync for real, with an ssh stand-in that
// runs the remote command here: the server's ocmsctl _send, then rsync.
func (w *world) realRsync(t *testing.T) {
	t.Helper()
	installTestCtl(t, w.server.Root)
	t.Setenv("OCMSCTL_TEST_CONF", w.server.ConfDir)
	ssh := filepath.Join(t.TempDir(), "ssh")
	script := "#!/bin/sh\n# Skip the options and the host, run the command here.\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in -l|-p|-o|-i) shift 2 ;; -*) shift ;; *) shift; break ;; esac; done\n" +
		"exec /bin/sh -c \"$*\"\n"
	if err := os.WriteFile(ssh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := w.local.Exec // the ssh calls of the server's ocmsctl
	w.local.Exec = func(ctx context.Context, c sitectl.Cmd) error {
		if c.Name != "rsync" {
			return fake(ctx, c)
		}
		c.Args = append([]string{"-e", ssh}, c.Args...)
		c.Stdout, c.Stderr = w.out, w.out
		return sitectl.RunCmd(ctx, c)
	}
}

// Real rsync, as the operator's machine runs it: openrsync on macOS, rsync
// elsewhere. Production's links never arrive, links an older pull left in
// the uploads are dropped, and a local file that differs only in content is
// replaced.
func TestPullCopiesOnlyFilesAndDirectories(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync is not installed")
	}
	w := newWorld(t)
	dir := w.addSite(t)
	w.realRsync(t)

	same := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	writeTree(t, dir, map[string]string{"uploads/a.jpg": "production", "uploads/sub/b.jpg": "b", "logs/app.log": "log"})
	for link, target := range map[string]string{"uploads/leak.jpg": "../.env", "uploads/sub/up": "../..", "logs/leak.log": "../.env"} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(t.TempDir())
	writeTree(t, ".", map[string]string{".env": "DEVELOPER_SECRET=1\n", "uploads/a.jpg": "local copy", "elsewhere/keep": "x"})
	for link, target := range map[string]string{"uploads/stale.jpg": "../.env", "uploads/sub": "../elsewhere"} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(dir, "uploads", "a.jpg"), "uploads/a.jpg"} {
		if err := os.Chtimes(path, same, same); err != nil {
			t.Fatal(err)
		}
	}

	if err := run(context.Background(), w.local, []string{"pull", "root@server", "example_com", "--yes", "--no-db"}, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	if got := read(t, "uploads/a.jpg"); got != "production" {
		t.Errorf("uploads/a.jpg = %q: same size and time, other content", got)
	}
	if got := read(t, "uploads/sub/b.jpg"); got != "b" {
		t.Errorf("uploads/sub/b.jpg = %q", got)
	}
	if entries, _ := os.ReadDir("elsewhere"); len(entries) != 1 {
		t.Errorf("the pull wrote through a link into elsewhere/: %v", entries)
	}
	for _, tree := range []string{"uploads", "logs"} {
		err := filepath.WalkDir(tree, func(path string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && !entry.Type().IsRegular() {
				t.Errorf("%s is a %v after the pull", path, entry.Type())
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// deploy --themes sends the themes this checkout's server would load.
func TestDeployTakesThemesFromTheCheckoutConfiguration(t *testing.T) {
	w := newWorld(t)
	dir := w.addSite(t)
	t.Chdir(t.TempDir())
	writeTree(t, ".", map[string]string{
		".env":                               "OCMS_CUSTOM_DIR=./site-custom\n",
		"site-custom/themes/mine/theme.json": "{}",
		"custom/themes/stale/theme.json":     "{}",
	})
	if err := run(context.Background(), w.local, []string{"deploy", "root@server", "example_com", "--themes-only"}, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	installed, err := os.ReadDir(filepath.Join(dir, "custom", "themes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].Name() != "mine" {
		t.Errorf("installed themes %v, want mine", installed)
	}
}

// ocmsctl reads a site's settings by the keys and defaults the server itself
// uses; a renamed key or a changed default would send backups, pull and logs
// to the wrong place.
func TestSettingsFollowTheServerConfig(t *testing.T) {
	defaults := map[string]string{} // key -> envDefault
	typ := reflect.TypeFor[config.Config]()
	for i := range typ.NumField() {
		key, _, _ := strings.Cut(typ.Field(i).Tag.Get("env"), ",")
		if key != "" {
			defaults[key] = typ.Field(i).Tag.Get("envDefault")
		}
	}
	app := newApp()
	// pull and deploy see the real environment (site.mk passes the paths
	// in it); the tests' worlds replace it.
	t.Setenv("OCMS_SETTINGS_PROBE", "seen")
	if app.LookupEnv == nil {
		t.Fatal("newApp reads no environment")
	}
	if got, _ := app.LookupEnv("OCMS_SETTINGS_PROBE"); got != "seen" {
		t.Errorf("newApp's environment lookup returned %q", got)
	}
	for _, s := range []sitectl.Setting{app.DB, app.Uploads, app.Custom, app.ErrorLog, app.Port} {
		want, ok := defaults[s.Env]
		if !ok {
			t.Errorf("%s: the server reads no %s", s.Name, s.Env)
			continue
		}
		if want != "" {
			want = filepath.Clean(want)
		}
		if s.Default != want {
			t.Errorf("%s: default %q, the server's is %q", s.Env, s.Default, want)
		}
	}
	source, err := os.ReadFile(filepath.Join("..", "ocms", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `os.Getenv("`+app.EnvFileEnv+`")`) {
		t.Errorf("cmd/ocms does not read its env file from %s", app.EnvFileEnv)
	}
}
