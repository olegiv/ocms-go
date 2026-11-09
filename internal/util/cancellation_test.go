// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

func TestRequestCancellationPreservesOtherFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := RequestCancellation(ctx, errors.New("storage failure")); got != nil {
		t.Fatalf("storage failure classified as %v", got)
	}
	if got := RequestCancellation(ctx, sql.ErrTxDone); !errors.Is(got, context.Canceled) {
		t.Fatalf("cancelled transaction classified as %v", got)
	}
	if got := RequestCancellation(t.Context(), fmt.Errorf("query: %w", context.DeadlineExceeded)); !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("wrapped deadline classified as %v", got)
	}
}
