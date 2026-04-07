// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package api provides REST API handlers for the CMS.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
)

// assertStatusCode checks that the response has the expected status code.
func assertStatusCode(t *testing.T, w *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if w.Code != expected {
		t.Errorf("expected status %d, got %d", expected, w.Code)
	}
}

// assertErrorResponse unmarshals and validates an error response.
func assertErrorResponse(t *testing.T, w *httptest.ResponseRecorder, expectedCode string) ErrorResponse {
	t.Helper()
	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Error.Code != expectedCode {
		t.Errorf("expected code '%s', got %s", expectedCode, resp.Error.Code)
	}
	return resp
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()

	data := map[string]string{"key": "value"}
	WriteJSON(w, http.StatusOK, data)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %s", ct)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["key"] != "value" {
		t.Errorf("expected key 'value', got %s", resp["key"])
	}
}

func TestWriteSuccess(t *testing.T) {
	w := httptest.NewRecorder()

	data := map[string]string{"name": "test"}
	meta := &Meta{Total: 100, Page: 1, PerPage: 20}
	WriteSuccess(w, data, meta)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Meta == nil {
		t.Fatal("expected meta to be present")
	}
	if resp.Meta.Total != 100 {
		t.Errorf("expected total 100, got %d", resp.Meta.Total)
	}
}

func TestWriteCreated(t *testing.T) {
	w := httptest.NewRecorder()

	data := map[string]string{"id": "123"}
	WriteCreated(w, data)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, w.Code)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()

	WriteError(w, http.StatusBadRequest, "validation_error", "Invalid input", map[string]string{
		"field": "name",
	})

	assertStatusCode(t, w, http.StatusBadRequest)
	resp := assertErrorResponse(t, w, "validation_error")

	if resp.Error.Message != "Invalid input" {
		t.Errorf("expected message 'Invalid input', got %s", resp.Error.Message)
	}
	if resp.Error.Details["field"] != "name" {
		t.Errorf("expected details.field 'name', got %s", resp.Error.Details["field"])
	}
}

func TestWriteBadRequest(t *testing.T) {
	w := httptest.NewRecorder()
	WriteBadRequest(w, "Bad input", nil)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestWriteNotFound(t *testing.T) {
	w := httptest.NewRecorder()
	WriteNotFound(w, "Resource not found")
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestWriteUnauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	WriteUnauthorized(w, "Not authenticated")
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestWriteForbidden(t *testing.T) {
	w := httptest.NewRecorder()
	WriteForbidden(w, "Access denied")
	assertStatusCode(t, w, http.StatusForbidden)
}

func TestWriteInternalError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteInternalError(w, "Something went wrong")
	assertStatusCode(t, w, http.StatusInternalServerError)
}

func TestLogAndWriteInternalError(t *testing.T) {
	w := httptest.NewRecorder()
	testErr := errors.New("database connection failed")
	LogAndWriteInternalError(w, "Failed to create page", "error", testErr)

	assertStatusCode(t, w, http.StatusInternalServerError)
	resp := assertErrorResponse(t, w, "internal_error")
	if resp.Error.Message != "Failed to create page" {
		t.Errorf("expected message 'Failed to create page', got %q", resp.Error.Message)
	}
}

func TestWriteValidationError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteValidationError(w, map[string]string{
		"email": "Invalid email format",
		"name":  "Required field",
	})

	assertStatusCode(t, w, http.StatusUnprocessableEntity)
	resp := assertErrorResponse(t, w, "validation_error")

	if len(resp.Error.Details) != 2 {
		t.Errorf("expected 2 error details, got %d", len(resp.Error.Details))
	}
}

func TestMeta(t *testing.T) {
	meta := Meta{
		Total:   100,
		Page:    2,
		PerPage: 20,
		Pages:   5,
	}

	if meta.Total != 100 {
		t.Errorf("expected total 100, got %d", meta.Total)
	}
	if meta.Page != 2 {
		t.Errorf("expected page 2, got %d", meta.Page)
	}
	if meta.PerPage != 20 {
		t.Errorf("expected per_page 20, got %d", meta.PerPage)
	}
	if meta.Pages != 5 {
		t.Errorf("expected pages 5, got %d", meta.Pages)
	}
}

func TestResponse(t *testing.T) {
	resp := Response{
		Data: map[string]string{"test": "data"},
		Meta: &Meta{Total: 10},
	}

	jsonData, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal response: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if decoded.Meta == nil {
		t.Error("expected meta to be present")
	}
}

func TestStatusResponse(t *testing.T) {
	resp := StatusResponse{
		Status:  "ok",
		Version: "v1",
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %s", resp.Status)
	}
	if resp.Version != "v1" {
		t.Errorf("expected version 'v1', got %s", resp.Version)
	}
}

func TestAPIUserIDPtr_WithAPIKey(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pages", nil)

	apiKey := store.ApiKey{ID: 42, CreatedBy: 7, Name: "test-key"}
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAPIKey, apiKey)
	req = req.WithContext(ctx)

	got := h.apiUserIDPtr(req)
	if got == nil {
		t.Fatal("expected non-nil user ID")
	}
	if *got != 7 {
		t.Errorf("expected user ID 7, got %d", *got)
	}
}

func TestAPIUserIDPtr_NoAPIKey(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pages", nil)

	got := h.apiUserIDPtr(req)
	if got != nil {
		t.Errorf("expected nil user ID, got %d", *got)
	}
}

func TestAPIEventMeta_WithAPIKey(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages", nil)

	apiKey := store.ApiKey{ID: 42, CreatedBy: 7, Name: "test-key"}
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAPIKey, apiKey)
	req = req.WithContext(ctx)

	meta := h.apiEventMeta(req, map[string]any{"page_id": int64(99)})

	if meta["source"] != "api" {
		t.Errorf("expected source 'api', got %v", meta["source"])
	}
	if meta["api_key_id"] != int64(42) {
		t.Errorf("expected api_key_id 42, got %v", meta["api_key_id"])
	}
	if _, exists := meta["api_key_name"]; exists {
		t.Error("api_key_name should not be present in event metadata")
	}
	if meta["page_id"] != int64(99) {
		t.Errorf("expected page_id 99, got %v", meta["page_id"])
	}
}

func TestAPIEventMeta_NoAPIKey(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages", nil)

	meta := h.apiEventMeta(req, map[string]any{"slug": "test"})

	if meta["source"] != "api" {
		t.Errorf("expected source 'api', got %v", meta["source"])
	}
	if _, ok := meta["api_key_id"]; ok {
		t.Error("expected no api_key_id when no API key in context")
	}
	if meta["slug"] != "test" {
		t.Errorf("expected slug 'test', got %v", meta["slug"])
	}
}

func TestLogAndRespondError_NilEventService(t *testing.T) {
	h := &Handler{} // eventService is nil
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages", nil)

	h.logAndRespondError(w, req, "page", "Failed to create page", "error", errors.New("db error"))

	assertStatusCode(t, w, http.StatusInternalServerError)
	resp := assertErrorResponse(t, w, "internal_error")
	if resp.Error.Message != "Failed to create page" {
		t.Errorf("expected message 'Failed to create page', got %q", resp.Error.Message)
	}
}

func TestAPILogger_Error500_NilEventService(t *testing.T) {
	h := &Handler{} // eventService is nil
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages", nil)

	log := h.newAPILogger(req, "page")
	log.Error500(w, "Failed to create page", "error", errors.New("db error"))

	assertStatusCode(t, w, http.StatusInternalServerError)
	resp := assertErrorResponse(t, w, "internal_error")
	if resp.Error.Message != "Failed to create page" {
		t.Errorf("expected message 'Failed to create page', got %q", resp.Error.Message)
	}
}

func TestAPILogger_Info_NilEventService(t *testing.T) {
	h := &Handler{} // eventService is nil
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages", nil)

	log := h.newAPILogger(req, "page")
	// Should not panic
	log.Info("API: Page created", map[string]any{"page_id": int64(1)})
}

func TestAPILogger_Error_NilEventService(t *testing.T) {
	h := &Handler{} // eventService is nil
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", nil)

	log := h.newAPILogger(req, "media")
	// Should not panic
	log.Error("API: Media upload failed", map[string]any{"filename": "test.jpg"})
}

func TestLogEvent_NilEventService(t *testing.T) {
	h := &Handler{} // eventService is nil
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages", nil)

	// Should not panic
	h.logEvent(req, "page", "info", "API: Page created", map[string]any{"page_id": int64(1)})
}
