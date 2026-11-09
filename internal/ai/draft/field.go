// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// Field preserves omission for a non-nullable optional input. Use Set to supply
// a value; an omitted field must not replace existing content during an update.
type Field[T any] struct {
	Present bool `json:"-"`
	Value   T    `json:"-"`
}

// Set supplies a non-nullable value, including an empty string or collection.
func Set[T any](value T) Field[T] { return Field[T]{Present: true, Value: value} }

// IsZero lets encoding/json's omitzero preserve absent fields in retry hashes.
func (f Field[T]) IsZero() bool { return !f.Present }

// MarshalJSON emits the supplied value rather than the presence wrapper.
func (f Field[T]) MarshalJSON() ([]byte, error) { return json.Marshal(f.Value) }

// UnmarshalJSON refuses null even for a Go type such as a slice that accepts it.
func (f *Field[T]) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("this draft field cannot be null")
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*f = Set(value)
	return nil
}

// Schema exposes T to the same Huma schema generator used by MCP adapters.
func (Field[T]) Schema(registry huma.Registry) *huma.Schema {
	schema := *registry.Schema(reflect.TypeFor[T](), true, "")
	schema.Nullable = false
	return &schema
}

// Nullable distinguishes an omitted value from an explicit null that clears it.
type Nullable[T any] struct {
	Present bool `json:"-"`
	Null    bool `json:"-"`
	Value   T    `json:"-"`
}

// SetNullable supplies a nullable field's non-null value.
func SetNullable[T any](value T) Nullable[T] { return Nullable[T]{Present: true, Value: value} }

// Clear requests an explicit null, rather than omitting the field.
func Clear[T any]() Nullable[T] { return Nullable[T]{Present: true, Null: true} }

// IsZero lets absent nullable fields disappear from encoded input.
func (f Nullable[T]) IsZero() bool { return !f.Present }

// MarshalJSON retains explicit nulls for canonical retry hashes.
func (f Nullable[T]) MarshalJSON() ([]byte, error) {
	if f.Null {
		return []byte("null"), nil
	}
	return json.Marshal(f.Value)
}

// UnmarshalJSON records null separately from omission and zero values.
func (f *Nullable[T]) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*f = Clear[T]()
		return nil
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*f = SetNullable(value)
	return nil
}

// Schema allows null only for explicitly nullable properties.
func (Nullable[T]) Schema(registry huma.Registry) *huma.Schema {
	schema := *registry.Schema(reflect.TypeFor[T](), true, "")
	schema.Nullable = true
	return &schema
}
