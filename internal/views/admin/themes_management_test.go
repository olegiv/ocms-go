// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/olegiv/ocms-go/internal/i18n"
)

func TestThemeListManagementControls(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ru"} {
		pc := &PageContext{AdminLang: lang, User: UserInfo{Role: "admin"}}
		data := ThemesListData{Themes: []ThemeViewItem{
			{Name: "custom", ConfigName: `<script>alert(1)</script>`, CanDelete: true},
			{Name: "active", ConfigName: "Active", IsActive: true},
			{Name: "default", ConfigName: "Core", IsEmbedded: true},
		}}
		var buffer bytes.Buffer
		if err := ThemesListPage(pc, data).Render(templ.WithNonce(t.Context(), "test-nonce"), &buffer); err != nil {
			t.Fatal(err)
		}
		html := buffer.String()
		for _, expected := range []string{`action="/admin/themes/import"`, `enctype="multipart/form-data"`, `accept=".tar.gz"`, `name="confirm_password"`, `autocomplete="current-password"`, `action="/admin/themes/custom/delete"`, `aria-labelledby="delete-theme-title-custom"`, i18n.T(lang, "themes.import_title"), i18n.T(lang, "themes.import_warning")} {
			if !strings.Contains(html, expected) {
				t.Errorf("%s: missing %s", lang, expected)
			}
		}
		if strings.Contains(html, `action="/admin/themes/active/delete"`) || strings.Contains(html, `action="/admin/themes/default/delete"`) || strings.Contains(html, `<script>alert(1)</script>`) {
			t.Fatal("protected controls or unsafe theme metadata rendered")
		}
		if strings.Count(html, "/static/dist/js/dialog.min.js") != 1 {
			t.Fatal("dialog script must load once")
		}
		data.IsDemoMode = true
		buffer.Reset()
		if err := ThemesListPage(pc, data).Render(t.Context(), &buffer); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buffer.String(), `action="/admin/themes/import"`) || strings.Contains(buffer.String(), `action="/admin/themes/custom/delete"`) {
			t.Fatal("demo rendered management forms")
		}
	}
}
