// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"html/template"
	"net/url"
	"strconv"
	"strings"

	"github.com/olegiv/ocms-go/internal/contentpath"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// previewMenu filters at the source-ID boundary, before titles or links reach a theme.
func (h *FrontendHandler) previewMenu(ctx context.Context, slug, language string) []service.MenuItem {
	items := h.menuService.GetMenuForLanguage(slug, language)
	var filter func([]service.MenuItem) []service.MenuItem
	filter = func(items []service.MenuItem) []service.MenuItem {
		var out []service.MenuItem
		for _, item := range items {
			if item.PageID != nil {
				p, err := h.queries.GetPageByID(ctx, *item.PageID)
				if err != nil || !h.publicPreviewMenuPage(ctx, p) {
					continue
				}
			} else if !h.publicPreviewMenuURL(ctx, item.URL) {
				continue
			}
			item.Children = filter(item.Children)
			out = append(out, item)
		}
		return out
	}
	return filter(items)
}

func (h *FrontendHandler) publicPreviewMenuPage(ctx context.Context, p store.Page) bool {
	if p.Status != "published" {
		return false
	}
	_, ok, err := contentpath.PageLanguagePrefix(ctx, h.queries, p)
	return err == nil && ok
}

func (h *FrontendHandler) publicPreviewMenuURL(ctx context.Context, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || strings.Contains(u.Path, "/preview/") {
		return false
	}
	if u.Path == "" && u.Host == "" && u.Scheme == "" {
		return true
	}
	if u.Scheme != "" && u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	if u.Host != "" {
		base, siteErr := contentpath.SiteURL(h.getConfiguredSiteURL(ctx))
		if siteErr != nil {
			return false
		}
		target := *u
		if target.Scheme == "" {
			target.Scheme = base.Scheme
		}
		origin, e := contentpath.Origin(&target)
		configured, ce := contentpath.Origin(base)
		if e != nil || ce != nil {
			return false
		}
		if origin != configured {
			return true
		}
	}
	languages, err := h.queries.ListActiveLanguages(ctx)
	if err != nil {
		return false
	}
	language, remaining, explicit := contentpath.MatchLanguagePrefix(u.Path, contentpath.ActiveLanguages(languages))
	if !explicit {
		language, err = contentpath.RouteLanguage(ctx, h.queries, "")
		remaining = u.Path
		if err != nil {
			return false
		}
	}
	path := strings.Trim(remaining, "/")
	if util.IsPrivateRoutePath("/" + path) {
		return false
	}
	if path == "" || path == "blog" || path == "search" {
		return true
	}
	if strings.HasPrefix(path, "page/") {
		id, e := strconv.ParseInt(strings.TrimPrefix(path, "page/"), 10, 64)
		if e != nil {
			return false
		}
		p, e := h.queries.GetPageByID(ctx, id)
		return e == nil && h.publicPreviewMenuPage(ctx, p)
	}
	if p, e := h.queries.GetPageBySlug(ctx, path); e == nil {
		return p.LanguageCode == language.Code && h.publicPreviewMenuPage(ctx, p)
	}
	if p, e := h.queries.GetPageByAlias(ctx, path); e == nil {
		return p.LanguageCode == language.Code && h.publicPreviewMenuPage(ctx, p)
	}
	_, public, err := contentpath.DynamicRoute(ctx, h.queries, "/"+path, language.Code, language.Code)
	return err == nil && public
}

// HTML templates get request-local overrides; original theme functions stay unchanged.
func (h *FrontendHandler) previewTemplateFuncs(ctx context.Context, language string) template.FuncMap {
	blocked := template.FuncMap{}
	if h.moduleFuncsProvider != nil {
		for name := range h.moduleFuncsProvider.AllTemplateFuncs() {
			blocked[name] = func(...any) any { return nil }
		}
	}
	blocked["getMenu"] = func(slug string) []service.MenuItem { return h.previewMenu(ctx, slug, language) }
	blocked["getMenuForLanguage"] = func(slug, lang string) []service.MenuItem { return h.previewMenu(ctx, slug, lang) }
	blocked["renderWidget"] = func(...any) template.HTML { return "" }
	return blocked
}
