// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	apiv2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/web"
)

func TestSwaggerUsesSameOriginPaths(t *testing.T) {
	templates, err := fs.Sub(web.Templates, "templates")
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Route("/api/v2", func(r chi.Router) {
		docs, docsErr := apiv2.NewDocsServer(templates, apiv2.Register(r, apiv2.Deps{}))
		if docsErr != nil {
			t.Fatal(docsErr)
		}
		r.Get("/docs", docs.ServeDocs)
		r.Get("/openapi.json", docs.ServeOpenAPIJSON)
		r.Get("/openapi.yaml", docs.ServeOpenAPIYAML)
	})
	for _, tc := range []struct{ name, scheme, forwarded string }{
		{"direct HTTPS with spoofed headers", "https", "http"},
		{"HTTP backend behind TLS termination", "http", "https"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, endpoint := range []string{"docs", "openapi.json", "openapi.yaml"} {
				path := "/api/v2/" + endpoint
				req := httptest.NewRequest(http.MethodGet, tc.scheme+"://request-host.invalid"+path, nil)
				req.Header.Set("X-Forwarded-Proto", tc.forwarded)
				req.Header.Set("X-Forwarded-Host", "forwarded-host.invalid")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, req)
				if response.Code != http.StatusOK {
					t.Fatalf("%s returned %d", path, response.Code)
				}
				body := response.Body.String()
				if strings.Contains(body, "request-host.invalid") || strings.Contains(body, "forwarded-host.invalid") {
					t.Errorf("%s reflected a request host", path)
				}
				if endpoint == "docs" {
					if !strings.Contains(body, "url: '/api/v2/openapi.json'") {
						t.Error("Swagger UI must load its spec from the same origin")
					}
					continue
				}
				var spec struct{ Servers []struct{ URL string } }
				if err := yaml.Unmarshal(response.Body.Bytes(), &spec); err != nil {
					t.Fatal(err)
				}
				if len(spec.Servers) != 1 || spec.Servers[0].URL != "/api/v2" {
					t.Errorf("%s has servers %v, want only /api/v2", path, spec.Servers)
				}
			}
		})
	}
}

// The old adapter derived an absolute base URL even though the template
// ignored it. Keep request origin inputs out of the docs adapter itself.
func TestDocsDoNotDeriveRequestOrigin(t *testing.T) {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, "docs.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for node := range ast.Preorder(file) {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			if n.Sel.Name == "Host" || n.Sel.Name == "TLS" {
				t.Errorf("%s reads %s to derive a request origin", positions.Position(n.Pos()), n.Sel.Name)
			}
		case *ast.BasicLit:
			value, unquoteErr := strconv.Unquote(n.Value)
			if n.Kind == token.STRING && unquoteErr == nil {
				switch strings.ToLower(value) {
				case "forwarded", "x-forwarded-host", "x-forwarded-proto":
					t.Errorf("%s references the %s origin header", positions.Position(n.Pos()), value)
				}
			}
		}
	}
}
