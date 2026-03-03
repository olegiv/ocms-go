// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/olegiv/ocms-go/internal/util"
)

var webhookRequireAllowedHosts atomic.Bool

var (
	webhookAllowedHostsMu sync.RWMutex
	webhookAllowedHosts   map[string]struct{}
)

// SetRequireAllowedHosts configures whether webhook destination host allowlist
// must be configured and enforced.
func SetRequireAllowedHosts(required bool) {
	webhookRequireAllowedHosts.Store(required)
}

func isAllowedHostsRequired() bool {
	return webhookRequireAllowedHosts.Load()
}

// ConfigureAllowedHosts parses and stores webhook destination host allowlist.
func ConfigureAllowedHosts(raw string) error {
	hosts, err := ParseAllowedHosts(raw)
	if err != nil {
		return err
	}

	webhookAllowedHostsMu.Lock()
	webhookAllowedHosts = hosts
	webhookAllowedHostsMu.Unlock()
	return nil
}

// ParseAllowedHosts parses comma-separated exact webhook destination hosts.
func ParseAllowedHosts(raw string) (map[string]struct{}, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	entries := strings.Split(trimmed, ",")
	hosts := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		host, err := normalizeAllowedHostEntry(entry)
		if err != nil {
			return nil, err
		}
		hosts[host] = struct{}{}
	}
	return hosts, nil
}

func normalizeAllowedHostEntry(entry string) (string, error) {
	host := strings.TrimSpace(strings.ToLower(entry))
	host = strings.Trim(host, ".")
	if host == "" {
		return "", fmt.Errorf("webhook destination host allowlist contains empty entry")
	}
	if strings.Contains(host, "://") || strings.Contains(host, "/") {
		return "", fmt.Errorf("invalid webhook destination host allowlist entry %q", entry)
	}
	if strings.ContainsAny(host, `\\?#@ `) {
		return "", fmt.Errorf("invalid webhook destination host allowlist entry %q", entry)
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return "", fmt.Errorf("webhook destination host allowlist entry must not include port: %q", entry)
	}
	if strings.Contains(host, ":") {
		return "", fmt.Errorf("webhook destination host allowlist entry must not include port: %q", entry)
	}
	return host, nil
}

func snapshotAllowedHosts() map[string]struct{} {
	webhookAllowedHostsMu.RLock()
	defer webhookAllowedHostsMu.RUnlock()
	if len(webhookAllowedHosts) == 0 {
		return nil
	}
	copyHosts := make(map[string]struct{}, len(webhookAllowedHosts))
	for host := range webhookAllowedHosts {
		copyHosts[host] = struct{}{}
	}
	return copyHosts
}

// DestinationHost returns normalized destination hostname for a webhook URL.
func DestinationHost(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("invalid webhook destination URL: %w", err)
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return "", fmt.Errorf("invalid webhook destination URL: missing hostname")
	}
	return host, nil
}

// ValidateDestinationHostPolicy validates webhook destination hostname against
// the configured allowlist policy.
func ValidateDestinationHostPolicy(rawURL string, allowedHosts map[string]struct{}, requireAllowedHosts bool) error {
	host, err := DestinationHost(rawURL)
	if err != nil {
		return err
	}
	if requireAllowedHosts && len(allowedHosts) == 0 {
		return fmt.Errorf("webhook destination host allowlist is required but OCMS_WEBHOOK_ALLOWED_HOSTS is empty")
	}
	if len(allowedHosts) == 0 {
		return nil
	}
	if _, ok := allowedHosts[host]; !ok {
		return fmt.Errorf("webhook destination host %q is not in OCMS_WEBHOOK_ALLOWED_HOSTS allowlist", host)
	}
	return nil
}

// ValidateDestinationURL validates webhook destination URL against SSRF and
// destination host policies currently configured for the process.
func ValidateDestinationURL(rawURL string) error {
	return ValidateDestinationURLWithPolicy(rawURL, snapshotAllowedHosts(), isAllowedHostsRequired())
}

// ValidateDestinationURLWithPolicy validates webhook destination URL using
// explicit host allowlist policy values.
func ValidateDestinationURLWithPolicy(rawURL string, allowedHosts map[string]struct{}, requireAllowedHosts bool) error {
	if err := util.ValidateWebhookURL(rawURL); err != nil {
		return err
	}
	if err := ValidateDestinationHostPolicy(rawURL, allowedHosts, requireAllowedHosts); err != nil {
		return err
	}
	return nil
}
