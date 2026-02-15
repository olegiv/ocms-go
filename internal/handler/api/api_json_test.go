// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	t.Run("accepts valid object", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"name":"ok"}`))
		w := httptest.NewRecorder()
		var got payload

		if err := decodeJSON(w, req, &got, maxAPIJSONBodyBytes); err != nil {
			t.Fatalf("decodeJSON() error = %v, want nil", err)
		}
		if got.Name != "ok" {
			t.Fatalf("decoded name = %q, want %q", got.Name, "ok")
		}
	})

	t.Run("rejects unknown field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"name":"ok","extra":1}`))
		w := httptest.NewRecorder()
		var got payload

		if err := decodeJSON(w, req, &got, maxAPIJSONBodyBytes); err == nil {
			t.Fatal("decodeJSON() expected unknown-field error")
		}
	})

	t.Run("rejects multiple JSON objects", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"name":"one"}{"name":"two"}`))
		w := httptest.NewRecorder()
		var got payload

		if err := decodeJSON(w, req, &got, maxAPIJSONBodyBytes); err == nil {
			t.Fatal("decodeJSON() expected multi-object error")
		}
	})

	t.Run("enforces size limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"name":"0123456789"}`))
		w := httptest.NewRecorder()
		var got payload

		if err := decodeJSON(w, req, &got, 8); err == nil {
			t.Fatal("decodeJSON() expected size-limit error")
		}
	})
}
