// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"database/sql"
	"html/template"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/store"
)

// widgetTestDB creates a minimal in-memory SQLite database with the widgets table.
func widgetTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE widgets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			theme TEXT NOT NULL DEFAULT 'default',
			area TEXT NOT NULL DEFAULT 'sidebar',
			widget_type TEXT NOT NULL DEFAULT 'html',
			title TEXT,
			content TEXT,
			settings TEXT,
			position INTEGER NOT NULL DEFAULT 0,
			is_active INTEGER NOT NULL DEFAULT 1,
			language_code TEXT NOT NULL DEFAULT 'en',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("failed to create widgets table: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestToWidgetView(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name         string
		widget       store.Widget
		wantID       int64
		wantType     string
		wantTitle    string
		wantContent  template.HTML
		wantSettings string
		wantIsActive bool
		wantPosition int64
	}{
		{
			name: "active widget with HTML content",
			widget: store.Widget{
				ID:           1,
				WidgetType:   "html",
				Title:        sql.NullString{String: "My Widget", Valid: true},
				Content:      sql.NullString{String: "<p>Hello <strong>World</strong></p>", Valid: true},
				Settings:     sql.NullString{String: `{"color":"red"}`, Valid: true},
				Position:     3,
				IsActive:     1,
				LanguageCode: "en",
				CreatedAt:    now,
				UpdatedAt:    now,
			},
			wantID:       1,
			wantType:     "html",
			wantTitle:    "My Widget",
			wantContent:  template.HTML("<p>Hello <strong>World</strong></p>"),
			wantSettings: `{"color":"red"}`,
			wantIsActive: true,
			wantPosition: 3,
		},
		{
			name: "inactive widget",
			widget: store.Widget{
				ID:           2,
				WidgetType:   "text",
				Title:        sql.NullString{String: "Inactive", Valid: true},
				Content:      sql.NullString{String: "content", Valid: true},
				Settings:     sql.NullString{String: "{}", Valid: true},
				Position:     0,
				IsActive:     0,
				LanguageCode: "en",
				CreatedAt:    now,
				UpdatedAt:    now,
			},
			wantID:       2,
			wantType:     "text",
			wantTitle:    "Inactive",
			wantIsActive: false,
			wantPosition: 0,
		},
		{
			name: "script tags sanitized from content",
			widget: store.Widget{
				ID:         3,
				WidgetType: "html",
				Title:      sql.NullString{String: "XSS Widget", Valid: true},
				Content:    sql.NullString{String: `<script>alert('xss')</script><p>Safe</p>`, Valid: true},
				Settings:   sql.NullString{String: "{}", Valid: true},
				IsActive:   1,
				CreatedAt:  now,
				UpdatedAt:  now,
			},
			wantID:       3,
			wantType:     "html",
			wantTitle:    "XSS Widget",
			wantContent:  template.HTML("<p>Safe</p>"),
			wantIsActive: true,
		},
		{
			name: "null title and content",
			widget: store.Widget{
				ID:         4,
				WidgetType: "text",
				Title:      sql.NullString{Valid: false},
				Content:    sql.NullString{Valid: false},
				Settings:   sql.NullString{Valid: false},
				IsActive:   1,
				CreatedAt:  now,
				UpdatedAt:  now,
			},
			wantID:       4,
			wantType:     "text",
			wantTitle:    "",
			wantContent:  template.HTML(""),
			wantSettings: "",
			wantIsActive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toWidgetView(tt.widget)
			if got.ID != tt.wantID {
				t.Errorf("ID = %d, want %d", got.ID, tt.wantID)
			}
			if got.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tt.wantType)
			}
			if got.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, tt.wantTitle)
			}
			if tt.wantContent != "" && got.Content != tt.wantContent {
				t.Errorf("Content = %q, want %q", got.Content, tt.wantContent)
			}
			if tt.wantSettings != "" && got.Settings != tt.wantSettings {
				t.Errorf("Settings = %q, want %q", got.Settings, tt.wantSettings)
			}
			if got.IsActive != tt.wantIsActive {
				t.Errorf("IsActive = %v, want %v", got.IsActive, tt.wantIsActive)
			}
			if got.Position != tt.wantPosition {
				t.Errorf("Position = %d, want %d", got.Position, tt.wantPosition)
			}
		})
	}
}

func TestGetWidgetsForArea(t *testing.T) {
	db := widgetTestDB(t)
	ctx := context.Background()

	_, err := db.Exec(`
		INSERT INTO widgets (theme, area, widget_type, title, content, settings, position, is_active, language_code)
		VALUES
			('default', 'sidebar', 'html', 'Widget 1', '<p>Content 1</p>', '{}', 0, 1, 'en'),
			('default', 'sidebar', 'html', 'Widget 2', '<p>Content 2</p>', '{}', 1, 1, 'en'),
			('default', 'footer', 'html', 'Footer Widget', '<p>Footer</p>', '{}', 0, 1, 'en'),
			('custom', 'sidebar', 'html', 'Custom Widget', '<p>Custom</p>', '{}', 0, 1, 'en')
	`)
	if err != nil {
		t.Fatalf("failed to insert widgets: %v", err)
	}

	svc := NewWidgetService(db)

	t.Run("returns widgets for matching theme and area", func(t *testing.T) {
		widgets := svc.GetWidgetsForArea(ctx, "default", "sidebar", "en")
		if len(widgets) != 2 {
			t.Errorf("len(widgets) = %d, want 2", len(widgets))
		}
		for _, w := range widgets {
			if w.IsActive != true {
				t.Error("all returned widgets should be active")
			}
		}
	})

	t.Run("returns empty for unknown area", func(t *testing.T) {
		widgets := svc.GetWidgetsForArea(ctx, "default", "unknown-area", "en")
		if len(widgets) != 0 {
			t.Errorf("len(widgets) = %d, want 0", len(widgets))
		}
	})

	t.Run("returns widgets for different theme", func(t *testing.T) {
		widgets := svc.GetWidgetsForArea(ctx, "custom", "sidebar", "en")
		if len(widgets) != 1 {
			t.Errorf("len(widgets) = %d, want 1", len(widgets))
		}
		if len(widgets) > 0 && widgets[0].Title != "Custom Widget" {
			t.Errorf("Title = %q, want Custom Widget", widgets[0].Title)
		}
	})

	t.Run("cached result is returned on second call", func(t *testing.T) {
		// First call populates cache
		first := svc.GetWidgetsForArea(ctx, "default", "footer", "en")
		// Second call should return from cache
		second := svc.GetWidgetsForArea(ctx, "default", "footer", "en")
		if len(first) != len(second) {
			t.Errorf("cached result len %d != first result len %d", len(second), len(first))
		}
	})
}

func TestGetAllWidgetsForTheme(t *testing.T) {
	db := widgetTestDB(t)
	ctx := context.Background()

	_, err := db.Exec(`
		INSERT INTO widgets (theme, area, widget_type, title, content, settings, position, is_active, language_code)
		VALUES
			('default', 'sidebar', 'html', 'Sidebar 1', '<p>S1</p>', '{}', 0, 1, 'en'),
			('default', 'sidebar', 'html', 'Sidebar 2', '<p>S2</p>', '{}', 1, 1, 'en'),
			('default', 'footer', 'html', 'Footer 1', '<p>F1</p>', '{}', 0, 1, 'en'),
			('default', 'header', 'html', 'Header Inactive', '<p>H</p>', '{}', 0, 0, 'en'),
			('other', 'sidebar', 'html', 'Other Sidebar', '<p>O</p>', '{}', 0, 1, 'en')
	`)
	if err != nil {
		t.Fatalf("failed to insert widgets: %v", err)
	}

	svc := NewWidgetService(db)

	t.Run("returns widgets grouped by area", func(t *testing.T) {
		result := svc.GetAllWidgetsForTheme(ctx, "default")
		if len(result["sidebar"]) != 2 {
			t.Errorf("sidebar widgets = %d, want 2", len(result["sidebar"]))
		}
		if len(result["footer"]) != 1 {
			t.Errorf("footer widgets = %d, want 1", len(result["footer"]))
		}
		// Inactive widget should be excluded
		if len(result["header"]) != 0 {
			t.Errorf("header widgets = %d, want 0 (inactive should be excluded)", len(result["header"]))
		}
	})

	t.Run("returns empty map for unknown theme", func(t *testing.T) {
		result := svc.GetAllWidgetsForTheme(ctx, "nonexistent-theme")
		if len(result) != 0 {
			t.Errorf("result len = %d, want 0", len(result))
		}
	})

	t.Run("different theme is isolated", func(t *testing.T) {
		result := svc.GetAllWidgetsForTheme(ctx, "other")
		if len(result["sidebar"]) != 1 {
			t.Errorf("other theme sidebar widgets = %d, want 1", len(result["sidebar"]))
		}
	})
}
