// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"fmt"
	"testing"

	"github.com/olegiv/ocms-go/internal/ai/authoring"
)

// The per-user draft limit reaches the agent as its own machine code, not as a
// retryable fault: waiting does not free a slot, only a human action does.
func TestDraftLimitErrorKeepsItsCodeAndIsNotRetryable(t *testing.T) {
	err := fmt.Errorf("create draft: %w", &authoring.Error{Code: "draft_limit_reached", Message: "You already have 50 AI drafts, the maximum."})
	te, internal := toToolError(err)
	if te == nil || internal || te.Code != "draft_limit_reached" || te.Retryable || te.RetryAfterSeconds != 0 || te.Message == "" {
		t.Fatalf("mapped draft limit: %+v internal=%v", te, internal)
	}
}
