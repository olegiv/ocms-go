// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// audit runs after the state transaction commits. Never include credentials,
// authorization query strings, client state or unpublished content here.
func (s *Service) audit(ctx context.Context, g store.OauthGrant, message string, warning bool) {
	level := model.EventLevelInfo
	if warning {
		level = model.EventLevelWarning
	}
	_ = service.NewEventService(s.db).LogAuthEvent(ctx, level, message, &g.UserID, "", "", map[string]any{
		"grant_id": g.ID, "client_id": g.ClientID, "scopes": g.Scopes,
	})
}
