// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages

import (
	"errors"
	"fmt"
	"testing"

	"github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/idempotency"
	"github.com/olegiv/ocms-go/internal/pagewrite"
)

type databaseFailure int

func (e databaseFailure) Error() string { return "fixture database error" }
func (e databaseFailure) Code() int     { return int(e) }

func TestBusyWriteIsRetryableAndPreservesItsCause(t *testing.T) {
	for _, code := range []int{5, 6, 261} {
		cause := databaseFailure(code)
		err := writeError(fmt.Errorf("transaction: %w", cause))
		var domain *v2.Error
		if !errors.As(err, &domain) || domain.Code() != "write_retryable" || domain.Kind != v2.ErrConflict || domain.RetryAfterSeconds() != 1 || !errors.Is(err, cause) {
			t.Fatalf("busy error lost retry semantics or cause: %v", err)
		}
	}
	busy := databaseFailure(5)
	var domain *v2.Error
	if got := writeError(v2.NewInternalError("Failed to create page", busy)); !errors.As(got, &domain) || domain.RetryAfterSeconds() != 1 || !errors.Is(got, busy) {
		t.Fatalf("wrapped busy error lost retry semantics or cause: %v", got)
	}
	other := databaseFailure(19)
	if got := writeError(other); !errors.Is(got, other) {
		t.Fatalf("constraint failure was mislabeled retryable: %v", got)
	}
}

func TestWriteRefusalsTakePrecedenceOverBusyCauses(t *testing.T) {
	busy := databaseFailure(5)
	for _, tc := range []struct {
		code string
		msg  string
		err  error
	}{
		{"forbidden", "Editor role required", &v2.Error{Kind: v2.ErrForbidden, Msg: "Editor role required", Wrap: busy}},
		{"revision_conflict", "revision_conflict", errors.Join(&pagewrite.Conflict{CurrentRevision: 2}, busy)},
		{"idempotency_key_reused", "idempotency_key_reused", errors.Join(idempotency.ErrKeyReused, busy)},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var domain *v2.Error
			if got := writeError(tc.err); !errors.As(got, &domain) || domain.Code() != tc.code || domain.RetryAfterSeconds() != 0 || domain.Msg != tc.msg || !errors.Is(got, busy) {
				t.Fatalf("refusal lost its code, message or cause: %v", got)
			}
		})
	}
}
