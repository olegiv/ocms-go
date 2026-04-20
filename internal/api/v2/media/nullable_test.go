// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package media_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/olegiv/ocms-go/internal/api/v2/media"
)

// TestNullableInt64ThreeStateDecode asserts the sentinel distinguishes
// absent / null / value. This is the contract the media.Update path relies on
// to treat JSON `"folder_id": null` as "move to root" instead of "unchanged".
// Catches the regression Codex flagged where the plain *int64 form silently
// collapsed absent and null into the same nil pointer.
func TestNullableInt64ThreeStateDecode(t *testing.T) {
	type payload struct {
		FolderID media.NullableInt64 `json:"folder_id,omitempty"`
	}

	// Absent from JSON: zero-value struct; IsSet=false.
	var p1 payload
	if err := json.Unmarshal([]byte(`{}`), &p1); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if p1.FolderID.IsSet {
		t.Errorf("absent field: want IsSet=false, got IsSet=true")
	}

	// Explicit JSON null: IsSet=true, IsNull=true.
	var p2 payload
	if err := json.Unmarshal([]byte(`{"folder_id":null}`), &p2); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if !p2.FolderID.IsSet {
		t.Errorf("null field: want IsSet=true, got IsSet=false")
	}
	if !p2.FolderID.IsNull {
		t.Errorf("null field: want IsNull=true, got IsNull=false")
	}

	// Numeric value: IsSet=true, IsNull=false, Value=42.
	var p3 payload
	if err := json.Unmarshal([]byte(`{"folder_id":42}`), &p3); err != nil {
		t.Fatalf("unmarshal int: %v", err)
	}
	if !p3.FolderID.IsSet {
		t.Errorf("int field: want IsSet=true, got IsSet=false")
	}
	if p3.FolderID.IsNull {
		t.Errorf("int field: want IsNull=false, got IsNull=true")
	}
	if p3.FolderID.Value != 42 {
		t.Errorf("int field: want Value=42, got %d", p3.FolderID.Value)
	}

	// Zero value explicitly: IsSet=true, IsNull=false, Value=0.
	var p4 payload
	if err := json.Unmarshal([]byte(`{"folder_id":0}`), &p4); err != nil {
		t.Fatalf("unmarshal 0: %v", err)
	}
	if !p4.FolderID.IsSet || p4.FolderID.IsNull || p4.FolderID.Value != 0 {
		t.Errorf("zero int: want IsSet=true IsNull=false Value=0, got %+v", p4.FolderID)
	}

	_ = context.Background() // silences unused import from future expansion
}
