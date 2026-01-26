// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogAndHTTPError(t *testing.T) {
	tests := []struct {
		name       string
		message    string
		statusCode int
		logMsg     string
	}{
		{"bad request", "Bad Request", http.StatusBadRequest, "validation failed"},
		{"not found", "Not Found", http.StatusNotFound, "resource missing"},
		{"internal error", "Internal Server Error", http.StatusInternalServerError, "database error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			logAndHTTPError(w, tt.message, tt.statusCode, tt.logMsg)

			if w.Code != tt.statusCode {
				t.Errorf("status code = %d, want %d", w.Code, tt.statusCode)
			}

			body := w.Body.String()
			if body == "" {
				t.Error("body should not be empty")
			}
		})
	}
}

func TestLogAndInternalError(t *testing.T) {
	w := httptest.NewRecorder()
	logAndInternalError(w, "database connection failed", "error", errors.New("connection refused"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestRequireEntityWithError(t *testing.T) {
	type TestEntity struct {
		ID   int64
		Name string
	}

	t.Run("entity found", func(t *testing.T) {
		w := httptest.NewRecorder()
		queryFn := func(id int64) (TestEntity, error) {
			return TestEntity{ID: id, Name: "Test"}, nil
		}

		entity, ok := requireEntityWithError(w, "item", 1, queryFn)

		if !ok {
			t.Error("expected ok to be true")
		}
		if entity.ID != 1 || entity.Name != "Test" {
			t.Errorf("entity = %+v, want {ID:1 Name:Test}", entity)
		}
		if w.Code != http.StatusOK {
			t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("entity not found", func(t *testing.T) {
		w := httptest.NewRecorder()
		queryFn := func(id int64) (TestEntity, error) {
			return TestEntity{}, sql.ErrNoRows
		}

		entity, ok := requireEntityWithError(w, "item", 1, queryFn)

		if ok {
			t.Error("expected ok to be false")
		}
		if entity.ID != 0 {
			t.Errorf("entity should be zero value, got %+v", entity)
		}
		if w.Code != http.StatusNotFound {
			t.Errorf("status code = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("database error", func(t *testing.T) {
		w := httptest.NewRecorder()
		queryFn := func(id int64) (TestEntity, error) {
			return TestEntity{}, errors.New("connection refused")
		}

		entity, ok := requireEntityWithError(w, "item", 1, queryFn)

		if ok {
			t.Error("expected ok to be false")
		}
		if entity.ID != 0 {
			t.Errorf("entity should be zero value, got %+v", entity)
		}
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status code = %d, want %d", w.Code, http.StatusInternalServerError)
		}
	})
}

func TestRequireEntityWithJSONError(t *testing.T) {
	type TestEntity struct {
		ID   int64
		Name string
	}

	t.Run("entity found", func(t *testing.T) {
		w := httptest.NewRecorder()
		queryFn := func(id int64) (TestEntity, error) {
			return TestEntity{ID: id, Name: "Test"}, nil
		}

		entity, ok := requireEntityWithJSONError(w, "Item", 1, queryFn)

		if !ok {
			t.Error("expected ok to be true")
		}
		if entity.ID != 1 || entity.Name != "Test" {
			t.Errorf("entity = %+v, want {ID:1 Name:Test}", entity)
		}
	})

	t.Run("entity not found", func(t *testing.T) {
		w := httptest.NewRecorder()
		queryFn := func(id int64) (TestEntity, error) {
			return TestEntity{}, sql.ErrNoRows
		}

		_, ok := requireEntityWithJSONError(w, "Item", 1, queryFn)

		if ok {
			t.Error("expected ok to be false")
		}

		resp := assertJSONResponse(t, w, http.StatusNotFound, false)
		if resp["error"] != "Item not found" {
			t.Errorf("error = %q, want %q", resp["error"], "Item not found")
		}
	})

	t.Run("database error", func(t *testing.T) {
		w := httptest.NewRecorder()
		queryFn := func(id int64) (TestEntity, error) {
			return TestEntity{}, errors.New("connection refused")
		}

		_, ok := requireEntityWithJSONError(w, "Item", 1, queryFn)

		if ok {
			t.Error("expected ok to be false")
		}

		assertJSONResponse(t, w, http.StatusInternalServerError, false)
	})
}

func TestRequireEntityWithErrorDifferentTypes(t *testing.T) {
	// Test with int64 type
	t.Run("int64 entity", func(t *testing.T) {
		w := httptest.NewRecorder()
		queryFn := func(id int64) (int64, error) {
			return 42, nil
		}

		result, ok := requireEntityWithError(w, "count", 1, queryFn)
		if !ok || result != 42 {
			t.Errorf("got (%d, %v), want (42, true)", result, ok)
		}
	})

	// Test with string type
	t.Run("string entity", func(t *testing.T) {
		w := httptest.NewRecorder()
		queryFn := func(id int64) (string, error) {
			return "hello", nil
		}

		result, ok := requireEntityWithError(w, "message", 1, queryFn)
		if !ok || result != "hello" {
			t.Errorf("got (%q, %v), want (hello, true)", result, ok)
		}
	})

	// Test with struct type
	t.Run("struct entity", func(t *testing.T) {
		type User struct {
			ID    int64
			Email string
			Name  string
		}

		w := httptest.NewRecorder()
		queryFn := func(id int64) (User, error) {
			return User{ID: id, Email: "test@example.com", Name: "Test User"}, nil
		}

		result, ok := requireEntityWithError(w, "user", 5, queryFn)
		if !ok {
			t.Error("expected ok to be true")
		}
		if result.ID != 5 || result.Email != "test@example.com" {
			t.Errorf("got %+v", result)
		}
	})
}
