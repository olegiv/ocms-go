// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package preview issues explicit, revocable access to immutable page snapshots.
package preview

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

const DefaultTTL = 24 * time.Hour
const MaxActiveLinks = 10

var ErrUnavailable = errors.New("preview_unavailable")
var ErrLimit = errors.New("preview_limit")

type Service struct {
	Q   *store.Queries
	TTL time.Duration
	Now func() time.Time
}

// Link is returned only from explicit issuance. It is never an idempotency receipt.
type Link struct {
	PageID    int64     `json:"page_id"`
	Revision  int64     `json:"revision"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
	Warning   string    `json:"warning"`
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func hashToken(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

// SessionURL does not grant access. The reader must still have an editor session.
func SessionURL(origin, language string, pageID, revision int64) string {
	prefix := ""
	if language != "" {
		prefix = "/" + url.PathEscape(language)
	}
	return fmt.Sprintf("%s%s/preview/pages/%d/revisions/%d", strings.TrimRight(origin, "/"), prefix, pageID, revision)
}

// Issue rechecks the trusted caller and ownership in the same transaction as the cap and insert.
func (s Service) Issue(ctx context.Context, principal provenance.Principal, pageID, revision int64, origin string, authorize func(context.Context, *store.Queries) error) (Link, error) {
	base, err := url.Parse(origin)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return Link{}, ErrUnavailable
	}
	if base.Scheme == "http" && base.Hostname() != "localhost" {
		ip := net.ParseIP(base.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return Link{}, ErrUnavailable
		}
	}
	ttl := s.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if ttl <= 0 || ttl > DefaultTTL || principal.UserID <= 0 || pageID <= 0 || revision <= 0 {
		return Link{}, ErrUnavailable
	}
	now := s.now()
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return Link{}, err
	}
	raw := base64.RawURLEncoding.EncodeToString(secret)
	var result Link
	_, err = pagewrite.InQueriesTx(ctx, s.Q, principal, func(work *pagewrite.Tx) error {
		if err := pagewrite.RequireEditor(ctx, work.Q, principal.UserID); err != nil {
			return ErrUnavailable
		}
		if authorize != nil {
			if err := authorize(ctx, work.Q); err != nil {
				return err
			}
		}
		user, err := work.Q.GetUserByID(ctx, principal.UserID)
		if err != nil {
			return err
		}
		page, err := work.Q.GetPageByID(ctx, pageID)
		if err != nil {
			return ErrUnavailable
		}
		access, err := work.Q.GetPageAIAccess(ctx, pageID)
		if err != nil || !access.AiEditable || page.AuthorID != principal.UserID || page.Status != "draft" || page.ScheduledAt.Valid {
			return ErrUnavailable
		}
		if _, err = Load(ctx, work.Q, pageID, revision); err != nil {
			return err
		}
		count, err := work.Q.CountActivePreviewLinks(ctx, store.CountActivePreviewLinksParams{CreatedBy: user.ID, ExpiresAt: now.Unix()})
		if err != nil {
			return err
		}
		issued, err := work.Q.CountIssuedPreviewLinks(ctx, store.CountIssuedPreviewLinksParams{CreatedBy: user.ID, CreatedAt: now.Add(-time.Hour).Unix()})
		if err != nil {
			return err
		}
		if issued >= 60 {
			return ErrLimit
		}
		if count >= MaxActiveLinks {
			return ErrLimit
		}
		expires := now.Add(ttl)
		if principal.OAuthGrantID != "" {
			grant, e := work.Q.GetOAuthGrant(ctx, principal.OAuthGrantID)
			if e != nil {
				return ErrUnavailable
			}
			if until := time.Unix(grant.ExpiresAt, 0); until.Before(expires) {
				expires = until
			}
		}
		if principal.APIKeyID > 0 {
			key, e := work.Q.GetAPIKeyByID(ctx, principal.APIKeyID)
			if e != nil {
				return ErrUnavailable
			}
			if key.ExpiresAt.Valid && key.ExpiresAt.Time.Before(expires) {
				expires = key.ExpiresAt.Time
			}
		}
		created, err := work.Q.CreatePreviewLink(ctx, store.CreatePreviewLinkParams{TokenHash: hashToken(raw), PageID: pageID, Revision: revision, CreatedBy: user.ID, SessionVersion: user.SessionVersion,
			IssuingGrantID: sql.NullString{String: principal.OAuthGrantID, Valid: principal.OAuthGrantID != ""}, IssuingKeyID: sql.NullInt64{Int64: principal.APIKeyID, Valid: principal.APIKeyID > 0}, CreatedAt: now.Unix(), ExpiresAt: expires.Unix()})
		if err != nil {
			return err
		}
		// Validate the stored grant/key binding as well as the adapter's checks.
		if _, err = work.Q.GetPreviewLinkByHash(ctx, store.GetPreviewLinkByHashParams{TokenHash: created.TokenHash, ExpiresAt: now.Unix()}); err != nil {
			return ErrUnavailable
		}
		result = Link{PageID: pageID, Revision: revision, URL: strings.TrimRight(origin, "/") + "/preview/share/" + raw, ExpiresAt: time.Unix(created.ExpiresAt, 0).UTC(), Warning: "Anyone holding this link can read this revision until it expires or is revoked."}
		return nil
	})
	return result, err
}

// Resolve never returns a token or its URL and always checks current lifecycle/issuer state.
func (s Service) Resolve(ctx context.Context, raw string) (Snapshot, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return Snapshot{}, ErrUnavailable
	}
	link, err := s.Q.GetPreviewLinkByHash(ctx, store.GetPreviewLinkByHashParams{TokenHash: hashToken(raw), ExpiresAt: s.now().Unix()})
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	return Load(ctx, s.Q, link.PageID, link.Revision)
}

type requestKey struct{}

func Context(ctx context.Context) context.Context { return context.WithValue(ctx, requestKey{}, true) }
func IsRequest(ctx context.Context) bool          { yes, _ := ctx.Value(requestKey{}).(bool); return yes }
