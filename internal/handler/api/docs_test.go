// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/web"
)

func TestServeDocsEmbedsSwaggerUI(t *testing.T) {
	templatesFS, err := fs.Sub(web.Templates, "templates")
	if err != nil {
		t.Fatalf("templatesFS: %v", err)
	}
	h, err := NewDocsHandler(DocsConfig{TemplateFS: templatesFS})
	if err != nil {
		t.Fatalf("NewDocsHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeDocs(rec, httptest.NewRequest(http.MethodGet, "/api/v1/docs", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	mustContain := []string{
		"/static/dist/swagger-ui/swagger-ui-bundle.js",
		"/static/dist/swagger-ui/swagger-ui.css",
		"url: '/api/v1/openapi.json'",
		"persistAuthorization: false",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("docs body missing %q", want)
		}
	}
	if strings.Contains(body, "unpkg.com") {
		t.Error("docs body should not reference unpkg.com after self-host migration")
	}
	if strings.Contains(body, "{{.CSPNonce}}") {
		t.Error("template placeholder {{.CSPNonce}} was not expanded")
	}
}
