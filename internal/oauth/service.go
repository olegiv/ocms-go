// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package oauth implements oCMS-owned MCP authorization using the go-oauth2 grant engine.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	lib "github.com/go-oauth2/oauth2/v4"
	liberrors "github.com/go-oauth2/oauth2/v4/errors"
	"github.com/go-oauth2/oauth2/v4/manage"
	"github.com/go-oauth2/oauth2/v4/server"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

const (
	// AccessPrefix separates OAuth credentials from the existing base64url API keys.
	AccessPrefix  = "ocms.oauth.at."
	refreshPrefix = "ocms.oauth.rt."
	codePrefix    = "ocms.oauth.code."
	// MetadataPath is the public protected-resource discovery URL for /api/mcp.
	MetadataPath = "/.well-known/oauth-protected-resource/api/mcp"
)

// Options describes the configured issuer, independent of request Host headers.
type Options struct {
	Issuer      string
	Development bool
	// AllowedHosts are the exact hosts that may serve client metadata
	// documents. nil selects DefaultAllowedHosts; an empty, non-nil slice
	// allows no live fetch at all, leaving pre-registered clients and
	// (when enabled) dynamic registration. Every entry must be a bare
	// lowercase DNS hostname, or New refuses to start.
	AllowedHosts []string
	// PreRegisteredClients contains owner-reviewed public CIMD documents as a
	// JSON array. Empty keeps live metadata discovery as the only CIMD source.
	PreRegisteredClients string
	AllowDCR             bool
	AccessTTL            time.Duration
	RefreshTTL           time.Duration
}

// Principal is the currently authorized account and grant, never agent-supplied identity.
type Principal struct {
	TokenID   string
	UserID    int64
	GrantID   string
	ClientID  string
	Scopes    []string
	ExpiresAt time.Time
}

// Service owns OAuth persistence and protocol integration.
type Service struct {
	db      *sql.DB
	q       *store.Queries
	options Options
	clients *clientResolver
	now     func() time.Time
}

// New constructs an issuer. HTTP is permitted only for literal development loopback origins.
func New(db *sql.DB, options Options) (*Service, error) {
	u, err := url.Parse(options.Issuer)
	developmentHTTP := err == nil && u != nil && options.Development && u.Scheme == "http" && loopbackHost(u.Hostname())
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" ||
		(u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !developmentHTTP) {
		return nil, fmt.Errorf("OAuth issuer must be a configured HTTPS origin (development permits loopback HTTP)")
	}
	options.Issuer = u.Scheme + "://" + u.Host
	if options.AccessTTL == 0 {
		options.AccessTTL = time.Hour
	}
	if options.RefreshTTL == 0 {
		options.RefreshTTL = 30 * 24 * time.Hour
	}
	if options.AccessTTL < time.Minute || options.AccessTTL > 24*time.Hour ||
		options.RefreshTTL < 24*time.Hour || options.RefreshTTL > 90*24*time.Hour {
		return nil, fmt.Errorf("OAuth token lifetimes are outside supported bounds")
	}
	if options.AllowedHosts == nil {
		options.AllowedHosts = DefaultAllowedHosts()
	}
	if err := validateAllowedHosts(options.AllowedHosts); err != nil {
		return nil, err
	}
	s := &Service{db: db, q: store.New(db), options: options, now: time.Now}
	s.clients = newClientResolver(s)
	if err := s.clients.loadPreRegistered(options.PreRegisteredClients); err != nil {
		return nil, err
	}
	return s, nil
}

// DefaultAllowedHosts returns the client metadata hosts trusted when none are
// configured: the hosts of the verified Claude and ChatGPT clients.
func DefaultAllowedHosts() []string { return []string{"claude.ai", "chatgpt.com"} }

// SplitAllowedHosts reads a comma or space separated host list, lowercasing
// each entry. It returns a non-nil slice even for empty input: an explicitly
// empty setting means "no hosts", not "use the defaults" (see
// Options.AllowedHosts). Entries are validated by New.
func SplitAllowedHosts(raw string) []string {
	hosts := strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	if hosts == nil {
		hosts = []string{}
	}
	return hosts
}

// validateAllowedHosts rejects an allowlist entry that is not a bare
// lowercase DNS hostname. An entry with a scheme, port, path or wildcard
// never matches a request host, so it would silently allow less than the
// operator meant, and an IP literal is never a client metadata host
// (security audit 2026-11-03, A-4).
func validateAllowedHosts(hosts []string) error {
	for _, host := range hosts {
		if !validHostname(host) {
			return fmt.Errorf("OAuth client metadata host %q is not a bare lowercase DNS hostname (no scheme, port, path, wildcard or IP address)", host)
		}
	}
	return nil
}

// validHostname reports whether host is a lowercase DNS name of at least two
// labels, each of 1-63 letters, digits or inner hyphens, in at most 253
// characters, and not an IP address.
func validHostname(host string) bool {
	if host == "" || len(host) > 253 || net.ParseIP(host) != nil {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	// A purely numeric last label would make this an IPv4 literal in a
	// short form such as "127.1".
	last := labels[len(labels)-1]
	return strings.Trim(last, "0123456789") != ""
}

// Issuer returns the canonical configured issuer.
func (s *Service) Issuer() string { return s.options.Issuer }

// Resource returns the sole audience accepted by this authorization server.
func (s *Service) Resource() string { return s.Issuer() + "/api/mcp" }

// RoleScopes maps current application roles to the narrow OAuth permission vocabulary.
func RoleScopes(role string) []string {
	if role != model.RoleAdmin && role != model.RoleEditor {
		return nil
	}
	return []string{model.PermissionMCPAccess, model.PermissionMCPDrafts, model.PermissionPagesRead}
}

// IntersectScopes retains only scopes the current role still permits.
func IntersectScopes(scopes []string, role string) []string {
	allowed := RoleScopes(role)
	return slices.DeleteFunc(slices.Clone(scopes), func(scope string) bool { return !slices.Contains(allowed, scope) })
}

// Problem is a protocol-safe error; descriptions never include submitted credentials.
type Problem struct{ Code, Description string }

func (p *Problem) Error() string { return p.Code }

func problem(code string) *Problem {
	return &Problem{Code: code, Description: "The authorization request could not be completed."}
}

func randomCredential(prefix string) string {
	return prefix + base64.RawURLEncoding.EncodeToString(randBytes())
}

func randBytes() []byte {
	b := make([]byte, 32)
	// crypto/rand.Read never returns an error on supported Go runtimes.
	_, _ = rand.Read(b)
	return b
}

func tokenHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func validCredential(raw, prefix string) bool {
	if !strings.HasPrefix(raw, prefix) {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, prefix))
	return err == nil && len(b) == 32 && len(raw) == len(prefix)+43
}

// RecognizesAccessToken prevents failed OAuth authentication from falling through to API keys.
func RecognizesAccessToken(raw string) bool { return strings.HasPrefix(raw, "ocms.oauth.") }

// activeGrant reports a missing or unusable grant as invalid_grant and a
// storage fault as temporarily_unavailable, so callers can keep the two apart.
func (s *Service) activeGrant(ctx context.Context, q *store.Queries, id string) (store.OauthGrant, error) {
	g, err := q.GetOAuthGrant(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return store.OauthGrant{}, storageProblem(ctx, "failed to load OAuth grant", err, "grant_id", id)
	}
	if err != nil || g.RevokedAt != 0 || g.ExpiresAt <= s.now().Unix() || g.Resource != s.Resource() {
		return store.OauthGrant{}, problem("invalid_grant")
	}
	u, err := q.GetUserByID(ctx, g.UserID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return store.OauthGrant{}, storageProblem(ctx, "failed to load OAuth grant account", err, "grant_id", id)
	}
	if err != nil || u.SessionVersion != g.SessionVersion || len(RoleScopes(u.Role)) == 0 {
		return store.OauthGrant{}, problem("invalid_grant")
	}
	if len(IntersectScopes(strings.Fields(g.Scopes), u.Role)) != len(strings.Fields(g.Scopes)) {
		return store.OauthGrant{}, problem("invalid_grant")
	}
	return g, nil
}

// VerifyAccess resolves an opaque token on every request, including current grant/account state.
func (s *Service) VerifyAccess(ctx context.Context, raw string) (Principal, error) {
	if !validCredential(raw, AccessPrefix) {
		return Principal{}, problem("invalid_token")
	}
	t, err := s.q.GetOAuthTokenByAccess(ctx, tokenHash(raw))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Principal{}, storageProblem(ctx, "failed to load OAuth access token", err)
	}
	if err != nil || t.AccessRevokedAt != 0 || t.AccessExpiresAt <= s.now().Unix() {
		return Principal{}, problem("invalid_token")
	}
	g, err := s.activeGrant(ctx, s.q, t.GrantID)
	if err != nil {
		return Principal{}, retryableOr(err, "invalid_token")
	}
	// Last-used metadata follows access validation as well as token exchange.
	// Throttle durable touches so ordinary reads do not all acquire a writer.
	// The touch is bookkeeping only: failing it must not reject a valid token.
	now := s.now().Unix()
	if g.LastUsedAt < now-60 {
		if err := s.q.TouchOAuthGrantIfDue(ctx, store.TouchOAuthGrantIfDueParams{ID: g.ID, LastUsedAt: now, BeforeTime: now - 60}); err != nil {
			slog.WarnContext(ctx, "failed to record OAuth grant use", "error", err, "grant_id", g.ID)
		}
	}
	return Principal{TokenID: t.ID, UserID: g.UserID, GrantID: g.ID, ClientID: g.ClientID, Scopes: intersectGranted(t.Scopes, g.Scopes),
		ExpiresAt: time.Unix(t.AccessExpiresAt, 0)}, nil
}

// RecheckAccess repeats token/grant/account checks on transaction-bound queries.
// The token ID comes from VerifyAccess, never from a content request.
func (s *Service) RecheckAccess(ctx context.Context, q *store.Queries, prior Principal) (Principal, error) {
	t, err := q.GetOAuthTokenByID(ctx, prior.TokenID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Principal{}, storageProblem(ctx, "failed to reload OAuth access token", err, "grant_id", prior.GrantID)
	}
	if err != nil || t.GrantID != prior.GrantID || t.AccessRevokedAt != 0 || t.AccessExpiresAt <= s.now().Unix() {
		return Principal{}, problem("invalid_token")
	}
	g, err := s.activeGrant(ctx, q, t.GrantID)
	if err != nil {
		return Principal{}, retryableOr(err, "invalid_token")
	}
	if g.UserID != prior.UserID || g.ClientID != prior.ClientID {
		return Principal{}, problem("invalid_token")
	}
	return Principal{TokenID: t.ID, UserID: g.UserID, GrantID: g.ID, ClientID: g.ClientID, Scopes: intersectGranted(t.Scopes, g.Scopes), ExpiresAt: time.Unix(t.AccessExpiresAt, 0)}, nil
}

// ListGrants returns at most 100 active connections for the signed-in account.
func (s *Service) ListGrants(ctx context.Context, userID int64) ([]store.ListOAuthGrantsForUserRow, error) {
	return s.q.ListOAuthGrantsForUser(ctx, store.ListOAuthGrantsForUserParams{UserID: userID, ExpiresAt: s.now().Unix()})
}

// RevokeGrant revokes only a connection owned by the signed-in account.
func (s *Service) RevokeGrant(ctx context.Context, userID int64, id string) error {
	n, err := s.q.RevokeOwnedOAuthGrant(ctx, store.RevokeOwnedOAuthGrantParams{
		RevokedAt: s.now().Unix(), ID: id, UserID: userID})
	if err == nil && n == 1 {
		if g, loadErr := s.q.GetOAuthGrant(ctx, id); loadErr == nil {
			s.audit(ctx, g, "OAuth connection revoked by account", false)
		}
	}
	return err
}

func (s *Service) engine(ts *tokenStore, client registeredClient) (*manage.Manager, *server.Server) {
	m := manage.NewDefaultManager()
	m.SetAuthorizeCodeExp(time.Minute)
	m.SetAuthorizeCodeTokenCfg(&manage.Config{AccessTokenExp: s.options.AccessTTL, RefreshTokenExp: s.options.RefreshTTL, IsGenerateRefresh: true})
	m.SetRefreshTokenCfg(&manage.RefreshingConfig{IsGenerateRefresh: true, IsRemoveRefreshing: true})
	m.MapAuthorizeGenerate(authorizeGenerator{})
	m.MapAccessGenerate(accessGenerator{})
	m.MapTokenStorage(ts)
	m.MapClientStorage(clientStorage{client: client})
	m.SetValidateURIHandler(func(_, redirect string) error {
		if !client.acceptsRedirect(redirect) {
			return liberrors.ErrInvalidRedirectURI
		}
		return nil
	})
	cfg := server.NewConfig()
	cfg.AllowedResponseTypes = []lib.ResponseType{lib.Code}
	cfg.AllowedGrantTypes = []lib.GrantType{lib.AuthorizationCode, lib.Refreshing}
	cfg.AllowedCodeChallengeMethods = []lib.CodeChallengeMethod{lib.CodeChallengeS256}
	cfg.ForcePKCE = true
	srv := server.NewServer(cfg, m)
	srv.SetClientInfoHandler(server.ClientFormHandler)
	return m, srv
}

// authorizeGenerator implements the library's separate code generator interface.
type authorizeGenerator struct{}

func (authorizeGenerator) Token(_ context.Context, _ *lib.GenerateBasic) (string, error) {
	return randomCredential(codePrefix), nil
}

type accessGenerator struct{}

func (accessGenerator) Token(_ context.Context, _ *lib.GenerateBasic, refresh bool) (string, string, error) {
	r := ""
	if refresh {
		r = randomCredential(refreshPrefix)
	}
	return randomCredential(AccessPrefix), r, nil
}

func scopeString(scopes []string) string {
	copyScopes := slices.Clone(scopes)
	slices.Sort(copyScopes)
	return strings.Join(slices.Compact(copyScopes), " ")
}

func intersectGranted(scopes, granted string) []string {
	allowed := strings.Fields(granted)
	return slices.DeleteFunc(strings.Fields(scopes), func(scope string) bool { return !slices.Contains(allowed, scope) })
}

func rollback(tx *sql.Tx) { _ = tx.Rollback() }

// protocolProblem passes protocol decisions through. Anything else is a
// server fault: reporting it as invalid_grant would make clients discard
// credentials that are still valid.
func protocolProblem(err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return problem("server_error")
}

// failure logs a server-side fault and returns the protocol error the client sees.
// Callers never pass credentials, client state or request values in attrs.
func failure(ctx context.Context, code, msg string, err error, attrs ...any) *Problem {
	slog.ErrorContext(ctx, msg, append([]any{"error", err}, attrs...)...)
	return problem(code)
}

// logFault logs err unless it is already a protocol decision.
func logFault(ctx context.Context, msg string, err error, attrs ...any) {
	var p *Problem
	if !errors.As(err, &p) {
		slog.ErrorContext(ctx, msg, append([]any{"error", err}, attrs...)...)
	}
}

// storageProblem keeps protocol decisions and turns every other error into a
// logged, retryable temporarily_unavailable.
func storageProblem(ctx context.Context, msg string, err error, attrs ...any) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return failure(ctx, "temporarily_unavailable", msg, err, attrs...)
}

// retryableOr keeps a retryable fault and reports every other failure as code.
func retryableOr(err error, code string) *Problem {
	if Retryable(err) {
		return protocolProblem(err)
	}
	return problem(code)
}

// Retryable reports a server-side fault after which the presented credential
// may still be valid, so the caller must not answer invalid_token.
func Retryable(err error) bool {
	var p *Problem
	return errors.As(err, &p) && (p.Code == "temporarily_unavailable" || p.Code == "server_error")
}
