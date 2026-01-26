// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewWidgetsHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewWidgetsHandler(db, nil, sm, nil)
	if h == nil {
		t.Fatal("NewWidgetsHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestWidgetTypes(t *testing.T) {
	if len(WidgetTypes) == 0 {
		t.Error("WidgetTypes should not be empty")
	}

	// Check for expected widget types
	expectedTypes := map[string]bool{
		"text":         false,
		"recent_posts": false,
		"categories":   false,
		"tags":         false,
		"search":       false,
		"custom_menu":  false,
	}

	for _, wt := range WidgetTypes {
		if wt.ID == "" {
			t.Error("widget type ID should not be empty")
		}
		if wt.Name == "" {
			t.Error("widget type Name should not be empty")
		}
		if _, exists := expectedTypes[wt.ID]; exists {
			expectedTypes[wt.ID] = true
		}
	}

	for typeID, found := range expectedTypes {
		if !found {
			t.Errorf("expected widget type %q not found", typeID)
		}
	}
}

func TestWidgetCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	widget, err := queries.CreateWidget(context.Background(), store.CreateWidgetParams{
		Theme:      "default",
		Area:       "sidebar",
		WidgetType: "text",
		Title:      sql.NullString{String: "Test Widget", Valid: true},
		Content:    sql.NullString{String: "<p>Hello World</p>", Valid: true},
		Position:   0,
		IsActive:   1,
	})
	if err != nil {
		t.Fatalf("CreateWidget failed: %v", err)
	}

	if widget.Theme != "default" {
		t.Errorf("Theme = %q, want %q", widget.Theme, "default")
	}
	if widget.Area != "sidebar" {
		t.Errorf("Area = %q, want %q", widget.Area, "sidebar")
	}
	if widget.WidgetType != "text" {
		t.Errorf("WidgetType = %q, want %q", widget.WidgetType, "text")
	}
	if !widget.Title.Valid || widget.Title.String != "Test Widget" {
		t.Errorf("Title = %v, want %q", widget.Title, "Test Widget")
	}
}

func TestWidgetList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create test widgets
	widgets := []store.CreateWidgetParams{
		{Theme: "default", Area: "sidebar", WidgetType: "text", Position: 0, IsActive: 1},
		{Theme: "default", Area: "sidebar", WidgetType: "search", Position: 1, IsActive: 1},
		{Theme: "default", Area: "footer", WidgetType: "text", Position: 0, IsActive: 1},
	}
	for _, w := range widgets {
		if _, err := queries.CreateWidget(context.Background(), w); err != nil {
			t.Fatalf("CreateWidget failed: %v", err)
		}
	}

	t.Run("get all by theme", func(t *testing.T) {
		result, err := queries.GetAllWidgetsByTheme(context.Background(), "default")
		if err != nil {
			t.Fatalf("GetAllWidgetsByTheme failed: %v", err)
		}
		if len(result) != 3 {
			t.Errorf("got %d widgets, want 3", len(result))
		}
	})

	t.Run("get by theme and area", func(t *testing.T) {
		result, err := queries.GetWidgetsByThemeAndArea(context.Background(), store.GetWidgetsByThemeAndAreaParams{
			Theme: "default",
			Area:  "sidebar",
		})
		if err != nil {
			t.Fatalf("GetWidgetsByThemeAndArea failed: %v", err)
		}
		if len(result) != 2 {
			t.Errorf("got %d widgets, want 2", len(result))
		}
	})
}

func TestWidgetUpdate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	widget, err := queries.CreateWidget(context.Background(), store.CreateWidgetParams{
		Theme:      "default",
		Area:       "sidebar",
		WidgetType: "text",
		Title:      sql.NullString{String: "Original Title", Valid: true},
		Position:   0,
		IsActive:   1,
	})
	if err != nil {
		t.Fatalf("CreateWidget failed: %v", err)
	}

	_, err = queries.UpdateWidget(context.Background(), store.UpdateWidgetParams{
		ID:         widget.ID,
		WidgetType: "text",
		Title:      sql.NullString{String: "Updated Title", Valid: true},
		Content:    sql.NullString{String: "Updated content", Valid: true},
		Position:   1,
		IsActive:   0,
	})
	if err != nil {
		t.Fatalf("UpdateWidget failed: %v", err)
	}

	updated, err := queries.GetWidget(context.Background(), widget.ID)
	if err != nil {
		t.Fatalf("GetWidget failed: %v", err)
	}

	if !updated.Title.Valid || updated.Title.String != "Updated Title" {
		t.Errorf("Title = %v, want %q", updated.Title, "Updated Title")
	}
	if updated.IsActive != 0 {
		t.Error("IsActive should be 0")
	}
	if updated.Position != 1 {
		t.Errorf("Position = %d, want 1", updated.Position)
	}
}

func TestWidgetDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	widget, err := queries.CreateWidget(context.Background(), store.CreateWidgetParams{
		Theme:      "default",
		Area:       "sidebar",
		WidgetType: "text",
		Position:   0,
		IsActive:   1,
	})
	if err != nil {
		t.Fatalf("CreateWidget failed: %v", err)
	}

	if err := queries.DeleteWidget(context.Background(), widget.ID); err != nil {
		t.Fatalf("DeleteWidget failed: %v", err)
	}

	_, err = queries.GetWidget(context.Background(), widget.ID)
	if err == nil {
		t.Error("expected error when getting deleted widget")
	}
}

func TestWidgetUpdatePosition(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	widget, err := queries.CreateWidget(context.Background(), store.CreateWidgetParams{
		Theme:      "default",
		Area:       "sidebar",
		WidgetType: "text",
		Position:   0,
		IsActive:   1,
	})
	if err != nil {
		t.Fatalf("CreateWidget failed: %v", err)
	}

	if err := queries.UpdateWidgetPosition(context.Background(), store.UpdateWidgetPositionParams{
		ID:       widget.ID,
		Position: 5,
	}); err != nil {
		t.Fatalf("UpdateWidgetPosition failed: %v", err)
	}

	updated, err := queries.GetWidget(context.Background(), widget.ID)
	if err != nil {
		t.Fatalf("GetWidget failed: %v", err)
	}

	if updated.Position != 5 {
		t.Errorf("Position = %d, want 5", updated.Position)
	}
}

func TestWidgetGetMaxPosition(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create widgets with different positions
	widgets := []store.CreateWidgetParams{
		{Theme: "default", Area: "sidebar", WidgetType: "text", Position: 2, IsActive: 1},
		{Theme: "default", Area: "sidebar", WidgetType: "search", Position: 5, IsActive: 1},
		{Theme: "default", Area: "sidebar", WidgetType: "tags", Position: 1, IsActive: 1},
	}
	for _, w := range widgets {
		if _, err := queries.CreateWidget(context.Background(), w); err != nil {
			t.Fatalf("CreateWidget failed: %v", err)
		}
	}

	maxPos, err := queries.GetMaxWidgetPosition(context.Background(), store.GetMaxWidgetPositionParams{
		Theme: "default",
		Area:  "sidebar",
	})
	if err != nil {
		t.Fatalf("GetMaxWidgetPosition failed: %v", err)
	}

	// Expect max to be 5
	if maxPos == nil {
		t.Fatal("maxPos should not be nil")
	}
	if v, ok := maxPos.(int64); ok && v != 5 {
		t.Errorf("maxPos = %d, want 5", v)
	}
}

func TestWidgetWithSettings(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	settings := `{"posts_count": 5, "show_date": true}`
	widget, err := queries.CreateWidget(context.Background(), store.CreateWidgetParams{
		Theme:      "default",
		Area:       "sidebar",
		WidgetType: "recent_posts",
		Title:      sql.NullString{String: "Recent Posts", Valid: true},
		Settings:   sql.NullString{String: settings, Valid: true},
		Position:   0,
		IsActive:   1,
	})
	if err != nil {
		t.Fatalf("CreateWidget failed: %v", err)
	}

	if !widget.Settings.Valid {
		t.Error("Settings should be valid")
	}
	if widget.Settings.String != settings {
		t.Errorf("Settings = %q, want %q", widget.Settings.String, settings)
	}
}

func TestWidgetActiveFilter(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	// Create active and inactive widgets
	widgets := []store.CreateWidgetParams{
		{Theme: "default", Area: "sidebar", WidgetType: "text", Position: 0, IsActive: 1},
		{Theme: "default", Area: "sidebar", WidgetType: "search", Position: 1, IsActive: 0},
		{Theme: "default", Area: "sidebar", WidgetType: "tags", Position: 2, IsActive: 1},
	}
	for _, w := range widgets {
		if _, err := queries.CreateWidget(context.Background(), w); err != nil {
			t.Fatalf("CreateWidget failed: %v", err)
		}
	}

	// Get all widgets for theme and area
	result, err := queries.GetWidgetsByThemeAndArea(context.Background(), store.GetWidgetsByThemeAndAreaParams{
		Theme: "default",
		Area:  "sidebar",
	})
	if err != nil {
		t.Fatalf("GetWidgetsByThemeAndArea failed: %v", err)
	}

	// Filter to count active ones
	activeCount := 0
	for _, w := range result {
		if w.IsActive == 1 {
			activeCount++
		}
	}

	if activeCount != 2 {
		t.Errorf("got %d active widgets, want 2", activeCount)
	}
}

func TestWidgetAreaWithWidgets(t *testing.T) {
	area := WidgetAreaWithWidgets{
		Widgets: []store.Widget{
			{ID: 1, WidgetType: "text"},
			{ID: 2, WidgetType: "search"},
		},
	}

	if len(area.Widgets) != 2 {
		t.Errorf("got %d widgets, want 2", len(area.Widgets))
	}
}

func TestWidgetsListData(t *testing.T) {
	data := WidgetsListData{
		WidgetAreas: []WidgetAreaWithWidgets{},
		WidgetTypes: WidgetTypes,
	}

	if data.WidgetAreas == nil {
		t.Error("WidgetAreas should not be nil")
	}
	if len(data.WidgetTypes) == 0 {
		t.Error("WidgetTypes should not be empty")
	}
}

func TestCreateWidgetRequest(t *testing.T) {
	req := CreateWidgetRequest{
		Theme:      "default",
		Area:       "sidebar",
		WidgetType: "text",
		Title:      "My Widget",
		Content:    "<p>Hello</p>",
		Settings:   `{"key": "value"}`,
	}

	if req.Theme != "default" {
		t.Errorf("Theme = %q, want %q", req.Theme, "default")
	}
	if req.WidgetType != "text" {
		t.Errorf("WidgetType = %q, want %q", req.WidgetType, "text")
	}
}

func TestUpdateWidgetRequest(t *testing.T) {
	req := UpdateWidgetRequest{
		WidgetType: "recent_posts",
		Title:      "Updated Title",
		Content:    "Updated content",
		Settings:   `{"count": 10}`,
		IsActive:   true,
	}

	if req.WidgetType != "recent_posts" {
		t.Errorf("WidgetType = %q, want %q", req.WidgetType, "recent_posts")
	}
	if !req.IsActive {
		t.Error("IsActive should be true")
	}
}

func TestReorderWidgetsRequest(t *testing.T) {
	req := ReorderWidgetsRequest{
		Widgets: []struct {
			ID       int64 `json:"id"`
			Position int64 `json:"position"`
		}{
			{ID: 1, Position: 0},
			{ID: 2, Position: 1},
			{ID: 3, Position: 2},
		},
	}

	if len(req.Widgets) != 3 {
		t.Errorf("got %d widgets, want 3", len(req.Widgets))
	}
	if req.Widgets[0].ID != 1 || req.Widgets[0].Position != 0 {
		t.Error("first widget should have ID 1 and position 0")
	}
}

func TestMoveWidgetRequest(t *testing.T) {
	req := MoveWidgetRequest{
		Area: "footer",
	}

	if req.Area != "footer" {
		t.Errorf("Area = %q, want %q", req.Area, "footer")
	}
}
