// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"encoding/json"
	"net/http"
)

// MaxJSONBodyBytes is the default maximum JSON request body size (1 MiB).
const MaxJSONBodyBytes int64 = 1 << 20

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"error":   message,
	})
}

// writeJSONSuccess writes a JSON success response.
func writeJSONSuccess(w http.ResponseWriter, data map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	if data == nil {
		data = make(map[string]any)
	}
	data["success"] = true
	_ = json.NewEncoder(w).Encode(data)
}

// decodeJSONWithLimit decodes JSON request body with an explicit size cap.
func decodeJSONWithLimit(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = MaxJSONBodyBytes
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)

	return dec.Decode(v)
}
