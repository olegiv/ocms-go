package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// assertJSONResponse validates common JSON response properties.
func assertJSONResponse(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantSuccess bool) map[string]any {
	t.Helper()

	if w.Code != wantStatus {
		t.Errorf("status code = %d, want %d", w.Code, wantStatus)
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if success, ok := resp["success"].(bool); !ok || success != wantSuccess {
		t.Errorf("success = %v, want %v", resp["success"], wantSuccess)
	}

	return resp
}

func TestWriteJSONError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		message    string
	}{
		{"bad request", http.StatusBadRequest, "Invalid input"},
		{"not found", http.StatusNotFound, "Resource not found"},
		{"internal error", http.StatusInternalServerError, "Something went wrong"},
		{"unauthorized", http.StatusUnauthorized, "Access denied"},
		{"empty message", http.StatusBadRequest, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeJSONError(w, tt.statusCode, tt.message)

			resp := assertJSONResponse(t, w, tt.statusCode, false)

			// Check error message
			if errMsg, ok := resp["error"].(string); !ok || errMsg != tt.message {
				t.Errorf("error = %q, want %q", resp["error"], tt.message)
			}
		})
	}
}

func TestWriteJSONSuccess(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
	}{
		{
			name: "with data",
			data: map[string]any{
				"id":   1,
				"name": "Test",
			},
		},
		{
			name: "nil data",
			data: nil,
		},
		{
			name: "empty map",
			data: map[string]any{},
		},
		{
			name: "nested data",
			data: map[string]any{
				"user": map[string]any{
					"id":   1,
					"name": "John",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeJSONSuccess(w, tt.data)

			resp := assertJSONResponse(t, w, http.StatusOK, true)

			// Check data fields are present (if data was provided)
			if tt.data != nil {
				for key := range tt.data {
					if _, ok := resp[key]; !ok {
						t.Errorf("missing key %q in response", key)
					}
				}
			}
		})
	}
}

func TestWriteJSONSuccessOverwritesSuccess(t *testing.T) {
	// Test that even if data contains success: false, it gets overwritten to true
	w := httptest.NewRecorder()
	data := map[string]any{
		"success": false, // Should be overwritten
		"id":      1,
	}
	writeJSONSuccess(w, data)

	// assertJSONResponse checks that success is true
	assertJSONResponse(t, w, http.StatusOK, true)
}
