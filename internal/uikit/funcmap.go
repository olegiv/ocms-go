// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package uikit provides reusable template helpers, pagination logic,
// and view model types that can be shared across Go web projects.
package uikit

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"
)

// MonthsRu contains Russian month names in genitive case.
var MonthsRu = []string{
	"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря",
}

// TemplateFuncs returns a template.FuncMap with pure, reusable helper functions.
// These functions have no external dependencies beyond the Go standard library.
//
// Callers can merge project-specific functions on top:
//
//	funcs := uikit.TemplateFuncs()
//	funcs["myFunc"] = myProjectFunc
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// String functions
		"lower":     strings.ToLower,
		"upper":     strings.ToUpper,
		"repeat":    strings.Repeat,
		"hasPrefix": strings.HasPrefix,
		"truncate": func(s string, length int) string {
			if len(s) <= length {
				return s
			}
			return s[:length] + "..."
		},
		"contains": func(collection, element any) bool {
			if slice, ok := collection.([]string); ok {
				if elem, ok := element.(string); ok {
					for _, s := range slice {
						if s == elem {
							return true
						}
					}
				}
				return false
			}
			if s, ok := collection.(string); ok {
				if substr, ok := element.(string); ok {
					return strings.Contains(s, substr)
				}
			}
			return false
		},

		// HTML/URL safety
		"safe": func(s string) template.HTML {
			return template.HTML(s)
		},
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
		"safeURL": func(s string) template.URL {
			return template.URL(s)
		},

		// Math
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"multiply": func(a, b int) int {
			return a * b
		},
		"seq": func(start, end int) []int {
			var result []int
			for i := start; i <= end; i++ {
				result = append(result, i)
			}
			return result
		},

		// Time
		"now": time.Now,
		"timeBefore": func(t1, t2 time.Time) bool {
			return t1.Before(t2)
		},
		"formatDate": func(t time.Time) string {
			return t.Format("Jan 2, 2006")
		},
		"formatDateTime": func(t time.Time) string {
			return t.Format("Jan 2, 2006 3:04 PM")
		},
		"formatDateLocale": func(t any, lang string) string {
			return ApplyTimeFormatter(t, lang, FormatDateForLocale)
		},
		"formatDateTimeLocale": func(t any, lang string) string {
			return ApplyTimeFormatter(t, lang, FormatDateTimeForLocale)
		},

		// JSON
		"toJSON": func(v any) template.JS {
			b, err := json.Marshal(v)
			if err != nil {
				return "[]"
			}
			return template.JS(b)
		},
		"parseJSON": func(s string) []string {
			if s == "" || s == "[]" {
				return []string{}
			}
			var result []string
			if err := json.Unmarshal([]byte(s), &result); err != nil {
				return []string{}
			}
			return result
		},
		"prettyJSON": func(s string) string {
			var data any
			if err := json.Unmarshal([]byte(s), &data); err != nil {
				return s
			}
			pretty, err := json.MarshalIndent(data, "", "  ")
			if err != nil {
				return s
			}
			return string(pretty)
		},

		// Formatting
		"formatBytes": func(bytes int64) string {
			const unit = 1024
			if bytes < unit {
				return fmt.Sprintf("%d B", bytes)
			}
			div, exp := int64(unit), 0
			for n := bytes / unit; n >= unit; n /= unit {
				div *= unit
				exp++
			}
			return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
		},
		"formatNumber": func(n int64) string {
			if n < 1000 {
				return strconv.FormatInt(n, 10)
			}
			s := strconv.FormatInt(n, 10)
			var result strings.Builder
			for i, c := range s {
				if i > 0 && (len(s)-i)%3 == 0 {
					result.WriteRune(',')
				}
				result.WriteRune(c)
			}
			return result.String()
		},

		// Type conversion
		"deref": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"int64": func(v any) int64 {
			switch val := v.(type) {
			case int:
				return int64(val)
			case int32:
				return int64(val)
			case int64:
				return val
			case float64:
				return int64(val)
			case string:
				i, _ := strconv.ParseInt(val, 10, 64)
				return i
			default:
				return 0
			}
		},
		"atoi": func(s string) int64 {
			if s == "" {
				return 0
			}
			i, _ := strconv.ParseInt(s, 10, 64)
			return i
		},

		// Data structures
		"dict": func(values ...any) map[string]any {
			if len(values)%2 != 0 {
				return nil
			}
			dict := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					continue
				}
				dict[key] = values[i+1]
			}
			return dict
		},
	}
}

// FormatDateForLocale formats a date according to the specified language.
func FormatDateForLocale(t time.Time, lang string) string {
	if lang == "ru" {
		return fmt.Sprintf("%d %s %d", t.Day(), MonthsRu[t.Month()-1], t.Year())
	}
	return t.Format("Jan 2, 2006")
}

// FormatDateTimeForLocale formats a time.Time as a localized datetime string.
func FormatDateTimeForLocale(t time.Time, lang string) string {
	if lang == "ru" {
		return fmt.Sprintf("%d %s %d, %02d:%02d", t.Day(), MonthsRu[t.Month()-1], t.Year(), t.Hour(), t.Minute())
	}
	return t.Format("Jan 2, 2006 3:04 PM")
}

// ApplyTimeFormatter applies a time formatting function to a value that may be time.Time or *time.Time.
// Returns an empty string for nil pointers or unsupported types.
func ApplyTimeFormatter(t any, lang string, formatter func(time.Time, string) string) string {
	switch v := t.(type) {
	case time.Time:
		return formatter(v, lang)
	case *time.Time:
		if v == nil {
			return ""
		}
		return formatter(*v, lang)
	default:
		return ""
	}
}
