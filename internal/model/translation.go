// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import "time"

// Entity types for translations
const (
	EntityTypePage     = "page"
	EntityTypeCategory = "category"
	EntityTypeTag      = "tag"
)

// Translation represents a link between content entities across languages.
// For example, Page 1 (English) linked to Page 2 (Russian) would be:
// Translation { EntityType: "page", EntityID: 1, LanguageID: 2, TranslationID: 2 }
type Translation struct {
	ID            int64     `json:"id"`
	EntityType    string    `json:"entity_type"`    // page, category, tag, menu_item
	EntityID      int64     `json:"entity_id"`      // ID of the source entity
	LanguageID    int64     `json:"language_id"`    // Language this translation is for
	TranslationID int64     `json:"translation_id"` // ID of the translated entity
	CreatedAt     time.Time `json:"created_at"`
}

// TranslationWithLanguage includes language information for display purposes.
type TranslationWithLanguage struct {
	Translation
	LanguageCode       string `json:"language_code"`
	LanguageName       string `json:"language_name"`
	LanguageNativeName string `json:"language_native_name"`
}

// TranslationLink represents a simplified view of a translation for UI purposes.
type TranslationLink struct {
	LanguageID   int64  `json:"language_id"`
	LanguageCode string `json:"language_code"`
	LanguageName string `json:"language_name"`
	NativeName   string `json:"native_name"`
	EntityID     int64  `json:"entity_id"` // The translated entity ID
	Exists       bool   `json:"exists"`    // Whether translation exists
}
