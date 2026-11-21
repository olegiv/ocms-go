// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"regexp"
	"strings"
	"testing"
)

func TestAPIReferenceNavigation(t *testing.T) {
	pc := testPageContext()
	pc.CurrentPath = "/admin/api-docs"
	for _, role := range []string{"admin", "editor"} {
		pc.User.Role = role
		body := renderAdminTemplate(t, APIReferencePage(pc))
		for _, part := range []string{`href="/admin/api-docs" class="nav-link active"`, `<iframe src="/api/v2/docs" title="`, `<main class="admin-main">`} {
			if !strings.Contains(body, part) {
				t.Errorf("%s: missing %s", role, part)
			}
		}
	}

	pc.CurrentPath = "/admin/api-keys"
	pc.User.Role = "admin"
	body := renderAdminTemplate(t, APIKeysListPage(pc, APIKeysListData{}))
	links := regexp.MustCompile(`<a [^>]*href="/admin/api-docs"[^>]*>`).FindAllString(body, -1)
	if len(links) != 2 {
		t.Fatalf("expected sidebar and API keys reference links, got %v", links)
	}
	for _, link := range links {
		if strings.Contains(link, "target=") {
			t.Errorf("API reference link opens another context: %s", link)
		}
	}
}

func TestDashboardDemoWalkthrough(t *testing.T) {
	pc := testPageContext()
	pc.IsDemoMode = true
	body := renderAdminTemplate(t, DashboardPage(pc, DashboardData{}))
	for _, path := range []string{"/admin/demo/article", "/admin/pages/new", "/admin/media", "/admin/forms"} {
		if !strings.Contains(body, `href="`+path+`"`) {
			t.Errorf("demo dashboard missing %s", path)
		}
	}
	for _, path := range []string{"/admin/media/upload", "/admin/forms/new", "/admin/users/new"} {
		if strings.Contains(body, `href="`+path+`"`) {
			t.Errorf("demo dashboard offers disabled action %s", path)
		}
	}
	pc.IsDemoMode = false
	body = renderAdminTemplate(t, DashboardPage(pc, DashboardData{}))
	if strings.Contains(body, `href="/admin/demo/article"`) || !strings.Contains(body, `href="/admin/media/upload"`) {
		t.Error("regular dashboard has demo shortcuts")
	}
}
