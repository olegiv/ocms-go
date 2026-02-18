// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"testing"

	"golang.org/x/time/rate"
)

func TestAllowProxyBudget_NoLimiter(t *testing.T) {
	m := New()

	if !m.allowProxyBudget() {
		t.Fatal("expected request budget to be allowed when limiter is unset")
	}
}

func TestAllowProxyBudget_WithLimiter(t *testing.T) {
	m := New()
	m.globalRateLimiter = rate.NewLimiter(rate.Limit(1), 1)

	if !m.allowProxyBudget() {
		t.Fatal("expected first request to be allowed")
	}
	if m.allowProxyBudget() {
		t.Fatal("expected second immediate request to be blocked by global limiter")
	}
}
