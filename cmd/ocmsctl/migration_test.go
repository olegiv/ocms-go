// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/sitectl"
)

func TestDeploymentMakeTargetsUseGoCLI(t *testing.T) {
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"Makefile", "site.mk"} {
		for _, command := range []string{"deploy", "pull", "server-setup", "deploy-binary"} {
			if file == "site.mk" && command == "server-setup" {
				continue
			} // server setup belongs to the core checkout
			t.Run(file+"/"+command, func(t *testing.T) {
				site := t.TempDir()
				cmd := exec.Command(makeBin, "-n", "-f", filepath.Join(root, file), "SITE_DIR="+site, "CORE_DIR="+root,
					"BUILD_DIR="+filepath.Join(site, "bin"), "VERSION=test", "GIT_COMMIT=test", "BUILD_TIME=test",
					"HOST=root@server", "SITES=example_com", "SITE=example_com", "DEPLOY_HOST=root@server", "DEPLOY_SITES=example_com", "SERVER_ARCH=arm64", command)
				cmd.Dir = site
				out, err := cmd.CombinedOutput()
				if command == "deploy-binary" {
					if err == nil || !strings.Contains(string(out), "No rule to make target") {
						t.Fatalf("retired target still works: %v\n%s", err, out)
					}
					return
				}
				if err != nil {
					t.Fatalf("make %s: %v\n%s", command, err, out)
				}
				verb := command
				if command == "server-setup" {
					verb = "bootstrap"
				}
				if want := "/ocmsctl"; !strings.Contains(string(out), want) || !strings.Contains(string(out), verb+" root@server") {
					t.Fatalf("not routed to Go CLI: %s", out)
				}
				if strings.Contains(string(out), ".sh") {
					t.Fatalf("deployment still invokes shell scripts: %s", out)
				}
				if command != "pull" && !strings.Contains(string(out), "--arch arm64") {
					t.Fatalf("server architecture omitted: %s", out)
				}
				// A site's deploy and pull get its paths as make resolved
				// them, and no env file: this site has none, and neither
				// command needs one.
				if file == "site.mk" {
					want := `OCMS_UPLOADS_DIR="` + filepath.Join(site, "uploads") + `"`
					if !strings.Contains(string(out), want) || strings.Contains(string(out), "OCMS_ENV_FILE") {
						t.Fatalf("want %s and no OCMS_ENV_FILE in: %s", want, out)
					}
				}
			})
		}
	}
}

func TestBootstrapInstallsGoToolAndServerConfiguration(t *testing.T) {
	for _, failTransfer := range []bool{false, true} {
		name := "success"
		if failTransfer {
			name = "failed upload"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			payload := []byte("\x7fELFfixture-tool")
			binary := filepath.Join(t.TempDir(), "ocmsctl-linux-arm64")
			if err := os.WriteFile(binary, payload, 0600); err != nil {
				t.Fatal(err)
			}
			original := w.local.Exec
			var uploaded []byte
			w.local.Exec = func(ctx context.Context, c sitectl.Cmd) error {
				if c.Name == "ssh" && len(c.Args) == 3 && strings.HasPrefix(c.Args[2], "set -e;") {
					var err error
					uploaded, err = io.ReadAll(c.Stdin)
					if err != nil {
						return err
					}
					if failTransfer {
						return errors.New("transfer interrupted")
					}
					return nil
				}
				return original(ctx, c)
			}
			err := run(context.Background(), w.local, []string{"bootstrap", "root@server", "--ctl", binary, "--arch", "arm64"}, nil)
			unit := filepath.Join(w.server.SystemdDir, "ocms@.service")
			if failTransfer {
				if err == nil {
					t.Fatal("failed bootstrap reported success")
				}
				if _, err := os.Stat(unit); !os.IsNotExist(err) {
					t.Fatalf("failed upload initialized server: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(uploaded, payload) {
				t.Fatal("bootstrap did not stream the tool")
			}
			contents := read(t, unit)
			for _, want := range []string{"ExecStart=/opt/ocms/bin/ocms", "TimeoutStopSec=60", "KillSignal=SIGTERM"} {
				if !strings.Contains(contents, want) {
					t.Fatalf("installed unit lacks %s", want)
				}
			}
			cron := read(t, w.server.CronFile)
			for _, want := range []string{"/bin/ocmsctl backup --all", "/bin/ocmsctl check --all"} {
				if !strings.Contains(cron, want) {
					t.Fatalf("cron lacks %s: %s", want, cron)
				}
			}
			if strings.Contains(cron, ".sh") {
				t.Fatalf("cron still invokes shell scripts: %s", cron)
			}
		})
	}
}

func TestFailedPullPreservesLocalDatabase(t *testing.T) {
	for _, failSSH := range []bool{false, true} {
		name := "invalid gzip"
		if failSSH {
			name = "interrupted ssh"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.addSite(t)
			w.systemctl = nil
			db := filepath.Join(t.TempDir(), "local.db")
			if err := os.WriteFile(db, []byte("existing local data"), 0600); err != nil {
				t.Fatal(err)
			}
			original := w.local.Exec
			w.local.Exec = func(ctx context.Context, c sitectl.Cmd) error {
				if c.Name == "ssh" && len(c.Args) > 3 && c.Args[3] == "snapshot" {
					if _, err := io.WriteString(c.Stdout, "incomplete snapshot"); err != nil {
						return err
					}
					if failSSH {
						return errors.New("connection lost")
					}
					return nil
				}
				return original(ctx, c)
			}
			err := run(context.Background(), w.local, []string{"pull", "root@server", "example_com", "--yes", "--port", "0", "--db", db, "--no-uploads", "--no-logs"}, nil)
			if err == nil {
				t.Fatal("failed snapshot was accepted")
			}
			if got := read(t, db); got != "existing local data" {
				t.Fatalf("local data replaced with %q", got)
			}
			if len(w.systemctl) != 0 {
				t.Fatalf("online snapshot touched running units: %v", w.systemctl)
			}
			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(db), ".pull-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("private staging left behind: %v %v", leftovers, err)
			}
		})
	}
}
