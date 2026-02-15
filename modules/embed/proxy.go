// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/util"
)

const (
	maxDifyProxyBodyBytes = 1 << 20
	difyProxyTimeout      = 90 * time.Second
	maxDifyMessageIDLen   = 128
	maxDifyUserIDLen      = 128
	maxDifyQueryLen       = 4096
	embedProxyTokenHeader = "X-Embed-Proxy-Token"
)

var difyIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9._:@-]+$`)

var difyProxyHTTPClient = &http.Client{
	Timeout: difyProxyTimeout,
	Transport: &http.Transport{
		DialContext: util.SSRFSafeDialContext(&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}),
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("stopped after 5 redirects")
		}
		if err := util.ValidateWebhookURL(req.URL.String()); err != nil {
			return fmt.Errorf("redirect blocked: %w", err)
		}
		return nil
	},
}

func (m *Module) handleDifyChatMessagesProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !m.isRequestOriginAllowed(r) {
		m.ctx.Logger.Warn("blocked embed proxy request by origin policy",
			"path", r.URL.Path,
			"origin", r.Header.Get("Origin"),
			"referer", r.Header.Get("Referer"))
		m.logEmbedSecurityEvent(r, "Embed proxy blocked by origin policy", map[string]any{
			"provider": "dify",
			"path":     r.URL.Path,
			"origin":   r.Header.Get("Origin"),
			"referer":  r.Header.Get("Referer"),
		})
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !m.isProxyTokenAuthorized(r) {
		m.ctx.Logger.Warn("blocked embed proxy request by token policy", "path", r.URL.Path)
		m.logEmbedSecurityEvent(r, "Embed proxy blocked by token policy", map[string]any{
			"provider": "dify",
			"path":     r.URL.Path,
		})
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	apiEndpoint, apiKey, ok := m.getDifyProxyConfig()
	if !ok {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDifyProxyBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	if !json.Valid(body) {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}
	if _, err := extractAndValidateDifyChatUser(body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m.proxyDifyRequest(w, r, apiEndpoint, apiKey, http.MethodPost, "/chat-messages", nil, body)
}

func (m *Module) handleDifySuggestedProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !m.isRequestOriginAllowed(r) {
		m.ctx.Logger.Warn("blocked embed proxy request by origin policy",
			"path", r.URL.Path,
			"origin", r.Header.Get("Origin"),
			"referer", r.Header.Get("Referer"))
		m.logEmbedSecurityEvent(r, "Embed proxy blocked by origin policy", map[string]any{
			"provider": "dify",
			"path":     r.URL.Path,
			"origin":   r.Header.Get("Origin"),
			"referer":  r.Header.Get("Referer"),
		})
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !m.isProxyTokenAuthorized(r) {
		m.ctx.Logger.Warn("blocked embed proxy request by token policy", "path", r.URL.Path)
		m.logEmbedSecurityEvent(r, "Embed proxy blocked by token policy", map[string]any{
			"provider": "dify",
			"path":     r.URL.Path,
		})
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	apiEndpoint, apiKey, ok := m.getDifyProxyConfig()
	if !ok {
		http.NotFound(w, r)
		return
	}

	messageID := strings.TrimSpace(chi.URLParam(r, "messageID"))
	if err := validateDifyIdentifier(messageID, maxDifyMessageIDLen, "message ID"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(r.URL.Query().Get("user"))
	if err := validateDifyIdentifier(userID, maxDifyUserIDLen, "user query parameter"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	query := url.Values{}
	query.Set("user", userID)
	path := "/messages/" + url.PathEscape(messageID) + "/suggested"
	m.proxyDifyRequest(w, r, apiEndpoint, apiKey, http.MethodGet, path, query, nil)
}

func (m *Module) getDifyProxyConfig() (apiEndpoint string, apiKey string, ok bool) {
	settings, found := m.getEnabledProviderSettings("dify")
	if !found {
		return "", "", false
	}

	apiEndpoint = strings.TrimSuffix(strings.TrimSpace(settings["api_endpoint"]), "/")
	apiKey = strings.TrimSpace(settings["api_key"])
	if apiEndpoint == "" || apiKey == "" {
		return "", "", false
	}
	parsedEndpoint, err := url.Parse(apiEndpoint)
	if err != nil || !strings.EqualFold(parsedEndpoint.Scheme, "https") {
		m.ctx.Logger.Warn("invalid Dify endpoint scheme in embed settings", "api_endpoint", apiEndpoint)
		return "", "", false
	}
	if err := util.ValidateWebhookURL(apiEndpoint); err != nil {
		m.ctx.Logger.Warn("invalid Dify endpoint in embed settings", "error", err)
		return "", "", false
	}
	return apiEndpoint, apiKey, true
}

func (m *Module) proxyDifyRequest(
	w http.ResponseWriter,
	r *http.Request,
	apiEndpoint, apiKey, method, path string,
	query url.Values,
	body []byte,
) {
	targetURL := apiEndpoint + path
	if len(query) > 0 {
		targetURL += "?" + query.Encode()
	}

	if !m.allowProxyBudget() {
		m.ctx.Logger.Warn("embed proxy global rate limit exceeded", "path", r.URL.Path, "ip", embedClientIP(r))
		m.logEmbedSecurityEvent(r, "Embed proxy blocked by global rate limit", map[string]any{
			"provider": "dify",
			"path":     r.URL.Path,
		})
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}

	if !m.acquireProxySlot() {
		m.logEmbedSecurityEvent(r, "Embed proxy blocked by concurrency limit", map[string]any{
			"provider": "dify",
			"path":     r.URL.Path,
		})
		http.Error(w, "Service busy", http.StatusTooManyRequests)
		return
	}
	defer m.releaseProxySlot()

	ctx, cancel := context.WithTimeout(r.Context(), difyProxyTimeout)
	defer cancel()

	var requestBody io.Reader
	if body != nil {
		requestBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, requestBody)
	if err != nil {
		m.ctx.Logger.Error("failed to create Dify proxy request", "error", err)
		http.Error(w, "Failed to build proxy request", http.StatusInternalServerError)
		return
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "text/event-stream, application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := difyProxyHTTPClient.Do(req)
	if err != nil {
		m.ctx.Logger.Error("Dify proxy request failed", "error", err)
		http.Error(w, "Failed to contact upstream service", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)

	if _, err := io.Copy(w, resp.Body); err != nil {
		m.ctx.Logger.Debug("Dify proxy response copy interrupted", "error", err)
	}
}

func (m *Module) acquireProxySlot() bool {
	if m.proxySemaphore == nil {
		return true
	}
	select {
	case m.proxySemaphore <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m *Module) releaseProxySlot() {
	if m.proxySemaphore == nil {
		return
	}
	select {
	case <-m.proxySemaphore:
	default:
	}
}

func (m *Module) allowProxyBudget() bool {
	if m.globalRateLimiter == nil {
		return true
	}
	return m.globalRateLimiter.Allow()
}

func (m *Module) isProxyTokenAuthorized(r *http.Request) bool {
	if m == nil || !m.requireProxyToken {
		return true
	}

	expected := strings.TrimSpace(m.proxyToken)
	if expected == "" {
		return false
	}

	provided := strings.TrimSpace(r.Header.Get(embedProxyTokenHeader))
	if provided == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (m *Module) logEmbedSecurityEvent(r *http.Request, message string, metadata map[string]any) {
	if m == nil || m.ctx == nil || m.ctx.Events == nil || r == nil {
		return
	}
	clientIP := embedClientIP(r)
	_ = m.ctx.Events.LogSecurityEvent(r.Context(), model.EventLevelWarning, message, nil, clientIP, r.URL.Path, metadata)
}

func embedClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	return middleware.GetClientIP(r)
}

func validateDifyIdentifier(value string, maxLen int, label string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("missing %s", label)
	}
	if len(trimmed) > maxLen {
		return fmt.Errorf("%s is too long", label)
	}
	if !difyIdentifierPattern.MatchString(trimmed) {
		return fmt.Errorf("invalid %s format", label)
	}
	return nil
}

func extractAndValidateDifyChatUser(body []byte) (string, error) {
	type difyChatPayload struct {
		User  string      `json:"user"`
		Query interface{} `json:"query"`
	}

	var payload difyChatPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("invalid JSON body")
	}
	if err := validateDifyIdentifier(payload.User, maxDifyUserIDLen, "user"); err != nil {
		return "", err
	}
	if queryText, ok := payload.Query.(string); ok && len(queryText) > maxDifyQueryLen {
		return "", fmt.Errorf("query is too long")
	}

	return payload.User, nil
}
