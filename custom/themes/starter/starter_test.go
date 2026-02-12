// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package starter_test

import (
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/theme"
)

// emptyFS is an empty embed.FS (no embedded themes).
var emptyFS embed.FS

// starterThemePath returns the absolute path to the starter theme directory.
func starterThemePath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	return wd
}

// customDirFromTheme returns the custom/ directory path (parent of themes/).
func customDirFromTheme(t *testing.T) string {
	t.Helper()
	themePath := starterThemePath(t)
	// starter theme is at custom/themes/starter, so custom/ is two levels up
	return filepath.Dir(filepath.Dir(themePath))
}

// loadStarterTheme creates a theme manager with the starter theme loaded.
func loadStarterTheme(t *testing.T) *theme.Manager {
	t.Helper()
	customDir := customDirFromTheme(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := theme.NewManager(emptyFS, customDir, logger)
	m.SetFuncMap(minimalFuncMap())
	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}
	return m
}

// minimalFuncMap returns template functions needed to parse the starter theme templates.
func minimalFuncMap() template.FuncMap {
	return template.FuncMap{
		"safeHTML":              func(s string) string { return s },
		"safeCSS":              func(s string) string { return s },
		"safeURL":              func(s string) string { return s },
		"T":                    func(lang, key string, args ...any) string { return key },
		"TTheme":               func(lang, key string, args ...any) string { return key },
		"defaultLangCode":      func() string { return "en" },
		"buildMenuTree":        func(items any) any { return nil },
		"buildBreadcrumbs":     func(items any, pageID int64) any { return nil },
		"dateFormat":           func(t any, layout string) string { return "" },
		"dateFormatMedium":     func(t any, lang string) string { return "" },
		"truncateHTML":         func(s string, max int) string { return s },
		"raw":                  func(s string) string { return s },
		"nl2br":                func(s string) string { return s },
		"addCacheBuster":       func(s string) string { return s },
		"analyticsHead":        func() string { return "" },
		"analyticsBody":        func() string { return "" },
		"hcaptchaEnabled":      func() bool { return false },
		"hcaptchaHead":         func() string { return "" },
		"hcaptchaWidget":       func() string { return "" },
		"seq":                  func(start, end int) []int { return nil },
		"add":                  func(a, b int) int { return a + b },
		"sub":                  func(a, b int) int { return a - b },
		"mul":                  func(a, b int) int { return a * b },
		"div":                  func(a, b int) int { return a / b },
		"mod":                  func(a, b int) int { return a % b },
		"gt":                   func(a, b int) bool { return a > b },
		"lt":                   func(a, b int) bool { return a < b },
		"gte":                  func(a, b int) bool { return a >= b },
		"lte":                  func(a, b int) bool { return a <= b },
		"eq":                   func(a, b int) bool { return a == b },
		"hasPrefix":            func(s, prefix string) bool { return false },
		"hasSuffix":            func(s, suffix string) bool { return false },
		"contains":             func(s, substr string) bool { return false },
		"replace":              func(s, old, nw string) string { return s },
		"lower":                func(s string) string { return s },
		"upper":                func(s string) string { return s },
		"title":                func(s string) string { return s },
		"join":                 func(a []string, sep string) string { return "" },
		"split":                func(s, sep string) []string { return nil },
		"default":              func(defVal, val any) any { return val },
		"json":                 func(v any) string { return "" },
		"jsonIndent":           func(v any) string { return "" },
		"toJSON":               func(v any) string { return "" },
		"parseJSON":            func(s string) any { return nil },
		"dict":                 func(pairs ...any) map[string]any { return nil },
		"list":                 func(items ...any) []any { return items },
		"mapWidgetsByArea":     func(widgets any) any { return nil },
		"renderWidget":         func(widget any, settings any, lang string) string { return "" },
		"embedHead":            func() string { return "" },
		"embedScripts":         func() string { return "" },
		"analyticsExtHead":     func() string { return "" },
		"analyticsExtBody":     func() string { return "" },
		"embedBody":            func() string { return "" },
		"now":                  func() any { return nil },
		"timeBefore":           func(t1, t2 any) bool { return false },
		"formatDate":           func(t any) string { return "" },
		"formatDateTime":       func(t any) string { return "" },
		"privacyHead":          func() string { return "" },
		"privacyFooterLink":    func() string { return "" },
		"formatDateLocale":     func(t any, lang string) string { return "" },
		"formatDateTimeLocale": func(t any, lang string) string { return "" },
		"truncate":             func(s string, length int) string { return s },
		"safe":                 func(s string) string { return s },
		"multiply":             func(a, b int) int { return a * b },
		"repeat":               func(s string, count int) string { return s },
		"formatBytes":          func(bytes int64) string { return "" },
		"deref":                func(p any) int64 { return 0 },
		"mediaAlt":             func(alt, filename string) string { return alt },
		"mediaURL":             func(u string) string { return u },
		"imageSrc":             func(u string, variant string) string { return u },
		"imageSrcset":          func(u string) string { return "" },
		"informerBar":          func() string { return "" },
	}
}

// --- Theme Configuration Tests ---

func TestThemeJsonExists(t *testing.T) {
	path := filepath.Join(starterThemePath(t), "theme.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("theme.json does not exist")
	}
}

func TestThemeJsonValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(starterThemePath(t), "theme.json"))
	if err != nil {
		t.Fatalf("failed to read theme.json: %v", err)
	}

	var config theme.Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid theme.json: %v", err)
	}

	if config.Name != "Starter" {
		t.Errorf("Name = %q, want %q", config.Name, "Starter")
	}
	if config.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", config.Version, "1.0.0")
	}
	if config.Author != "oCMS" {
		t.Errorf("Author = %q, want %q", config.Author, "oCMS")
	}
	if config.Description == "" {
		t.Error("Description is empty")
	}
	if config.Screenshot != "screenshot.svg" {
		t.Errorf("Screenshot = %q, want %q", config.Screenshot, "screenshot.svg")
	}
}

func TestThemeTemplateMapping(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(starterThemePath(t), "theme.json"))
	if err != nil {
		t.Fatalf("failed to read theme.json: %v", err)
	}

	var config theme.Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid theme.json: %v", err)
	}

	requiredTemplates := []string{"home", "page", "list", "404", "category", "tag", "search"}
	for _, name := range requiredTemplates {
		tmplPath, ok := config.Templates[name]
		if !ok {
			t.Errorf("template mapping missing for %q", name)
			continue
		}
		if !strings.HasPrefix(tmplPath, "pages/") || !strings.HasSuffix(tmplPath, ".html") {
			t.Errorf("template %q has unexpected path: %q", name, tmplPath)
		}
	}
}

func TestThemeSettings(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(starterThemePath(t), "theme.json"))
	if err != nil {
		t.Fatalf("failed to read theme.json: %v", err)
	}

	var config theme.Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid theme.json: %v", err)
	}

	if len(config.Settings) == 0 {
		t.Fatal("expected at least one theme setting")
	}

	settingsMap := make(map[string]theme.Setting)
	for _, s := range config.Settings {
		settingsMap[s.Key] = s
	}

	// Verify accent_color setting
	accent, ok := settingsMap["accent_color"]
	if !ok {
		t.Fatal("expected accent_color setting")
	}
	if accent.Type != "color" {
		t.Errorf("accent_color type = %q, want %q", accent.Type, "color")
	}
	if accent.Default != "#2d6a4f" {
		t.Errorf("accent_color default = %q, want %q", accent.Default, "#2d6a4f")
	}

	// Verify show_sidebar setting
	sidebar, ok := settingsMap["show_sidebar"]
	if !ok {
		t.Fatal("expected show_sidebar setting")
	}
	if sidebar.Type != "select" {
		t.Errorf("show_sidebar type = %q, want %q", sidebar.Type, "select")
	}
	if len(sidebar.Options) != 2 {
		t.Errorf("show_sidebar options count = %d, want 2", len(sidebar.Options))
	}

	// Verify hero_style setting
	hero, ok := settingsMap["hero_style"]
	if !ok {
		t.Fatal("expected hero_style setting")
	}
	if hero.Type != "select" {
		t.Errorf("hero_style type = %q, want %q", hero.Type, "select")
	}
	if hero.Default != "gradient" {
		t.Errorf("hero_style default = %q, want %q", hero.Default, "gradient")
	}

	// Verify favicon setting
	favicon, ok := settingsMap["favicon"]
	if !ok {
		t.Fatal("expected favicon setting")
	}
	if favicon.Type != "image" {
		t.Errorf("favicon type = %q, want %q", favicon.Type, "image")
	}
}

func TestThemeWidgetAreas(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(starterThemePath(t), "theme.json"))
	if err != nil {
		t.Fatalf("failed to read theme.json: %v", err)
	}

	var config theme.Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid theme.json: %v", err)
	}

	if len(config.WidgetAreas) == 0 {
		t.Fatal("expected at least one widget area")
	}

	areaIDs := make(map[string]bool)
	for _, area := range config.WidgetAreas {
		areaIDs[area.ID] = true
		if area.Name == "" {
			t.Errorf("widget area %q has empty name", area.ID)
		}
	}

	requiredAreas := []string{"sidebar", "footer-1", "footer-2"}
	for _, id := range requiredAreas {
		if !areaIDs[id] {
			t.Errorf("missing required widget area: %q", id)
		}
	}
}

// --- Template Files Tests ---

func TestTemplateFilesExist(t *testing.T) {
	themePath := starterThemePath(t)

	requiredFiles := []string{
		"templates/layouts/base.html",
		"templates/pages/home.html",
		"templates/pages/page.html",
		"templates/pages/list.html",
		"templates/pages/404.html",
		"templates/pages/category.html",
		"templates/pages/tag.html",
		"templates/pages/search.html",
		"templates/pages/form.html",
		"templates/partials/header.html",
		"templates/partials/footer.html",
		"templates/partials/sidebar.html",
		"templates/partials/pagination.html",
		"templates/partials/language-switcher.html",
		"templates/partials/post-card.html",
	}

	for _, file := range requiredFiles {
		path := filepath.Join(themePath, file)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("required template file missing: %s", file)
		}
	}
}

func TestTemplateContentDefineBlocks(t *testing.T) {
	themePath := starterThemePath(t)

	pageTemplates := []string{
		"home.html", "page.html", "list.html", "404.html",
		"category.html", "tag.html", "search.html", "form.html",
	}

	for _, filename := range pageTemplates {
		path := filepath.Join(themePath, "templates", "pages", filename)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("failed to read %s: %v", filename, err)
			continue
		}

		if !strings.Contains(string(content), `{{define "content"}}`) {
			t.Errorf("%s missing {{define \"content\"}} block", filename)
		}
	}
}

func TestPartialDefineBlocks(t *testing.T) {
	themePath := starterThemePath(t)

	partials := map[string]string{
		"header.html":    `{{define "header.html"}}`,
		"footer.html":    `{{define "footer.html"}}`,
		"sidebar.html":   `{{define "sidebar.html"}}`,
		"pagination.html": `{{define "pagination.html"}}`,
	}

	for filename, expectedDefine := range partials {
		path := filepath.Join(themePath, "templates", "partials", filename)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("failed to read %s: %v", filename, err)
			continue
		}

		if !strings.Contains(string(content), expectedDefine) {
			t.Errorf("%s missing %s block", filename, expectedDefine)
		}
	}
}

func TestBaseLayoutStructure(t *testing.T) {
	path := filepath.Join(starterThemePath(t), "templates", "layouts", "base.html")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read base.html: %v", err)
	}

	htmlContent := string(content)

	// Verify required HTML structure
	requiredElements := []string{
		"<!DOCTYPE html>",
		"<html",
		"<head>",
		"</head>",
		"<body",
		"</body>",
		"</html>",
		`{{template "header.html" .}}`,
		`{{template "content" .}}`,
		`{{template "footer.html" .}}`,
		`{{template "sidebar.html" .}}`,
	}

	for _, elem := range requiredElements {
		if !strings.Contains(htmlContent, elem) {
			t.Errorf("base.html missing: %s", elem)
		}
	}

	// Verify SEO meta tags
	seoElements := []string{
		"og:type",
		"og:title",
		"twitter:card",
		".MetaDescription",
		".Canonical",
	}

	for _, elem := range seoElements {
		if !strings.Contains(htmlContent, elem) {
			t.Errorf("base.html missing SEO element: %s", elem)
		}
	}

	// Verify theme CSS and JS references
	if !strings.Contains(htmlContent, "/themes/starter/static/css/theme.css") {
		t.Error("base.html missing theme CSS reference")
	}
	if !strings.Contains(htmlContent, "/themes/starter/static/js/theme.js") {
		t.Error("base.html missing theme JS reference")
	}

	// Verify integration points (privacy, analytics, embeds)
	integrations := []string{
		"privacyHead",
		"analyticsExtHead",
		"analyticsExtBody",
		"embedHead",
		"embedBody",
		"informerBar",
	}

	for _, fn := range integrations {
		if !strings.Contains(htmlContent, fn) {
			t.Errorf("base.html missing integration: %s", fn)
		}
	}
}

// --- Static Assets Tests ---

func TestStaticAssetsExist(t *testing.T) {
	themePath := starterThemePath(t)

	requiredAssets := []string{
		"static/css/theme.css",
		"static/js/theme.js",
		"static/screenshot.svg",
	}

	for _, asset := range requiredAssets {
		path := filepath.Join(themePath, asset)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			t.Errorf("static asset missing: %s", asset)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("static asset is empty: %s", asset)
		}
	}
}

func TestCSSContainsCustomProperties(t *testing.T) {
	path := filepath.Join(starterThemePath(t), "static", "css", "theme.css")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read theme.css: %v", err)
	}

	css := string(content)

	// Verify custom properties exist
	requiredVars := []string{
		"--st-accent",
		"--st-bg",
		"--st-text",
		"--st-font-heading",
		"--st-font-body",
		"--st-container",
	}

	for _, v := range requiredVars {
		if !strings.Contains(css, v) {
			t.Errorf("theme.css missing CSS variable: %s", v)
		}
	}

	// Verify responsive breakpoints
	if !strings.Contains(css, "@media") {
		t.Error("theme.css missing responsive media queries")
	}

	// Verify st- prefix is used consistently
	if !strings.Contains(css, ".st-header") {
		t.Error("theme.css missing .st-header class")
	}
	if !strings.Contains(css, ".st-card") {
		t.Error("theme.css missing .st-card class")
	}
	if !strings.Contains(css, ".st-footer") {
		t.Error("theme.css missing .st-footer class")
	}
}

func TestJavaScriptStructure(t *testing.T) {
	path := filepath.Join(starterThemePath(t), "static", "js", "theme.js")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read theme.js: %v", err)
	}

	js := string(content)

	// Verify essential functionality
	requiredFeatures := []string{
		"menu-toggle",    // Mobile menu
		"site-header",    // Header scroll effect
		"st-search-form", // Search toggle
		"smooth",         // Smooth scroll
	}

	for _, feature := range requiredFeatures {
		if !strings.Contains(js, feature) {
			t.Errorf("theme.js missing feature: %s", feature)
		}
	}

	// Verify IIFE pattern (encapsulation)
	if !strings.Contains(js, "(function()") {
		t.Error("theme.js should use IIFE pattern for encapsulation")
	}
}

func TestScreenshotSVG(t *testing.T) {
	path := filepath.Join(starterThemePath(t), "static", "screenshot.svg")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read screenshot.svg: %v", err)
	}

	svg := string(content)
	if !strings.Contains(svg, "<svg") {
		t.Error("screenshot.svg is not a valid SVG file")
	}
	if !strings.Contains(svg, "xmlns") {
		t.Error("screenshot.svg missing xmlns attribute")
	}
}

// --- Locale / Translation Tests ---

func TestLocaleFilesExist(t *testing.T) {
	themePath := starterThemePath(t)

	localeFiles := []string{
		"locales/en/messages.json",
		"locales/ru/messages.json",
	}

	for _, file := range localeFiles {
		path := filepath.Join(themePath, file)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("locale file missing: %s", file)
		}
	}
}

type messageFile struct {
	Language string    `json:"language"`
	Messages []message `json:"messages"`
}

type message struct {
	ID          string `json:"id"`
	Message     string `json:"message"`
	Translation string `json:"translation"`
}

func TestLocaleFilesValid(t *testing.T) {
	themePath := starterThemePath(t)

	languages := map[string]string{
		"en": "locales/en/messages.json",
		"ru": "locales/ru/messages.json",
	}

	for lang, file := range languages {
		t.Run(lang, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(themePath, file))
			if err != nil {
				t.Fatalf("failed to read %s: %v", file, err)
			}

			var mf messageFile
			if err := json.Unmarshal(data, &mf); err != nil {
				t.Fatalf("invalid JSON in %s: %v", file, err)
			}

			if mf.Language != lang {
				t.Errorf("language = %q, want %q", mf.Language, lang)
			}
			if len(mf.Messages) == 0 {
				t.Error("messages array is empty")
			}

			for _, msg := range mf.Messages {
				if msg.ID == "" {
					t.Error("message has empty id")
				}
				if msg.Translation == "" {
					t.Errorf("message %q has empty translation", msg.ID)
				}
			}
		})
	}
}

func TestLocaleMessagesConsistency(t *testing.T) {
	themePath := starterThemePath(t)

	// Load both locale files
	enData, err := os.ReadFile(filepath.Join(themePath, "locales/en/messages.json"))
	if err != nil {
		t.Fatalf("failed to read en messages: %v", err)
	}
	ruData, err := os.ReadFile(filepath.Join(themePath, "locales/ru/messages.json"))
	if err != nil {
		t.Fatalf("failed to read ru messages: %v", err)
	}

	var enFile, ruFile messageFile
	if err := json.Unmarshal(enData, &enFile); err != nil {
		t.Fatalf("invalid en JSON: %v", err)
	}
	if err := json.Unmarshal(ruData, &ruFile); err != nil {
		t.Fatalf("invalid ru JSON: %v", err)
	}

	// Build ID sets
	enIDs := make(map[string]bool)
	for _, msg := range enFile.Messages {
		enIDs[msg.ID] = true
	}
	ruIDs := make(map[string]bool)
	for _, msg := range ruFile.Messages {
		ruIDs[msg.ID] = true
	}

	// Every English key should have a Russian translation
	for id := range enIDs {
		if !ruIDs[id] {
			t.Errorf("Russian translation missing for key: %q", id)
		}
	}

	// Every Russian key should have an English translation
	for id := range ruIDs {
		if !enIDs[id] {
			t.Errorf("English translation missing for key: %q", id)
		}
	}
}

func TestLocaleRequiredKeys(t *testing.T) {
	themePath := starterThemePath(t)

	data, err := os.ReadFile(filepath.Join(themePath, "locales/en/messages.json"))
	if err != nil {
		t.Fatalf("failed to read en messages: %v", err)
	}

	var mf messageFile
	if err := json.Unmarshal(data, &mf); err != nil {
		t.Fatalf("invalid en JSON: %v", err)
	}

	ids := make(map[string]bool)
	for _, msg := range mf.Messages {
		ids[msg.ID] = true
	}

	// Theme should override these visible frontend keys to demonstrate the feature
	requiredKeys := []string{
		"frontend.read_more",
		"frontend.all_posts",
		"frontend.recent_posts",
		"frontend.view_all_posts",
		"frontend.page_not_found",
		"frontend.go_home",
		"frontend.related_posts",
		"search.title",
		"search.placeholder",
		"search.no_results",
		"sidebar.categories",
		"sidebar.tags",
		"sidebar.recent_posts",
	}

	for _, key := range requiredKeys {
		if !ids[key] {
			t.Errorf("required translation key missing: %q", key)
		}
	}
}

func TestLocaleKeysUseFrontendPrefix(t *testing.T) {
	themePath := starterThemePath(t)

	data, err := os.ReadFile(filepath.Join(themePath, "locales/en/messages.json"))
	if err != nil {
		t.Fatalf("failed to read en messages: %v", err)
	}

	var mf messageFile
	if err := json.Unmarshal(data, &mf); err != nil {
		t.Fatalf("invalid en JSON: %v", err)
	}

	// All theme translation keys should be frontend-facing
	validPrefixes := []string{"frontend.", "search.", "sidebar.", "pagination.", "forms.public."}
	for _, msg := range mf.Messages {
		hasValid := false
		for _, prefix := range validPrefixes {
			if strings.HasPrefix(msg.ID, prefix) {
				hasValid = true
				break
			}
		}
		if !hasValid {
			t.Errorf("translation key %q does not have a valid frontend prefix", msg.ID)
		}
	}
}

// --- Theme Loading via Manager Tests ---

func TestStarterThemeLoads(t *testing.T) {
	m := loadStarterTheme(t)

	if !m.HasTheme("starter") {
		t.Fatal("starter theme was not loaded")
	}
}

func TestStarterThemeNotEmbedded(t *testing.T) {
	m := loadStarterTheme(t)

	if m.IsEmbedded("starter") {
		t.Error("starter theme should not be marked as embedded")
	}
}

func TestStarterThemeConfig(t *testing.T) {
	m := loadStarterTheme(t)

	th, err := m.GetTheme("starter")
	if err != nil {
		t.Fatalf("GetTheme: %v", err)
	}

	if th.Config.Name != "Starter" {
		t.Errorf("Config.Name = %q, want %q", th.Config.Name, "Starter")
	}
	if th.Config.Version != "1.0.0" {
		t.Errorf("Config.Version = %q, want %q", th.Config.Version, "1.0.0")
	}
	if th.Config.Author != "oCMS" {
		t.Errorf("Config.Author = %q, want %q", th.Config.Author, "oCMS")
	}
}

func TestStarterThemeCanBeActivated(t *testing.T) {
	m := loadStarterTheme(t)

	if err := m.SetActiveTheme("starter"); err != nil {
		t.Fatalf("SetActiveTheme: %v", err)
	}

	active := m.GetActiveTheme()
	if active == nil {
		t.Fatal("active theme is nil after SetActiveTheme")
	}
	if active.Name != "starter" {
		t.Errorf("active theme name = %q, want %q", active.Name, "starter")
	}
}

func TestStarterThemeHasTemplates(t *testing.T) {
	m := loadStarterTheme(t)

	th, err := m.GetTheme("starter")
	if err != nil {
		t.Fatalf("GetTheme: %v", err)
	}

	if th.Templates == nil {
		t.Fatal("Templates is nil")
	}

	// Verify content templates exist (parsed with content_ prefix)
	expectedPages := []string{
		"content_home", "content_page", "content_list", "content_404",
		"content_category", "content_tag", "content_search", "content_form",
	}

	for _, name := range expectedPages {
		if th.Templates.Lookup(name) == nil {
			t.Errorf("content template %q not found", name)
		}
	}

	// Verify layout
	if th.Templates.Lookup("layouts/base.html") == nil {
		t.Error("layout template layouts/base.html not found")
	}

	// Verify partials
	expectedPartials := []string{
		"header.html", "footer.html", "sidebar.html",
		"pagination.html", "language-switcher.html", "post-card.html",
	}

	for _, name := range expectedPartials {
		if th.Templates.Lookup(name) == nil {
			t.Errorf("partial template %q not found", name)
		}
	}
}

func TestStarterThemeTranslationsLoaded(t *testing.T) {
	m := loadStarterTheme(t)

	th, err := m.GetTheme("starter")
	if err != nil {
		t.Fatalf("GetTheme: %v", err)
	}

	if th.Translations == nil {
		t.Fatal("Translations is nil")
	}

	// Check English translations loaded
	if _, ok := th.Translations["en"]; !ok {
		t.Error("English translations not loaded")
	}

	// Check Russian translations loaded
	if _, ok := th.Translations["ru"]; !ok {
		t.Error("Russian translations not loaded")
	}
}

func TestStarterThemeTranslate(t *testing.T) {
	m := loadStarterTheme(t)

	th, err := m.GetTheme("starter")
	if err != nil {
		t.Fatalf("GetTheme: %v", err)
	}

	// Test English override
	trans, ok := th.Translate("en", "frontend.read_more")
	if !ok {
		t.Error("expected en translation for frontend.read_more")
	}
	if trans == "" {
		t.Error("en translation for frontend.read_more is empty")
	}

	// Test Russian override
	trans, ok = th.Translate("ru", "frontend.read_more")
	if !ok {
		t.Error("expected ru translation for frontend.read_more")
	}
	if trans == "" {
		t.Error("ru translation for frontend.read_more is empty")
	}

	// Test missing key returns false
	_, ok = th.Translate("en", "nonexistent.key")
	if ok {
		t.Error("expected no translation for nonexistent key")
	}

	// Test missing language returns false
	_, ok = th.Translate("fr", "frontend.read_more")
	if ok {
		t.Error("expected no translation for unsupported language")
	}
}

func TestStarterThemeSettings(t *testing.T) {
	m := loadStarterTheme(t)

	th, err := m.GetTheme("starter")
	if err != nil {
		t.Fatalf("GetTheme: %v", err)
	}

	if !th.HasSetting("accent_color") {
		t.Error("expected accent_color setting")
	}
	if !th.HasSetting("show_sidebar") {
		t.Error("expected show_sidebar setting")
	}
	if !th.HasSetting("hero_style") {
		t.Error("expected hero_style setting")
	}
	if !th.HasSetting("favicon") {
		t.Error("expected favicon setting")
	}
	if th.HasSetting("nonexistent") {
		t.Error("expected nonexistent setting to return false")
	}

	if def := th.GetSettingDefault("accent_color"); def != "#2d6a4f" {
		t.Errorf("accent_color default = %q, want %q", def, "#2d6a4f")
	}
	if def := th.GetSettingDefault("show_sidebar"); def != "yes" {
		t.Errorf("show_sidebar default = %q, want %q", def, "yes")
	}
}

func TestStarterThemeReload(t *testing.T) {
	m := loadStarterTheme(t)

	// Reload should work for non-embedded theme
	if err := m.ReloadTheme("starter"); err != nil {
		t.Errorf("ReloadTheme: %v", err)
	}

	// Theme should still be loaded after reload
	if !m.HasTheme("starter") {
		t.Error("starter theme missing after reload")
	}
}

func TestStarterThemeStaticPath(t *testing.T) {
	m := loadStarterTheme(t)

	th, err := m.GetTheme("starter")
	if err != nil {
		t.Fatalf("GetTheme: %v", err)
	}

	if th.StaticPath == "" {
		t.Error("StaticPath is empty for custom theme")
	}
	if th.Path == "" {
		t.Error("Path is empty for custom theme")
	}
	if th.EmbeddedFS != nil {
		t.Error("EmbeddedFS should be nil for custom theme")
	}
}

// --- README Tests ---

func TestREADMEExists(t *testing.T) {
	path := filepath.Join(starterThemePath(t), "README.md")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		t.Fatal("README.md does not exist")
	}
	if info.Size() == 0 {
		t.Error("README.md is empty")
	}
}

func TestREADMEContent(t *testing.T) {
	path := filepath.Join(starterThemePath(t), "README.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read README.md: %v", err)
	}

	readme := string(content)

	requiredSections := []string{
		"# Starter Theme",
		"## Overview",
		"## Directory Structure",
		"## Activation",
		"## Theme Settings",
		"## Widget Areas",
		"## Customization",
	}

	for _, section := range requiredSections {
		if !strings.Contains(readme, section) {
			t.Errorf("README.md missing section: %s", section)
		}
	}

	if !strings.Contains(readme, "OCMS_ACTIVE_THEME=starter") {
		t.Error("README.md missing activation instructions")
	}
}
