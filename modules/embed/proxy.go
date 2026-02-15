// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	maxDifyProxyBodyBytes = 1 << 20
	difyProxyTimeout      = 90 * time.Second
)

func (m *Module) handleDifyChatMessagesProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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

	userID := strings.TrimSpace(r.URL.Query().Get("user"))
	if userID == "" {
		http.Error(w, "Missing user query parameter", http.StatusBadRequest)
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

	resp, err := http.DefaultClient.Do(req)
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
