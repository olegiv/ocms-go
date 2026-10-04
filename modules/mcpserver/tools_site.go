// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/seo"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// maxPerPage is the largest per_page the v2 list services honour.
const maxPerPage = 100

// SiteInfoInput is the (empty) input of get_site_info.
type SiteInfoInput struct{}

// SiteInfo is the output of get_site_info.
type SiteInfo struct {
	Site      SiteDetails    `json:"site"`
	Languages []LanguageInfo `json:"languages" doc:"Active languages. Pages in the default language have no URL prefix."`
	Access    AccessDetails  `json:"access"`
	Server    ServerDetails  `json:"server"`
}

// SiteDetails describes the site.
type SiteDetails struct {
	Name            string `json:"name" doc:"Site name."`
	Description     string `json:"description,omitempty" doc:"Site description."`
	URL             string `json:"url,omitempty" doc:"Public site URL; empty unless the administrator configured a valid absolute http(s) URL."`
	DefaultLanguage string `json:"default_language,omitempty" doc:"Code of the default language."`
}

// LanguageInfo describes one active language.
type LanguageInfo struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	NativeName string `json:"native_name,omitempty"`
	IsDefault  bool   `json:"is_default"`
}

// AccessDetails describes what the calling API key may access.
type AccessDetails struct {
	KeyName       string     `json:"key_name" doc:"Name the administrator gave the API key."`
	KeyPrefix     string     `json:"key_prefix" doc:"Public prefix of the API key."`
	Permissions   []string   `json:"permissions" doc:"Permissions granted to the API key."`
	DraftsVisible bool       `json:"drafts_visible" doc:"Whether unpublished drafts are visible: the site must expose drafts over MCP and the key must hold pages:read."`
	ExpiresAt     *time.Time `json:"expires_at,omitempty" doc:"When the API key expires."`
}

// ServerDetails describes this MCP server.
type ServerDetails struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	ReadOnly   bool   `json:"read_only" doc:"True when the server exposes read-only tools only."`
	MaxPerPage int    `json:"max_per_page" doc:"Largest per_page value list tools accept."`
}

// getSiteInfo implements get_site_info.
func (m *Module) getSiteInfo(ctx context.Context, call *toolCall, _ SiteInfoInput) (SiteInfo, error) {
	languages, err := m.svc.queries.ListActiveLanguages(ctx)
	if err != nil {
		return SiteInfo{}, fmt.Errorf("listing active languages: %w", err)
	}
	permissions := slices.Clone(call.identity.scopes)
	if permissions == nil {
		permissions = []string{}
	}
	name, err := m.configValue(ctx, model.ConfigKeySiteName)
	if err != nil {
		return SiteInfo{}, err
	}
	description, err := m.configValue(ctx, model.ConfigKeySiteDescription)
	if err != nil {
		return SiteInfo{}, err
	}
	siteURL, _, err := m.resolveSiteURL(ctx)
	if err != nil {
		return SiteInfo{}, err
	}
	out := SiteInfo{
		Site: SiteDetails{
			Name:        name,
			Description: description,
			URL:         siteURL,
		},
		Languages: make([]LanguageInfo, 0, len(languages)),
		Access: AccessDetails{
			KeyName:       call.identity.key.Name,
			KeyPrefix:     call.identity.key.KeyPrefix,
			Permissions:   permissions,
			DraftsVisible: draftsVisible(call.actor),
		},
		Server: ServerDetails{
			Name:       "oCMS",
			Version:    moduleVersion,
			ReadOnly:   true,
			MaxPerPage: maxPerPage,
		},
	}
	if call.identity.key.ExpiresAt.Valid {
		expires := call.identity.key.ExpiresAt.Time
		out.Access.ExpiresAt = &expires
	}
	for _, lang := range routableLanguages(languages) {
		out.Languages = append(out.Languages, LanguageInfo{
			Code:       lang.Code,
			Name:       lang.Name,
			NativeName: lang.NativeName,
			IsDefault:  lang.IsDefault,
		})
		if lang.IsDefault {
			out.Site.DefaultLanguage = lang.Code
		}
	}
	return out, nil
}

// routableLanguages keeps the languages the public router serves; others
// stay editable in the admin UI but never route.
func routableLanguages(languages []store.Language) []store.Language {
	return slices.DeleteFunc(slices.Clone(languages), func(lang store.Language) bool {
		return !util.IsRoutableLanguageCode(lang.Code)
	})
}

// configValue reads a site config value, preferring the shared config cache.
// An unset key reads as "". Database failures are returned, not hidden: an
// empty value would look like a deliberate configuration.
func (m *Module) configValue(ctx context.Context, key string) (string, error) {
	if m.svc.cache != nil {
		// The cache reports a missing key as "" and fails only when it
		// cannot load; then the database is asked directly.
		if value, err := m.svc.cache.GetConfig(ctx, key); err == nil {
			return value, nil
		}
	}
	cfg, err := m.svc.queries.GetConfigByKey(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading config %s: %w", key, err)
	}
	return cfg.Value, nil
}

// siteURLStatus says whether the configured site URL can be used.
type siteURLStatus int

const (
	siteURLUnset siteURLStatus = iota
	siteURLValid
	siteURLInvalid
)

// resolveSiteURL returns the configured public site URL without a trailing
// slash, and whether it is set and usable; the URL is "" unless it is valid. Only an absolute http(s) URL is
// used; like the discovery documents, tool output never falls back to the
// request Host, which a reverse proxy may have rewritten to an internal
// upstream. The site configuration page does not validate the value, so an
// unusable one is logged once, until the value changes.
func (m *Module) resolveSiteURL(ctx context.Context) (string, siteURLStatus, error) {
	raw, err := m.configValue(ctx, model.ConfigKeySiteURL)
	if err != nil {
		return "", siteURLUnset, err
	}
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return "", siteURLUnset, nil
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		if prev := m.invalidSiteURL.Swap(&value); prev == nil || *prev != value {
			m.logger.Warn("configured site URL is not an absolute http(s) URL; MCP results omit page URLs",
				"site_url", value)
		}
		return "", siteURLInvalid, nil
	}
	return value, siteURLValid, nil
}

// pageURLs builds public page URLs for one tool call.
type pageURLs struct {
	base string
	// isDefault maps each active, routable language code to whether it is
	// the default (prefix-less) language.
	isDefault map[string]bool
}

// newPageURLs loads what page URL construction needs. Without a usable site
// URL it builds nothing and skips the language query.
func (m *Module) newPageURLs(ctx context.Context) (pageURLs, error) {
	base, status, err := m.resolveSiteURL(ctx)
	if err != nil {
		return pageURLs{}, err
	}
	if status != siteURLValid {
		return pageURLs{}, nil
	}
	languages, err := m.svc.queries.ListActiveLanguages(ctx)
	if err != nil {
		return pageURLs{}, fmt.Errorf("listing languages for page URLs: %w", err)
	}
	urls := pageURLs{base: base, isDefault: make(map[string]bool, len(languages))}
	for _, lang := range routableLanguages(languages) {
		urls.isDefault[lang.Code] = lang.IsDefault
	}
	return urls, nil
}

// forPage returns the public URL of a published page in an active language,
// or "" when the page has no public URL.
func (u pageURLs) forPage(status, slug, languageCode string) string {
	if u.base == "" || status != model.PageStatusPublished {
		return ""
	}
	isDefault, active := u.isDefault[languageCode]
	if !active {
		return ""
	}
	path, ok := seo.CanonicalLanguagePath("/"+slug, languageCode, isDefault)
	if !ok {
		return ""
	}
	return u.base + path
}
