// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package connectedapps manages credential metadata and revocation without
// exposing secrets or granting new access.
package connectedapps

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

const (
	PageSize = 25
	maxPage  = 1_000_000
)

var (
	ErrForbidden = errors.New("connected apps access denied")
	ErrNotFound  = errors.New("connection not found")
	ErrInvalid   = errors.New("invalid connection request")
)

// Actor comes from the authenticated session, never a submitted form.
type Actor struct {
	UserID, SessionVersion int64
}

type Pagination struct {
	OAuthPage, KeyPage int64
}

// Connection contains display metadata only. IDs are revocation selectors,
// not credentials. A registered name/host does not verify a vendor's identity.
type Connection struct {
	ID, Kind, Name, ClientID, Host, Scopes, Status string
	OwnerID                                        int64
	OwnerName                                      string
	CreatedAt, LastUsedAt, ExpiresAt               time.Time
}

type Page struct {
	OAuth, APIKeys      []Connection
	OAuthMore, KeysMore bool
	Pagination          Pagination
}

type Service struct {
	db *sql.DB
}

func New(db *sql.DB) *Service { return &Service{db: db} }

// ParsePagination gives each credential class its own bounded page query.
func ParsePagination(values url.Values) (Pagination, error) {
	p := Pagination{OAuthPage: 1, KeyPage: 1}
	for key, target := range map[string]*int64{"oauth_page": &p.OAuthPage, "key_page": &p.KeyPage} {
		v, ok := values[key]
		if !ok {
			continue
		}
		if len(v) != 1 {
			return Pagination{}, ErrInvalid
		}
		n, err := strconv.ParseInt(v[0], 10, 64)
		if err != nil || n < 1 || n > maxPage {
			return Pagination{}, ErrInvalid
		}
		*target = n
	}
	return p, nil
}

func checkActor(ctx context.Context, q *store.Queries, actor Actor, allUsers bool) error {
	u, err := q.GetUserByID(ctx, actor.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if u.SessionVersion != actor.SessionVersion ||
		(u.Role != model.RoleAdmin && u.Role != model.RoleEditor && u.Role != model.RolePublic) ||
		(allUsers && u.Role != model.RoleAdmin) {
		return ErrForbidden
	}
	return nil
}

func allFlag(allUsers bool) int64 {
	if allUsers {
		return 1
	}
	return 0
}

// List reads one consistent snapshot with a hard row limit per class.
func (s *Service) List(ctx context.Context, actor Actor, allUsers bool, pages Pagination, oauthEnabled bool) (Page, error) {
	if pages.OAuthPage < 1 || pages.OAuthPage > maxPage || pages.KeyPage < 1 || pages.KeyPage > maxPage {
		return Page{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := store.New(tx)
	if err := checkActor(ctx, q, actor, allUsers); err != nil {
		return Page{}, err
	}
	grants, err := q.ListConnectedOAuthGrants(ctx, store.ListConnectedOAuthGrantsParams{
		AllUsers: allFlag(allUsers), UserID: actor.UserID, PageLimit: PageSize + 1, PageOffset: (pages.OAuthPage - 1) * PageSize,
	})
	if err != nil {
		return Page{}, fmt.Errorf("list OAuth connections: %w", err)
	}
	keys, err := q.ListConnectedAPIKeys(ctx, store.ListConnectedAPIKeysParams{
		AllUsers: allFlag(allUsers), UserID: actor.UserID, PageLimit: PageSize + 1, PageOffset: (pages.KeyPage - 1) * PageSize,
	})
	if err != nil {
		return Page{}, fmt.Errorf("list API key connections: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Page{}, err
	}
	page := Page{Pagination: pages, OAuthMore: len(grants) > PageSize && pages.OAuthPage < maxPage, KeysMore: len(keys) > PageSize && pages.KeyPage < maxPage}
	now := time.Now()
	for _, g := range grants[:min(len(grants), PageSize)] {
		host := ""
		if u, err := url.Parse(g.ClientID); err == nil && u.Scheme == "https" && u.User == nil {
			host = u.Hostname()
		}
		status := "active"
		switch {
		case g.RevokedAt != 0:
			status = "revoked"
		case g.ExpiresAt <= now.Unix():
			status = "expired"
		case !oauthEnabled || !mcpRole(g.OwnerRole) || g.SessionVersion != g.OwnerSessionVersion || !hasScope(g.Scopes, model.PermissionMCPAccess):
			status = "unavailable"
		}
		page.OAuth = append(page.OAuth, Connection{ID: g.ID, Kind: "oauth", Name: g.ClientName,
			ClientID: g.ClientID, Host: host, Scopes: g.Scopes, Status: status,
			OwnerID: g.UserID, OwnerName: g.OwnerName, CreatedAt: unixTime(g.CreatedAt),
			LastUsedAt: unixTime(g.LastUsedAt), ExpiresAt: unixTime(g.ExpiresAt)})
	}
	for _, k := range keys[:min(len(keys), PageSize)] {
		var permissions []string
		if err := json.Unmarshal([]byte(k.Permissions), &permissions); err != nil {
			return Page{}, fmt.Errorf("invalid stored key permissions: %w", err)
		}
		status := "active"
		switch {
		case !k.IsActive:
			status = "revoked"
		case k.ExpiresAt.Valid && !k.ExpiresAt.Time.After(now):
			status = "expired"
		case !mcpRole(k.OwnerRole):
			status = "unavailable"
		}
		page.APIKeys = append(page.APIKeys, Connection{ID: strconv.FormatInt(k.ID, 10), Kind: "api_key",
			Name: k.Name, Scopes: strings.Join(permissions, " "), Status: status,
			OwnerID: k.CreatedBy, OwnerName: k.OwnerName, CreatedAt: k.CreatedAt,
			LastUsedAt: k.LastUsedAt.Time, ExpiresAt: k.ExpiresAt.Time})
	}
	return page, nil
}

func unixTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0)
}

func mcpRole(role string) bool { return role == model.RoleAdmin || role == model.RoleEditor }

func hasScope(scopes, wanted string) bool {
	for _, scope := range strings.Fields(scopes) {
		if scope == wanted {
			return true
		}
	}
	return false
}

// Revoke rechecks session, role and ownership in the write transaction.
// It only removes access; repeated revocation preserves the first timestamp.
func (s *Service) Revoke(ctx context.Context, actor Actor, allUsers bool, kind, id string) error {
	if id == "" || len(id) > 128 || (kind != "oauth" && kind != "api_key") {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := store.New(tx)
	if err := checkActor(ctx, q, actor, allUsers); err != nil {
		return err
	}
	var affected int64
	if kind == "oauth" {
		affected, err = q.RevokeConnectedOAuthGrant(ctx, store.RevokeConnectedOAuthGrantParams{
			ID: id, UserID: actor.UserID, AllUsers: allFlag(allUsers), RevokedAt: time.Now().Unix(),
		})
	} else {
		keyID, parseErr := strconv.ParseInt(id, 10, 64)
		if parseErr != nil || keyID <= 0 {
			return ErrInvalid
		}
		affected, err = q.RevokeConnectedAPIKey(ctx, store.RevokeConnectedAPIKeyParams{
			ID: keyID, UserID: actor.UserID, AllUsers: allFlag(allUsers), UpdatedAt: time.Now(),
		})
	}
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}
