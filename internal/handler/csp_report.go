// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

const (
	// maxCSPReportBytes bounds a report request; browsers send well under 4 KiB
	// per violation and batch only a few.
	maxCSPReportBytes = 16 << 10
	// maxCSPReportsPerRequest bounds how many reports one request can log.
	maxCSPReportsPerRequest = 10
	// maxCSPReportField bounds each logged field.
	maxCSPReportField = 256
)

// CSPReport handles POST /csp-report, where browsers send
// Content-Security-Policy violation reports through report-uri; the Reporting
// API format is accepted too. Each report is logged with bounded, sanitized
// fields, so a blocked
// script or a missing module host shows up in the logs instead of failing
// silently in the browser. Anyone can post here, so the route is rate-limited
// per IP and the request writes nothing but a log line.
func CSPReport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCSPReportBytes)
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	for _, report := range cspReports(raw) {
		slog.Warn("csp violation",
			"document", cspReportField(report, "document-uri", "documentURL"),
			"directive", cspReportField(report, "effective-directive", "effectiveDirective", "violated-directive"),
			"blocked", cspReportField(report, "blocked-uri", "blockedURL"),
			"source", cspReportField(report, "source-file", "sourceFile"),
			"line", cspReportField(report, "line-number", "lineNumber"),
			"disposition", cspReportField(report, "disposition"),
		)
	}
	w.WriteHeader(http.StatusNoContent)
}

// cspReports extracts violation bodies from either format: the legacy
// {"csp-report": {...}} object, or a Reporting API array of
// {"type": "csp-violation", "body": {...}} entries.
func cspReports(raw json.RawMessage) []map[string]any {
	var legacy struct {
		Report map[string]any `json:"csp-report"`
	}
	if json.Unmarshal(raw, &legacy) == nil && legacy.Report != nil {
		return []map[string]any{legacy.Report}
	}

	var batch []struct {
		Type string         `json:"type"`
		Body map[string]any `json:"body"`
	}
	if json.Unmarshal(raw, &batch) != nil {
		return nil
	}
	var reports []map[string]any
	for _, entry := range batch {
		if entry.Type == "csp-violation" && entry.Body != nil {
			reports = append(reports, entry.Body)
			if len(reports) == maxCSPReportsPerRequest {
				break
			}
		}
	}
	return reports
}

// cspReportField returns the first of keys present in report as a string
// without control characters, truncated to maxCSPReportField runes.
func cspReportField(report map[string]any, keys ...string) string {
	for _, key := range keys {
		var value string
		switch v := report[key].(type) {
		case string:
			value = v
		case float64:
			value = strconv.FormatFloat(v, 'f', -1, 64)
		default:
			continue
		}
		var b strings.Builder
		kept := 0
		for _, c := range value {
			if c < 0x20 || c == 0x7f || (c >= 0x80 && c < 0xa0) {
				continue
			}
			if kept == maxCSPReportField {
				break
			}
			b.WriteRune(c)
			kept++
		}
		return b.String()
	}
	return ""
}
