// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
)

var blockedUploadExtensions = map[string]struct{}{
	".asp":   {},
	".aspx":  {},
	".cgi":   {},
	".htm":   {},
	".html":  {},
	".js":    {},
	".json":  {},
	".jsp":   {},
	".mjs":   {},
	".php":   {},
	".shtml": {},
	".svg":   {},
	".svgz":  {},
	".xhtml": {},
	".xml":   {},
}

var inlineUploadExtensions = map[string]struct{}{
	".gif":  {},
	".ico":  {},
	".jpeg": {},
	".jpg":  {},
	".mp4":  {},
	".png":  {},
	".webm": {},
	".webp": {},
}

// SecureUploads hardens uploads serving by denying active content extensions
// and forcing download disposition for non-inline file types.
func SecureUploads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ext := strings.ToLower(path.Ext(r.URL.Path))
		if _, blocked := blockedUploadExtensions[ext]; blocked {
			slog.Warn("blocked upload file with active extension", "path", r.URL.Path, "ext", ext)
			http.NotFound(w, r)
			return
		}

		if _, inline := inlineUploadExtensions[ext]; !inline {
			filename := sanitizeContentDispositionFilename(path.Base(r.URL.Path))
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
			w.Header().Set("X-Download-Options", "noopen")
		}

		next.ServeHTTP(w, r)
	})
}

func sanitizeContentDispositionFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		return "download"
	}

	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			// Skip control bytes in header value.
			continue
		case r == '"' || r == '\\' || r == ';':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}

	clean := strings.TrimSpace(b.String())
	if clean == "" || clean == "." || clean == "/" {
		return "download"
	}

	return clean
}
