// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package authoring

import (
	"context"
	"errors"
	"fmt"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/store"
)

func (s *Service) CreatePreviewLink(ctx context.Context, a v2.Actor, in PreviewInput) (preview.Link, error) {
	result, err := (preview.Service{Q: s.Q, TTL: s.PreviewTTL, Now: s.Now}).Issue(ctx, a.Principal("mcp"), in.PageID, in.Revision, s.SiteURL, func(ctx context.Context, q *store.Queries) error {
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
	if errors.Is(err, preview.ErrSiteURL) {
		return preview.Link{}, &v2.Error{Kind: v2.ErrInternal, MachineCode: "site_url_required", Msg: "Ask the administrator to configure a valid HTTPS site URL before sharing previews. HTTP is allowed only for localhost or loopback addresses.", Wrap: fmt.Errorf("issue preview for page %d revision %d: %w", in.PageID, in.Revision, err)}
	}
	if errors.Is(err, preview.ErrActiveLimit) {
		return preview.Link{}, &Error{Code: "preview_limit_reached", Message: "You have reached the active preview link limit. Ask the editor to revoke a link, or wait for one to expire."}
	}
	var limited *preview.RateLimitError
	if errors.As(err, &limited) {
		return preview.Link{}, &Error{Code: "preview_rate_limited", Message: "Preview link issuance limit reached; retry after the indicated delay. Revoking links does not reset this limit.", Retryable: true, RetryAfterSeconds: limited.RetryAfterSeconds}
	}
	return result, err
}
