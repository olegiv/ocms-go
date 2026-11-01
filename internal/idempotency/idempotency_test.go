// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package idempotency_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/idempotency"
)

func TestCanonicalInputPreservesNullAndOmission(t *testing.T) {
	id := uuid.NewString()
	first, err := idempotency.New(1, "create", id, json.RawMessage(`{"body":"x","target":2,"request_id":"excluded"}`))
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := idempotency.New(1, "create", id, json.RawMessage(`{"request_id":"different","target":2,"body":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash != reordered.Hash {
		t.Fatal("JSON property order or request_id changed hash")
	}
	for _, input := range []string{`{"body":"x","target":2,"summary":null}`, `{"body":"x","target":3}`, `{"body":"x","target":2,"base_revision":1}`, `{"body":"x","target":2,"reported":{"model":"different"}}`} {
		changed, err := idempotency.New(1, "create", id, json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		if changed.Hash == first.Hash {
			t.Fatalf("distinct typed input reused hash: %s", input)
		}
	}
	for _, bad := range []string{"", "not-a-uuid", "urn:uuid:" + id, "00000000-0000-0000-0000-000000000000"} {
		if _, err := idempotency.New(1, "create", bad, map[string]any{}); err == nil {
			t.Fatalf("accepted request ID %q", bad)
		}
	}
}
