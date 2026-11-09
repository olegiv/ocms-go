// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package grounding resolves selected site content from storage, never through HTTP.
package grounding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/olegiv/ocms-go/internal/contentpath"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

const MaxRedirectHops = 3

// ErrUnavailable deliberately does not distinguish missing and inaccessible content.
var ErrUnavailable = errors.New("site reference unavailable")

// Access is trusted policy derived from the current authenticated principal.
type Access struct {
	UserID    int64
	AllDrafts bool
	OwnDrafts bool
}

// Resolver reuses the public router's pure language/alias/redirect rules.
type Resolver struct {
	Queries *store.Queries
	base    *url.URL
	access  Access
}

func NewResolver(q *store.Queries, siteURL string, access Access) (*Resolver, error) {
	base, err := contentpath.SiteURL(siteURL)
	if err != nil {
		return nil, err
	}
	return &Resolver{Queries: q, base: base, access: access}, nil
}

func (r *Resolver) visible(ctx context.Context, page store.Page) (bool, error) {
	if page.Status == model.PageStatusPublished || r.access.AllDrafts {
		return true, nil
	}
	if !r.access.OwnDrafts || r.access.UserID <= 0 || page.AuthorID != r.access.UserID || page.Status != model.PageStatusDraft || page.ScheduledAt.Valid {
		return false, nil
	}
	access, err := r.Queries.GetPageAIAccess(ctx, page.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && access.AiEditable, err
}

func (r *Resolver) check(ctx context.Context, page store.Page) (store.Page, error) {
	if util.IsReservedPageSlug(page.Slug) || util.ReservedRoutePath(page.Slug) != "" {
		return store.Page{}, ErrUnavailable
	}
	visible, err := r.visible(ctx, page)
	if err != nil {
		return store.Page{}, fmt.Errorf("reference visibility: %w", err)
	}
	if !visible {
		return store.Page{}, ErrUnavailable
	}
	_, valid, err := contentpath.PageLanguagePrefix(ctx, r.Queries, page)
	if err != nil {
		return store.Page{}, err
	}
	if !valid {
		return store.Page{}, ErrUnavailable
	}
	return page, nil
}

// Resolve accepts a positive page ID, page:ID, slug, alias/path or same-origin URL.
// Redirects are ordered exactly as in middleware, bounded here to three hops.
func (r *Resolver) Resolve(ctx context.Context, ref string) (store.Page, error) {
	if err := ctx.Err(); err != nil {
		return store.Page{}, err
	}
	raw := strings.TrimSpace(ref)
	idText := strings.TrimPrefix(raw, "page:")
	if id, err := strconv.ParseInt(idText, 10, 64); err == nil && id > 0 && strconv.FormatInt(id, 10) == idText {
		page, err := r.Queries.GetPageByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return store.Page{}, ErrUnavailable
		}
		if err != nil {
			return store.Page{}, err
		}
		return r.check(ctx, page)
	}
	if raw == "" || strings.HasPrefix(raw, "page:") {
		return store.Page{}, ErrUnavailable
	}
	parsed, parseErr := url.Parse(raw)
	if parseErr != nil {
		return store.Page{}, ErrUnavailable
	}
	for _, part := range strings.Split(parsed.Path, "/") {
		if part == "." || part == ".." {
			return store.Page{}, ErrUnavailable
		}
	}
	local, err := contentpath.LocalURL(r.base, raw)
	if err != nil {
		return store.Page{}, ErrUnavailable
	}
	page, err := r.resolvePath(ctx, local, false, nil)
	if err != nil {
		return store.Page{}, err
	}
	if page == nil {
		return store.Page{}, ErrUnavailable
	}
	return *page, nil
}

// siteFacts are the redirects and active languages one path walk reads. A run
// that checks many links loads them once instead of once per link.
type siteFacts struct {
	redirects []store.Redirect
	languages map[string]store.Language
}

func (r *Resolver) loadSiteFacts(ctx context.Context) (siteFacts, error) {
	redirects, err := r.Queries.ListEnabledRedirects(ctx)
	if err != nil {
		return siteFacts{}, err
	}
	languages, err := r.Queries.ListActiveLanguages(ctx)
	if err != nil {
		return siteFacts{}, err
	}
	return siteFacts{redirects: redirects, languages: contentpath.ActiveLanguages(languages)}, nil
}

// resolvePath shares one bounded walk between reference reads and inline-link checks.
// Public page/alias selection precedes an authorized draft fallback, exactly as in the frontend.
// A nil facts loads the redirects and languages for this one walk.
func (r *Resolver) resolvePath(ctx context.Context, local *url.URL, link bool, facts *siteFacts) (*store.Page, error) {
	if facts == nil {
		loaded, err := r.loadSiteFacts(ctx)
		if err != nil {
			return nil, err
		}
		facts = &loaded
	}
	redirects, langMap := facts.redirects, facts.languages
	visited := map[string]bool{}
	for hops := 0; ; hops++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := local.Path + "?" + local.RawQuery
		if visited[key] || util.IsPrivateRoutePath(local.Path) {
			return nil, ErrUnavailable
		}
		visited[key] = true
		rules := redirects
		if preview.RewritesSharePath(local.Path) {
			// HTTP bypasses redirect rules on scrubbed paths, and only
			// canonical paths can reach the stored public alias lookup.
			if local.RawPath != "" {
				return nil, ErrUnavailable
			}
			rules = nil
		}
		target, _, matched, err := contentpath.FirstRedirect(rules, local.Path, local.RawQuery)
		if err != nil {
			return nil, ErrUnavailable
		}
		if matched {
			if hops >= MaxRedirectHops {
				return nil, ErrUnavailable
			}
			local, err = contentpath.LocalURL(local, target)
			if err != nil {
				return nil, ErrUnavailable
			}
			continue
		}
		routePath := contentpath.RoutingPath(local.Path, local.RawPath)
		explicit, stripped := "", routePath
		if lang, rest, ok := contentpath.MatchLanguagePrefix(routePath, langMap); ok {
			explicit, stripped = lang.Code, rest
		}
		language, err := contentpath.RouteLanguage(ctx, r.Queries, explicit)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnavailable
		}
		if err != nil {
			return nil, err
		}
		slug := strings.TrimPrefix(stripped, "/")
		if link && (slug == "" || slug == "blog" || slug == "search") {
			return nil, nil
		}
		idPage, other, err := contentpath.DynamicRoute(ctx, r.Queries, stripped, explicit, language.Code)
		if err != nil {
			return nil, err
		}
		if other {
			if link {
				return nil, nil
			}
			return nil, ErrUnavailable
		}
		if idPage != nil {
			prefix, ok, err := contentpath.PageLanguagePrefix(ctx, r.Queries, *idPage)
			if err != nil {
				return nil, err
			}
			if !ok || hops >= MaxRedirectHops {
				return nil, ErrUnavailable
			}
			path := "/" + idPage.Slug
			if prefix != "" {
				path = "/" + prefix + path
			}
			local, err = contentpath.LocalURL(r.base, path)
			if err != nil {
				return nil, ErrUnavailable
			}
			continue
		}

		if util.IsReservedPageSlug(slug) || util.ReservedRoutePath(slug) != "" {
			return nil, ErrUnavailable
		}
		single := util.IsValidSlug(slug)
		if single {
			page, err := contentpath.PublishedPage(ctx, r.Queries, slug, language.Code)
			if err == nil {
				checked, checkErr := r.check(ctx, page)
				return &checked, checkErr
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
		alias := contentpath.AliasPath(local.Path, explicit)
		if util.IsValidAlias(alias) {
			page, err := r.Queries.GetPublishedPageByAlias(ctx, alias)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if err == nil {
				prefix, valid, err := contentpath.AliasLanguagePrefix(ctx, r.Queries, page, explicit)
				if err != nil {
					return nil, err
				}
				if !valid || hops >= MaxRedirectHops {
					return nil, ErrUnavailable
				}
				path := "/" + page.Slug
				if prefix != "" {
					path = "/" + prefix + path
				}
				local, err = contentpath.LocalURL(r.base, path)
				if err != nil {
					return nil, ErrUnavailable
				}
				continue
			}
		}
		if single {
			page, err := r.Queries.GetPageBySlug(ctx, slug)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if err == nil && page.LanguageCode == language.Code {
				checked, checkErr := r.check(ctx, page)
				return &checked, checkErr
			}
		}
		return nil, ErrUnavailable
	}
}

// URL returns only a published page's canonical public URL, never a share token.
func (r *Resolver) URL(ctx context.Context, page store.Page) (string, error) {
	if page.Status != model.PageStatusPublished {
		return "", nil
	}
	prefix, ok, err := contentpath.PageLanguagePrefix(ctx, r.Queries, page)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrUnavailable
	}
	path := "/" + page.Slug
	if prefix != "" {
		path = "/" + prefix + path
	}
	return r.base.ResolveReference(&url.URL{Path: path}).String(), nil
}

// InternalLink checks page links plus the shared language namespace's homepage.
func (r *Resolver) InternalLink(ctx context.Context, path string) (bool, error) {
	return r.internalLink(ctx, path, nil)
}

// LinkChecker loads the redirects and active languages once and returns a
// checker equivalent to InternalLink for every link of one draft run. The
// facts are a snapshot taken now, which is how one validation pass sees them.
func (r *Resolver) LinkChecker(ctx context.Context) (func(context.Context, string) (bool, error), error) {
	facts, err := r.loadSiteFacts(ctx)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, path string) (bool, error) {
		return r.internalLink(ctx, path, &facts)
	}, nil
}

func (r *Resolver) internalLink(ctx context.Context, path string, facts *siteFacts) (bool, error) {
	local, err := contentpath.LocalURL(r.base, path)
	if err != nil {
		return false, nil
	}
	_, err = r.resolvePath(ctx, local, true, facts)
	if errors.Is(err, ErrUnavailable) {
		return false, nil
	}
	return err == nil, err
}
