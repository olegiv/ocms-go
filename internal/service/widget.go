// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package service provides business logic services.
package service

import (
	"context"
	"database/sql"
	"html/template"
	"sync"
	"time"

	"github.com/microcosm-cc/bluemonday"

	"github.com/olegiv/ocms-go/internal/store"
)

// htmlSanitizer provides a reusable HTML sanitization policy for widget content.
// It uses bluemonday's UGCPolicy which allows safe HTML tags for user-generated content
// while stripping potentially dangerous elements like <script>, event handlers, etc.
var htmlSanitizer = bluemonday.UGCPolicy()

// WidgetView represents a widget for template rendering.
type WidgetView struct {
	ID       int64
	Type     string
	Title    string
	Content  template.HTML
	Settings string
	IsActive bool
	Position int64
}

// WidgetService provides widget fetching and caching for the frontend.
type WidgetService struct {
	queries *store.Queries
	cache   map[string][]WidgetView
	cacheMu sync.RWMutex
	ttl     time.Duration
	lastExp time.Time
}

// NewWidgetService creates a new WidgetService.
func NewWidgetService(db *sql.DB) *WidgetService {
	return &WidgetService{
		queries: store.New(db),
		cache:   make(map[string][]WidgetView),
		ttl:     5 * time.Minute,
	}
}

// cacheKey creates a cache key for theme and area.
func cacheKey(theme, area string) string {
	return theme + ":" + area
}

// GetWidgetsForArea returns active widgets for a specific theme and area.
func (s *WidgetService) GetWidgetsForArea(ctx context.Context, theme, area string) []WidgetView {
	key := cacheKey(theme, area)

	// Check cache
	s.cacheMu.RLock()
	if widgets, ok := s.cache[key]; ok && time.Since(s.lastExp) < s.ttl {
		s.cacheMu.RUnlock()
		return widgets
	}
	s.cacheMu.RUnlock()

	// Fetch from database
	dbWidgets, err := s.queries.GetWidgetsByThemeAndArea(ctx, store.GetWidgetsByThemeAndAreaParams{
		Theme: theme,
		Area:  area,
	})
	if err != nil {
		return nil
	}

	widgets := make([]WidgetView, 0, len(dbWidgets))
	for _, w := range dbWidgets {
		widgets = append(widgets, toWidgetView(w))
	}

	// Update cache
	s.cacheMu.Lock()
	s.cache[key] = widgets
	s.lastExp = time.Now()
	s.cacheMu.Unlock()

	return widgets
}

// GetAllWidgetsForTheme returns all active widgets for a theme, grouped by area.
func (s *WidgetService) GetAllWidgetsForTheme(ctx context.Context, theme string) map[string][]WidgetView {
	dbWidgets, err := s.queries.GetAllWidgetsByTheme(ctx, theme)
	if err != nil {
		return make(map[string][]WidgetView)
	}

	result := make(map[string][]WidgetView)
	for _, w := range dbWidgets {
		if w.IsActive != 1 {
			continue
		}
		result[w.Area] = append(result[w.Area], toWidgetView(w))
	}

	return result
}

// InvalidateCache clears the widget cache.
func (s *WidgetService) InvalidateCache() {
	s.cacheMu.Lock()
	s.cache = make(map[string][]WidgetView)
	s.lastExp = time.Time{}
	s.cacheMu.Unlock()
}

// toWidgetView converts a store.Widget to WidgetView with HTML sanitization.
func toWidgetView(w store.Widget) WidgetView {
	sanitizedContent := htmlSanitizer.Sanitize(w.Content.String)
	return WidgetView{
		ID:       w.ID,
		Type:     w.WidgetType,
		Title:    w.Title.String,
		Content:  template.HTML(sanitizedContent),
		Settings: w.Settings.String,
		IsActive: w.IsActive == 1,
		Position: w.Position,
	}
}
