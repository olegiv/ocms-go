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
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/sitectl"
)

// pull's rsync reads on the server as the site user: a path in the site's
// .env cannot make it copy files only root may read.
func TestRootPullReadsAsTheSiteUser(t *testing.T) {
	w, site, rootOnly, _ := nobodySite(t)
	writeTree(t, site, map[string]string{"uploads/a.jpg": "public", ".env": "OCMS_UPLOADS_DIR=" + rootOnly + "\n"})
	writeTree(t, rootOnly, map[string]string{"secret.txt": "root's"})
	giveTo(t, site, "nobody")
	w.realRsync(t)
	t.Chdir(t.TempDir())

	err := run(context.Background(), w.local, []string{"pull", "root@server", "site", "--yes", "--no-db", "--no-logs"}, nil)
	if err == nil {
		t.Error("pull read a directory only root may read")
	}
	if _, err := os.Stat(filepath.Join("uploads", "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("the secret arrived: %v\n%s", err, w.out)
	}

	writeTree(t, site, map[string]string{".env": "OCMS_DB_PATH=./data/ocms.db\n"})
	giveTo(t, site, "nobody")
	if err := run(context.Background(), w.local, []string{"pull", "root@server", "site", "--yes", "--no-db", "--no-logs"}, nil); err != nil {
		t.Fatalf("%v\n%s", err, w.out)
	}
	if got := read(t, filepath.Join("uploads", "a.jpg")); got != "public" {
		t.Errorf("uploads/a.jpg = %q", got)
	}
}

// deploy --themes changes no theme until the server holds the binary
// intact, and restarts the sites once.
func TestDeployThemesWaitForAVerifiedBinary(t *testing.T) {
	w := newWorld(t)
	dir := w.addSite(t)
	t.Chdir(t.TempDir())
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
	exec := w.local.Exec
	w.local.Exec = func(ctx context.Context, c sitectl.Cmd) error {
		if c.Stdin != nil && slices.Contains(c.Args, "install") {
			c.Stdin = strings.NewReader("\x7fELFdamaged in transit")
		}
		return exec(ctx, c)
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
	for _, want := range []string{"OCMS_REQUIRE_ROOT_TESTS=1", "-run '^TestRoot' ./cmd/ocmsctl/"} {
		if !strings.Contains(workflow, want) {
			t.Errorf("CI runs no root tests: %q is not in go.yml", want)
		}
	}
	source := read(t, "main_test.go")
	body := source[strings.Index(source, "func rootAndNobody("):]
	body = body[:strings.Index(body, "\n}\n")]
	if len(checks) != 1 || !strings.Contains(body, check) {
		t.Errorf("root checks at %v: want one, in rootAndNobody", checks)
	}
	// What makes CI fail rather than skip.
	if !strings.Contains(body, `os.Getenv("OCMS_REQUIRE_ROOT_TESTS") != ""`) || !strings.Contains(body, "t.Fatalf(") {
		t.Error("rootAndNobody no longer fails under OCMS_REQUIRE_ROOT_TESTS")
	}
}
