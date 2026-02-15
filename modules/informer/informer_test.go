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

	if s.Version != "0" {
		t.Errorf("expected Version '0', got %q", s.Version)
	}
}

func TestMigrations(t *testing.T) {
	m := New()
	migrations := m.Migrations()

	if len(migrations) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(migrations))
	}

	if migrations[0].Version != 1 {
		t.Errorf("expected migration version 1, got %d", migrations[0].Version)
	}

	if migrations[1].Version != 2 {
		t.Errorf("expected migration version 2, got %d", migrations[1].Version)
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

func TestRenderBarEscapesHTML(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:   true,
			Text:      `Check our <a href="/sale">sale page</a>!`,
			BgColor:   "#000000",
			TextColor: "#ffffff",
		},
	}

	output := string(m.renderBar())

	if strings.Contains(output, `<a href="/sale">sale page</a>`) {
		t.Error("output should not render raw HTML")
	}
	if !strings.Contains(output, `Check our &lt;a href=&#34;/sale&#34;&gt;sale page&lt;/a&gt;!`) {
		t.Error("output should escape HTML in notification text")
	}
}

func TestRenderBarCookieScript(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:   true,
			Text:      "Test",
			BgColor:   "#000000",
			TextColor: "#ffffff",
			Version:   "1738900000",
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

	if !strings.Contains(output, `ver="1738900000"`) {
		t.Error("script should embed settings version for cookie comparison")
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

	if s.Version != "1" {
		t.Errorf("expected Version '1' from DB default, got %q", s.Version)
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

func TestSaveIncrementsVersion(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Load initial version (should be "1" from DB default)
	initial, err := loadSettings(db)
	if err != nil {
		t.Fatalf("failed to load initial settings: %v", err)
	}
	if initial.Version != "1" {
		t.Fatalf("expected initial version '1', got %q", initial.Version)
	}

	// Save once — version should increment to 2
	if err := saveSettings(db, &Settings{Enabled: true, Text: "test", BgColor: "#000", TextColor: "#fff"}); err != nil {
		t.Fatalf("failed to save: %v", err)
	}
	after1, err := loadSettings(db)
	if err != nil {
		t.Fatalf("failed to load after first save: %v", err)
	}
	if after1.Version != "2" {
		t.Errorf("expected version '2' after first save, got %q", after1.Version)
	}

	// Save again — version should increment to 3
	if err := saveSettings(db, &Settings{Enabled: true, Text: "updated", BgColor: "#000", TextColor: "#fff"}); err != nil {
		t.Fatalf("failed to save: %v", err)
	}
	after2, err := loadSettings(db)
	if err != nil {
		t.Fatalf("failed to load after second save: %v", err)
	}
	if after2.Version != "3" {
		t.Errorf("expected version '3' after second save, got %q", after2.Version)
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

func TestMigrationV2UpDown(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	m := New()
	migrations := m.Migrations()

	// Apply migration 1 (create table)
	if err := migrations[0].Up(db); err != nil {
		t.Fatalf("migration 1 up failed: %v", err)
	}

	// Apply migration 2 (add version column)
	if err := migrations[1].Up(db); err != nil {
		t.Fatalf("migration 2 up failed: %v", err)
	}

	// Verify version column exists and has default value
	var version int
	err = db.QueryRow("SELECT version FROM informer_settings WHERE id = 1").Scan(&version)
	if err != nil {
		t.Fatalf("failed to query version column: %v", err)
	}
	if version != 1 {
		t.Errorf("expected default version 1, got %d", version)
	}

	// Rollback migration 2
	if err := migrations[1].Down(db); err != nil {
		t.Fatalf("migration 2 down failed: %v", err)
	}

	// Verify version column is gone
	err = db.QueryRow("SELECT version FROM informer_settings WHERE id = 1").Scan(&version)
	if err == nil {
		t.Error("expected error querying removed version column")
	}

	// Verify table still exists and data is intact
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM informer_settings").Scan(&count)
	if err != nil {
		t.Fatalf("table should still exist after migration 2 down: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after migration 2 down, got %d", count)
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
