// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/olegiv/ocms-go/internal/ai/draft"
)

// Future authoring tools use the existing generic builder. Keep its validation
// aligned with the engine before any CMS-writing tool is registered in R1.5.
func TestDraftSchemasMatchMCPBuilderAndTypedDecoders(t *testing.T) {
	create, err := schemaFor[draft.PageDraft]()
	if err != nil {
		t.Fatal(err)
	}
	update, err := schemaFor[draft.Changes]()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []struct {
		name   string
		mcp    *jsonschema.Schema
		engine func() (*jsonschema.Schema, error)
		decode func([]byte) error
		cases  map[string]bool
	}{
		{"create", create, draft.CreateSchema, func(raw []byte) error { _, err := draft.DecodeCreate(raw, "en"); return err }, map[string]bool{
			`{"title":"Valid","summary":"","body_markdown":""}`:                                                  true,
			`{"title":"Valid","summary":"","body_markdown":"","slug":null,"featured_image_id":null}`:             true,
			`{"title":"Valid","summary":"","body_markdown":"","status":null}`:                                    false,
			`{"title":"Valid","summary":"","body_markdown":null}`:                                                false,
			`{"title":"Valid","summary":"","body_markdown":"","tags":null}`:                                      false,
			`{"title":"Valid","summary":"","body_markdown":"","reported":{"verified":true}}`:                     false,
			`{"title":"Valid","summary":"","body_markdown":"","references":[{"ref":"page:1","role":"unknown"}]}`: false,
			`{"title":"Valid"}`: false,
		}},
		{"update", update, draft.UpdateSchema, func(raw []byte) error { _, err := draft.DecodeChanges(raw, "en"); return err }, map[string]bool{
			`{"title":"Valid"}`: true,
			`{"meta_title":null,"category_id":null,"tags":[]}`: true,
			`{}`:                     false,
			`{"title":null}`:         false,
			`{"body_markdown":null}`: false,
			`{"tags":null}`:          false,
			`{"language_code":"ru"}`: false,
			`{"page_type":"page"}`:   false,
			`{"author_id":2}`:        false,
			`{"scheduled_at":null}`:  false,
			`{"reported":{"model":"claimed","verified":true}}`: false,
		}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			engine, err := mode.engine()
			if err != nil {
				t.Fatal(err)
			}
			engineValidator, err := engine.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			mcpValidator, err := mode.mcp.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			for raw, want := range mode.cases {
				var value any
				if err := json.Unmarshal([]byte(raw), &value); err != nil {
					t.Fatal(err)
				}
				if (engineValidator.Validate(value) == nil) != want || (mcpValidator.Validate(value) == nil) != want || (mode.decode([]byte(raw)) == nil) != want {
					t.Errorf("schema/decoder drift for %s, want accepted=%v", raw, want)
				}
			}
		})
	}
}
