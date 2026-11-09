// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"context"
	"database/sql"
	"errors"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/store"
)

// RequireEditor checks that the account acting for a REST write still holds
// the editor or admin role, as the admin UI requires for the same change. A
// key whose creator was demoted gets 403 and one whose creator was deleted
// gets 401, never a server error.
func RequireEditor(ctx context.Context, q *store.Queries, a Actor) error {
	if a.ActingUserID() <= 0 {
		return NewError(ErrUnauthorized, "Authenticated user required")
	}
	return EditorError(pagewrite.RequireEditor(ctx, q, a.ActingUserID()), "Failed to check account role")
}

// EditorError maps an error from a write guarded by pagewrite.RequireEditor:
// a missing account is 401, a missing role 403, an *Error passes through and
// anything else becomes an internal error with msg. nil stays nil.
func EditorError(err error, msg string) error {
	var apiErr *Error
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pagewrite.ErrEditorRequired) && errors.Is(err, sql.ErrNoRows):
		return NewError(ErrUnauthorized, "Account unavailable")
	case errors.Is(err, pagewrite.ErrEditorRequired):
		return NewError(ErrForbidden, "Editor role required")
	case errors.As(err, &apiErr):
		return apiErr
	default:
		return NewInternalError(msg, err)
	}
}
