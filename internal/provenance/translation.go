// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/olegiv/ocms-go/internal/store"
)

// TranslatedText deliberately excludes lifecycle, ownership and sibling membership.
type TranslatedText struct {
	Title           string `json:"title"`
	Body            string `json:"body"`
	Summary         string `json:"summary"`
	MetaTitle       string `json:"meta_title"`
	MetaDescription string `json:"meta_description"`
	MetaKeywords    string `json:"meta_keywords"`
}

func TextFingerprint(fields TranslatedText) string {
	raw, _ := json.Marshal(fields)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func PageFingerprint(p store.Page) string {
	return TextFingerprint(TranslatedText{p.Title, p.Body, p.Summary, p.MetaTitle, p.MetaDescription, p.MetaKeywords})
}
