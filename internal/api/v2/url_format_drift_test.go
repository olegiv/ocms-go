// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2_test

import (
	"maps"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	apiv2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// urlFormatFields are the page body properties holding a caller-supplied URL.
// Each must stay on `uri-reference`, never `uri` — see the test below.
var urlFormatFields = []string{"canonical_url", "video_url"}

// TestPageURLFieldsUseURIReference fails if canonical_url or video_url is
// declared with format `uri` on either the create or the update body.
//
// huma v2.39.1 made `uri` reject every relative reference AND the empty string.
// Both are values this API has always accepted: a relative canonical URL is
// valid content, and an explicit "" is the only way a PATCH can clear the
// field (Service.Update treats a non-nil pointer as "write this value", so
// omitting the key means "leave unchanged"). Switching to `uri` would silently
// make these fields write-once for every API client.
//
// Bug state: change either tag in pages/types.go back to format:"uri" and this
// names the offending property.
func TestPageURLFieldsUseURIReference(t *testing.T) {
	_, schemas := pageBodySchemas(t)
	if len(schemas) != 2 {
		t.Fatalf("resolved %d page body schemas, want create and update", len(schemas))
	}

	checked := 0
	for name, schema := range schemas {
		for _, field := range urlFormatFields {
			prop := schema.Properties[field]
			if prop == nil {
				t.Errorf("%s: property %q is missing from the generated schema", name, field)
				continue
			}
			checked++
			if prop.Format != "uri-reference" {
				t.Errorf("%s.%s has format %q, want \"uri-reference\"; format \"uri\" rejects "+
					"relative URLs and the empty string that clears the field",
					name, field, prop.Format)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no URL properties were inspected — the test is passing vacuously")
	}
}

// TestPageURLValidationAcceptsClearAndRelative pins the request-validation
// outcomes that clients depend on, at the layer where huma enforces `format`.
// The schema assertion above catches a tag edit; this catches huma changing
// what a given format means, which is exactly how v2.39.1 broke `uri`.
func TestPageURLValidationAcceptsClearAndRelative(t *testing.T) {
	registry, schemas := pageBodySchemas(t)
	create := schemas["create"]
	if create == nil {
		t.Fatal("create body schema not resolved")
	}

	// Every case supplies the required fields so the only possible complaint
	// is about the URL itself.
	base := func(extra map[string]any) map[string]any {
		body := map[string]any{"title": "T", "slug": "t", "body": "b"}
		maps.Copy(body, extra)
		return body
	}

	tests := []struct {
		name string
		body map[string]any
	}{
		{"omitted", base(nil)},
		{"empty clears the field", base(map[string]any{"canonical_url": ""})},
		{"relative path", base(map[string]any{"canonical_url": "/about"})},
		{"scheme relative", base(map[string]any{"canonical_url": "//cdn.example.com/a"})},
		{"absolute", base(map[string]any{"canonical_url": "https://example.com/a"})},
		{"empty video url", base(map[string]any{"video_url": ""})},
		{"relative video url", base(map[string]any{"video_url": "/media/clip.mp4"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var res huma.ValidateResult
			pb := huma.NewPathBuffer(make([]byte, 0, 128), 0)
			huma.Validate(registry, create, pb, huma.ModeWriteToServer, tt.body, &res)
			if len(res.Errors) > 0 {
				t.Errorf("request validation rejected a supported value: %v", res.Errors)
			}
		})
	}
}

// pageBodySchemas registers the API and resolves the create and update page
// request-body schemas from the generated OpenAPI document, so the assertions
// run against what /api/v2/openapi.json actually serves.
func pageBodySchemas(t *testing.T) (huma.Registry, map[string]*huma.Schema) {
	t.Helper()

	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)

	r := chi.NewRouter()
	queries := store.New(db)
	h := apiv2.Register(r, apiv2.Deps{DB: db, Queries: queries})
	pages.Register(h.API, pages.NewService(db, queries, nil, nil, pages.Policy{}))

	oapi := h.OpenAPI()
	registry := oapi.Components.Schemas

	resolve := func(op *huma.Operation) *huma.Schema {
		if op == nil || op.RequestBody == nil {
			return nil
		}
		mt := op.RequestBody.Content["application/json"]
		if mt == nil || mt.Schema == nil {
			return nil
		}
		if mt.Schema.Ref != "" {
			return registry.SchemaFromRef(mt.Schema.Ref)
		}
		return mt.Schema
	}

	schemas := map[string]*huma.Schema{}
	if item := oapi.Paths["/pages"]; item != nil {
		if s := resolve(item.Post); s != nil {
			schemas["create"] = s
		}
	}
	if item := oapi.Paths["/pages/{id}"]; item != nil {
		if s := resolve(item.Put); s != nil {
			schemas["update"] = s
		} else if s := resolve(item.Patch); s != nil {
			schemas["update"] = s
		}
	}
	return registry, schemas
}
