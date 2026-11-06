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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

const DefaultTTL = 24 * time.Hour
const MaxActiveLinks = 10

// MaxIssuedPerHour bounds link issuance per user over a rolling hour.
const MaxIssuedPerHour = 60

// SharePath never includes a credential. Browsers send its fragment in a POST body.
const SharePath = "/preview/share"

// ShareOpenPath differs from the landing so opening another share is a full
// navigation, rather than a fragment change on an already-rendered preview.
const ShareOpenPath = "/preview/share/open"

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
	URL       ShareURL  `json:"url"`
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
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return Link{}, ErrSiteURL
	}
	if base.Scheme == "http" && base.Hostname() != "localhost" {
		ip := net.ParseIP(base.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return Link{}, ErrSiteURL
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
		if editorErr := pagewrite.RequireEditor(ctx, work.Q, principal.UserID); editorErr != nil {
			if errors.Is(editorErr, pagewrite.ErrEditorRequired) {
				return ErrUnavailable
			}
			return fmt.Errorf("check issuer role: %w", editorErr)
		}
		if authorize != nil {
			if authorizeErr := authorize(ctx, work.Q); authorizeErr != nil {
				return authorizeErr
			}
		}
		user, workErr := work.Q.GetUserByID(ctx, principal.UserID)
		if workErr != nil {
			return workErr
		}
		page, workErr := work.Q.GetPageByID(ctx, pageID)
		if workErr != nil {
			return unavailable(workErr, "load page")
		}
		access, workErr := work.Q.GetPageAIAccess(ctx, pageID)
		if workErr != nil {
			return unavailable(workErr, "load page AI access")
		}
		if !access.AiEditable || page.AuthorID != principal.UserID || page.Status != "draft" || page.ScheduledAt.Valid {
			return ErrUnavailable
		}
		if _, workErr = Load(ctx, work.Q, pageID, revision); workErr != nil {
			return workErr
		}
		count, workErr := work.Q.CountActivePreviewLinks(ctx, store.CountActivePreviewLinksParams{CreatedBy: user.ID, ExpiresAt: now.Unix()})
		if workErr != nil {
			return workErr
		}
		issued, workErr := work.Q.CountIssuedPreviewLinks(ctx, store.CountIssuedPreviewLinksParams{CreatedBy: user.ID, CreatedAt: now.Add(-time.Hour).Unix()})
		if workErr != nil {
			return workErr
		}
		if issued >= MaxIssuedPerHour {
			cutoff, cutoffErr := work.Q.GetPreviewIssuanceCutoff(ctx, store.GetPreviewIssuanceCutoffParams{CreatedBy: user.ID, Since: now.Add(-time.Hour).Unix(), IssuanceOffset: MaxIssuedPerHour - 1})
			if cutoffErr != nil {
				return fmt.Errorf("load preview issuance cutoff: %w", cutoffErr)
			}
			// The count includes the cutoff second. Wait until fewer than
			// MaxIssuedPerHour records remain, even if old data exceeds it.
			return &RateLimitError{RetryAfterSeconds: max(1, cutoff+int64(time.Hour/time.Second)+1-now.Unix())}
		}
		if count >= MaxActiveLinks {
			return ErrActiveLimit
		}
		expires := now.Add(ttl)
		if principal.OAuthGrantID != "" {
			grant, e := work.Q.GetOAuthGrant(ctx, principal.OAuthGrantID)
			if e != nil {
				return unavailable(e, "load issuing grant")
			}
			if until := time.Unix(grant.ExpiresAt, 0); until.Before(expires) {
				expires = until
			}
		}
		if principal.APIKeyID > 0 {
			key, e := work.Q.GetAPIKeyByID(ctx, principal.APIKeyID)
			if e != nil {
				return unavailable(e, "load issuing key")
			}
			if key.ExpiresAt.Valid && key.ExpiresAt.Time.Before(expires) {
				expires = key.ExpiresAt.Time
			}
		}
		created, workErr := work.Q.CreatePreviewLink(ctx, store.CreatePreviewLinkParams{TokenHash: hashToken(raw), PageID: pageID, Revision: revision, CreatedBy: user.ID, SessionVersion: user.SessionVersion,
			IssuingGrantID: sql.NullString{String: principal.OAuthGrantID, Valid: principal.OAuthGrantID != ""}, IssuingKeyID: sql.NullInt64{Int64: principal.APIKeyID, Valid: principal.APIKeyID > 0}, CreatedAt: now.Unix(), ExpiresAt: expires.Unix()})
		if workErr != nil {
			return workErr
		}
		// Validate the stored grant/key binding as well as the adapter's checks.
		if _, workErr = work.Q.GetPreviewLinkByHash(ctx, store.GetPreviewLinkByHashParams{TokenHash: created.TokenHash, ExpiresAt: now.Unix()}); workErr != nil {
			return unavailable(workErr, "check preview link binding")
		}
		// Issuing a bearer link is recorded with the issuance itself: no link
		// exists without its audit event. The token never enters the event.
		metadata, workErr := json.Marshal(map[string]any{"page_id": pageID, "revision": revision, "link_id": created.ID,
			"expires_at": time.Unix(created.ExpiresAt, 0).UTC().Format(time.RFC3339), "transport": principal.Transport,
			"oauth_grant_id": principal.OAuthGrantID, "api_key_id": principal.APIKeyID})
		if workErr != nil {
			return workErr
		}
		if _, workErr = work.Q.CreateEvent(ctx, store.CreateEventParams{Level: model.EventLevelInfo, Category: model.EventCategoryPage,
			Message: "Preview share link created", UserID: sql.NullInt64{Int64: user.ID, Valid: true}, Metadata: string(metadata), CreatedAt: now}); workErr != nil {
			return fmt.Errorf("record preview link: %w", workErr)
		}
		base.Path, base.RawPath, base.Fragment = SharePath, "", raw
		result = Link{PageID: pageID, Revision: revision, URL: ShareURL(base.String()), ExpiresAt: time.Unix(created.ExpiresAt, 0).UTC(), Warning: "Anyone holding this link can read this revision until it expires or is revoked."}
		return nil
	})
	return result, err
}

// unavailable keeps a missing row as ErrUnavailable and returns any other
// lookup error as a fault, so a storage problem is logged and retried rather
// than reported as a draft that cannot be shared (2026-11-03 review S11).
func unavailable(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnavailable
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Resolve never returns a token or its URL and always checks current lifecycle/issuer state.
func (s Service) Resolve(ctx context.Context, raw string) (Snapshot, error) {
	snapshot, _, err := s.ResolveWithIssuer(ctx, raw)
	return snapshot, err
}

// ResolveWithIssuer is Resolve that also returns the ID of the user who issued
// the link. The recorded revision can be older than the current page, so its
// author need not be that user: an anonymous reader may see the issuer's
// content, but the recorded author's profile is not the issuer's to share.
func (s Service) ResolveWithIssuer(ctx context.Context, raw string) (Snapshot, int64, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return Snapshot{}, 0, ErrUnavailable
	}
	link, err := s.Q.GetPreviewLinkByHash(ctx, store.GetPreviewLinkByHashParams{TokenHash: hashToken(raw), ExpiresAt: s.now().Unix()})
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, 0, ErrUnavailable
	}
	if err != nil {
		// A storage fault must not make a valid link look expired.
		return Snapshot{}, 0, fmt.Errorf("load preview link: %w", err)
	}
	snapshot, err := Load(ctx, s.Q, link.PageID, link.Revision)
	if err != nil {
		return Snapshot{}, 0, err
	}
	return snapshot, link.CreatedBy, nil
}

type requestKey struct{}

func Context(ctx context.Context) context.Context { return context.WithValue(ctx, requestKey{}, true) }
func IsRequest(ctx context.Context) bool          { yes, _ := ctx.Value(requestKey{}).(bool); return yes }
