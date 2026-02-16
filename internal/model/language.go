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
	{Code: "en", Name: "English", NativeName: "English", Direction: "ltr"},
	{Code: "ru", Name: "Russian", NativeName: "Русский", Direction: "ltr"},
	{Code: "de", Name: "German", NativeName: "Deutsch", Direction: "ltr"},
	{Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr"},
	{Code: "es", Name: "Spanish", NativeName: "Español", Direction: "ltr"},
	{Code: "it", Name: "Italian", NativeName: "Italiano", Direction: "ltr"},
	{Code: "pt", Name: "Portuguese", NativeName: "Português", Direction: "ltr"},
	{Code: "nl", Name: "Dutch", NativeName: "Nederlands", Direction: "ltr"},
	{Code: "pl", Name: "Polish", NativeName: "Polski", Direction: "ltr"},
	{Code: "uk", Name: "Ukrainian", NativeName: "Українська", Direction: "ltr"},
	{Code: "zh", Name: "Chinese", NativeName: "中文", Direction: "ltr"},
	{Code: "ja", Name: "Japanese", NativeName: "日本語", Direction: "ltr"},
	{Code: "ko", Name: "Korean", NativeName: "한국어", Direction: "ltr"},
	{Code: "ar", Name: "Arabic", NativeName: "العربية", Direction: "rtl"},
	{Code: "he", Name: "Hebrew", NativeName: "עברית", Direction: "rtl"},
	{Code: "fa", Name: "Persian", NativeName: "فارسی", Direction: "rtl"},
	{Code: "tr", Name: "Turkish", NativeName: "Türkçe", Direction: "ltr"},
	{Code: "vi", Name: "Vietnamese", NativeName: "Tiếng Việt", Direction: "ltr"},
	{Code: "th", Name: "Thai", NativeName: "ไทย", Direction: "ltr"},
	{Code: "hi", Name: "Hindi", NativeName: "हिन्दी", Direction: "ltr"},
}
