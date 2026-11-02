// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package contentpath

import (
	"errors"
	"net/url"
	"strings"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// Bound both memory and work for public wildcard matching. Over-budget rules
// do not match; exact and trailing-prefix redirects remain linear scans.
const maxRedirectMatchStates = 1 << 20

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

	return matchPathPartsWithCaptures(sourceParts, requestParts)
}

// Match suffix states once, then materialize only successful captures. Choosing
// the first viable end offset preserves shortest-first ** capture semantics.
func matchPathPartsWithCaptures(sourceParts, requestParts []string) (bool, []string) {
	width := len(requestParts) + 1
	if len(sourceParts)+1 > maxRedirectMatchStates/width {
		return false, nil
	}
	matched := make([]bool, (len(sourceParts)+1)*width)
	matched[len(sourceParts)*width+len(requestParts)] = true
	for si := len(sourceParts) - 1; si >= 0; si-- {
		for ri := len(requestParts); ri >= 0; ri-- {
			index := si*width + ri
			if sourceParts[si] == "**" {
				matched[index] = matched[index+width] || (ri < len(requestParts) && matched[index+1])
			} else if ri < len(requestParts) && (sourceParts[si] == "*" || sourceParts[si] == requestParts[ri]) {
				matched[index] = matched[index+width+1]
			}
		}
	}
	if !matched[0] {
		return false, nil
	}
	var captures []string
	ri := 0
	for si, part := range sourceParts {
		switch part {
		case "**":
			start := ri
			for !matched[(si+1)*width+ri] {
				ri++
			}
			captures = append(captures, strings.Join(requestParts[start:ri], "/"))
		case "*":
			captures = append(captures, requestParts[ri])
			ri++
		default:
			ri++
		}
	}
	return true, captures
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
