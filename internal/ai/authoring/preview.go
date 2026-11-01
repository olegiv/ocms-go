// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring

import (
	"context"
	"errors"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

func (s *Service) CreatePreviewLink(ctx context.Context, a v2.Actor, in PreviewInput) (preview.Link, error) {
	principal := provenance.Principal{UserID: a.ActingUserID(), Source: "oauth", Transport: "mcp", OAuthGrantID: a.OAuthGrantID, OAuthClientID: a.OAuthClientID}
	if a.APIKey != nil {
		principal.Source = "api"
		principal.APIKeyID = a.APIKey.ID
	}
	result, err := (preview.Service{Q: s.Q, TTL: s.PreviewTTL, Now: s.Now}).Issue(ctx, principal, in.PageID, in.Revision, s.SiteURL, func(ctx context.Context, q *store.Queries) error {
		if err := s.authorize(ctx, q, a); err != nil {
			return err
		}
		if s.CheckPolicy != nil {
			return s.CheckPolicy(ctx, q)
		}
		return nil
	})
	if errors.Is(err, preview.ErrUnavailable) {
		return preview.Link{}, &Error{Code: "preview_unavailable", Message: "The requested draft revision is unavailable for sharing"}
	}
	if errors.Is(err, preview.ErrLimit) {
		return preview.Link{}, &Error{Code: "quota_exceeded", Message: "Revoke an existing preview link before creating another"}
	}
	return result, err
}
