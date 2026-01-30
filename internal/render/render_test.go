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

func TestFormatDateForLocale(t *testing.T) {
	testTime := time.Date(2025, time.March, 15, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		time     time.Time
		lang     string
		expected string
	}{
		{"english", testTime, "en", "Mar 15, 2025"},
		{"russian", testTime, "ru", "15 марта 2025"},
		{"unknown language falls back to english", testTime, "de", "Mar 15, 2025"},
		{"empty language falls back to english", testTime, "", "Mar 15, 2025"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDateForLocale(tt.time, tt.lang)
			if got != tt.expected {
				t.Errorf("formatDateForLocale(%v, %q) = %q, want %q", tt.time, tt.lang, got, tt.expected)
			}
		})
	}
}

func TestFormatDateTimeForLocale(t *testing.T) {
	testTime := time.Date(2025, time.March, 15, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		time     time.Time
		lang     string
		expected string
	}{
		{"english", testTime, "en", "Mar 15, 2025 2:30 PM"},
		{"russian", testTime, "ru", "15 марта 2025, 14:30"},
		{"unknown language falls back to english", testTime, "de", "Mar 15, 2025 2:30 PM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDateTimeForLocale(tt.time, tt.lang)
			if got != tt.expected {
				t.Errorf("formatDateTimeForLocale(%v, %q) = %q, want %q", tt.time, tt.lang, got, tt.expected)
			}
		})
	}
}

func TestApplyTimeFormatter(t *testing.T) {
	testTime := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	formatter := func(t time.Time, _ string) string {
		return t.Format("2006-01-02")
	}

	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{"time.Time value", testTime, "2025-01-01"},
		{"*time.Time pointer", &testTime, "2025-01-01"},
		{"nil *time.Time", (*time.Time)(nil), ""},
		{"wrong type string", "not a time", ""},
		{"wrong type int", 12345, ""},
		{"nil", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyTimeFormatter(tt.input, "en", formatter)
			if got != tt.expected {
				t.Errorf("applyTimeFormatter(%v) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRussianMonths(t *testing.T) {
	// Verify all 12 months are defined
	if len(monthsRu) != 12 {
		t.Errorf("monthsRu has %d entries, want 12", len(monthsRu))
	}

	// Verify months are in correct order (genitive case)
	expectedMonths := []string{
		"января", "февраля", "марта", "апреля", "мая", "июня",
		"июля", "августа", "сентября", "октября", "ноября", "декабря",
	}

	for i, expected := range expectedMonths {
		if monthsRu[i] != expected {
			t.Errorf("monthsRu[%d] = %q, want %q", i, monthsRu[i], expected)
		}
	}
}

func TestFormatDateForLocale_AllMonths(t *testing.T) {
	year := 2025
	day := 15

	// Test all months in Russian
	for month := time.January; month <= time.December; month++ {
		testTime := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		result := formatDateForLocale(testTime, "ru")

		expectedMonth := monthsRu[month-1]
		if result != "15 "+expectedMonth+" 2025" {
			t.Errorf("formatDateForLocale for month %d = %q, want %q",
				month, result, "15 "+expectedMonth+" 2025")
		}
	}
}

func TestTemplateFuncs_FormatFunctions(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()

	// Test formatDate
	formatDate := funcs["formatDate"].(func(time.Time) string)
	testTime := time.Date(2025, time.March, 15, 0, 0, 0, 0, time.UTC)
	if got := formatDate(testTime); got != "Mar 15, 2025" {
		t.Errorf("formatDate() = %q, want %q", got, "Mar 15, 2025")
	}

	// Test formatDateTime
	formatDateTime := funcs["formatDateTime"].(func(time.Time) string)
	testTime = time.Date(2025, time.March, 15, 14, 30, 0, 0, time.UTC)
	if got := formatDateTime(testTime); got != "Mar 15, 2025 2:30 PM" {
		t.Errorf("formatDateTime() = %q, want %q", got, "Mar 15, 2025 2:30 PM")
	}
}

func TestTemplateFuncs_StringFunctions(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()

	// Test truncate
	truncate := funcs["truncate"].(func(string, int) string)
	tests := []struct {
		input    string
		length   int
		expected string
	}{
		{"hello world", 5, "hello..."},
		{"hello", 5, "hello"},
		{"hello", 10, "hello"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		if got := truncate(tt.input, tt.length); got != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.length, got, tt.expected)
		}
	}

	// Test lower and upper
	lower := funcs["lower"].(func(string) string)
	upper := funcs["upper"].(func(string) string)
	if got := lower("HELLO"); got != "hello" {
		t.Errorf("lower(HELLO) = %q, want %q", got, "hello")
	}
	if got := upper("hello"); got != "HELLO" {
		t.Errorf("upper(hello) = %q, want %q", got, "HELLO")
	}

	// Test hasPrefix
	hasPrefix := funcs["hasPrefix"].(func(string, string) bool)
	if !hasPrefix("hello world", "hello") {
		t.Error("hasPrefix should return true")
	}
	if hasPrefix("hello world", "world") {
		t.Error("hasPrefix should return false")
	}
}

func TestTemplateFuncs_MathFunctions(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()

	add := funcs["add"].(func(int, int) int)
	sub := funcs["sub"].(func(int, int) int)
	multiply := funcs["multiply"].(func(int, int) int)

	if got := add(5, 3); got != 8 {
		t.Errorf("add(5, 3) = %d, want 8", got)
	}
	if got := sub(5, 3); got != 2 {
		t.Errorf("sub(5, 3) = %d, want 2", got)
	}
	if got := multiply(5, 3); got != 15 {
		t.Errorf("multiply(5, 3) = %d, want 15", got)
	}
}

func TestTemplateFuncs_SeqFunction(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	seq := funcs["seq"].(func(int, int) []int)

	tests := []struct {
		start    int
		end      int
		expected []int
	}{
		{1, 5, []int{1, 2, 3, 4, 5}},
		{0, 0, []int{0}},
		{-2, 2, []int{-2, -1, 0, 1, 2}},
		{5, 3, nil}, // start > end returns empty
	}

	for _, tt := range tests {
		got := seq(tt.start, tt.end)
		if len(got) != len(tt.expected) {
			t.Errorf("seq(%d, %d) length = %d, want %d", tt.start, tt.end, len(got), len(tt.expected))
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("seq(%d, %d)[%d] = %d, want %d", tt.start, tt.end, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestTemplateFuncs_FormatBytes(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	formatBytes := funcs["formatBytes"].(func(int64) string)

	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1099511627776, "1.0 TB"},
	}

	for _, tt := range tests {
		if got := formatBytes(tt.bytes); got != tt.expected {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.expected)
		}
	}
}

func TestTemplateFuncs_Int64(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	int64Func := funcs["int64"].(func(any) int64)

	tests := []struct {
		name     string
		input    any
		expected int64
	}{
		{"int", 42, 42},
		{"int32", int32(42), 42},
		{"int64", int64(42), 42},
		{"float64", float64(42.9), 42},
		{"string", "42", 42},
		{"invalid string", "abc", 0},
		{"nil", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := int64Func(tt.input); got != tt.expected {
				t.Errorf("int64(%v) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestTemplateFuncs_Atoi(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	atoi := funcs["atoi"].(func(string) int64)

	tests := []struct {
		input    string
		expected int64
	}{
		{"42", 42},
		{"0", 0},
		{"-10", -10},
		{"", 0},
		{"abc", 0},
		{"12abc", 0},
	}

	for _, tt := range tests {
		if got := atoi(tt.input); got != tt.expected {
			t.Errorf("atoi(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestTemplateFuncs_Deref(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	deref := funcs["deref"].(func(*int64) int64)

	val := int64(42)
	if got := deref(&val); got != 42 {
		t.Errorf("deref(&42) = %d, want 42", got)
	}

	if got := deref(nil); got != 0 {
		t.Errorf("deref(nil) = %d, want 0", got)
	}
}

func TestTemplateFuncs_ParseJSON(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	parseJSON := funcs["parseJSON"].(func(string) []string)

	tests := []struct {
		input    string
		expected []string
	}{
		{`["a","b","c"]`, []string{"a", "b", "c"}},
		{`[]`, []string{}},
		{``, []string{}},
		{`invalid`, []string{}},
		{`["single"]`, []string{"single"}},
	}

	for _, tt := range tests {
		got := parseJSON(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("parseJSON(%q) length = %d, want %d", tt.input, len(got), len(tt.expected))
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("parseJSON(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestTemplateFuncs_Contains(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	contains := funcs["contains"].(func(any, any) bool)

	// String slice
	slice := []string{"a", "b", "c"}
	if !contains(slice, "b") {
		t.Error("contains(slice, 'b') should be true")
	}
	if contains(slice, "d") {
		t.Error("contains(slice, 'd') should be false")
	}

	// String contains
	if !contains("hello world", "world") {
		t.Error("contains('hello world', 'world') should be true")
	}
	if contains("hello world", "xyz") {
		t.Error("contains('hello world', 'xyz') should be false")
	}

	// Wrong types
	if contains(123, "a") {
		t.Error("contains(int, string) should be false")
	}
	if contains(slice, 123) {
		t.Error("contains(slice, int) should be false")
	}
}

func TestTemplateFuncs_Dict(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	dict := funcs["dict"].(func(...any) map[string]any)

	// Valid dict
	result := dict("key1", "value1", "key2", 42)
	if result["key1"] != "value1" {
		t.Errorf("dict key1 = %v, want 'value1'", result["key1"])
	}
	if result["key2"] != 42 {
		t.Errorf("dict key2 = %v, want 42", result["key2"])
	}

	// Odd number of args returns nil
	result = dict("key1", "value1", "key2")
	if result != nil {
		t.Error("dict with odd args should return nil")
	}

	// Non-string key is skipped
	result = dict(123, "value1", "key2", "value2")
	if _, ok := result["key2"]; !ok {
		t.Error("dict should contain key2")
	}
	if len(result) != 1 {
		t.Errorf("dict with non-string key should have 1 entry, got %d", len(result))
	}
}

func TestTemplateFuncs_PrettyJSON(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	prettyJSON := funcs["prettyJSON"].(func(string) string)

	// Valid JSON
	input := `{"a":1,"b":2}`
	result := prettyJSON(input)
	if result == input {
		t.Error("prettyJSON should format the JSON")
	}

	// Invalid JSON returns original
	invalid := "not json"
	if got := prettyJSON(invalid); got != invalid {
		t.Errorf("prettyJSON(invalid) = %q, want %q", got, invalid)
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

func TestTemplateFuncs_Now(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	now := funcs["now"].(func() time.Time)

	before := time.Now()
	result := now()
	after := time.Now()

	if result.Before(before) || result.After(after) {
		t.Error("now() should return current time")
	}
}

func TestTemplateFuncs_Repeat(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	repeat := funcs["repeat"].(func(string, int) string)

	tests := []struct {
		s        string
		count    int
		expected string
	}{
		{"a", 3, "aaa"},
		{"ab", 2, "abab"},
		{"x", 0, ""},
		{"", 5, ""},
	}

	for _, tt := range tests {
		if got := repeat(tt.s, tt.count); got != tt.expected {
			t.Errorf("repeat(%q, %d) = %q, want %q", tt.s, tt.count, got, tt.expected)
		}
	}
}
