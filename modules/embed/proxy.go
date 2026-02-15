// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/util"
)

const (
	maxDifyProxyBodyBytes = 1 << 20
	difyProxyTimeout      = 90 * time.Second
	maxDifyMessageIDLen   = 128
	maxDifyUserIDLen      = 128
)

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
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	apiEndpoint, apiKey, ok := m.getDifyProxyConfig()
	if !ok {
		http.NotFound(w, r)
		return
	}

	messageID := strings.TrimSpace(chi.URLParam(r, "messageID"))
	if messageID == "" {
		http.Error(w, "Missing message ID", http.StatusBadRequest)
		return
	}
	if len(messageID) > maxDifyMessageIDLen {
		http.Error(w, "Message ID is too long", http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(r.URL.Query().Get("user"))
	if userID == "" {
		http.Error(w, "Missing user query parameter", http.StatusBadRequest)
		return
	}
	if len(userID) > maxDifyUserIDLen {
		http.Error(w, "User query parameter is too long", http.StatusBadRequest)
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

	if !m.acquireProxySlot() {
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
