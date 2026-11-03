// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	lib "github.com/go-oauth2/oauth2/v4"
	"golang.org/x/sync/singleflight"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

type registeredClient struct {
	ID          string   `json:"client_id"`
	Name        string   `json:"client_name"`
	Redirects   []string `json:"redirect_uris"`
	AuthMethod  string   `json:"token_endpoint_auth_method"`
	AuthMethods []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	GrantTypes  []string `json:"grant_types,omitempty"`
	Responses   []string `json:"response_types,omitempty"`
}

func (c registeredClient) GetID() string     { return c.ID }
func (c registeredClient) GetSecret() string { return "" }
func (c registeredClient) GetDomain() string { return "" }
func (c registeredClient) IsPublic() bool    { return true }
func (c registeredClient) GetUserID() string { return "" }

func loopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loopbackRedirectHost(host string) bool {
	return host == "localhost" || loopbackHost(host)
}

func validDCRRedirect(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u != nil && validRedirect(raw) && (u.Scheme != "http" || u.Hostname() != "localhost")
}

func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u != nil && len(raw) <= 2048 && u.Host != "" && u.User == nil && u.Fragment == "" &&
		validRedirectPort(u.Port()) &&
		(u.Scheme == "https" || (u.Scheme == "http" && loopbackRedirectHost(u.Hostname())))
}

// validRedirectPort accepts an absent port or one in 1-65535. url.Parse only
// checks that a port is numeric, so ":0" and ":99999" would otherwise be
// registered, matched against and redirected to.
func validRedirectPort(port string) bool {
	if port == "" {
		return true
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

func (c registeredClient) acceptsRedirect(raw string) bool {
	if !validRedirect(raw) {
		return false
	}
	for _, registered := range c.Redirects {
		if registered == raw {
			return true
		}
		a, _ := url.Parse(registered)
		b, _ := url.Parse(raw)
		if a == nil || b == nil || a.Scheme != "http" || b.Scheme != "http" ||
			!loopbackRedirectHost(a.Hostname()) || a.Hostname() != b.Hostname() {
			continue
		}
		// Native loopback clients choose a callback port at login; the
		// registered host and the rest of the URI must still match exactly.
		a.Host = a.Hostname()
		b.Host = b.Hostname()
		if a.String() == b.String() {
			return true
		}
	}
	return false
}

type clientStorage struct{ client registeredClient }

func (s clientStorage) GetByID(_ context.Context, id string) (lib.ClientInfo, error) {
	if id != s.client.ID {
		return nil, problem("invalid_client")
	}
	return s.client, nil
}

const (
	// cimdFetchTimeout bounds one metadata fetch, however many callers wait for it.
	cimdFetchTimeout = 5 * time.Second
	// cimdFailureTTL is how long a failed fetch of one metadata URL is
	// remembered: unauthenticated requests naming an allowed host's URL would
	// otherwise each cost an outbound request (security audit 2026-11-03, A-7).
	cimdFailureTTL = 60 * time.Second
	// cimdFailureCapacity bounds the remembered failures. When it is full of
	// fresh entries a new failure is simply not remembered.
	cimdFailureCapacity = 256
)

type clientResolver struct {
	service       *Service
	http          *http.Client
	group         singleflight.Group
	preRegistered map[string]registeredClient

	failuresMu sync.Mutex
	failures   map[string]cimdFailure
}

// cimdFailure is a failed metadata fetch. The protocol error is kept as it
// was first answered, so a transport fault stays retryable
// (temporarily_unavailable) while it is remembered.
type cimdFailure struct {
	problem *Problem
	until   time.Time
}

func newClientResolver(s *Service) *clientResolver {
	return &clientResolver{service: s, http: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext:           util.SSRFSafeDialContext(&net.Dialer{Timeout: 5 * time.Second}),
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
			MaxConnsPerHost:       4,
			MaxIdleConns:          8,
			IdleConnTimeout:       time.Minute,
		},
	}}
}

func (c *clientResolver) resolve(ctx context.Context, id string) (registeredClient, error) {
	if id == "" || len(id) > 2048 {
		return registeredClient{}, problem("invalid_client")
	}
	cached, err := c.service.q.GetOAuthClient(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return registeredClient{}, storageProblem(ctx, "failed to load OAuth client", err, "client_id", id)
	}
	if client, ok := c.preRegistered[id]; ok {
		redirects, _ := json.Marshal(client.Redirects)
		if err != nil || cached.Source != "cimd" || cached.Name != client.Name ||
			cached.RedirectUris != string(redirects) || cached.ExpiresAt <= c.service.now().Unix() {
			if err := c.service.saveClient(ctx, client, "cimd", c.service.now().Add(15*time.Minute).Unix()); err != nil {
				return registeredClient{}, storageProblem(ctx, "failed to refresh pre-registered OAuth client", err, "client_id", id)
			}
		}
		return client, nil
	}
	if err == nil && cached.ExpiresAt > c.service.now().Unix() {
		var redirects []string
		if json.Unmarshal([]byte(cached.RedirectUris), &redirects) != nil {
			return registeredClient{}, problem("server_error")
		}
		return registeredClient{ID: id, Name: cached.Name, Redirects: redirects, AuthMethod: "none"}, nil
	}
	if !validCIMDID(id, c.service.options.AllowedHosts) {
		return registeredClient{}, problem("invalid_client")
	}
	if p := c.recentFailure(id); p != nil {
		return registeredClient{}, p
	}
	result := c.group.DoChan(id, func() (any, error) {
		// One flight serves every caller waiting for this URL, so it must
		// not end with the caller that happened to start it. It keeps that
		// caller's values but not its cancellation, and has its own deadline.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cimdFetchTimeout)
		defer cancel()
		return c.fetch(fetchCtx, id)
	})
	select {
	case <-ctx.Done():
		return registeredClient{}, problem("temporarily_unavailable")
	case r := <-result:
		if r.Err != nil {
			return registeredClient{}, r.Err
		}
		client, ok := r.Val.(registeredClient)
		if !ok {
			return registeredClient{}, problem("server_error")
		}
		return client, nil
	}
}

func validCIMDID(id string, allowedHosts []string) bool {
	host, ok := cimdHost(id)
	return ok && slices.Contains(allowedHosts, host)
}

// cimdHost returns the lowercased host of a client ID that is a well-formed
// metadata document URL: HTTPS on port 443, a path, no credentials, query or
// fragment, and not a private address.
func cimdHost(id string) (string, bool) {
	u, err := url.Parse(id)
	if err != nil || id == "" || len(id) > 2048 || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" || u.Path == "" || (u.Port() != "" && u.Port() != "443") {
		return "", false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && util.IsPrivateIP(ip) {
		return "", false
	}
	return strings.ToLower(u.Hostname()), true
}

func (c *clientResolver) loadPreRegistered(raw string) error {
	if raw == "" {
		return nil
	}
	var clients []registeredClient
	if len(raw) > 64<<10 || json.Unmarshal([]byte(raw), &clients) != nil || len(clients) > 20 {
		return errors.New("invalid pre-registered OAuth client configuration")
	}
	c.preRegistered = make(map[string]registeredClient, len(clients))
	for _, client := range clients {
		_, duplicate := c.preRegistered[client.ID]
		if duplicate || !c.pinnableID(client.ID) || !validCIMDClient(client) {
			return errors.New("invalid pre-registered OAuth client configuration")
		}
		client.AuthMethod = "none"
		c.preRegistered[client.ID] = client
	}
	return nil
}

// recentFailure returns the remembered answer for a metadata URL whose fetch
// failed less than cimdFailureTTL ago, or nil.
func (c *clientResolver) recentFailure(id string) *Problem {
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()
	failure, ok := c.failures[id]
	if !ok {
		return nil
	}
	if !c.service.now().Before(failure.until) {
		delete(c.failures, id)
		return nil
	}
	return failure.problem
}

// rememberFailure records a failed fetch of the remote document. Only the
// remote answer is remembered: a fault in this server's own storage says
// nothing about the URL and is retried at once.
func (c *clientResolver) rememberFailure(id string, err error) {
	var p *Problem
	if !errors.As(err, &p) {
		return
	}
	now := c.service.now()
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()
	if c.failures == nil {
		c.failures = make(map[string]cimdFailure)
	}
	if _, known := c.failures[id]; !known && len(c.failures) >= cimdFailureCapacity {
		for key, failure := range c.failures {
			if !now.Before(failure.until) {
				delete(c.failures, key)
			}
		}
		if len(c.failures) >= cimdFailureCapacity {
			return
		}
	}
	c.failures[id] = cimdFailure{problem: p, until: now.Add(cimdFailureTTL)}
}

// pinnableID reports whether id may name a pre-registered client: a metadata
// document URL on an allowed host. With an empty allowlist (no live
// discovery) the owner-reviewed documents are the only CIMD clients, and
// they are never fetched, so any well-formed URL is accepted.
func (c *clientResolver) pinnableID(id string) bool {
	hosts := c.service.options.AllowedHosts
	if len(hosts) == 0 {
		_, ok := cimdHost(id)
		return ok
	}
	return validCIMDID(id, hosts)
}

func (c *clientResolver) fetch(ctx context.Context, id string) (registeredClient, error) {
	client, err := c.download(ctx, id)
	if err != nil {
		c.rememberFailure(id, err)
		return registeredClient{}, err
	}
	if err := c.service.saveClient(ctx, client, "cimd", c.service.now().Add(15*time.Minute).Unix()); err != nil {
		logFault(ctx, "failed to cache OAuth client metadata", err, "client_id", id)
		return registeredClient{}, problem("temporarily_unavailable")
	}
	return client, nil
}

// download retrieves and validates the metadata document at id.
func (c *clientResolver) download(ctx context.Context, id string) (registeredClient, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if err != nil {
		return registeredClient{}, problem("invalid_client")
	}
	req.Header.Set("Accept", "application/json")
	// A transport fault, a timeout, rate limiting or a 5xx says nothing about
	// the client's identity, so it is retryable; any other non-200 answer
	// (redirects are not followed) or a document that fails validation is
	// invalid_client. Every failure is logged: the client sees only the code.
	resp, err := c.http.Do(req)
	if err != nil {
		return registeredClient{}, failure(ctx, "temporarily_unavailable", "OAuth client metadata fetch failed", err, "client_id", id)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusInternalServerError || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests {
		slog.WarnContext(ctx, "OAuth client metadata host unavailable", "client_id", id, "status", resp.StatusCode)
		return registeredClient{}, problem("temporarily_unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil {
		return registeredClient{}, failure(ctx, "temporarily_unavailable", "OAuth client metadata read failed", err, "client_id", id)
	}
	if len(raw) > 64<<10 || resp.StatusCode != http.StatusOK {
		slog.WarnContext(ctx, "OAuth client metadata rejected", "client_id", id, "status", resp.StatusCode, "oversize", len(raw) > 64<<10)
		return registeredClient{}, problem("invalid_client")
	}
	if !jsonContentType(resp.Header.Get("Content-Type")) {
		slog.WarnContext(ctx, "OAuth client metadata is not served as JSON", "client_id", id)
		return registeredClient{}, problem("invalid_client")
	}
	var client registeredClient
	if json.Unmarshal(raw, &client) != nil || client.ID != id || !validCIMDClient(client) {
		slog.WarnContext(ctx, "OAuth client metadata document invalid", "client_id", id)
		return registeredClient{}, problem("invalid_client")
	}
	client.AuthMethod = "none"
	return client, nil
}

// jsonContentType reports whether a Content-Type header names JSON:
// application/json or a structured-syntax suffix such as application/jrd+json.
func jsonContentType(header string) bool {
	mediaType, _, err := mime.ParseMediaType(header)
	return err == nil && (mediaType == "application/json" || (strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json")))
}

func validClient(c registeredClient) bool {
	if !validClientMetadata(c, validDCRRedirect) {
		return false
	}
	for _, grant := range c.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			return false
		}
	}
	return true
}

func validCIMDClient(c registeredClient) bool {
	// Published metadata describes capabilities across authorization servers.
	// Extra advertised grants do not enable them at our token endpoint.
	// When present, the supported-method set is authoritative; select only
	// our public-client method rather than the legacy preference.
	if c.AuthMethods != nil {
		if len(c.AuthMethods) > 20 || !slices.Contains(c.AuthMethods, "none") {
			return false
		}
		c.AuthMethod = "none"
	}
	return validClientMetadata(c, validRedirect) &&
		(len(c.GrantTypes) == 0 || slices.Contains(c.GrantTypes, "authorization_code"))
}

func validClientMetadata(c registeredClient, checkRedirect func(string) bool) bool {
	if c.AuthMethod != "none" || c.Name == "" || len(c.Name) > 200 || len(c.Redirects) == 0 || len(c.Redirects) > 20 {
		return false
	}
	for _, response := range c.Responses {
		if response != "code" {
			return false
		}
	}
	for _, redirect := range c.Redirects {
		if !checkRedirect(redirect) {
			return false
		}
	}
	return true
}

func (s *Service) saveClient(ctx context.Context, c registeredClient, source string, expires int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	q := s.q.WithTx(tx)
	existing, err := q.GetOAuthClient(ctx, c.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && existing.Source != source {
		return problem("invalid_client")
	}
	isNew := errors.Is(err, sql.ErrNoRows)
	// Public DCR registrations have their own budget, so exhausting that
	// optional endpoint cannot deny first-use CIMD or owner-pinned clients.
	count, err := q.CountOAuthClientsBySource(ctx, source)
	if err != nil {
		return err
	}
	if isNew && count >= 1000 {
		if err := q.DeleteExpiredUnusedOAuthClients(ctx, s.now().Unix()); err != nil {
			return err
		}
		count, err = q.CountOAuthClientsBySource(ctx, source)
		if err != nil || count >= 1000 {
			return problem("temporarily_unavailable")
		}
	}
	raw, err := json.Marshal(c.Redirects)
	if err != nil {
		return err
	}
	if err := q.UpsertOAuthClient(ctx, store.UpsertOAuthClientParams{
		ID: c.ID, Name: c.Name, RedirectUris: string(raw), Source: source, ExpiresAt: expires}); err != nil {
		return err
	}
	return tx.Commit()
}
