// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/seo"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

var (
	errFeedNotFound    = errors.New("feed not found")
	errFeedUnavailable = errors.New("feed requires a valid configured site_url")
)

// FeedLink describes a subscription link exposed to frontend themes.
type FeedLink struct {
	Title    string
	MIMEType string
	URL      string
}

// RSSFeed serves site, category and tag subscriptions in RSS 2.0 format.
func (h *FrontendHandler) RSSFeed(w http.ResponseWriter, r *http.Request) {
	h.serveFeed(w, r, "rss.xml", seo.RSSMediaType, seo.BuildRSSFeed)
}

// AtomFeed serves site, category and tag subscriptions in Atom 1.0 format.
func (h *FrontendHandler) AtomFeed(w http.ResponseWriter, r *http.Request) {
	h.serveFeed(w, r, "atom.xml", seo.AtomMediaType, seo.BuildAtomFeed)
}

func (h *FrontendHandler) serveFeed(w http.ResponseWriter, r *http.Request, filename, mediaType string, build func(seo.Feed) ([]byte, error)) {
	// Failures must never be cached or mistaken for valid empty subscriptions.
	w.Header().Set("Cache-Control", "no-store")
	feed, err := h.loadFeed(r, filename)
	if err != nil {
		status, message := http.StatusInternalServerError, "Failed to generate feed"
		switch {
		case errors.Is(err, errFeedNotFound):
			status, message = http.StatusNotFound, "Feed not found"
		case errors.Is(err, errFeedUnavailable):
			status, message = http.StatusServiceUnavailable, "Feed is unavailable until site_url is a valid HTTP(S) origin"
		default:
			if h.logger != nil {
				h.logger.Error("failed to generate feed", "error", err)
			}
		}
		writeFeedError(w, r, status, message)
		return
	}
	body, err := build(feed)
	if err != nil {
		writeFeedError(w, r, http.StatusInternalServerError, "Failed to serialize feed")
		return
	}

	// Weak validators remain valid when response compression changes the bytes.
	etag := fmt.Sprintf(`W/"%x"`, sha256.Sum256(body))
	w.Header().Set("Content-Type", mediaType+"; charset=utf-8")
	w.Header().Set("Cache-Control", "public, no-cache")
	w.Header().Set("ETag", etag)
	if feedETagMatches(r.Header.Values("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func writeFeedError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		return
	}
	http.Error(w, message, status)
}

func feedETagMatches(values []string, etag string) bool {
	opaque := strings.TrimPrefix(etag, "W/")
	for _, value := range values {
		for _, candidate := range strings.Split(value, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" || strings.TrimPrefix(candidate, "W/") == opaque {
				return true
			}
		}
	}
	return false
}

func (h *FrontendHandler) loadFeed(r *http.Request, filename string) (seo.Feed, error) {
	ctx := r.Context()
	language, err := h.feedLanguage(r)
	if err != nil {
		return seo.Feed{}, err
	}
	feed := seo.Feed{Language: language.Code, UpdatedAt: language.UpdatedAt}
	config := make(map[string]string)
	for _, key := range []string{model.ConfigKeySiteURL, model.ConfigKeySiteName, model.ConfigKeySiteDescription} {
		value, updatedAt, err := h.feedConfig(ctx, key, language.Code)
		if err != nil {
			return seo.Feed{}, err
		}
		config[key] = value
		feed.UpdatedAt = laterFeedTime(feed.UpdatedAt, updatedAt)
	}
	origin, err := seo.NormalizeFeedOrigin(config[model.ConfigKeySiteURL])
	if err != nil {
		return seo.Feed{}, errFeedUnavailable
	}
	feed.Title = strings.TrimSpace(config[model.ConfigKeySiteName])
	if feed.Title == "" {
		feed.Title = "Opossum CMS"
	}
	feed.Author = feed.Title
	feed.Description = config[model.ConfigKeySiteDescription]
	if feed.Description == "" {
		feed.Description = feed.Title
	}
	params := store.ListFeedPostsParams{LanguageCode: language.Code}
	path, err := h.feedTaxonomy(ctx, r, &feed, &params)
	if err != nil {
		return seo.Feed{}, err
	}
	prefix := ""
	if !language.IsDefault {
		prefix = "/" + language.Code
	}
	feed.URL = origin + prefix + path + "/" + filename
	feed.SiteURL = origin + prefix + path
	rows, err := h.queries.ListFeedPosts(ctx, params)
	if err != nil {
		return seo.Feed{}, fmt.Errorf("list feed posts: %w", err)
	}
	for _, row := range rows {
		if !util.IsValidSlug(row.Slug) {
			continue // Legacy unroutable posts must not advertise broken URLs.
		}
		published := row.CreatedAt
		if row.PublishedAt.Valid {
			published = row.PublishedAt.Time
		}
		updated := laterFeedTime(row.UpdatedAt, published)
		feed.UpdatedAt = laterFeedTime(feed.UpdatedAt, updated)
		feed.Entries = append(feed.Entries, seo.FeedEntry{
			ID:    origin + "/page/" + strconv.FormatInt(row.ID, 10),
			Title: row.Title, URL: origin + prefix + "/" + row.Slug,
			Summary: seo.FeedSummary(row.Summary, row.Body), Author: row.AuthorName,
			PublishedAt: published, UpdatedAt: updated,
		})
	}
	return feed, nil
}

func (h *FrontendHandler) feedLanguage(r *http.Request) (store.Language, error) {
	code, valid := explicitPageLanguage(r)
	if !valid {
		return store.Language{}, errFeedNotFound
	}
	var language store.Language
	var err error
	if code == "" {
		language, err = h.queries.GetDefaultLanguage(r.Context())
	} else {
		language, err = h.queries.GetLanguageByCode(r.Context(), code)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return store.Language{}, errFeedNotFound
	}
	if err != nil {
		return store.Language{}, fmt.Errorf("resolve feed language: %w", err)
	}
	if !language.IsActive || !util.IsRoutableLanguageCode(language.Code) {
		return store.Language{}, errFeedNotFound
	}
	return language, nil
}

func (h *FrontendHandler) feedConfig(ctx context.Context, key, languageCode string) (string, time.Time, error) {
	cfg, err := h.queries.GetConfigByKey(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read feed configuration: %w", err)
	}
	if !model.IsTranslatableConfigKey(key) {
		return cfg.Value, cfg.UpdatedAt, nil
	}
	translation, err := h.queries.GetConfigTranslationByKeyAndLangCode(ctx, store.GetConfigTranslationByKeyAndLangCodeParams{
		ConfigKey: key, Code: languageCode,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, fmt.Errorf("read feed configuration translation: %w", err)
	}
	if err == nil && translation.Value != "" {
		return translation.Value, laterFeedTime(cfg.UpdatedAt, translation.UpdatedAt), nil
	}
	return cfg.Value, cfg.UpdatedAt, nil
}

func (h *FrontendHandler) feedTaxonomy(ctx context.Context, r *http.Request, feed *seo.Feed, params *store.ListFeedPostsParams) (string, error) {
	scope, slug := chi.URLParam(r, "taxonomy"), chi.URLParam(r, "slug")
	if scope == "" {
		return "", nil
	}
	if !util.IsValidSlug(slug) {
		return "", errFeedNotFound
	}
	var name, languageCode string
	var updatedAt time.Time
	var err error
	switch scope {
	case "category":
		var category store.Category
		category, err = h.queries.GetCategoryBySlug(ctx, slug)
		name, languageCode, updatedAt = category.Name, category.LanguageCode, category.UpdatedAt
		params.CategoryID = category.ID
		if category.Description.Valid && category.Description.String != "" {
			feed.Description = category.Description.String
		}
	case "tag":
		var tag store.Tag
		tag, err = h.queries.GetTagBySlug(ctx, slug)
		name, languageCode, updatedAt = tag.Name, tag.LanguageCode, tag.UpdatedAt
		params.TagID = tag.ID
	default:
		return "", errFeedNotFound
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", errFeedNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve feed taxonomy: %w", err)
	}
	if languageCode != feed.Language {
		return "", errFeedNotFound
	}
	feed.Title += " — " + name
	feed.UpdatedAt = laterFeedTime(feed.UpdatedAt, updatedAt)
	return "/" + scope + "/" + slug, nil
}

func laterFeedTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func (data *BaseTemplateData) addFeedLinks(path, title string) {
	origin, err := seo.NormalizeFeedOrigin(data.SiteURL)
	if err != nil || data.CurrentLanguage == nil || !util.IsRoutableLanguageCode(data.LangCode) {
		return
	}
	baseURL := origin + data.LangPrefix + path
	data.FeedLinks = append(data.FeedLinks,
		FeedLink{Title: title + " — RSS", MIMEType: seo.RSSMediaType, URL: baseURL + "/rss.xml"},
		FeedLink{Title: title + " — Atom", MIMEType: seo.AtomMediaType, URL: baseURL + "/atom.xml"},
	)
}
