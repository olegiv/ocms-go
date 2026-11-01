// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package contentpath

import (
	"errors"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
	"net/url"
	"strings"
)

func MatchRedirect(requestPath, sourcePath string, isWildcard bool) (bool, []string) {
	if !isWildcard {
		return requestPath == sourcePath, nil
	}

	// Handle trailing prefix wildcard (e.g., /user/login*)
	// Must end with single * but not ** (which is handled by segment matching)
	if strings.HasSuffix(sourcePath, "*") && !strings.HasSuffix(sourcePath, "**") {
		// Check if it's a standalone segment wildcard like /blog/*
		// by verifying the character before * is not /
		prefix := strings.TrimSuffix(sourcePath, "*")
		if !strings.HasSuffix(prefix, "/") {
			// This is a prefix wildcard like /user/login* (not /user/login/*)
			reqPath := strings.TrimSuffix(requestPath, "/")
			prefixNorm := strings.TrimSuffix(prefix, "/")

			// Match exact prefix or prefix followed by more path
			if reqPath == prefixNorm || strings.HasPrefix(reqPath, prefix) {
				// Capture everything after the prefix
				captured := strings.TrimPrefix(reqPath, prefix)
				captured = strings.TrimPrefix(captured, "/")
				return true, []string{captured}
			}
			return false, nil
		}
	}

	sourceParts := strings.Split(strings.Trim(sourcePath, "/"), "/")
	requestParts := strings.Split(strings.Trim(requestPath, "/"), "/")

	var captures []string
	matched := matchPathPartsWithCaptures(sourceParts, requestParts, 0, 0, &captures)
	return matched, captures
}

// matchPathPartsWithCaptures recursively matches path parts with wildcard support and captures matched segments.
func matchPathPartsWithCaptures(sourceParts, requestParts []string, si, ri int, captures *[]string) bool {
	// Both exhausted - match
	if si >= len(sourceParts) && ri >= len(requestParts) {
		return true
	}

	// Source exhausted but request has more - no match
	if si >= len(sourceParts) {
		return false
	}

	// Request exhausted but source has more - check for ** only
	if ri >= len(requestParts) {
		// Only match if remaining source parts are all **
		for i := si; i < len(sourceParts); i++ {
			if sourceParts[i] != "**" {
				return false
			}
			// ** matching zero segments captures empty string
			*captures = append(*captures, "")
		}
		return true
	}

	part := sourceParts[si]

	switch part {
	case "*":
		// Single wildcard - matches exactly one segment, capture it
		*captures = append(*captures, requestParts[ri])
		return matchPathPartsWithCaptures(sourceParts, requestParts, si+1, ri+1, captures)

	case "**":
		// Double wildcard - matches zero or more segments
		// Try matching zero segments first (move source forward)
		capturesBefore := len(*captures)
		*captures = append(*captures, "") // Capture empty for zero match
		if matchPathPartsWithCaptures(sourceParts, requestParts, si+1, ri, captures) {
			return true
		}
		// Backtrack: remove captures added during failed attempt
		*captures = (*captures)[:capturesBefore]

		// Try matching one or more segments
		// Collect all segments that ** should match
		for endIdx := ri; endIdx <= len(requestParts); endIdx++ {
			capturesBefore := len(*captures)
			captured := strings.Join(requestParts[ri:endIdx], "/")
			*captures = append(*captures, captured)
			if matchPathPartsWithCaptures(sourceParts, requestParts, si+1, endIdx, captures) {
				return true
			}
			// Backtrack
			*captures = (*captures)[:capturesBefore]
		}
		return false

	default:
		// Literal match
		if part == requestParts[ri] {
			return matchPathPartsWithCaptures(sourceParts, requestParts, si+1, ri+1, captures)
		}
		return false
	}
}

// buildTargetURL builds the final target URL by substituting captured wildcard segments.
// Each "*" or "**" in the target URL is replaced with the corresponding captured segment.
func BuildRedirectTarget(rd store.Redirect, captures []string) string {
	if !rd.IsWildcard || len(captures) == 0 {
		return rd.TargetUrl
	}

	targetURL := rd.TargetUrl

	// Replace each * in the target with the corresponding captured segment
	for _, capture := range captures {
		// Find and replace the first * (which could be part of ** too)
		if idx := strings.Index(targetURL, "*"); idx != -1 {
			// Check if it's ** (double wildcard)
			if idx+1 < len(targetURL) && targetURL[idx+1] == '*' {
				// Replace ** with the captured segment
				targetURL = targetURL[:idx] + capture + targetURL[idx+2:]
			} else {
				// Replace single * with the captured segment
				targetURL = targetURL[:idx] + capture + targetURL[idx+1:]
			}
		}
	}

	return targetURL
}

// ErrUnsafeRedirect is returned for a redirect target rejected by the frontend.
var ErrUnsafeRedirect = errors.New("unsafe content redirect")

// FirstRedirect preserves database ordering, query propagation and private-route exclusions.
func FirstRedirect(redirects []store.Redirect, path, query string) (string, int, bool, error) {
	if util.IsPrivateRoutePath(path) {
		return "", 0, false, nil
	}
	for _, rd := range redirects {
		matched, captures := MatchRedirect(path, rd.SourcePath, rd.IsWildcard)
		if !matched {
			continue
		}
		target := BuildRedirectTarget(rd, captures)
		if query != "" {
			if strings.Contains(target, "?") {
				target += "&" + query
			} else {
				target += "?" + query
			}
		}
		parsed, err := url.Parse(strings.ReplaceAll(target, "\\", "/"))
		if err != nil || parsed.Hostname() != "" {
			return "", 0, true, ErrUnsafeRedirect
		}
		return parsed.String(), int(rd.StatusCode), true, nil
	}
	return "", 0, false, nil
}
