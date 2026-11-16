// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/sitectl"
)

func TestDryRunDoesNotStopSites(t *testing.T) {
	w := newWorld(t)
	w.addSite(t)
	w.systemctl = nil
	err := run(context.Background(), w.server, []string{"stop", "--all", "--dry-run"}, nil)
	if len(w.systemctl) != 0 {
		t.Fatalf("dry-run executed mutations: %v (err=%v)", w.systemctl, err)
	}
}

func TestSnapshotUsesConfiguredSiteDB(t *testing.T) {
	w := newWorld(t)
	dir := w.addSite(t)
	db, err := sql.Open("sqlite", filepath.Join(dir, "data", "tenant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE pages(title TEXT); INSERT INTO pages VALUES ('current')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.ReplaceAll(data, []byte("OCMS_DB_PATH=./data/ocms.db"), []byte("OCMS_DB_PATH=./data/tenant.db"))
	if err := os.WriteFile(envPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), w.server, []string{"snapshot", "example_com"}, nil); err != nil {
		t.Fatalf("snapshot ignored configured OCMS_DB_PATH: %v", err)
	}
}

func TestPrintedRollbackCommandWorks(t *testing.T) {
	w := newWorld(t)
	w.addSite(t)
	site, err := w.server.Site("example_com")
	if err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	w.server.Now = func() time.Time { return instant }
	install := func(content string) error {
		data := []byte("\x7fELF" + content)
		sum := fmt.Sprintf("%x", sha256.Sum256(data))
		return w.server.Install(context.Background(), bytes.NewReader(data), sum, []sitectl.Site{site})
	}
	if err := install("healthy"); err != nil {
		t.Fatal(err)
	}
	instant = instant.Add(time.Minute)
	original := w.server.Exec
	w.server.Exec = func(ctx context.Context, c sitectl.Cmd) error {
		if c.Name == "systemctl" && c.Args[0] == "start" {
			return fmt.Errorf("start failed")
		}
		return original(ctx, c)
	}
	w.out.Reset()
	if err := install("broken"); err == nil {
		t.Fatal("expected failed install")
	}
	_, command, ok := strings.Cut(w.out.String(), "To go back: ")
	if !ok {
		t.Fatal("no recovery command")
	}
	command = strings.ReplaceAll(strings.TrimSpace(command), "<site>...", site.ID)
	w.server.Exec = original
	if err := run(context.Background(), w.server, strings.Fields(command)[1:], nil); err != nil {
		t.Fatalf("printed recovery command %q fails: %v", command, err)
	}
}

func TestDeployRequiresTargets(t *testing.T) {
	w := newWorld(t)
	w.addSite(t)
	binary := filepath.Join(t.TempDir(), "ocms")
	if err := os.WriteFile(binary, []byte("\x7fELFreview"), 0600); err != nil {
		t.Fatal(err)
	}
	w.systemctl = nil
	err := run(context.Background(), w.local, []string{"deploy", "root@server", "--no-build", "--binary", binary}, strings.NewReader(""))
	if err == nil {
		t.Fatalf("targetless deploy succeeded; systemctl=%v; output=%s", w.systemctl, w.out.String())
	}
	if _, statErr := os.Stat(filepath.Join(w.server.Root, "bin", "ocms")); statErr == nil {
		t.Fatal("targetless deploy installed a binary before rejecting arguments")
	}
}

func TestServerMutationsRejectDryRun(t *testing.T) {
	for _, args := range [][]string{
		{"init"}, {"site", "remove", "example_com"}, {"site", "add", "other.com", "root"},
		{"stop", "--all"}, {"start", "--all"}, {"restart", "--all"},
		{"rollback", "--all"}, {"install", "--all"}, {"themes", "example_com"}, {"backup"}, {"check"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			w := newWorld(t)
			w.addSite(t)
			w.systemctl = nil
			args = append(args, "--dry-run")
			err := run(context.Background(), w.server, args, strings.NewReader(""))
			if err == nil || !strings.Contains(err.Error(), "dry-run") {
				t.Fatalf("expected unsupported dry-run error: %v", err)
			}
			if len(w.systemctl) != 0 {
				t.Fatalf("executed %v", w.systemctl)
			}
			if _, err := w.server.Site("example_com"); err != nil {
				t.Fatalf("registry changed: %v", err)
			}
		})
	}
}

func TestPullUsesConfiguredRemoteAndLocalDB(t *testing.T) {
	for _, absolute := range []bool{false, true} {
		t.Run(fmt.Sprint("absolute=", absolute), func(t *testing.T) {
			w := newWorld(t)
			dir := w.addSite(t)
			custom := filepath.Join(dir, "data", "tenant.db")
			for path, title := range map[string]string{custom: "current", filepath.Join(dir, "data", "ocms.db"): "stale"} {
				db, err := sql.Open("sqlite", "file:"+path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("CREATE TABLE pages(title TEXT); INSERT INTO pages VALUES (?)", title); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			selected := "./data/tenant.db"
			if absolute {
				selected = custom
			}
			if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(fmt.Sprintf("OCMS_DB_PATH='%s'\n", selected)), 0600); err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(t.TempDir(), "local.db")
			err := run(context.Background(), w.local, []string{"pull", "root@server", "example_com", "--yes", "--port", "0", "--no-uploads", "--no-logs", "--db", dst}, nil)
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", dst)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var title string
			if err := db.QueryRow("SELECT title FROM pages").Scan(&title); err != nil {
				t.Fatal(err)
			}
			if title != "current" {
				t.Fatalf("pulled %q database", title)
			}
		})
	}
}

func TestSiteMakePullUsesConfiguredDatabase(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, dbPath := range []string{"data/tenant.db", "data/ocms.db"} {
		t.Run(dbPath, func(t *testing.T) {
			site := t.TempDir()
			if err := os.WriteFile(filepath.Join(site, ".env"), []byte("OCMS_DB_PATH="+dbPath+"\nOCMS_SERVER_PORT=8397\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("make", "-n", "-f", filepath.Join(root, "site.mk"), "SITE_DIR="+site, "CORE_DIR="+root, "DEPLOY_HOST=root@server", "SITE=example_com", "VERSION=test", "GIT_COMMIT=test", "BUILD_TIME=test", "pull")
			cmd.Dir = site
			cmd.Env = []string{"PATH=/usr/local/go/bin:/usr/bin:/bin"}
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make pull: %v: %s", err, output)
			}
			want := `--port 8397 --db "` + filepath.Join(site, dbPath) + `"`
			if !strings.Contains(string(output), want) {
				t.Fatalf("missing %s in %s", want, output)
			}
		})
	}
}
