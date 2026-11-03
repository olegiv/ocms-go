// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"encoding/json"

	"github.com/olegiv/ocms-go/internal/security"
)

// pageEditorValidStylesJSON is the page sanitizer's style allowlist encoded for
// TinyMCE's valid_styles option. The page form hands it to the editor while
// OCMS_SANITIZE_PAGE_HTML is on.
var pageEditorValidStylesJSON = mustEncodeValidStyles(security.PageEditorValidStyles())

// mustEncodeValidStyles encodes the allowlist once at startup. Encoding a
// map[string]string cannot fail; if it ever did, failing at startup is better
// than an editor that silently keeps or drops the wrong styles.
func mustEncodeValidStyles(validStyles map[string]string) string {
	encoded, err := json.Marshal(validStyles)
	if err != nil {
		panic("encode page editor valid_styles: " + err.Error())
	}
	return string(encoded)
}
