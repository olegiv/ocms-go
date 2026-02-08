// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package informer

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestNew(t *testing.T) {
	m := New()

	if m.Name() != "informer" {
		t.Errorf("expected name 'informer', got %q", m.Name())
	}

	if m.Version() != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", m.Version())
	}

	if m.AdminURL() != "/admin/informer" {
		t.Errorf("expected AdminURL '/admin/informer', got %q", m.AdminURL())
	}

	if m.SidebarLabel() != "Informer" {
		t.Errorf("expected SidebarLabel 'Informer', got %q", m.SidebarLabel())
	}
}

func TestDefaultSettings(t *testing.T) {
	s := defaultSettings()

	if s.Enabled {
		t.Error("expected Enabled to be false by default")
	}

	if s.Text != "" {
		t.Errorf("expected empty Text by default, got %q", s.Text)
	}

	if s.BgColor != "#1e40af" {
		t.Errorf("expected BgColor '#1e40af', got %q", s.BgColor)
	}

	if s.TextColor != "#ffffff" {
		t.Errorf("expected TextColor '#ffffff', got %q", s.TextColor)
	}
}

func TestMigrations(t *testing.T) {
	m := New()
	migrations := m.Migrations()

	if len(migrations) != 1 {
		t.Fatalf("expected 1 migration, got %d", len(migrations))
	}

	if migrations[0].Version != 1 {
		t.Errorf("expected migration version 1, got %d", migrations[0].Version)
	}
}

func TestRenderBarDisabled(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled: false,
			Text:    "Test message",
		},
	}

	output := m.renderBar()
	if output != "" {
		t.Error("expected empty output when module is disabled")
	}
}

func TestRenderBarEmptyText(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled: true,
			Text:    "",
		},
	}

	output := m.renderBar()
	if output != "" {
		t.Error("expected empty output when text is empty")
	}
}

func TestRenderBarNilSettings(t *testing.T) {
	m := &Module{
		settings: nil,
	}

	output := m.renderBar()
	if output != "" {
		t.Error("expected empty output when settings is nil")
	}
}

func TestRenderBarEnabled(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:   true,
			Text:      "Maintenance in progress",
			BgColor:   "#ff0000",
			TextColor: "#ffffff",
		},
	}

	output := string(m.renderBar())

	if !strings.Contains(output, "informer-bar") {
		t.Error("output should contain informer-bar id")
	}

	if !strings.Contains(output, "Maintenance in progress") {
		t.Error("output should contain the notification text")
	}

	if !strings.Contains(output, "background:#ff0000") {
		t.Error("output should contain the background color")
	}

	if !strings.Contains(output, "color:#ffffff") {
		t.Error("output should contain the text color")
	}

	if !strings.Contains(output, "informer-bar-spinner") {
		t.Error("output should contain the spinner element")
	}

	if !strings.Contains(output, "informer-spin") {
		t.Error("output should contain the spin animation")
	}

	if !strings.Contains(output, "dismissInformer") {
		t.Error("output should contain the dismiss function")
	}

	if !strings.Contains(output, cookieName) {
		t.Error("output should reference the cookie name")
	}
}

func TestRenderBarHTMLEscaping(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:   true,
			Text:      `<script>alert("xss")</script>`,
			BgColor:   "#000000",
			TextColor: "#ffffff",
		},
	}

	output := string(m.renderBar())

	if strings.Contains(output, `<script>alert("xss")</script>`) {
		t.Error("output should escape HTML in text to prevent XSS")
	}

	if !strings.Contains(output, "&lt;script&gt;") {
		t.Error("output should contain escaped HTML entities")
	}
}

func TestRenderBarCookieScript(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:   true,
			Text:      "Test",
			BgColor:   "#000000",
			TextColor: "#ffffff",
		},
	}

	output := string(m.renderBar())

	if !strings.Contains(output, "setCookie") {
		t.Error("output should contain setCookie function")
	}

	if !strings.Contains(output, "getCookie") {
		t.Error("output should contain getCookie function")
	}

	if !strings.Contains(output, "display:none") {
		t.Error("bar should be hidden by default (shown via JS if cookie not set)")
	}

	if !strings.Contains(output, "display='flex'") {
		t.Error("script should show bar when cookie is not set")
	}
}

func TestRenderBarCloseButton(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:   true,
			Text:      "Test",
			BgColor:   "#000000",
			TextColor: "#ffffff",
		},
	}

	output := string(m.renderBar())

	if !strings.Contains(output, `aria-label="Close"`) {
		t.Error("close button should have aria-label for accessibility")
	}

	if !strings.Contains(output, "informer-bar-close") {
		t.Error("output should contain close button class")
	}
}

func TestTemplateFuncs(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled: false,
		},
	}

	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Fatal("TemplateFuncs should not return nil")
	}

	fn, ok := funcs["informerBar"]
	if !ok {
		t.Fatal("TemplateFuncs should contain 'informerBar'")
	}

	barFn, ok := fn.(func() string)
	if ok {
		result := barFn()
		if result != "" {
			t.Error("informerBar should return empty string when disabled")
		}
	}
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	m := New()
	for _, migration := range m.Migrations() {
		if err := migration.Up(db); err != nil {
			t.Fatalf("failed to run migration: %v", err)
		}
	}
	return db
}

func TestLoadSettingsDefault(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	s, err := loadSettings(db)
	if err != nil {
		t.Fatalf("failed to load settings: %v", err)
	}

	if s.Enabled {
		t.Error("expected Enabled to be false by default")
	}

	if s.Text != "" {
		t.Errorf("expected empty Text, got %q", s.Text)
	}

	if s.BgColor != "#1e40af" {
		t.Errorf("expected BgColor '#1e40af', got %q", s.BgColor)
	}

	if s.TextColor != "#ffffff" {
		t.Errorf("expected TextColor '#ffffff', got %q", s.TextColor)
	}
}

func TestSaveAndLoadSettings(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	newSettings := &Settings{
		Enabled:   true,
		Text:      "System update in progress",
		BgColor:   "#dc2626",
		TextColor: "#fef2f2",
	}

	if err := saveSettings(db, newSettings); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	loaded, err := loadSettings(db)
	if err != nil {
		t.Fatalf("failed to load settings: %v", err)
	}

	if !loaded.Enabled {
		t.Error("expected Enabled to be true")
	}

	if loaded.Text != "System update in progress" {
		t.Errorf("expected Text 'System update in progress', got %q", loaded.Text)
	}

	if loaded.BgColor != "#dc2626" {
		t.Errorf("expected BgColor '#dc2626', got %q", loaded.BgColor)
	}

	if loaded.TextColor != "#fef2f2" {
		t.Errorf("expected TextColor '#fef2f2', got %q", loaded.TextColor)
	}
}

func TestSaveSettingsDisabled(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// First enable
	if err := saveSettings(db, &Settings{Enabled: true, Text: "test", BgColor: "#000", TextColor: "#fff"}); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	// Then disable
	if err := saveSettings(db, &Settings{Enabled: false, Text: "test", BgColor: "#000", TextColor: "#fff"}); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	loaded, err := loadSettings(db)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	if loaded.Enabled {
		t.Error("expected Enabled to be false after disabling")
	}
}

func TestMigrationUpDown(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	m := New()
	migration := m.Migrations()[0]

	// Up
	if err := migration.Up(db); err != nil {
		t.Fatalf("migration up failed: %v", err)
	}

	// Verify table exists
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM informer_settings").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query table: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}

	// Down
	if err := migration.Down(db); err != nil {
		t.Fatalf("migration down failed: %v", err)
	}

	// Verify table dropped
	err = db.QueryRow("SELECT COUNT(*) FROM informer_settings").Scan(&count)
	if err == nil {
		t.Error("expected error querying dropped table")
	}
}

func TestLoadSettingsNoTable(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, err = loadSettings(db)
	if err == nil {
		t.Error("expected error when table doesn't exist")
	}
}
