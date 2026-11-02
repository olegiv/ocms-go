// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package connectedapps

import (
	"mime"
	"net/http"
)

// ParseRevoke accepts a bounded form body; query parameters cannot select
// credentials, and duplicates cannot make adapters disagree about the target.
func ParseRevoke(w http.ResponseWriter, r *http.Request) (kind, id string, err error) {
	mediaType, _, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || parseErr != nil || mediaType != "application/x-www-form-urlencoded" {
		return "", "", ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil || len(r.PostForm["kind"]) != 1 || len(r.PostForm["id"]) != 1 {
		return "", "", ErrInvalid
	}
	kind, id = r.PostForm.Get("kind"), r.PostForm.Get("id")
	if (kind != "oauth" && kind != "api_key") || id == "" || len(id) > 128 {
		return "", "", ErrInvalid
	}
	return kind, id, nil
}
