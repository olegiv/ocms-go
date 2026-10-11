// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package theme

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/themes"
)

type themeArchiveEntry struct {
	name     string
	body     string
	typeFlag byte
	link     string
}

func themeArchiveEntries(name string) []themeArchiveEntry {
	entries := []themeArchiveEntry{
		{name: name + "/", typeFlag: tar.TypeDir},
		{name: name + "/theme.json", body: `{"name":"Imported theme","version":"1.0","engine":"html"}`},
		{name: name + "/templates/layouts/base.html", body: `<html>{{template "content" .}}</html>`},
		{name: name + "/static/style.css", body: `body { color: green; }`},
	}
	for _, page := range requiredPageTemplates {
		entries = append(entries, themeArchiveEntry{name: name + "/templates/pages/" + page + ".html", body: `{{define "content"}}<h1>{{.Title}}</h1>{{end}}`})
	}
	return entries
}

// withoutPage drops one page template from the fixture.
func withoutPage(entries []themeArchiveEntry, page string) []themeArchiveEntry {
	var kept []themeArchiveEntry
	for _, e := range entries {
		if !strings.HasSuffix(e.name, "/templates/pages/"+page+".html") {
			kept = append(kept, e)
		}
	}
	return kept
}

// TestRequiredPageTemplatesMatchDefaultTheme fails when the import validator's
// required list drifts from the pages the default theme ships, which are the
// pages the public handlers render: a theme that imports without one of them
// answers 500 on that page (the form page did, before "form" was required).
func TestRequiredPageTemplatesMatchDefaultTheme(t *testing.T) {
	entries, err := fs.ReadDir(themes.FS, "default/templates/pages")
	if err != nil {
		t.Fatal(err)
	}
	var shipped []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".html") {
			shipped = append(shipped, strings.TrimSuffix(e.Name(), ".html"))
		}
	}
	required := append([]string(nil), requiredPageTemplates...)
	sort.Strings(shipped)
	sort.Strings(required)
	if strings.Join(shipped, ",") != strings.Join(required, ",") {
		t.Fatalf("requiredPageTemplates = %v, default theme ships %v; keep them identical", required, shipped)
	}
}

// TestImportRejectsThemeWithoutFormTemplate fails when an archive that lacks
// templates/pages/form.html is accepted: every public form would then answer
// 500 once the theme is active.
func TestImportRejectsThemeWithoutFormTemplate(t *testing.T) {
	m := NewManager(themes.FS, t.TempDir(), testutil.TestLoggerSilent())
	_, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, withoutPage(themeArchiveEntries("my-theme"), "form"))))
	var verr ValidationError
	if !errors.As(err, &verr) || !strings.Contains(err.Error(), "templates/pages/form.html") {
		t.Fatalf("ImportArchive error = %v, want a validation error naming templates/pages/form.html", err)
	}
}

func makeThemeArchive(t *testing.T, entries []themeArchiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gz)
	for _, entry := range entries {
		typeFlag := entry.typeFlag
		if typeFlag == 0 {
			typeFlag = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: 0777, Typeflag: typeFlag, Linkname: entry.link}
		if typeFlag == tar.TypeReg {
			header.Size = int64(len(entry.body))
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := io.WriteString(tarWriter, entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func assertNoThemeStaging(t *testing.T, custom string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(custom, "themes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), importPrefix) || strings.HasPrefix(entry.Name(), deletePrefix) {
			t.Errorf("temporary directory left behind: %s", entry.Name())
		}
	}
}

func TestImportThemeLifecycle(t *testing.T) {
	m, custom := testManager(t)
	m.SetFuncMap(template.FuncMap{"greeting": func() string { return "Hello" }})
	entries := themeArchiveEntries("my-theme")
	entries[2].body = `<html>{{greeting}}{{template "content" .}}</html>`
	archive := makeThemeArchive(t, entries)
	name, err := m.ImportArchive(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	if name != "my-theme" || m.GetActiveTheme() != nil || !m.ListThemesWithActive()[0].CanDelete {
		t.Fatalf("import should register an inactive, deletable theme: %s", name)
	}
	thm, err := m.GetTheme(name)
	if err != nil {
		t.Fatal(err)
	}
	var rendered bytes.Buffer
	if err := thm.RenderPage(&rendered, "page", map[string]string{"Title": "Content"}); err != nil {
		t.Fatal(err)
	}
	if rendered.String() != "<html>Hello<h1>Content</h1></html>" {
		t.Fatal(rendered.String())
	}
	css, err := os.ReadFile(filepath.Join(thm.StaticPath, "style.css"))
	if err != nil || string(css) != entries[3].body {
		t.Fatalf("static assets: %s, %v", css, err)
	}
	info, err := os.Stat(filepath.Join(thm.Path, "theme.json"))
	if err != nil || info.Mode().Perm()&0111 != 0 {
		t.Fatalf("archive executable permissions were retained: %v", err)
	}
	assertNoThemeStaging(t, custom)

	restarted := NewManager(emptyFS, custom, testutil.TestLoggerSilent())
	restarted.SetFuncMap(m.funcMap)
	if err := restarted.LoadThemes(); err != nil {
		t.Fatal(err)
	}
	if !restarted.HasTheme(name) {
		t.Fatal("installed theme did not survive restart")
	}
	if err := restarted.SetActiveTheme(name); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RemoveTheme(name); !errors.Is(err, ErrThemeProtected) {
		t.Fatalf("active deletion: %v", err)
	}

	if err := m.RemoveTheme(name); err != nil {
		t.Fatal(err)
	}
	if m.HasTheme(name) {
		t.Fatal("deleted theme remains registered")
	}
	if _, err := os.Stat(thm.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted directory remains: %v", err)
	}
	assertNoThemeStaging(t, custom)
	restarted = NewManager(emptyFS, custom, testutil.TestLoggerSilent())
	if err := restarted.LoadThemes(); err != nil {
		t.Fatal(err)
	}
	if restarted.HasTheme(name) {
		t.Fatal("deleted theme reappeared after restart")
	}
	if _, err := m.ImportArchive(bytes.NewReader(archive)); err != nil {
		t.Fatalf("reinstallation: %v", err)
	}
}

func TestImportThemeRejectsInvalidArchives(t *testing.T) {
	tests := []struct {
		name string
		edit func([]themeArchiveEntry) []themeArchiveEntry
	}{
		{"traversal", func(e []themeArchiveEntry) []themeArchiveEntry { e[3].name = "my-theme/../outside"; return e }},
		{"absolute", func(e []themeArchiveEntry) []themeArchiveEntry { e[3].name = "/outside"; return e }},
		{"backslash", func(e []themeArchiveEntry) []themeArchiveEntry { e[3].name = `my-theme/..\outside`; return e }},
		{"windows drive", func(e []themeArchiveEntry) []themeArchiveEntry { e[3].name = "C:/outside"; return e }},
		{"symlink", func(e []themeArchiveEntry) []themeArchiveEntry {
			e[3].typeFlag = tar.TypeSymlink
			e[3].link = "../../outside"
			return e
		}},
		{"hardlink", func(e []themeArchiveEntry) []themeArchiveEntry {
			e[3].typeFlag = tar.TypeLink
			e[3].link = "my-theme/theme.json"
			return e
		}},
		{"special file", func(e []themeArchiveEntry) []themeArchiveEntry { e[3].typeFlag = tar.TypeFifo; return e }},
		{"duplicate", func(e []themeArchiveEntry) []themeArchiveEntry { return append(e, e[1]) }},
		{"multiple roots", func(e []themeArchiveEntry) []themeArchiveEntry {
			return append(e, themeArchiveEntry{name: "other/theme.json", body: "{}"})
		}},
		{"invalid identifier", func(e []themeArchiveEntry) []themeArchiveEntry { e[0].name = "UPPERCASE/"; return e }},
		{"invalid json", func(e []themeArchiveEntry) []themeArchiveEntry { e[1].body = "{"; return e }},
		{"missing manifest", func(e []themeArchiveEntry) []themeArchiveEntry { return append(e[:1], e[2:]...) }},
		{"missing version", func(e []themeArchiveEntry) []themeArchiveEntry { e[1].body = `{"name":"Theme"}`; return e }},
		{"unsupported engine", func(e []themeArchiveEntry) []themeArchiveEntry {
			e[1].body = `{"name":"Theme","version":"1","engine":"templ"}`
			return e
		}},
		{"missing layout", func(e []themeArchiveEntry) []themeArchiveEntry { return append(e[:2], e[3:]...) }},
		{"missing page", func(e []themeArchiveEntry) []themeArchiveEntry { return e[:len(e)-1] }},
		{"invalid template", func(e []themeArchiveEntry) []themeArchiveEntry { e[2].body = "{{if}}"; return e }},
		{"missing content", func(e []themeArchiveEntry) []themeArchiveEntry { e[4].body = "no content definition"; return e }},
		{"file parent conflict", func(e []themeArchiveEntry) []themeArchiveEntry {
			return append(e, themeArchiveEntry{name: "my-theme/theme.json/child", body: "child"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, custom := testManager(t)
			if _, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, tt.edit(themeArchiveEntries("my-theme"))))); err == nil {
				t.Fatal("invalid archive accepted")
			}
			if m.ThemeCount() != 0 {
				t.Fatal("invalid theme registered")
			}
			entries, err := os.ReadDir(filepath.Join(custom, "themes"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed import left files: %v, %v", entries, err)
			}
		})
	}
	for _, input := range [][]byte{nil, []byte("not gzip"), makeThemeArchive(t, nil)} {
		m, custom := testManager(t)
		if _, err := m.ImportArchive(bytes.NewReader(input)); err == nil {
			t.Fatal("empty/malformed archive accepted")
		}
		assertNoThemeStaging(t, custom)
	}
	valid := makeThemeArchive(t, themeArchiveEntries("my-theme"))
	corrupt := bytes.Clone(valid)
	corrupt[len(corrupt)-8] ^= 0xff
	for _, input := range [][]byte{valid[:len(valid)-5], corrupt, append(bytes.Clone(valid), []byte("junk")...)} {
		m, custom := testManager(t)
		if _, err := m.ImportArchive(bytes.NewReader(input)); err == nil {
			t.Fatal("damaged gzip trailer accepted")
		}
		assertNoThemeStaging(t, custom)
	}
}

func TestThemeArchiveLimits(t *testing.T) {
	t.Run("entry count", func(t *testing.T) {
		entries := themeArchiveEntries("many")
		for i := len(entries); i <= maxArchiveEntries; i++ {
			entries = append(entries, themeArchiveEntry{name: fmt.Sprintf("many/static/%d", i)})
		}
		m, custom := testManager(t)
		if _, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, entries))); err == nil {
			t.Fatal("entry limit not enforced")
		}
		assertNoThemeStaging(t, custom)
	})
	t.Run("declared extracted size", func(t *testing.T) {
		var buffer bytes.Buffer
		gz := gzip.NewWriter(&buffer)
		tarWriter := tar.NewWriter(gz)
		if err := tarWriter.WriteHeader(&tar.Header{Name: "large/file", Mode: 0644, Size: maxExtractedBytes + 1}); err != nil {
			t.Fatal(err)
		}
		// A malicious declared size must fail before trying to read its body.
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		m, custom := testManager(t)
		if _, err := m.ImportArchive(bytes.NewReader(buffer.Bytes())); err == nil || !strings.Contains(err.Error(), "100 MiB") {
			t.Fatalf("size limit: %v", err)
		}
		assertNoThemeStaging(t, custom)
	})
	t.Run("compressed and trailing extracted data", func(t *testing.T) {
		for _, level := range []int{gzip.NoCompression, gzip.BestSpeed} {
			var buffer bytes.Buffer
			gz, err := gzip.NewWriterLevel(&buffer, level)
			if err != nil {
				t.Fatal(err)
			}
			// An empty tar followed by oversized padding must also be bounded.
			limit := int64(MaxArchiveBytes + 1024)
			if level == gzip.BestSpeed {
				limit = maxExtractedBytes + 1024
			}
			if _, err := io.CopyN(gz, zeroReader{}, limit); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			m, custom := testManager(t)
			if _, err := m.ImportArchive(bytes.NewReader(buffer.Bytes())); err == nil {
				t.Fatal("trailing size limit not enforced")
			}
			assertNoThemeStaging(t, custom)
		}
	})
}

func TestThemeArchiveRejectsPAXSparseFiles(t *testing.T) {
	// PAX sparse files look like regular files, but tar.Reader synthesizes their
	// holes without consuming those bytes from the gzip stream.
	var records strings.Builder
	for _, pair := range [][2]string{{"GNU.sparse.map", "0,0"}, {"GNU.sparse.size", "104857600"}, {"GNU.sparse.numblocks", "1"}} {
		body := pair[0] + "=" + pair[1] + "\n"
		length := len(body) + 2
		for {
			record := fmt.Sprintf("%d %s", length, body)
			if len(record) == length {
				records.WriteString(record)
				break
			}
			length = len(record)
		}
	}
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	if err := writer.WriteHeader(&tar.Header{Name: "pax", Mode: 0644, Size: int64(records.Len()), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(writer, records.String()); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteHeader(&tar.Header{Name: "sparse/static/file", Mode: 0644, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	// tar.Writer intentionally cannot produce sparse archives. Convert the
	// first USTAR header to a PAX extension header and recalculate its checksum.
	data := raw.Bytes()
	data[156] = tar.TypeXHeader
	copy(data[148:156], "        ")
	checksum := 0
	for _, b := range data[:512] {
		checksum += int(b)
	}
	copy(data[148:156], fmt.Sprintf("%06o\x00 ", checksum))
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	if _, err := gz.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	m, custom := testManager(t)
	if _, err := m.ImportArchive(bytes.NewReader(archive.Bytes())); err == nil || !strings.Contains(err.Error(), "sparse files") {
		t.Fatalf("PAX sparse rejection: %v", err)
	}
	assertNoThemeStaging(t, custom)
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestThemeImportAndDeleteProtection(t *testing.T) {
	custom := testCustomDir(t)
	m := NewManager(themes.FS, custom, testutil.TestLoggerSilent())
	m.SetFuncMap(testutil.MinimalThemeFuncMap())
	createTestTheme(t, custom, "default", Config{Name: "Override", Version: "1"})
	if err := m.LoadThemes(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"default", "developer"} {
		if _, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, themeArchiveEntries(name)))); err == nil {
			t.Fatal("core name import accepted")
		}
		if err := m.RemoveTheme(name); !errors.Is(err, ErrThemeProtected) {
			t.Fatalf("core deletion: %v", err)
		}
	}
	for _, info := range m.ListThemesWithActive() {
		if info.CanDelete {
			t.Fatalf("protected theme deletable: %s", info.Name)
		}
	}
	for _, name := range []string{"missing", "../default", "", "UPPER"} {
		if err := m.RemoveTheme(name); err == nil {
			t.Fatalf("invalid deletion accepted: %s", name)
		}
	}
	for _, stage := range []string{importPrefix + "abandoned", deletePrefix + "abandoned"} {
		createTestTheme(t, custom, stage, Config{Name: "Hidden", Version: "1"})
	}
	if err := m.LoadThemes(); err != nil {
		t.Fatal(err)
	}
	if m.ThemeCount() != 2 {
		t.Fatal("startup loaded private staging directories")
	}
}

// TestImportCreatesMissingCustomDirectory fails when the first import on an
// installation without a custom directory fails on the missing parent
// instead of creating OCMS_CUSTOM_DIR and its themes directory.
func TestImportCreatesMissingCustomDirectory(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom")
	m := NewManager(emptyFS, custom, testutil.TestLoggerSilent())
	name, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, themeArchiveEntries("fresh"))))
	if err != nil {
		t.Fatalf("ImportArchive on a missing custom directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(custom, "themes", name, "theme.json")); err != nil {
		t.Fatalf("imported theme not installed under the created directory: %v", err)
	}
	if !m.HasTheme(name) {
		t.Fatal("imported theme not registered")
	}
}

func TestThemeFilesystemFailures(t *testing.T) {
	t.Run("destination exists on disk", func(t *testing.T) {
		m, custom := testManager(t)
		if err := os.Mkdir(filepath.Join(custom, "themes", "occupied"), 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, themeArchiveEntries("occupied")))); err == nil {
			t.Fatal("unregistered directory overwritten")
		}
		assertNoThemeStaging(t, custom)
	})
	t.Run("root symlink escape", func(t *testing.T) {
		custom, outside := t.TempDir(), t.TempDir()
		if err := os.Symlink(outside, filepath.Join(custom, "themes")); err != nil {
			t.Fatal(err)
		}
		m := NewManager(emptyFS, custom, testutil.TestLoggerSilent())
		if _, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, themeArchiveEntries("escape")))); err == nil {
			t.Fatal("root symlink escape accepted")
		}
		entries, err := os.ReadDir(outside)
		if err != nil || len(entries) != 0 {
			t.Fatal("wrote outside custom directory")
		}
	})
	t.Run("deletion symlink and rename failure", func(t *testing.T) {
		m, custom := testManager(t)
		if _, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, themeArchiveEntries("keep")))); err != nil {
			t.Fatal(err)
		}
		thmPath := filepath.Join(custom, "themes", "keep")
		if err := os.Rename(thmPath, thmPath+"-moved"); err != nil {
			t.Fatal(err)
		}
		if err := m.RemoveTheme("keep"); err == nil || !m.HasTheme("keep") {
			t.Fatal("filesystem failure changed registry")
		}
		if err := os.Symlink(thmPath+"-moved", thmPath); err != nil {
			t.Fatal(err)
		}
		if err := m.RemoveTheme("keep"); err == nil || !m.HasTheme("keep") {
			t.Fatal("symlink deletion accepted")
		}
		if _, err := os.Stat(filepath.Join(thmPath+"-moved", "theme.json")); err != nil {
			t.Fatal("symlink target changed")
		}
	})
	t.Run("read only themes directory", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root bypasses directory permissions")
		}
		m, custom := testManager(t)
		archive := makeThemeArchive(t, themeArchiveEntries("keep"))
		if _, err := m.ImportArchive(bytes.NewReader(archive)); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(custom, "themes")
		if err := os.Chmod(root, 0555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(root, 0755) })
		if _, err := m.ImportArchive(bytes.NewReader(archive)); err == nil {
			t.Fatal("read only import accepted")
		}
		if err := m.RemoveTheme("keep"); err == nil || !m.HasTheme("keep") {
			t.Fatal("failed rename changed registry")
		}
	})
}

func TestConcurrentThemeOperations(t *testing.T) {
	t.Run("duplicate import", func(t *testing.T) {
		m, custom := testManager(t)
		archive := makeThemeArchive(t, themeArchiveEntries("unique"))
		var successes atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Go(func() {
				if _, err := m.ImportArchive(bytes.NewReader(archive)); err == nil {
					successes.Add(1)
				}
			})
		}
		wg.Wait()
		if successes.Load() != 1 || m.ThemeCount() != 1 {
			t.Fatal("duplicate concurrent imports succeeded")
		}
		assertNoThemeStaging(t, custom)
	})
	t.Run("activate against delete", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			m, _ := testManager(t)
			if _, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, themeArchiveEntries("race")))); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			wg.Go(func() { _ = m.SetActiveTheme("race") })
			wg.Go(func() { _ = m.RemoveTheme("race") })
			wg.Wait()
			if active := m.GetActiveTheme(); active != nil && !m.HasTheme(active.Name) {
				t.Fatal("active theme deleted concurrently")
			}
		}
	})
}

func TestThemeDeletionCleanupFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	m, custom := testManager(t)
	if _, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, themeArchiveEntries("cleanup")))); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(custom, "themes")
	if err := os.Chmod(filepath.Join(root, "cleanup", "static"), 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			_ = os.Chmod(filepath.Join(root, entry.Name(), "static"), 0755)
		}
	})
	if err := m.RemoveTheme("cleanup"); err != nil {
		t.Fatalf("logical deletion should succeed despite cleanup failure: %v", err)
	}
	if m.HasTheme("cleanup") {
		t.Fatal("logically deleted theme remains registered")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), deletePrefix) {
		t.Fatalf("expected only hidden cleanup directory, got %v (%v)", entries, err)
	}
	restarted := NewManager(emptyFS, custom, testutil.TestLoggerSilent())
	if err := restarted.LoadThemes(); err != nil {
		t.Fatal(err)
	}
	if restarted.ThemeCount() != 0 {
		t.Fatal("restart loaded a theme awaiting cleanup")
	}
}

func TestThemeArchiveSkipsAppleDoubleEntries(t *testing.T) {
	m, custom := testManager(t)
	entries := append([]themeArchiveEntry{{name: "._apple", body: "\x00\x05\x16\x07 finder info"}}, themeArchiveEntries("apple")...)
	entries = append(entries, themeArchiveEntry{name: "apple/templates/pages/._home.html", body: "{{not a template"})
	name, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, entries)))
	if err != nil {
		t.Fatalf("macOS metadata entries rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(custom, "themes", name, "templates", "pages", "._home.html")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata entry extracted: %v", err)
	}
	assertNoThemeStaging(t, custom)

	// Skipped entries still count toward the entry limit.
	padded := themeArchiveEntries("padded")
	for i := 0; i < maxArchiveEntries; i++ {
		padded = append(padded, themeArchiveEntry{name: fmt.Sprintf("padded/._%d", i), body: "x"})
	}
	_, err = m.ImportArchive(bytes.NewReader(makeThemeArchive(t, padded)))
	var validation ValidationError
	if !errors.As(err, &validation) || !strings.Contains(err.Error(), "2000 entries") {
		t.Fatalf("entry limit bypassed by metadata entries: %v", err)
	}
}

func TestThemeErrorClassification(t *testing.T) {
	m, custom := testManager(t)
	var validation ValidationError
	for _, tt := range []struct {
		name    string
		entries []themeArchiveEntry
		want    string
	}{
		{"missing theme.json", themeArchiveEntries("nojson")[:1], "missing theme.json"},
		{"theme.json directory", []themeArchiveEntry{{name: "dirjson/", typeFlag: tar.TypeDir}, {name: "dirjson/theme.json/", typeFlag: tar.TypeDir}}, "regular file"},
		{"file and directory collision", append(themeArchiveEntries("clash"), themeArchiveEntry{name: "clash/static/style.css/nested.css", body: "x"}), "conflicting file and directory paths"},
		{"broken template", append(themeArchiveEntries("broken")[:2], themeArchiveEntry{name: "broken/templates/layouts/base.html", body: "{{template"}), "invalid theme"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, tt.entries)))
			if !errors.As(err, &validation) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected a validation error containing %q, got %v", tt.want, err)
			}
			if strings.Contains(err.Error(), custom) {
				t.Fatalf("validation error leaks the server path: %v", err)
			}
		})
	}
	for _, name := range []string{"missing", "../default", "UPPER"} {
		if err := m.RemoveTheme(name); !errors.As(err, &validation) {
			t.Fatalf("deleting %q should be a validation error: %v", name, err)
		}
	}
	if !errors.As(ErrThemeProtected, &validation) {
		t.Fatal("protection errors must be shown to the administrator")
	}

	// Internal failures must not be shown verbatim: they name server paths.
	// A missing custom directory is created on import, so a regular file in
	// its place is what makes the filesystem refuse.
	notADir := filepath.Join(custom, "not-a-dir")
	if err := os.WriteFile(notADir, []byte("file"), 0644); err != nil {
		t.Fatal(err)
	}
	broken := NewManager(emptyFS, notADir, testutil.TestLoggerSilent())
	_, err := broken.ImportArchive(bytes.NewReader(makeThemeArchive(t, themeArchiveEntries("internal"))))
	if err == nil || errors.As(err, &validation) {
		t.Fatalf("filesystem failure classified as validation: %v", err)
	}
}
