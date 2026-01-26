// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"encoding/json"
	"net/http"
)

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
