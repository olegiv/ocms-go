// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
)

// bearerRealm is advertised in WWW-Authenticate on 401 responses (RFC 6750).
// There is deliberately no resource_metadata parameter: the endpoint takes
// API keys, not OAuth tokens, so clients must not start an OAuth flow.
const bearerRealm = `Bearer realm="oCMS MCP"`

// buildHTTPHandler assembles the endpoint's middleware chain, outermost
// first:
//
//	no-store → POST only → per-IP limit → in-flight cap → cross-origin check
//	→ API key auth (+ WWW-Authenticate on 401) → mcp:access → TokenInfo
//	→ per-key limit → no batches → stored activation/settings → request context
//	→ SDK Streamable HTTP handler (stateless, JSON responses)
//
// Cheap rejections run before API key verification (Argon2), so probing the
// endpoint costs little, and the per-IP limit runs before the in-flight cap so
// a flood from one address cannot occupy the slots other callers need. The
// global chain (request ID, trusted-proxy RealIP, sentinel bans, logging,
// 30 s timeout, security headers) wraps every module route; panics are
// recovered by serveEndpoint, because the timeout middleware runs handlers
// out of reach of the global recoverer.
//
// Init runs once per process (the registry serializes activation), so the two
// rate limiters, each with a cleanup goroutine, are created once, like those
// of the REST routes.
func (m *Module) buildHTTPHandler(db *sql.DB) http.Handler {
	sdkHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return m.currentServer() },
		&mcp.StreamableHTTPOptions{
			// Protocol 2026-07-28 is served only by stateless handlers, and
			// statelessness keeps every request self-contained: no session
			// table, no long-lived SSE streams to outlive the 30 s timeout,
			// and any instance behind a load balancer can answer.
			Stateless:    true,
			JSONResponse: true,
			// The SDK's localhost guard rejects requests that reach a loopback
			// listener with a public Host header, which is exactly a reverse
			// proxy on the same host forwarding Host (docs/reverse-proxy.md).
			// DNS rebinding is countered by the cross-origin check and the
			// mandatory bearer authentication instead.
			DisableLocalhostProtection:   true,
			MaxRequestBodyBytes:          maxRequestBodyBytes,
			PropagateRequestCancellation: true,
			Logger:                       sdkLogger(m.logger, "mcp-transport"),
		},
	)

	var h http.Handler = sdkHandler
	h = withRequestContext(h)
	h = m.withStoredState(h)
	h = rejectBatches(maxRequestBodyBytes, h)
	h = middleware.APIRateLimit(keyRateLimitRPS, keyRateLimitBurst)(h)
	h = auth.RequireBearerToken(verifyValidatedKey, &auth.RequireBearerTokenOptions{
		// Keys without an expiry are valid unless OCMS_REQUIRE_API_KEY_EXPIRY
		// says otherwise, which middleware.APIKeyAuth already enforced.
		AllowMissingExpiration: true,
	})(h)
	h = requireMCPAccess(m.logger, h)
	h = middleware.APIKeyAuth(db)(h)
	h = withBearerChallenge(h)
	h = crossOriginGuard(m.logger, h)
	h = limitInFlight(m.logger, maxInFlightRequests, h)
	h = middleware.NewGlobalRateLimiter(ipRateLimitRPS, ipRateLimitBurst).Middleware()(h)
	h = requirePOST(h)
	return noStore(h)
}

// withStoredState refreshes security-sensitive state for every admitted
// request, including requests routed to a replica other than the admin save.
// The registry's activation gate is process-local, so check the stored flag
// here as well. A failed read must not authorize a request using stale state.
func (m *Module) withStoredState(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active, err := m.svc.queries.IsModuleActive(r.Context(), ModuleName)
		if err == nil && !active {
			http.NotFound(w, r)
			return
		}
		var settings Settings
		if err == nil {
			settings, err = m.reloadSettings(r.Context())
		}
		if err != nil {
			m.logger.Error("failed to refresh MCP request state", "error", err)
			middleware.WriteAPIError(w, http.StatusServiceUnavailable, "service_unavailable", "MCP server state is unavailable", nil)
			return
		}
		ctx := context.WithValue(r.Context(), requestSettingsKey{}, settings)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// noStore marks every response uncacheable: each is produced for one API
// key. The header is set when the status is written, because the SDK sets
// its own (weaker, "no-cache") Cache-Control when it writes a response.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&headerWriter{ResponseWriter: w, before: func(h http.Header, _ int) {
			h.Set("Cache-Control", "no-store")
		}}, r)
	})
}

// rejectBatches refuses JSON-RPC batch requests. go-sdk accepts a batch from
// any request that declares protocol 2025-03-26 or older (or none), and runs
// every call in it concurrently, so one HTTP request could carry thousands of
// tool calls past the per-key rate limit and the in-flight cap, which count
// requests. Protocol 2025-06-18 and later forbid batches and mainstream
// clients do not send them, so the endpoint takes one message per request.
//
// It buffers the body to inspect it, within the same cap the SDK applies. It
// runs after authentication and rate limiting, so only accepted callers'
// bodies are read, and the in-flight cap bounds how many are held at once.
func rejectBatches(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				middleware.WriteAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large",
					"Request body too large", nil)
				return
			}
			middleware.WriteAPIError(w, http.StatusBadRequest, "invalid_request",
				"Failed to read request body", nil)
			return
		}
		if isJSONArray(body) {
			middleware.WriteAPIError(w, http.StatusBadRequest, "batch_not_supported",
				"JSON-RPC batch requests are not supported; send one request per HTTP call", nil)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

// isJSONArray reports whether a JSON document is an array.
func isJSONArray(body []byte) bool {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '['
}

// withRequestContext stores the HTTP request's context as a value of itself.
// The SDK hides its cancellation from handlers of pre-2026-07-28 requests but
// forwards value lookups, which lets callContext re-attach it.
func withRequestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, requestContextKey{}, ctx)))
	})
}

// requirePOST answers every method but POST with 405 before any other work.
// A stateless Streamable HTTP server offers no GET stream and no DELETE.
func requirePOST(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			middleware.WriteAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed",
				"The MCP endpoint accepts POST requests only", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitInFlight rejects requests beyond capacity concurrent ones with 503
// rather than queueing them.
func limitInFlight(logger *slog.Logger, capacity int, next http.Handler) http.Handler {
	slots := make(chan struct{}, capacity)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			logger.Warn("MCP request rejected: too many in-flight requests",
				"ip", middleware.GetClientIP(r), "limit", capacity)
			w.Header().Set("Retry-After", strconv.Itoa(1))
			middleware.WriteAPIError(w, http.StatusServiceUnavailable, "service_unavailable",
				"Too many concurrent MCP requests. Please retry shortly.", nil)
		}
	})
}

// crossOriginGuard rejects cross-origin browser requests (Go's
// http.CrossOriginProtection: Sec-Fetch-Site, falling back to Origin versus
// Host). MCP clients are not browsers and send neither header, so they pass;
// a web page trying to reach the endpoint does not. This is the Origin
// validation the MCP transport specification requires.
func crossOriginGuard(logger *slog.Logger, next http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Warn("MCP request rejected: cross-origin browser request",
			"ip", middleware.GetClientIP(r),
			"origin", r.Header.Get("Origin"),
			"sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
		middleware.WriteAPIError(w, http.StatusForbidden, codeForbidden,
			"Cross-origin requests are not allowed", nil)
	}))
	return protection.Handler(next)
}

// requireMCPAccess admits only API keys holding mcp:access, so integration
// keys issued for REST never become usable by AI agents implicitly.
func requireMCPAccess(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := middleware.GetAPIKey(r)
		if key == nil {
			middleware.WriteAPIError(w, http.StatusUnauthorized, codeUnauthorized, msgUnauthenticated, nil)
			return
		}
		if !slices.Contains(middleware.ParseAPIKeyPermissions(key), model.PermissionMCPAccess) {
			logger.Warn("MCP request rejected: API key lacks mcp:access",
				"api_key_id", key.ID,
				"api_key_prefix", key.KeyPrefix,
				"ip", middleware.GetClientIP(r))
			middleware.WriteAPIError(w, http.StatusForbidden, codeForbidden,
				"API key lacks required permission: "+model.PermissionMCPAccess, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withBearerChallenge adds a WWW-Authenticate header to 401 responses, as
// RFC 6750 asks of bearer-protected resources.
func withBearerChallenge(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&headerWriter{ResponseWriter: w, before: func(h http.Header, status int) {
			if status == http.StatusUnauthorized {
				h.Set("WWW-Authenticate", bearerRealm)
			}
		}}, r)
	})
}

// headerWriter lets middleware adjust response headers at the moment the
// status is written, after the wrapped handler has set its own.
type headerWriter struct {
	http.ResponseWriter
	before      func(h http.Header, status int)
	wroteHeader bool
}

// WriteHeader runs the adjustment once, then writes the status.
func (hw *headerWriter) WriteHeader(code int) {
	if !hw.wroteHeader {
		hw.wroteHeader = true
		hw.before(hw.Header(), code)
	}
	hw.ResponseWriter.WriteHeader(code)
}

// Write writes an implicit 200 status first, like net/http does.
func (hw *headerWriter) Write(b []byte) (int, error) {
	if !hw.wroteHeader {
		hw.WriteHeader(http.StatusOK)
	}
	return hw.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer when it supports flushing.
func (hw *headerWriter) Flush() {
	if f, ok := hw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (hw *headerWriter) Unwrap() http.ResponseWriter {
	return hw.ResponseWriter
}
