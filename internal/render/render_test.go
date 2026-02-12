// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"testing"
	"time"
)

func TestBlankLinesRegex(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no blank lines",
			input:    "line1\nline2\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "one blank line (two newlines)",
			input:    "line1\n\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "two blank lines (three newlines)",
			input:    "line1\n\n\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "multiple blank lines",
			input:    "line1\n\n\n\n\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "blank lines with spaces",
			input:    "line1\n  \n\t\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "windows line endings",
			input:    "line1\r\n\r\n\r\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "mixed line endings",
			input:    "line1\n\r\n\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "blank lines at start",
			input:    "\n\n\nline1\nline2",
			expected: "\nline1\nline2",
		},
		{
			name:     "blank lines at end",
			input:    "line1\nline2\n\n\n",
			expected: "line1\nline2\n",
		},
		{
			name:     "multiple sections with blank lines",
			input:    "a\n\n\nb\n\n\nc",
			expected: "a\nb\nc",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "only newlines",
			input:    "\n\n\n\n",
			expected: "\n",
		},
		{
			name:     "html with blank lines",
			input:    "<div>\n\n\n<p>text</p>\n\n\n</div>",
			expected: "<div>\n<p>text</p>\n</div>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(blankLinesRegex.ReplaceAll([]byte(tt.input), []byte("\n")))
			if got != tt.expected {
				t.Errorf("blankLinesRegex.ReplaceAll(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestTemplateFuncs_PagesListURL(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	pagesListURL := funcs["pagesListURL"].(func(string, int64, string, int) string)

	tests := []struct {
		name     string
		status   string
		category int64
		search   string
		page     int
		expected string
	}{
		{"no params", "", 0, "", 1, "/admin/pages"},
		{"status only", "published", 0, "", 1, "/admin/pages?status=published"},
		{"category only", "", 5, "", 1, "/admin/pages?category=5"},
		{"search only", "", 0, "test", 1, "/admin/pages?search=test"},
		{"page 2", "", 0, "", 2, "/admin/pages?page=2"},
		{"all params", "draft", 3, "hello", 2, "/admin/pages?category=3&page=2&search=hello&status=draft"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pagesListURL(tt.status, tt.category, tt.search, tt.page)
			if got != tt.expected {
				t.Errorf("pagesListURL(%q, %d, %q, %d) = %q, want %q",
					tt.status, tt.category, tt.search, tt.page, got, tt.expected)
			}
		})
	}
}

// TestTemplateFuncs_UikitFuncsPresent verifies that uikit functions are available
// through the Renderer's TemplateFuncs method.
func TestTemplateFuncs_UikitFuncsPresent(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()

	uikitFuncs := []string{
		"lower", "upper", "truncate", "hasPrefix", "contains",
		"safe", "safeHTML", "safeURL",
		"add", "sub", "multiply", "seq",
		"now", "timeBefore", "formatDate", "formatDateTime",
		"formatDateLocale", "formatDateTimeLocale",
		"toJSON", "parseJSON", "prettyJSON",
		"formatBytes", "formatNumber",
		"deref", "int64", "atoi",
		"dict", "repeat",
	}

	for _, name := range uikitFuncs {
		if _, ok := funcs[name]; !ok {
			t.Errorf("TemplateFuncs missing uikit function: %s", name)
		}
	}
}

// TestTemplateFuncs_OCMSFuncsPresent verifies that oCMS-specific functions are available.
func TestTemplateFuncs_OCMSFuncsPresent(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()

	ocmsFuncs := []string{
		"countryName", "isDemoMode", "maskIP", "pagesListURL",
		"T", "TDefault", "TLang", "adminLangOptions",
		"getMenu", "getMenuForLanguage",
		"mediaAlt", "mediaCaption",
		"isAdmin", "isEditor", "userRole",
		"hcaptchaEnabled", "hcaptchaWidget",
		"sentinelIsActive", "sentinelIsIPBanned", "sentinelIsIPWhitelisted",
	}

	for _, name := range ocmsFuncs {
		if _, ok := funcs[name]; !ok {
			t.Errorf("TemplateFuncs missing oCMS function: %s", name)
		}
	}
}

// TestTemplateFuncs_FormatDate verifies date formatting still works through the renderer.
func TestTemplateFuncs_FormatDate(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()

	formatDate := funcs["formatDate"].(func(time.Time) string)
	testTime := time.Date(2025, time.March, 15, 0, 0, 0, 0, time.UTC)
	if got := formatDate(testTime); got != "Mar 15, 2025" {
		t.Errorf("formatDate() = %q, want %q", got, "Mar 15, 2025")
	}
}
