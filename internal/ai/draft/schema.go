// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

type inputSchema struct {
	schema   *jsonschema.Schema
	resolved *jsonschema.Resolved
}

var schemas = sync.OnceValues(func() ([2]inputSchema, error) {
	var result [2]inputSchema
	for index, typ := range []reflect.Type{reflect.TypeFor[PageDraft](), reflect.TypeFor[Changes]()} {
		registry := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
		raw, err := json.Marshal(huma.SchemaFromType(registry, typ))
		if err != nil {
			return result, err
		}
		var schema jsonschema.Schema
		if err = json.Unmarshal(raw, &schema); err != nil {
			return result, err
		}
		schema.Defs = make(map[string]*jsonschema.Schema)
		for name, definition := range registry.Map() {
			raw, err = json.Marshal(definition)
			if err != nil {
				return result, err
			}
			var converted jsonschema.Schema
			if err = json.Unmarshal(raw, &converted); err != nil {
				return result, err
			}
			schema.Defs[name] = &converted
		}
		resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
		if err != nil {
			return result, fmt.Errorf("resolve draft schema: %w", err)
		}
		result[index] = inputSchema{&schema, resolved}
	}
	return result, nil
})

// CreateSchema returns an independent copy generated from PageDraft's Huma tags.
func CreateSchema() (*jsonschema.Schema, error) { return schemaCopy(0) }

// UpdateSchema returns the strict non-empty Changes schema, not the create schema.
func UpdateSchema() (*jsonschema.Schema, error) { return schemaCopy(1) }

func schemaCopy(index int) (*jsonschema.Schema, error) {
	all, err := schemas()
	if err != nil {
		return nil, err
	}
	return all[index].schema.CloneSchemas(), nil
}

// ValidationError carries localized issues without quoting submitted content.
type ValidationError struct{ Issues []Issue }

func (e *ValidationError) Error() string { return "draft validation failed" }

// DecodeCreate enforces the create contract without applying mutable defaults.
func DecodeCreate(raw []byte, language string) (PageDraft, error) {
	type plain PageDraft
	var value plain
	err := decode(raw, 0, language, &value)
	return PageDraft(value), err
}

// DecodeChanges preserves absent/null/empty inputs and rejects forbidden fields.
func DecodeChanges(raw []byte, language string) (Changes, error) {
	type plain Changes
	var value plain
	err := decode(raw, 1, language, &value)
	return Changes(value), err
}

// UnmarshalJSON keeps ordinary json.Unmarshal as strict as the exported decoder.
func (p *PageDraft) UnmarshalJSON(raw []byte) error {
	value, err := DecodeCreate(raw, "en")
	if err == nil {
		*p = value
	}
	return err
}

// UnmarshalJSON refuses unknown update fields, null replacements and empty input.
func (p *Changes) UnmarshalJSON(raw []byte) error {
	value, err := DecodeChanges(raw, "en")
	if err == nil {
		*p = value
	}
	return err
}

func invalid(code, field, language string) error {
	return &ValidationError{Issues: []Issue{NewIssue(code, field, language)}}
}

func decode(raw []byte, index int, language string, target any) error {
	if len(raw) > MaxInputBytes || !utf8.Valid(raw) {
		return invalid("invalid_input", "$", language)
	}
	reader := json.NewDecoder(bytes.NewReader(raw))
	reader.UseNumber()
	if err := inspectJSON(reader, 0); err != nil {
		return invalid("invalid_input", "$", language)
	}
	if _, err := reader.Token(); !errors.Is(err, io.EOF) {
		return invalid("invalid_input", "$", language)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return invalid("invalid_input", "$", language)
	}
	for name := range object {
		if forbidden(name, index == 1) {
			return invalid("forbidden_field", name, language)
		}
	}
	all, err := schemas()
	if err != nil {
		return err
	}
	if err = all[index].resolved.Validate(object); err != nil {
		return invalid("invalid_input", "$", language)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return invalid("invalid_input", "$", language)
	}
	return nil
}

func forbidden(name string, update bool) bool {
	switch name {
	case "status", "scheduled_at", "published_at", "author", "author_id", "user_id", "owner_id", "ai_editable", "ai_access", "verified", "validated", "provenance", "api_key_id", "oauth_grant_id", "oauth_client_id", "revision":
		return true
	case "page_type", "language_code":
		return update
	}
	return false
}

// inspectJSON rejects duplicate keys and excessive nesting before typed decode.
// It never returns submitted values to callers or performs content resolution.
func inspectJSON(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("draft JSON nesting limit exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := make(map[string]bool)
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate or invalid draft field")
			}
			seen[name] = true
		}
		if err = inspectJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
