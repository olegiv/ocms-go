// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestServeOpenAPIYAML(t *testing.T) {
	h := &DocsHandler{}
	rec := httptest.NewRecorder()
	h.ServeOpenAPIYAML(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/yaml", ct)
	}

	var spec map[string]any
	if err := yaml.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("embedded YAML is not parseable: %v", err)
	}
	if got := spec["openapi"]; got != "3.1.0" {
		t.Errorf("openapi version = %v, want 3.1.0", got)
	}
	if _, ok := spec["paths"].(map[string]any); !ok {
		t.Error("spec is missing 'paths' map")
	}
}

func TestServeOpenAPIJSON(t *testing.T) {
	h := &DocsHandler{}
	rec := httptest.NewRecorder()
	h.ServeOpenAPIJSON(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var spec map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("emitted JSON is not parseable: %v", err)
	}
	if got := spec["openapi"]; got != "3.1.0" {
		t.Errorf("openapi version = %v, want 3.1.0", got)
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("spec is missing 'paths' map")
	}
	required := []string{"/status", "/pages", "/pages/{id}", "/media", "/tags", "/categories"}
	for _, p := range required {
		if _, found := paths[p]; !found {
			t.Errorf("spec missing path %q", p)
		}
	}
}
