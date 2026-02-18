// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func parseAllowedOrigins(raw string) (map[string]struct{}, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	entries := strings.Split(trimmed, ",")
	allowed := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		origin, err := normalizeOrigin(entry)
		if err != nil {
			return nil, err
		}
		allowed[origin] = struct{}{}
	}

	return allowed, nil
}

func normalizeOrigin(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("empty origin")
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid origin %q: %w", trimmed, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid origin scheme %q", trimmed)
	}
	if u.Host == "" {
		return "", fmt.Errorf("origin host is required for %q", trimmed)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("origin must not include path: %q", trimmed)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("origin must not include query/fragment/userinfo: %q", trimmed)
	}

	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}

func parseAllowedHosts(raw string) (map[string]struct{}, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	entries := strings.Split(trimmed, ",")
	allowed := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		host, err := normalizeHost(entry)
		if err != nil {
			return nil, err
		}
		allowed[host] = struct{}{}
	}
	return allowed, nil
}

func normalizeHost(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("empty host")
	}

	// Host allowlist entries are hostname-only to avoid implicit wildcarding
	// from host:port combinations.
	if strings.Contains(trimmed, "://") || strings.Contains(trimmed, "/") {
		return "", fmt.Errorf("invalid host %q", trimmed)
	}
	if _, _, err := net.SplitHostPort(trimmed); err == nil {
		return "", fmt.Errorf("host must not include port: %q", trimmed)
	}
	if strings.Contains(trimmed, ":") {
		return "", fmt.Errorf("host must not include port: %q", trimmed)
	}

	host := strings.Trim(strings.ToLower(trimmed), ".")
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	if _, err := url.Parse("https://" + host); err != nil {
		return "", fmt.Errorf("invalid host %q: %w", trimmed, err)
	}
	return host, nil
}

func (m *Module) isUpstreamHostAllowed(host string) bool {
	if len(m.allowedUpstreamHosts) == 0 {
		return true
	}
	normalized, err := normalizeHost(host)
	if err != nil {
		return false
	}
	_, ok := m.allowedUpstreamHosts[normalized]
	return ok
}

func (m *Module) isRequestOriginAllowed(r *http.Request) bool {
	if len(m.allowedOrigins) == 0 {
		return !m.requireOriginPolicy
	}

	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		normalized, err := normalizeOrigin(origin)
		if err != nil {
			return false
		}
		_, ok := m.allowedOrigins[normalized]
		return ok
	}

	// Fallback for clients that only send Referer.
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		u, err := url.Parse(referer)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return false
		}
		normalized := strings.ToLower(u.Scheme + "://" + u.Host)
		_, ok := m.allowedOrigins[normalized]
		return ok
	}

	// When policy is configured, requests without Origin/Referer are denied.
	return false
}
