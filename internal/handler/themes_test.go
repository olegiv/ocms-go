package handler

import (
	"testing"

	"ocms-go/internal/theme"
)

func TestNewThemesHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewThemesHandler(db, nil, sm, nil, nil)
	if h == nil {
		t.Fatal("NewThemesHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestThemeListData(t *testing.T) {
	data := ThemeListData{
		Themes: []theme.Info{
			{Name: "default", IsActive: true},
			{Name: "minimal", IsActive: false},
		},
	}

	if len(data.Themes) != 2 {
		t.Errorf("got %d themes, want 2", len(data.Themes))
	}

	activeCount := 0
	for _, th := range data.Themes {
		if th.IsActive {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Errorf("got %d active themes, want 1", activeCount)
	}
}

func TestThemeSettingsData(t *testing.T) {
	data := ThemeSettingsData{
		Theme: theme.Info{
			Name:     "default",
			IsActive: true,
		},
		Settings: map[string]string{
			"primary_color": "#3490dc",
			"font_family":   "Inter",
		},
		Errors: map[string]string{},
	}

	if data.Theme.Name != "default" {
		t.Errorf("Theme.Name = %q, want %q", data.Theme.Name, "default")
	}
	if !data.Theme.IsActive {
		t.Error("Theme.IsActive should be true")
	}
	if len(data.Settings) != 2 {
		t.Errorf("got %d settings, want 2", len(data.Settings))
	}
	if data.Settings["primary_color"] != "#3490dc" {
		t.Errorf("primary_color = %q, want %q", data.Settings["primary_color"], "#3490dc")
	}
	if len(data.Errors) != 0 {
		t.Error("Errors should be empty")
	}
}

func TestThemeSettingsDataWithErrors(t *testing.T) {
	data := ThemeSettingsData{
		Theme: theme.Info{Name: "default"},
		Settings: map[string]string{
			"invalid_color": "not-a-color",
		},
		Errors: map[string]string{
			"invalid_color": "Invalid color format",
		},
	}

	if len(data.Errors) != 1 {
		t.Errorf("got %d errors, want 1", len(data.Errors))
	}
	if data.Errors["invalid_color"] != "Invalid color format" {
		t.Errorf("error = %q, want %q", data.Errors["invalid_color"], "Invalid color format")
	}
}

func TestThemeListDataEmpty(t *testing.T) {
	data := ThemeListData{
		Themes: []theme.Info{},
	}

	if len(data.Themes) != 0 {
		t.Errorf("got %d themes, want 0", len(data.Themes))
	}
}
