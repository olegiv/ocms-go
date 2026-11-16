// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// tarNames lists the entries of the gzipped tar archives in dir whose names
// start with prefix.
func tarNames(t *testing.T, dir, prefix string) []string {
	t.Helper()
	archives, err := filepath.Glob(filepath.Join(dir, prefix+"*.tar.gz"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("archives %s* in %s: %v %v", prefix, dir, archives, err)
	}
	f, err := os.Open(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(zr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
}

// A site that keeps its uploads and custom files where its .env says must
// have those backed up, not the empty defaults beside them.
func TestBackupArchivesTheConfiguredContent(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	createDB(t, filepath.Join(site.Dir, env.DB.Default))
	shared := filepath.Join(env.SitesBase, "shared-custom")
	writeFiles(t, site.Dir, map[string]string{
		".env":              "APP_UPLOADS=./media\nAPP_CUSTOM=" + shared + "\n",
		"media/2026/a.jpg":  "jpeg",
		"uploads/stale.jpg": "not configured",
	})
	writeFiles(t, shared, map[string]string{"themes/mine/theme.json": "{}"})
	if err := env.Backup(context.Background(), sitesOf(site)); err != nil {
		t.Fatalf("%v\n%s", err, env.out)
	}
	backups := filepath.Join(site.Dir, "backups")
	if got := tarNames(t, backups, "uploads_"); !slices.Contains(got, "uploads/2026/a.jpg") || slices.Contains(got, "uploads/stale.jpg") {
		t.Errorf("uploads archive = %v, want the configured media directory", got)
	}
	if got := tarNames(t, backups, "custom_"); !slices.Contains(got, "custom/themes/mine/theme.json") {
		t.Errorf("custom archive = %v, want the configured custom directory", got)
	}

	// A content directory that holds the backups would archive every
	// earlier archive again, and keeps the old copies.
	writeFiles(t, site.Dir, map[string]string{".env": "APP_UPLOADS=.\n"})
	env.now = env.now.Add(backupRetention * 2)
	err := env.Backup(context.Background(), sitesOf(site))
	if err == nil || !strings.Contains(err.Error(), "holds the backups directory") {
		t.Fatalf("backup of a site whose uploads hold backups/: %v", err)
	}
	if got := entryNames(t, backups); len(got) < 3 {
		t.Errorf("a failed backup expired the earlier archives: %v", got)
	}
}

func TestThemesGoToTheConfiguredCustomDirectory(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	work := t.TempDir()
	writeFiles(t, work, map[string]string{"themes/mine/theme.json": "{}"})
	var packed bytes.Buffer
	if err := writeTarGz(&packed, filepath.Join(work, "themes"), ".", true); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, site.Dir, map[string]string{".env": "APP_CUSTOM=./site-custom\n"})
	if err := env.InstallThemes(context.Background(), site, &packed); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(site.Dir, "site-custom", "themes", "mine", "theme.json")); got != "{}" {
		t.Errorf("theme = %q", got)
	}
	if _, err := os.Stat(filepath.Join(site.Dir, "custom")); !os.IsNotExist(err) {
		t.Errorf("themes also went to the default custom directory: %v", err)
	}
}

func TestErrorLogsFollowTheSiteConfiguration(t *testing.T) {
	env := newTestEnv(t)
	site, _ := env.addSite(t, "a")
	writeFiles(t, site.Dir, map[string]string{".env": "APP_ERROR_LOG=./var/errors.log\n", "var/errors.log": "boom\n"})
	if err := env.Logs(context.Background(), site, 5, false, true); err != nil {
		t.Fatal(err)
	}
	if want := "tail -n 5 " + filepath.Join(site.Dir, "var", "errors.log"); !slices.Contains(env.host.calls, want) {
		t.Errorf("calls = %v, want %s", env.host.calls, want)
	}

	// No error log configured and no default: say so.
	env.ErrorLog.Default = ""
	writeFiles(t, site.Dir, map[string]string{".env": "OTHER=1\n"})
	if err := env.Logs(context.Background(), site, 5, false, true); err == nil || !strings.Contains(err.Error(), "keeps no error log (APP_ERROR_LOG") {
		t.Errorf("site without an error log: %v", err)
	}

	// A path tail could take but the remote shell could not is refused at
	// its source, by name.
	writeFiles(t, site.Dir, map[string]string{".env": "APP_ERROR_LOG='./my errors.log'\n"})
	if err := env.Logs(context.Background(), site, 5, false, true); err == nil || !strings.Contains(err.Error(), "APP_ERROR_LOG") {
		t.Errorf("unsafe error log path: %v", err)
	}
}

// What the server reports comes from a site's own .env, so the reading side
// checks it again.
func TestReportedSitePathsAreChecked(t *testing.T) {
	env := newTestEnv(t)
	report := func(uploads string) string {
		return "db /s/data/app.db\n" + uploads + "custom /s/custom\nerrorlog /s/logs/e.log\n"
	}
	if p, err := env.parsePaths(report("uploads /s/uploads\n") + "future /s/x\n"); err != nil || p.Uploads != "/s/uploads" || p.ErrorLog != "/s/logs/e.log" {
		t.Fatalf("parsePaths = %+v, %v", p, err)
	}
	for name, uploads := range map[string]string{
		"duplicate":   "uploads /s/uploads\nuploads /etc\n",
		"relative":    "uploads s/uploads\n",
		"root":        "uploads /\n",
		"unclean":     "uploads /s/../etc\n",
		"shell":       "uploads /s/up;rm\n",
		"missing":     "",
		"empty value": "uploads \n",
	} {
		if p, err := env.parsePaths(report(uploads)); err == nil {
			t.Errorf("%s: accepted %+v", name, p)
		}
	}
}

// The site's .env is read only inside SiteOp, which runs as the site user;
// a checkout's configuration only by readLocalConfig.
func TestSiteConfigurationIsReadAsTheSiteUser(t *testing.T) {
	readers := map[string]string{} // function -> what it calls
	for _, name := range sourceFiles(t) {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch callee := call.Fun.(type) {
				case *ast.SelectorExpr:
					if x, ok := callee.X.(*ast.Ident); ok && x.Name == "godotenv" {
						readers[fn.Name.Name] += " godotenv." + callee.Sel.Name
					}
					if callee.Sel.Name == "readSitePaths" {
						readers[fn.Name.Name] += " readSitePaths"
					}
				}
				return true
			})
		}
	}
	want := map[string]string{"SiteOp": " readSitePaths", "readSitePaths": " godotenv.Read", "readLocalConfig": " godotenv.Read"}
	for fn, calls := range readers {
		if want[fn] != calls {
			t.Errorf("%s calls%s: a site's .env is read by readSitePaths, from SiteOp only", fn, calls)
		}
	}
	if len(readers) != len(want) {
		t.Errorf("readers = %v, want %v", readers, want)
	}
}

// Where a site keeps its files is the application's to say (App's settings),
// so this package names none of those directories itself.
func TestSitectlNamesNoApplicationDirectory(t *testing.T) {
	forbidden := []string{"uploads", "custom", "logs", "data", "themes"}
	for _, name := range sourceFiles(t) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			first, _, _ := strings.Cut(strings.TrimPrefix(value, "./"), "/")
			if slices.Contains(forbidden, first) && !slices.Contains(siteOps, value) {
				t.Errorf("%s: %q names an application directory; use App's settings", fset.Position(lit.Pos()), value)
			}
			return true
		})
	}
}

func sourceFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(files, func(name string) bool { return strings.HasSuffix(name, "_test.go") })
}
