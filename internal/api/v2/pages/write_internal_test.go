// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pages

import (
	"errors"
	"fmt"
	"testing"

	"github.com/olegiv/ocms-go/internal/api/v2"
)

type databaseFailure int

func (e databaseFailure) Error() string { return "fixture database error" }
func (e databaseFailure) Code() int     { return int(e) }

func TestBusyWriteIsRetryableAndPreservesItsCause(t *testing.T) {
	for _, code := range []int{5, 6, 261} {
		cause := databaseFailure(code)
		err := writeError(fmt.Errorf("transaction: %w", cause))
		var domain *v2.Error
		if !errors.As(err, &domain) || domain.Code() != "write_retryable" || domain.Kind != v2.ErrConflict || !errors.Is(err, cause) {
			t.Fatalf("busy error lost retry semantics or cause: %v", err)
		}
	}
	other := databaseFailure(19)
	if got := writeError(other); !errors.Is(got, other) {
		t.Fatalf("constraint failure was mislabeled retryable: %v", got)
	}
}
