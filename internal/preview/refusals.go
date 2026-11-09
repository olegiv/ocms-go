// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview

import (
	"errors"
	"fmt"
)

var ErrSiteURL = errors.New("preview_site_url_required")
var ErrActiveLimit = fmt.Errorf("preview_active_limit: %w", ErrLimit)

// RateLimitError identifies the rolling issuance cap, including revoked links.
// Both limit kinds still match ErrLimit for callers that only need the category.
type RateLimitError struct {
	RetryAfterSeconds int64
}

func (e *RateLimitError) Error() string { return "preview_rate_limited" }
func (e *RateLimitError) Unwrap() error { return ErrLimit }
