// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import (
	"context"
	"database/sql"
	"errors"
)

// RequestCancellation identifies a request that ended while an operation ran.
func RequestCancellation(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	// database/sql may roll a cancelled transaction back before the caller
	// sees the context error. Preserve unrelated storage errors.
	if errors.Is(err, sql.ErrTxDone) {
		return ctx.Err()
	}
	return nil
}
