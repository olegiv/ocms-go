// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
)

func renderAdminTemplate(t *testing.T, component templ.Component) string {
	t.Helper()

	var buf bytes.Buffer
	if err := component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}

	return buf.String()
}

func testPageContext() *PageContext {
	return &PageContext{
		Title:       "Test",
		AdminLang:   "en",
		CurrentPath: "/admin/pages",
		User: UserInfo{
			ID:    1,
			Name:  "Admin",
			Email: "admin@example.com",
			Role:  "admin",
		},
	}
}

func TestSortStateValue(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  string
	}{
		{name: "none", state: "", want: sortStateNone},
		{name: "asc", state: sortDirAsc, want: sortDirAsc},
		{name: "desc", state: sortDirDesc, want: sortDirDesc},
		{name: "invalid", state: "invalid", want: sortStateNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sortStateValue(tt.state); got != tt.want {
				t.Fatalf("sortStateValue(%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestSortLinkClass(t *testing.T) {
	if got := sortLinkClass(sortStateNone); !strings.Contains(got, "admin-sort-link") || !strings.Contains(got, "text-muted-foreground") {
		t.Fatalf("sortLinkClass(none) = %q, expected admin-sort-link + muted classes", got)
	}

	if got := sortLinkClass(sortDirDesc); !strings.Contains(got, "admin-sort-link") || !strings.Contains(got, "font-semibold") {
		t.Fatalf("sortLinkClass(desc) = %q, expected admin-sort-link + active classes", got)
	}
}

func TestPagesListPageRendersActiveSortHighlight(t *testing.T) {
	pc := testPageContext()

	html := renderAdminTemplate(t, PagesListPage(pc, PagesListViewData{
		Pages: []PageListItemView{
			{ID: 1, Title: "Welcome", Slug: "welcome", Status: "draft", UpdatedAt: "Mar 3, 2026"},
		},
		Statuses:   []string{"draft", "published"},
		Pagination: PaginationData{BaseURL: "/admin/pages", SortField: "updated_at", SortDir: sortDirDesc},
	}))

	if !strings.Contains(html, `data-sort-state="desc"`) {
		t.Fatal("expected rendered pages table to include data-sort-state=\"desc\"")
	}
	if !strings.Contains(html, "admin-sort-link") {
		t.Fatal("expected rendered pages table to include admin-sort-link class")
	}
}

func TestTagsListPageRendersActiveSortHighlight(t *testing.T) {
	pc := testPageContext()
	pc.CurrentPath = "/admin/tags"

	html := renderAdminTemplate(t, TagsListPage(pc, TagsListData{
		Tags: []TagListItem{
			{ID: 1, Name: "Go", Slug: "go", UsageCount: 2, CreatedAt: time.Now()},
		},
		Pagination: PaginationData{BaseURL: "/admin/tags", SortField: "usage_count", SortDir: sortDirDesc},
	}))

	if !strings.Contains(html, `data-sort-state="desc"`) {
		t.Fatal("expected rendered tags table to include data-sort-state=\"desc\"")
	}
	if !strings.Contains(html, "admin-sort-link") {
		t.Fatal("expected rendered tags table to include admin-sort-link class")
	}
}
