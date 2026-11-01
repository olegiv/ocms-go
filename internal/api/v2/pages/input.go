// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Preserve explicit nulls for canonical request hashing. Pointer fields alone
// cannot distinguish a JSON null from an absent property.
func (in *CreatePageBody) UnmarshalJSON(data []byte) error {
	type body CreatePageBody
	var parsed body
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*in = CreatePageBody(parsed)
	return json.Unmarshal(data, &in.supplied)
}
func (in *UpdatePageBody) UnmarshalJSON(data []byte) error {
	type body UpdatePageBody
	var parsed body
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*in = UpdatePageBody(parsed)
	return json.Unmarshal(data, &in.supplied)
}

type updateRequest struct {
	PageID  int64          `json:"page_id"`
	Changes UpdatePageBody `json:"changes"`
}

// Hash the typed values and explicit-null field names separately, so later
// typed assignments cannot be hidden behind an earlier JSON null.
func canonicalWriteInput(value any) any {
	type payload struct {
		Values             any      `json:"values"`
		NullFields         []string `json:"explicit_null_fields"`
		ClearFeaturedImage bool     `json:"clear_featured_image,omitempty"`
	}
	switch input := value.(type) {
	case CreatePageBody:
		return payload{Values: input, NullFields: nullFields(input.supplied)}
	case updateRequest:
		return struct {
			PageID  int64   `json:"page_id"`
			Changes payload `json:"changes"`
		}{input.PageID, payload{Values: input.Changes, NullFields: nullFields(input.Changes.supplied), ClearFeaturedImage: input.Changes.ClearFeaturedImage}}
	default:
		return value
	}
}
func nullFields(supplied map[string]json.RawMessage) []string {
	var fields []string
	for key, raw := range supplied {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			fields = append(fields, key)
		}
	}
	sort.Strings(fields)
	return fields
}

func lifecycleSupplied(fields map[string]json.RawMessage) bool {
	for _, key := range []string{"status", "published_at", "scheduled_at"} {
		if _, ok := fields[key]; ok {
			return true
		}
	}
	return false
}
