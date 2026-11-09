// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package theme

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/util"
)

func formWarningManager(customDir string, logs *bytes.Buffer) *Manager {
	m := NewManager(emptyFS, customDir, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	funcs := testutil.MinimalThemeFuncMap()
	funcs["contains"] = strings.Contains
	funcs["checkedOption"] = util.JoinedValueHas
	m.SetFuncMap(funcs)
	return m
}

func TestCustomFormCheckboxWarningAndReload(t *testing.T) {
	customDir := testCustomDir(t)
	var logs bytes.Buffer
	m := formWarningManager(customDir, &logs)
	dir := createTestTheme(t, customDir, "example", Config{Name: "Example", Engine: EngineHTML})
	form := filepath.Join(dir, "templates", "pages", "form.html")
	const source = `{{define "content"}}
{{/* {{if contains .Title "Post"}}checked{{end}} */}}
{{if contains .Title "Post"}}<h1>{{.Title}}</h1>{{end}}
{{$selectedValue := index .Values "reach"}}
{{range $opt := .Options}}
<input type="checkbox" value="{{$opt}}" {{if (contains
  $selectedValue $opt)}}checked{{end}}>
{{end}}
{{end}}`
	if err := os.WriteFile(form, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.LoadThemes(); err != nil {
		t.Fatal(err)
	}
	logged := logs.String()
	if strings.Count(logged, "level=WARN") != 1 || !strings.Contains(logged, "checkedOption") ||
		!strings.Contains(logged, "theme=example") || !strings.Contains(logged, "template=pages/form.html:") {
		t.Errorf("custom theme load should warn once with a template location and migration advice:\n%s", logged)
	}
	if err := m.SetActiveTheme("example"); err != nil {
		t.Fatal(err)
	}
	render := func() string {
		t.Helper()
		var out strings.Builder
		data := map[string]any{"Title": "Post", "Values": map[string]string{"reach": "Postcard"}, "Options": []string{"Post", "Postcard"}}
		if err := m.GetActiveTheme().RenderPage(&out, "form", data); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if out := render(); !strings.Contains(out, `value="Post" checked>`) || !strings.Contains(out, `value="Postcard" checked>`) {
		t.Errorf("legacy template should still load and demonstrate the overlap:\n%s", out)
	}

	logs.Reset()
	corrected := strings.Replace(source, "(contains\n", "(checkedOption\n", 1)
	if err := os.WriteFile(form, []byte(corrected), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.ReloadTheme("example"); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Errorf("unexpected warning for a corrected form with an unrelated substring check:\n%s", &logs)
	}
	if out := render(); strings.Contains(out, `value="Post" checked>`) || !strings.Contains(out, `value="Postcard" checked>`) ||
		!strings.Contains(out, "<h1>Post</h1>") {
		t.Errorf("corrected template should check only Postcard and retain the title check:\n%s", out)
	}
	if err := os.WriteFile(form, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.ReloadTheme("example"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "template=pages/form.html:") {
		t.Errorf("reloading the legacy template should warn again:\n%s", &logs)
	}
}

func TestImportedFormCheckboxPartialWarning(t *testing.T) {
	var logs bytes.Buffer
	m := formWarningManager(testCustomDir(t), &logs)
	const name = "imported-form"
	entries := withoutPage(themeArchiveEntries(name), "form")
	entries = append(entries,
		themeArchiveEntry{name: name + "/templates/pages/form.html", body: `{{define "content"}}{{template "checkbox.html" .}}{{template "checkbox.html" .}}{{end}}`},
		themeArchiveEntry{name: name + "/templates/partials/checkbox.html", body: `{{with .Values}}<input type="checkbox" {{if "Post" | contains (index . "reach")}}checked="checked"{{end}}>{{end}}`},
		themeArchiveEntry{name: name + "/templates/partials/unused.html", body: `{{if contains "Postcard" "Post"}}checked{{end}}`},
	)
	if imported, err := m.ImportArchive(bytes.NewReader(makeThemeArchive(t, entries))); err != nil || imported != name {
		t.Fatalf("ImportArchive = %q, %v", imported, err)
	}
	logged := logs.String()
	if strings.Count(logged, "level=WARN") != 1 || !strings.Contains(logged, "theme="+name) || !strings.Contains(logged, "template=checkbox.html:") {
		t.Errorf("import should warn once for the referenced partial:\n%s", logged)
	}
}
