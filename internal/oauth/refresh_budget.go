// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

const refreshBudgetWindow = time.Hour

func refreshBudgetLimits(accessTTL time.Duration) (grant, account int64) {
	// Allow proactive renewal at half the access lifetime even at the
	// shortest supported TTL. The ordinary one-hour TTL has burst room.
	grant = max(8, int64((2*refreshBudgetWindow+accessTTL-1)/accessTTL))
	return grant, 10 * grant
}

// checkRefreshBudget reads committed rotations inside the same immediate
// transaction that consumes the current refresh token. Retention keeps
// retired evidence much longer than this window.
func (t *tokenStore) checkRefreshBudget(ctx context.Context, now int64) error {
	cutoff := now - int64(refreshBudgetWindow/time.Second)
	grantLimit, accountLimit := refreshBudgetLimits(t.s.options.AccessTTL)
	grantUsedAt, err := t.q.GetOAuthGrantRefreshBudget(ctx, store.GetOAuthGrantRefreshBudgetParams{
		GrantID: t.grant.ID, Cutoff: cutoff, BudgetOffset: grantLimit - 1,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return storageProblem(ctx, "failed to check grant refresh budget", err)
	}
	accountUsedAt, err := t.q.GetOAuthAccountRefreshBudget(ctx, store.GetOAuthAccountRefreshBudgetParams{
		UserID: t.grant.UserID, Cutoff: cutoff, BudgetOffset: accountLimit - 1,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return storageProblem(ctx, "failed to check account refresh budget", err)
	}
	if usedAt := max(grantUsedAt, accountUsedAt); usedAt != 0 {
		p := problem("temporarily_unavailable")
		p.Description = "Refresh limit reached. Retry with the same refresh token later."
		// The persisted timestamps have second precision. Keep the cutoff
		// second so a full elapsed hour passes even for a late rotation.
		p.retryAfterSeconds = usedAt - cutoff + 1
		return p
	}
	return nil
}
