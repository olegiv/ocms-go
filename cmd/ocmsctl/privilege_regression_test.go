// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/olegiv/ocms-go/internal/dbfile"
	"github.com/olegiv/ocms-go/internal/sitectl"
)

// pull's rsync reads on the server as the site user: a path in the site's
// .env cannot make it copy files only root may read.
func TestRootPullReadsAsTheSiteUser(t *testing.T) {
	w, site, rootOnly, _ := nobodySite(t)
	writeTree(t, site, map[string]string{"uploads/a.jpg": "public", ".env": "OCMS_UPLOADS_DIR=" + rootOnly + "\n"})
	writeTree(t, rootOnly, map[string]string{"secret.txt": "root's"})
	giveToNobody(t, site)
	w.realRsync(t)
	chdirTemp(t)

	err := run(context.Background(), w.local, []string{"pull", "root@server", "site", "--yes", "--no-db", "--no-logs"}, nil)
	if err == nil {
		t.Error("pull read a directory only root may read")
	}
	if _, err := os.Stat(filepath.Join("uploads", "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("the secret arrived: %v\n%s", err, w.out)
	}

	writeTree(t, site, map[string]string{".env": "OCMS_DB_PATH=./data/ocms.db\n"})
	giveToNobody(t, site)
	if err := run(context.Background(), w.local, []string{"pull", "root@server", "site", "--yes", "--no-db", "--no-logs"}, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	if got := read(t, filepath.Join("uploads", "a.jpg")); got != "public" {
		t.Errorf("uploads/a.jpg = %q", got)
	}
}

// As root, backups run as the site user in a child process; the alert must
// still carry why the backup failed, not just the child's exit status.
func TestRootBackupAlertsCarryTheCause(t *testing.T) {
	w, site, _, _ := nobodySite(t)
	giveToNobody(t, site)
	// Two parts fail (a joined error, over two lines), and the backup log
	// is on a full disk: the alert must still carry both causes.
	writeTree(t, site, map[string]string{"uploads/locked/a.jpg": "x"})
	if err := os.Chmod(filepath.Join(site, "uploads", "locked"), 0o700); err != nil { // root's, which nobody cannot read
		t.Fatal(err)
	}
	w.server.Stderr = fullDisk{}
	said := filepath.Join(w.server.ConfDir, "said")
	writeTree(t, w.server.ConfDir, map[string]string{"alert": "#!/bin/sh\nprintf '%s' \"$3\" > " + said + "\n"})
	if err := os.Chmod(filepath.Join(w.server.ConfDir, "alert"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), w.server, []string{"backup", "site"}, nil); err == nil {
		t.Fatal("a backup without a database succeeded")
	}
	if got := read(t, said); !strings.Contains(got, "database") || !strings.Contains(got, "ocms.db") || !strings.Contains(got, "uploads") {
		t.Errorf("the alert says %q, want both causes", got)
	}
}

// fullDisk fails every write, as a log on a full disk does.
type fullDisk struct{}

func (fullDisk) Write([]byte) (int, error) { return 0, syscall.ENOSPC }

// logs reads a file the site chose with the site user's rights.
func TestRootLogsReadAsTheSiteUser(t *testing.T) {
	w, site, rootOnly, _ := nobodySite(t)
	writeTree(t, rootOnly, map[string]string{"secret.log": "root's secret"})
	writeTree(t, site, map[string]string{".env": "OCMS_ERROR_LOG_PATH=" + filepath.Join(rootOnly, "secret.log") + "\n"})
	giveToNobody(t, site)
	w.out.Reset()
	if err := run(context.Background(), w.server, []string{"logs", "site", "--errors"}, nil); err == nil {
		t.Error("logs read a file only root may read")
	}
	if strings.Contains(w.out.String(), "root's secret") {
		t.Errorf("logs printed it:\n%s", w.out)
	}
}

func TestRootMaintenanceLockAllowsSiteUserAccess(t *testing.T) {
	const dbEnv = "OCMS_TEST_MAINTENANCE_LOCK_DB"
	if path := os.Getenv(dbEnv); path != "" {
		lock, err := dbfile.LockDSN(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = lock.Close() }()
		return
	}

	w, site, _, uid := nobodySite(t)
	db := filepath.Join(site, "data", "ocms.db")
	serverDB(t, db, "existing")
	giveToNobody(t, site)
	info, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := dbfile.LockDSN(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()

	program := filepath.Join(w.server.Root, "bin", "ocmsctl")
	cmd := exec.CommandContext(t.Context(), program, "-test.run=^TestRootMaintenanceLockAllowsSiteUserAccess$")
	cmd.Env = append(os.Environ(), dbEnv+"="+db)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: info.Sys().(*syscall.Stat_t).Gid}}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the site user could not acquire the maintenance lock: %v\n%s", err, output)
	}
}

// deploy --themes changes no theme until the server holds the binary
// intact, and restarts the sites once.
func TestDeployThemesWaitForAVerifiedBinary(t *testing.T) {
	w := newWorld(t)
	dir := w.addSite(t)
	chdirTemp(t)
	writeTree(t, ".", map[string]string{"bin/ocms-linux-amd64": "\x7fELFv1", "custom/themes/old/theme.json": "{}"})
	w.systemctl = nil
	if err := run(context.Background(), w.local, []string{"deploy", "root@server", "example_com", "--no-build", "--themes"}, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	if want := []string{"stop ocms@example_com", "start ocms@example_com"}; !slices.Equal(w.systemctl, want) {
		t.Errorf("systemctl calls = %v, want one restart %v", w.systemctl, want)
	}

	writeTree(t, ".", map[string]string{"bin/ocms-linux-amd64": "\x7fELFv2", "custom/themes/new/theme.json": "{}"})
	w.systemctl = nil
	remoteExec := w.local.Exec
	w.local.Exec = func(ctx context.Context, c sitectl.Cmd) error {
		if c.Stdin != nil && slices.Contains(c.Args, "install") {
			c.Stdin = strings.NewReader("\x7fELFdamaged in transit")
		}
		return remoteExec(ctx, c)
	}
	if err := run(context.Background(), w.local, []string{"deploy", "root@server", "example_com", "--no-build", "--themes"}, nil); err == nil {
		t.Fatal("a damaged binary was deployed")
	}
	if _, err := os.Stat(filepath.Join(dir, "custom", "themes", "new")); !os.IsNotExist(err) {
		t.Errorf("the themes changed before the binary was verified: %v", err)
	}
	if len(w.systemctl) != 0 {
		t.Errorf("systemctl calls %v", w.systemctl)
	}
	if got := read(t, filepath.Join(w.server.Root, "bin", "ocms")); got != "\x7fELFv1" {
		t.Errorf("the server runs %q", got)
	}
}

// Tests that need root skip through rootAndNobody and are named TestRoot*:
// CI runs exactly those as root, where OCMS_REQUIRE_ROOT_TESTS turns that
// skip into a failure. A test checking for root by itself, or named
// otherwise, would go unrun everywhere.
func TestTestsThatNeedRootRunInCI(t *testing.T) {
	check := "os.Geteuid" + "() != 0" // spelled apart, or this file would match
	var checks []string
	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "sitectl")} {
		files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("test files in %s: %v", dir, err)
		}
		for _, name := range files {
			for i, line := range strings.Split(read(t, name), "\n") {
				if strings.Contains(line, check) {
					checks = append(checks, fmt.Sprintf("%s:%d", name, i+1))
				}
			}
			file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
					continue
				}
				needsRoot := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if id, ok := call.Fun.(*ast.Ident); ok && (id.Name == "rootAndNobody" || id.Name == "nobodySite") {
							needsRoot = true
						}
					}
					return true
				})
				if needsRoot != strings.HasPrefix(fn.Name.Name, "TestRoot") {
					t.Errorf("%s: %s needs root %v; tests that need root, and only they, are named TestRoot*", name, fn.Name.Name, needsRoot)
				}
			}
		}
	}
	workflow := read(t, filepath.Join("..", "..", ".github", "workflows", "go.yml"))
	_, rootStep, found := strings.Cut(workflow, "name: Root tests")
	if !found {
		t.Fatal("the CI root test step is missing")
	}
	step, _, _ := strings.Cut(rootStep, "\n    - name:")
	for _, want := range []string{
		"shell: bash", // pipefail: the default shell would let tee hide a failure
		"OCMS_REQUIRE_ROOT_TESTS=1",
		"-run '^TestRoot' ./cmd/ocmsctl/",
		"grep '^--- PASS: TestRoot'",
		"'--- (FAIL|SKIP)'",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("the CI root step lacks %q:\n%s", want, step)
		}
	}
	source := read(t, "main_test.go")
	_, helper, found := strings.Cut(source, "func rootAndNobody(")
	if !found {
		t.Fatal("rootAndNobody is missing")
	}
	body, _, found := strings.Cut(helper, "\n}\n")
	if !found {
		t.Fatal("rootAndNobody has no closing brace")
	}
	if len(checks) != 1 || !strings.Contains(body, check) {
		t.Errorf("root checks at %v: want one, in rootAndNobody", checks)
	}
	// What makes CI fail rather than skip.
	if !strings.Contains(body, `os.Getenv("OCMS_REQUIRE_ROOT_TESTS") != ""`) || !strings.Contains(body, "t.Fatalf(") {
		t.Error("rootAndNobody no longer fails under OCMS_REQUIRE_ROOT_TESTS")
	}
}
