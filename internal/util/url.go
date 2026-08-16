// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// MaxCanonicalURLLength is the maximum accepted length of a page canonical URL.
// It matches the maxLength advertised by the v2 API schema and the maxlength
// attribute on the admin form field.
const MaxCanonicalURLLength = 2048

// ValidateCanonicalURL checks a caller-supplied page canonical URL and returns
// the trimmed value to store.
//
// An empty value is valid and means "compute the canonical URL from site_url
// and the slug". A non-empty value must be an absolute http/https URL with a
// host: the same string is emitted both as the <link rel="canonical"> href and
// as the og:url meta content, and Open Graph requires og:url to be absolute.
// Relative and scheme-relative references are therefore rejected, as are
// scripting schemes such as javascript: and data:, which would otherwise reach
// an href through a theme that opts out of contextual escaping.
//
// The trimmed value is returned so callers store exactly what was validated.
func ValidateCanonicalURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > MaxCanonicalURLLength {
		return "", fmt.Errorf("URL exceeds maximum length of %d characters", MaxCanonicalURLLength)
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", errors.New("invalid URL format")
	}

	// url.Parse accepts a relative reference with an empty scheme, so an
	// explicit check is required; "//host/path" parses with a host but no
	// scheme and is equally unusable as an absolute og:url.
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	case "":
		return "", errors.New("URL must be absolute and start with http:// or https://")
	default:
		return "", fmt.Errorf("URL scheme %q is not allowed; use http or https", parsed.Scheme)
	}

	if parsed.Hostname() == "" {
		return "", errors.New("URL must have a hostname")
	}
	if parsed.User != nil {
		return "", errors.New("URL must not contain credentials")
	}
	// url.Parse only checks that a port is numeric, so the range needs its own
	// check to keep an unusable value out of a published link.
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("URL port %q is not valid", port)
		}
	}

	return trimmed, nil
}
