// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"
	"strings"
)

const (
	sortQueryParam    = "sort"
	sortDirQueryParam = "dir"
	sortDirAsc        = "asc"
	sortDirDesc       = "desc"
	sortStateNone     = "none"
)

// SortConfig describes an allowed sortable field.
type SortConfig struct {
	DefaultDir string
}

func normalizeSortDir(dir string) string {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case sortDirAsc:
		return sortDirAsc
	case sortDirDesc:
		return sortDirDesc
	default:
		return ""
	}
}

func toggleSortDir(dir string) string {
	if normalizeSortDir(dir) == sortDirAsc {
		return sortDirDesc
	}
	return sortDirAsc
}

func sortFieldDefaultDir(
	field string,
	defaultDir string,
	allowed map[string]SortConfig,
) string {
	if allowed != nil {
		if cfg, ok := allowed[field]; ok {
			if dir := normalizeSortDir(cfg.DefaultDir); dir != "" {
				return dir
			}
		}
	}

	if dir := normalizeSortDir(defaultDir); dir != "" {
		return dir
	}
	return sortDirDesc
}

// parseSortParams validates sort query params against a whitelist.
// Invalid/missing params fall back to defaults.
func parseSortParams(
	r *http.Request,
	defaultField string,
	defaultDir string,
	allowed map[string]SortConfig,
) (field, dir string) {
	fallbackField := strings.TrimSpace(defaultField)
	fallbackDir := sortFieldDefaultDir(fallbackField, defaultDir, allowed)

	requestedField := strings.TrimSpace(r.URL.Query().Get(sortQueryParam))
	requestedDir := normalizeSortDir(r.URL.Query().Get(sortDirQueryParam))
	if requestedField == "" {
		return fallbackField, fallbackDir
	}

	if _, ok := allowed[requestedField]; !ok {
		return fallbackField, fallbackDir
	}

	fieldDefaultDir := sortFieldDefaultDir(requestedField, defaultDir, allowed)
	if requestedDir == "" {
		return requestedField, fieldDefaultDir
	}

	return requestedField, requestedDir
}
