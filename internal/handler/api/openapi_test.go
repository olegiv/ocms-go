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

// TestOpenAPISpecProtectedEndpointsInheritApiKeyAuth guards against regressing
// to a globally-anonymous security default. Protected write endpoints must not
// publish "no-auth" alternatives, otherwise Swagger UI / generated clients will
// send unauthenticated requests that the router rejects with 401/403.
func TestOpenAPISpecProtectedEndpointsInheritApiKeyAuth(t *testing.T) {
	var spec map[string]any
	if err := yaml.Unmarshal(openAPISpecYAML, &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	topSec, ok := spec["security"].([]any)
	if !ok || len(topSec) == 0 {
		t.Fatal("spec is missing top-level 'security' or it is empty")
	}
	for _, entry := range topSec {
		m, _ := entry.(map[string]any)
		if _, has := m["ApiKeyAuth"]; !has {
			t.Errorf("top-level security should require ApiKeyAuth, got entry %#v", entry)
		}
	}

	paths, _ := spec["paths"].(map[string]any)
	// protected operations: (path, method) pairs that must NOT carry a
	// per-operation `security: []` or `{}` fallback.
	protected := []struct{ path, method string }{
		{"/auth", "get"},
		{"/pages", "post"},
		{"/pages/{id}", "put"},
		{"/pages/{id}", "delete"},
		{"/media", "post"},
		{"/media/{id}", "put"},
		{"/media/{id}", "delete"},
		{"/tags", "post"},
		{"/tags/{id}", "put"},
		{"/tags/{id}", "delete"},
		{"/categories", "post"},
		{"/categories/{id}", "put"},
		{"/categories/{id}", "delete"},
	}
	for _, op := range protected {
		item, ok := paths[op.path].(map[string]any)
		if !ok {
			t.Errorf("path %q missing", op.path)
			continue
		}
		opMap, ok := item[op.method].(map[string]any)
		if !ok {
			t.Errorf("%s %s missing in spec", op.method, op.path)
			continue
		}
		sec, has := opMap["security"]
		if !has {
			continue // inherits required top-level — correct.
		}
		secList, _ := sec.([]any)
		for _, entry := range secList {
			if m, _ := entry.(map[string]any); len(m) == 0 {
				t.Errorf("%s %s declares an anonymous `{}` alternative; protected endpoints must require auth", op.method, op.path)
			}
		}
	}
}
