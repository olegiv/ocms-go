// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"
)

// ---------------------------------------------------------------------------
// FormatDateTime and FormatDateTimeLocale
// ---------------------------------------------------------------------------

func TestFormatDateTime_Standalone(t *testing.T) {
	testTime := time.Date(2025, time.March, 15, 14, 30, 0, 0, time.UTC)
	got := FormatDateTime(testTime)
	if got != "Mar 15, 2025 2:30 PM" {
		t.Errorf("FormatDateTime() = %q, want %q", got, "Mar 15, 2025 2:30 PM")
	}
}

func TestRenderer_FormatDateTimeLocale(t *testing.T) {
	r := &Renderer{}
	testTime := time.Date(2025, time.March, 15, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name  string
		input any
		lang  string
		want  string
	}{
		{"en datetime", testTime, "en", "Mar 15, 2025 2:30 PM"},
		{"ru datetime", testTime, "ru", "15 марта 2025, 14:30"},
		{"nil pointer", (*time.Time)(nil), "en", ""},
		{"pointer", &testTime, "en", "Mar 15, 2025 2:30 PM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.FormatDateTimeLocale(tt.input, tt.lang)
			if got != tt.want {
				t.Errorf("FormatDateTimeLocale(%v, %q) = %q, want %q", tt.input, tt.lang, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GetMenuService and InvalidateMenuCache with nil menu service
// ---------------------------------------------------------------------------

func TestGetMenuService_Nil(t *testing.T) {
	r := &Renderer{}
	if got := r.GetMenuService(); got != nil {
		t.Errorf("GetMenuService() = %v, want nil", got)
	}
}

func TestInvalidateMenuCache_NilMenuService(t *testing.T) {
	r := &Renderer{}
	// Should not panic when menuService is nil
	r.InvalidateMenuCache("main")
	r.InvalidateMenuCache("")
}

// ---------------------------------------------------------------------------
// SetSidebarModuleProvider and ListSidebarModules with provider
// ---------------------------------------------------------------------------

type mockSidebarProvider struct {
	modules []SidebarModule
}

func (m *mockSidebarProvider) ListSidebarModules() []SidebarModule {
	return m.modules
}

func TestSetSidebarModuleProvider(t *testing.T) {
	r := &Renderer{}

	provider := &mockSidebarProvider{
		modules: []SidebarModule{
			{Name: "bookmarks", Label: "Bookmarks", AdminURL: "/admin/bookmarks"},
			{Name: "analytics", Label: "Analytics", AdminURL: "/admin/analytics"},
		},
	}

	r.SetSidebarModuleProvider(provider)

	modules := r.ListSidebarModules()
	if len(modules) != 2 {
		t.Fatalf("ListSidebarModules() returned %d modules, want 2", len(modules))
	}
	if modules[0].Name != "bookmarks" {
		t.Errorf("modules[0].Name = %q, want %q", modules[0].Name, "bookmarks")
	}
}

// ---------------------------------------------------------------------------
// SetFlash / session helpers – with nil sessionManager (no-op paths)
// ---------------------------------------------------------------------------

func TestSetFlash_NilSession(t *testing.T) {
	r := &Renderer{sessionManager: nil}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Should not panic
	r.SetFlash(req, "test message", "info")
}

func TestSetAdminLang_NilSession(t *testing.T) {
	r := &Renderer{sessionManager: nil}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Should not panic
	r.SetAdminLang(req, "en")
}

func TestGetAdminLang_NilSession(t *testing.T) {
	r := &Renderer{sessionManager: nil}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	lang := r.GetAdminLang(req)
	// Should return default language (not panic)
	if lang == "" {
		t.Error("GetAdminLang() returned empty string with nil session")
	}
}

func TestSetSessionData_NilSession(t *testing.T) {
	r := &Renderer{sessionManager: nil}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Should not panic
	r.SetSessionData(req, "key", map[string]string{"a": "b"})
}

func TestPopSessionData_NilSession(t *testing.T) {
	r := &Renderer{sessionManager: nil}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	got := r.PopSessionData(req, "key")
	if got != nil {
		t.Errorf("PopSessionData() with nil session = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// AddTemplateFuncs and ReloadTemplates
// ---------------------------------------------------------------------------

func TestAddTemplateFuncs_InitializesMap(t *testing.T) {
	r := &Renderer{}
	if r.extraFuncs != nil {
		t.Fatal("expected nil extraFuncs before AddTemplateFuncs")
	}

	r.AddTemplateFuncs(template.FuncMap{
		"myFunc": func() string { return "hello" },
	})

	if r.extraFuncs == nil {
		t.Fatal("extraFuncs should be initialized after AddTemplateFuncs")
	}
	if _, ok := r.extraFuncs["myFunc"]; !ok {
		t.Error("myFunc not found in extraFuncs")
	}
}

func TestAddTemplateFuncs_MergesWithExisting(t *testing.T) {
	r := &Renderer{}
	r.AddTemplateFuncs(template.FuncMap{
		"func1": func() string { return "1" },
	})
	r.AddTemplateFuncs(template.FuncMap{
		"func2": func() string { return "2" },
	})

	if _, ok := r.extraFuncs["func1"]; !ok {
		t.Error("func1 not found after second AddTemplateFuncs")
	}
	if _, ok := r.extraFuncs["func2"]; !ok {
		t.Error("func2 not found")
	}
}

func TestReloadTemplates_NilFS(t *testing.T) {
	r := &Renderer{templatesFS: nil}
	err := r.ReloadTemplates()
	if err == nil {
		t.Error("ReloadTemplates() with nil FS should return error")
	}
}

// ---------------------------------------------------------------------------
// Template renderer creation and parsing with minimal in-memory FS
// ---------------------------------------------------------------------------

func newMinimalFS() fstest.MapFS {
	baseLayout := `{{define "base"}}<!DOCTYPE html><html><body>{{template "content" .}}</body></html>{{end}}`
	adminLayout := `{{define "admin"}}{{template "base" .}}{{end}}`
	return fstest.MapFS{
		"layouts/base.html":  {Data: []byte(baseLayout)},
		"layouts/admin.html": {Data: []byte(adminLayout)},
	}
}

func TestNew_EmptyFS(t *testing.T) {
	fs := newMinimalFS()
	r, err := New(Config{
		TemplatesFS: fs,
	})
	if err != nil {
		t.Fatalf("New() with minimal FS failed: %v", err)
	}
	if r == nil {
		t.Fatal("New() returned nil renderer")
	}
}

func TestGetTemplateFiles_MissingDir(t *testing.T) {
	r := &Renderer{}
	fs := newMinimalFS()
	files := r.getTemplateFiles(fs, "nonexistent_dir")
	if len(files) != 0 {
		t.Errorf("getTemplateFiles for nonexistent dir = %v, want empty", files)
	}
}

func TestGetTemplateFiles_ExistingDir(t *testing.T) {
	r := &Renderer{}
	fs := fstest.MapFS{
		"admin/pages.html": {Data: []byte(`{{define "content"}}pages{{end}}`)},
		"admin/users.html": {Data: []byte(`{{define "content"}}users{{end}}`)},
		"admin/not.txt":    {Data: []byte(`not html`)},
	}
	files := r.getTemplateFiles(fs, "admin")
	if len(files) != 2 {
		t.Errorf("getTemplateFiles returned %d files, want 2 (only .html)", len(files))
	}
}

func TestNew_WithAdminTemplate(t *testing.T) {
	contentTmpl := `{{define "content"}}Hello {{.Title}}{{end}}`
	fs := fstest.MapFS{
		"layouts/base.html":  {Data: []byte(`{{define "base"}}{{template "content" .}}{{end}}`)},
		"layouts/admin.html": {Data: []byte(`{{define "content"}}{{end}}`)},
		"admin/test.html":    {Data: []byte(contentTmpl)},
	}

	r, err := New(Config{
		TemplatesFS: fs,
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if r == nil {
		t.Fatal("renderer is nil")
	}
}

func TestReloadTemplates_WithFS(t *testing.T) {
	fs := newMinimalFS()
	r, err := New(Config{
		TemplatesFS: fs,
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// ReloadTemplates should succeed if templatesFS is set
	err = r.ReloadTemplates()
	if err != nil {
		t.Fatalf("ReloadTemplates() failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Render with a simple template
// ---------------------------------------------------------------------------

func TestRender_TemplateNotFound(t *testing.T) {
	r := &Renderer{templates: make(map[string]*template.Template)}
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	err := r.Render(w, req, "nonexistent/template", TemplateData{})
	if err == nil {
		t.Error("Render() with unknown template should return error")
	}
}

func TestRenderPage_ErrorHandling(t *testing.T) {
	// Renderer with no templates
	r := &Renderer{templates: make(map[string]*template.Template)}
	req := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	w := httptest.NewRecorder()

	// Should not panic; returns 500 on template not found
	r.RenderPage(w, req, "admin/nonexistent", TemplateData{})
	if w.Code != http.StatusInternalServerError {
		t.Errorf("RenderPage() status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestRenderWithPublicTemplate(t *testing.T) {
	// Create a minimal FS with a public template using "body" define
	fs := fstest.MapFS{
		"layouts/base.html":  {Data: []byte(`{{define "base"}}BASE{{end}}`)},
		"layouts/admin.html": {Data: []byte(`{{define "admin"}}ADMIN{{end}}`)},
		"public/home.html":   {Data: []byte(`{{define "body"}}HOME{{.Title}}{{end}}`)},
	}

	r, err := New(Config{TemplatesFS: fs})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	err = r.Render(w, req, "public/home", TemplateData{Title: "Test"})
	if err != nil {
		t.Fatalf("Render() public template failed: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Errorf("Render() status = %d, want 200", w.Code)
	}
}

func TestRenderError_FallbackTo500(t *testing.T) {
	// Renderer with a 500 error template but not a 404
	fs := fstest.MapFS{
		"layouts/base.html":  {Data: []byte(`{{define "base"}}{{template "content" .}}{{end}}`)},
		"layouts/admin.html": {Data: []byte(`{{define "admin_layout"}}ADMIN{{end}}`)},
		"errors/500.html":    {Data: []byte(`{{define "content"}}ERROR: {{.Title}}{{end}}`)},
	}

	r, err := New(Config{TemplatesFS: fs})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	w := httptest.NewRecorder()

	// RenderNotFound should fall back to 500 template when 404 not found
	r.RenderNotFound(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("RenderNotFound() status = %d, want 404", w.Code)
	}
}

func TestRenderForbidden_WithTemplate(t *testing.T) {
	fs := fstest.MapFS{
		"layouts/base.html":  {Data: []byte(`{{define "base"}}{{template "content" .}}{{end}}`)},
		"layouts/admin.html": {Data: []byte(`{{define "admin_layout"}}ADMIN{{end}}`)},
		"errors/500.html":    {Data: []byte(`{{define "content"}}ERROR{{end}}`)},
	}

	r, err := New(Config{TemplatesFS: fs})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	w := httptest.NewRecorder()

	r.RenderForbidden(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("RenderForbidden() status = %d, want 403", w.Code)
	}
}

func TestRenderInternalError_WithTemplate(t *testing.T) {
	fs := fstest.MapFS{
		"layouts/base.html":  {Data: []byte(`{{define "base"}}{{template "content" .}}{{end}}`)},
		"layouts/admin.html": {Data: []byte(`{{define "admin_layout"}}ADMIN{{end}}`)},
		"errors/500.html":    {Data: []byte(`{{define "content"}}ERROR{{end}}`)},
	}

	r, err := New(Config{TemplatesFS: fs})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	w := httptest.NewRecorder()

	r.RenderInternalError(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("RenderInternalError() status = %d, want 500", w.Code)
	}
}

// ---------------------------------------------------------------------------
// TemplateFuncs additional coverage
// ---------------------------------------------------------------------------

func TestTemplateFuncs_TimeBefore(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	timeBefore := funcs["timeBefore"].(func(time.Time, time.Time) bool)

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if !timeBefore(t1, t2) {
		t.Error("timeBefore(earlier, later) should be true")
	}
	if timeBefore(t2, t1) {
		t.Error("timeBefore(later, earlier) should be false")
	}
	if timeBefore(t1, t1) {
		t.Error("timeBefore(same, same) should be false")
	}
}

func TestTemplateFuncs_SafeHTML(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	safeHTML := funcs["safeHTML"].(func(string) template.HTML)

	input := "<strong>bold</strong>"
	got := safeHTML(input)
	if string(got) != input {
		t.Errorf("safeHTML(%q) = %q, want %q", input, got, input)
	}
}

func TestTemplateFuncs_SafeURL(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	safeURL := funcs["safeURL"].(func(string) template.URL)

	input := "https://example.com/path?q=1"
	got := safeURL(input)
	if string(got) != input {
		t.Errorf("safeURL(%q) = %q, want %q", input, got, input)
	}
}

func TestTemplateFuncs_ToJSON(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	toJSON := funcs["toJSON"].(func(any) template.JS)

	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"string slice", []string{"a", "b"}, `["a","b"]`},
		{"int", 42, "42"},
		{"nil", nil, "null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(toJSON(tt.input))
			if got != tt.want {
				t.Errorf("toJSON(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTemplateFuncs_FormatNumber(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	formatNumber := funcs["formatNumber"].(func(int64) string)

	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1000000, "1,000,000"},
		{12345678, "12,345,678"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatNumber(tt.n)
			if got != tt.want {
				t.Errorf("formatNumber(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestTemplateFuncs_FormatDateLocale(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	formatDateLocale := funcs["formatDateLocale"].(func(any, string) string)

	testTime := time.Date(2025, time.June, 15, 0, 0, 0, 0, time.UTC)
	nilTime := (*time.Time)(nil)

	tests := []struct {
		name  string
		input any
		lang  string
		want  string
	}{
		{"time.Time en", testTime, "en", "Jun 15, 2025"},
		{"time.Time ru", testTime, "ru", "15 июня 2025"},
		{"*time.Time", &testTime, "en", "Jun 15, 2025"},
		{"nil *time.Time", nilTime, "en", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDateLocale(tt.input, tt.lang)
			if got != tt.want {
				t.Errorf("formatDateLocale(%v, %q) = %q, want %q", tt.input, tt.lang, got, tt.want)
			}
		})
	}
}

func TestTemplateFuncs_FormatDateTimeLocale(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	formatDateTimeLocale := funcs["formatDateTimeLocale"].(func(any, string) string)

	testTime := time.Date(2025, time.June, 15, 10, 5, 0, 0, time.UTC)

	tests := []struct {
		name  string
		input any
		lang  string
		want  string
	}{
		{"en", testTime, "en", "Jun 15, 2025 10:05 AM"},
		{"ru", testTime, "ru", "15 июня 2025, 10:05"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDateTimeLocale(tt.input, tt.lang)
			if got != tt.want {
				t.Errorf("formatDateTimeLocale(%v, %q) = %q, want %q", tt.input, tt.lang, got, tt.want)
			}
		})
	}
}

func TestTemplateFuncs_TDefault(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	tDefault := funcs["TDefault"].(func(string, string, string) string)

	// Nonexistent key should fall back to defaultVal
	result := tDefault("en", "some.nonexistent.key.xyz", "My Default")
	if result != "My Default" {
		t.Errorf("TDefault with missing key = %q, want %q", result, "My Default")
	}
}

func TestTemplateFuncs_TLang(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	tLang := funcs["TLang"].(func(string, string) string)

	// Unknown language code should fall back to the code itself
	result := tLang("en", "zz")
	if result != "zz" {
		t.Errorf("TLang for unknown code = %q, want %q", result, "zz")
	}
}

func TestTemplateFuncs_IsDemoMode(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	isDemoMode := funcs["isDemoMode"].(func() bool)
	// Just verify it exists and is callable
	_ = isDemoMode()
}

func TestTemplateFuncs_MaskIP(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	maskIP := funcs["maskIP"].(func(string) string)

	// In non-demo mode, maskIP returns the IP unchanged
	ip := "192.168.1.1"
	got := maskIP(ip)
	// Without demo mode env var, IP is returned as-is
	if got == "" {
		t.Error("maskIP() returned empty string")
	}
}

func TestTemplateFuncs_CountryName(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	countryName, ok := funcs["countryName"]
	if !ok {
		t.Fatal("countryName not found in template funcs")
	}
	// Just check the func is present and callable
	_ = countryName
}

// ---------------------------------------------------------------------------
// SessionKeyAdminLang constant
// ---------------------------------------------------------------------------

func TestSessionKeyAdminLang_Value(t *testing.T) {
	if SessionKeyAdminLang != "admin_lang" {
		t.Errorf("SessionKeyAdminLang = %q, want %q", SessionKeyAdminLang, "admin_lang")
	}
}
