// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import "time"

// Language text directions
const (
	DirectionLTR = "ltr"
	DirectionRTL = "rtl"
)

// Language represents a content/UI language in the CMS.
type Language struct {
	ID         int64     `json:"id"`
	Code       string    `json:"code"`        // ISO 639-1: en, ru, de, fr
	Name       string    `json:"name"`        // English, Russian, German, French
	NativeName string    `json:"native_name"` // English, Русский, Deutsch, Français
	IsDefault  bool      `json:"is_default"`  // only one can be default
	IsActive   bool      `json:"is_active"`   // enabled for site
	Direction  string    `json:"direction"`   // ltr, rtl
	Position   int       `json:"position"`    // sort order in language switcher
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// IsRTL returns true if the language is right-to-left.
func (l *Language) IsRTL() bool {
	return l.Direction == DirectionRTL
}

// CommonLanguages provides a list of commonly used languages for selection UI.
var CommonLanguages = []struct {
	Code       string
	Name       string
	NativeName string
	Direction  string
}{
	{"en", "English", "English", "ltr"},
	{"ru", "Russian", "Русский", "ltr"},
	{"de", "German", "Deutsch", "ltr"},
	{"fr", "French", "Français", "ltr"},
	{"es", "Spanish", "Español", "ltr"},
	{"it", "Italian", "Italiano", "ltr"},
	{"pt", "Portuguese", "Português", "ltr"},
	{"nl", "Dutch", "Nederlands", "ltr"},
	{"pl", "Polish", "Polski", "ltr"},
	{"uk", "Ukrainian", "Українська", "ltr"},
	{"zh", "Chinese", "中文", "ltr"},
	{"ja", "Japanese", "日本語", "ltr"},
	{"ko", "Korean", "한국어", "ltr"},
	{"ar", "Arabic", "العربية", "rtl"},
	{"he", "Hebrew", "עברית", "rtl"},
	{"fa", "Persian", "فارسی", "rtl"},
	{"tr", "Turkish", "Türkçe", "ltr"},
	{"vi", "Vietnamese", "Tiếng Việt", "ltr"},
	{"th", "Thai", "ไทย", "ltr"},
	{"hi", "Hindi", "हिन्दी", "ltr"},
}
