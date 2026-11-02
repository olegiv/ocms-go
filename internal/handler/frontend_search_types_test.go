// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"log/slog"
	"testing"

	"github.com/olegiv/ocms-go/internal/service"
)

func TestFrontendSearchResultRetainsContentType(t *testing.T) {
	db, _ := testHandlerSetup(t)
	h := NewFrontendHandler(db, testThemeManager(), nil, slog.Default(), nil, nil)
	for _, pageType := range []string{"post", "page"} {
		view := h.searchResultToPageView(context.Background(), service.SearchResult{ID: 1, Slug: "example", PageType: pageType}, "/en")
		if view.Type != pageType || view.URL != "/en/example" {
			t.Errorf("search type %q: type=%q URL=%q", pageType, view.Type, view.URL)
		}
	}
}
