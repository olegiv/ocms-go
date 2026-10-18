// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/model"
)

func TestDashboardLayoutNavigation(t *testing.T) {
	for _, role := range []string{model.RoleAdmin, model.RoleEditor, model.RolePublic} {
		t.Run(role, func(t *testing.T) {
			pc := testPageContext()
			pc.AccountLayout = true
			pc.CurrentPath = "/account"
			pc.Title = "My account"
			pc.User = UserInfo{ID: 42, Name: "Account user", Email: "account@example.com", Role: role}
			pc.LangOptions = []LangOption{{Code: "en", Name: "English"}, {Code: "ru", Name: "Русский"}}
			pc.SidebarModules = []SidebarModule{{Name: "private", Label: "Private module", AdminURL: "/admin/private-module"}}
			body := renderAdminTemplate(t, AdminLayout(pc))
			for _, want := range []string{`class="admin-layout"`, `href="/account"`, `href="/"`, `action="/language" method="POST"`, `action="/logout" method="POST"`, `content="noindex, nofollow"`, "My account"} {
				if !strings.Contains(body, want) {
					t.Errorf("account layout missing %q", want)
				}
			}
			for _, forbidden := range []string{`href="/admin`, `action="/admin`, `href="/api/`, "Private module"} {
				if strings.Contains(body, forbidden) {
					t.Errorf("account layout exposed %q", forbidden)
				}
			}
		})
	}
}

func TestDashboardLayoutDefaultNavigation(t *testing.T) {
	for _, role := range []string{model.RoleAdmin, model.RoleEditor} {
		t.Run(role, func(t *testing.T) {
			pc := testPageContext()
			pc.User = UserInfo{ID: 42, Name: "Staff user", Role: role}
			pc.LangOptions = []LangOption{{Code: "en", Name: "English"}, {Code: "ru", Name: "Русский"}}
			pc.SidebarModules = []SidebarModule{{Name: "example", Label: "Example module", AdminURL: "/admin/example"}}
			body := renderAdminTemplate(t, AdminLayout(pc))
			for _, want := range []string{`href="/admin"`, `href="/admin/pages"`, `href="/admin/users/42"`, `action="/admin/language" method="POST"`} {
				if !strings.Contains(body, want) {
					t.Errorf("default layout missing %q", want)
				}
			}
			if strings.Contains(body, `content="noindex, nofollow"`) || strings.Contains(body, `href="/account"`) {
				t.Error("account variant changed default admin layout")
			}
			if got := strings.Contains(body, `href="/admin/example"`); got != (role == model.RoleAdmin) {
				t.Errorf("module navigation present = %v for role %s", got, role)
			}
		})
	}
}

func TestSidebarCollapseToggleAnnouncesState(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	for lang, labels := range map[string][2]string{
		"en": {"Collapse sidebar", "Expand sidebar"},
		"ru": {"Свернуть меню", "Развернуть меню"},
	} {
		t.Run(lang, func(t *testing.T) {
			pc := testPageContext()
			pc.AdminLang = lang
			body := renderAdminTemplate(t, Sidebar(pc))
			start := strings.Index(body, `class="sidebar-collapse-btn"`)
			if start < 0 {
				t.Fatal("sidebar collapse toggle not rendered")
			}
			toggle := body[start:]
			toggle = toggle[:strings.Index(toggle, "</button>")]
			for _, want := range []string{
				`data-collapse-label="` + labels[0] + `"`,
				`data-expand-label="` + labels[1] + `"`,
				`:title="sidebarCollapsed ? $el.dataset.expandLabel : $el.dataset.collapseLabel"`,
				`:aria-label="sidebarCollapsed ? $el.dataset.expandLabel : $el.dataset.collapseLabel"`,
				`:aria-expanded="!sidebarCollapsed"`,
				`aria-hidden="true"`,
				`:class="{ 'flipped': sidebarCollapsed }"`,
			} {
				if !strings.Contains(toggle, want) {
					t.Errorf("collapse toggle missing %q", want)
				}
			}
			// Tailwind's rotate-180 stacks on the SCSS transform: the chevron
			// would turn 360 degrees and never point to "expand".
			if strings.Contains(toggle, "rotate-") {
				t.Error("collapse chevron uses a Tailwind rotate utility as its state class")
			}
			if !strings.Contains(body, `<button type="button" class="sidebar-collapse-btn"`) {
				t.Error("collapse toggle is not type=button")
			}
		})
	}
}
