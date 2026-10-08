// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package seo

import (
	"encoding/json"
	"html/template"
	"strings"
	"testing"
	"time"
)

func TestBuildWebSiteSchema(t *testing.T) {
	name := "Новости Луны </script><script>alert(1)</script>"
	site := &SiteConfig{SiteName: name, SiteURL: " https://example.com/ ", SiteDescription: "Go & CMS"}
	schema := BuildWebSiteSchema(site)
	var got WebSiteSchema
	if err := json.Unmarshal([]byte(schema), &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "WebSite" || got.Context != "https://schema.org" || got.Name != name ||
		got.URL != "https://example.com/" || got.Description != site.SiteDescription || got.SearchAction != nil {
		t.Fatalf("unexpected website schema: %+v", got)
	}
	if strings.Contains(string(schema), "</script>") || strings.Contains(string(schema), "potentialAction") {
		t.Fatalf("unsafe or unexpected schema: %s", schema)
	}
	if BuildWebSiteSchema(nil) != "" || BuildWebSiteSchema(&SiteConfig{SiteURL: "https://example.com"}) != "" {
		t.Fatal("missing site data should omit website markup")
	}
	for _, origin := range []string{"", "javascript:alert(1)", "https://example.com/subsite", "https://u:p@example.com", "https://example.com?x=1", "https://example.com/#bad"} {
		site.SiteURL = origin
		if got := BuildWebSiteSchema(site); got != "" {
			t.Errorf("invalid origin %q emitted %s", origin, got)
		}
	}
}

func TestBuildBreadcrumbSchema(t *testing.T) {
	crumbs := []Breadcrumb{
		{Name: "Главная", URL: "/ru"}, {Name: "Блог", URL: "/ru/blog"},
		{Name: "Луна <&> \"Go\"", URL: "https://canonical.example/post?page=2"},
	}
	var got BreadcrumbSchema
	if err := json.Unmarshal([]byte(BuildBreadcrumbSchema(crumbs, "https://example.com/")), &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "BreadcrumbList" || got.Context != "https://schema.org" || len(got.ItemList) != 3 {
		t.Fatalf("unexpected breadcrumb schema: %+v", got)
	}
	for i, item := range got.ItemList {
		wantURL := crumbs[i].URL
		if strings.HasPrefix(wantURL, "/") {
			wantURL = "https://example.com" + wantURL
		}
		if item.Type != "ListItem" || item.Position != i+1 || item.Name != crumbs[i].Name || item.Item != wantURL {
			t.Errorf("item %d = %+v, want %q at %q", i, item, crumbs[i].Name, wantURL)
		}
	}
	for _, destination := range []string{"", "relative", "//external.example/post", "javascript:alert(1)", "https://u:p@example.com", "/post#fragment", "/bad\npath", "https://"} {
		invalid := []Breadcrumb{{Name: "Home", URL: "/"}, {Name: "Page", URL: destination}}
		if got := BuildBreadcrumbSchema(invalid, "https://example.com"); got != "" {
			t.Errorf("invalid destination %q emitted %s", destination, got)
		}
	}
	if BuildBreadcrumbSchema(crumbs[:1], "https://example.com") != "" ||
		BuildBreadcrumbSchema(crumbs, "") != "" ||
		BuildBreadcrumbSchema([]Breadcrumb{{Name: "Home", URL: "/"}, {Name: " ", URL: "/page"}}, "https://example.com") != "" {
		t.Fatal("incomplete trails should omit breadcrumb markup")
	}
}

func TestCombineJSONLD(t *testing.T) {
	article := BuildArticleSchema(&PageData{Title: "Post", Slug: "post"}, &SiteConfig{SiteURL: "https://example.com"}, time.Time{})
	crumbs := BuildBreadcrumbSchema([]Breadcrumb{{Name: "Home", URL: "/"}, {Name: "Post", URL: "/post"}}, "https://example.com")
	var schemas []json.RawMessage
	if err := json.Unmarshal([]byte(CombineJSONLD(article, "", crumbs)), &schemas); err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 2 {
		t.Fatalf("got %d schemas", len(schemas))
	}
	var gotArticle ArticleSchema
	if err := json.Unmarshal(schemas[0], &gotArticle); err != nil || gotArticle.Headline != "Post" {
		t.Fatalf("article lost during composition: %s (%v)", schemas[0], err)
	}
	if string(CombineJSONLD(article, "")) != string(article) || CombineJSONLD("", "") != "" ||
		CombineJSONLD(article, template.JS(`{"broken":`)) != "" {
		t.Fatal("single, empty or invalid schema composition changed")
	}
}
