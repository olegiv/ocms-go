// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package contentpath

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
)

// Origin normalizes HTTP(S) origins without DNS or network access.
func Origin(value *url.URL) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(value.Hostname()), ".")
	if address, err := netip.ParseAddr(host); err == nil {
		host = address.String()
	} else {
		var convertErr error
		host, convertErr = idna.Lookup.ToASCII(host)
		if convertErr != nil || host == "" {
			return "", errors.New("invalid site/link host")
		}
	}
	port := value.Port()
	if port == "" {
		switch strings.ToLower(value.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		default:
			return "", errors.New("unsupported site/link scheme")
		}
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", errors.New("invalid site/link port")
	}
	port = strconv.FormatUint(parsedPort, 10)
	return strings.ToLower(value.Scheme) + "://" + net.JoinHostPort(host, port), nil
}

// SiteURL validates the configured public origin. Request Host is never an authority.
func SiteURL(raw string) (*url.URL, error) {
	value, err := url.Parse(raw)
	if err != nil || value == nil || (value.Scheme != "http" && value.Scheme != "https") || value.Host == "" || value.User != nil || value.RawQuery != "" || value.Fragment != "" || (value.Path != "" && value.Path != "/") {
		return nil, errors.New("configured site URL must be an HTTP(S) origin")
	}
	if _, err = Origin(value); err != nil {
		return nil, err
	}
	value.Path = "/"
	return value, nil
}

// LocalURL accepts a same-origin URL or local path and rejects unsafe/foreign references.
func LocalURL(base *url.URL, raw string) (*url.URL, error) {
	if len(raw) > 2048 || strings.IndexFunc(raw, unicode.IsControl) >= 0 || strings.Contains(raw, "\\") {
		return nil, errors.New("invalid site reference")
	}
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || value == nil || value.User != nil || value.Opaque != "" {
		return nil, errors.New("invalid site reference")
	}
	if value.Scheme != "" && !strings.EqualFold(value.Scheme, "http") && !strings.EqualFold(value.Scheme, "https") {
		return nil, errors.New("invalid site reference scheme")
	}
	resolved := base.ResolveReference(value)
	ours, err := Origin(base)
	if err != nil {
		return nil, err
	}
	theirs, err := Origin(resolved)
	if err != nil || ours != theirs {
		return nil, errors.New("reference must use the configured site origin")
	}
	if !strings.HasPrefix(resolved.Path, "/") || strings.ContainsAny(resolved.Path, "\\\x00\r\n") {
		return nil, errors.New("invalid local reference path")
	}
	resolved.Fragment = ""
	resolved.RawFragment = ""
	return resolved, nil
}
