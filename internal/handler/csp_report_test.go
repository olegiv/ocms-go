// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureLogs routes slog output to a buffer for the rest of the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func postCSPReport(body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, RouteCSPReport, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/csp-report")
	rr := httptest.NewRecorder()
	CSPReport(rr, req)
	return rr
}

// TestCSPReportLogsBothFormats checks that violations sent through report-uri
// and through the Reporting API are both logged with their key fields.
func TestCSPReportLogsBothFormats(t *testing.T) {
	logs := captureLogs(t)

	legacy := `{"csp-report":{"document-uri":"https://site.example/about","violated-directive":"script-src-elem","effective-directive":"script-src-elem","blocked-uri":"https://evil.example/x.js","source-file":"https://site.example/about","line-number":42,"disposition":"enforce"}}`
	if rr := postCSPReport(legacy); rr.Code != http.StatusNoContent {
		t.Fatalf("legacy report: status %d, want 204", rr.Code)
	}
	batch := `[{"type":"csp-violation","body":{"documentURL":"https://site.example/","effectiveDirective":"frame-src","blockedURL":"https://frames.example/","lineNumber":7,"disposition":"enforce"}},{"type":"deprecation","body":{"id":"x"}}]`
	if rr := postCSPReport(batch); rr.Code != http.StatusNoContent {
		t.Fatalf("Reporting API batch: status %d, want 204", rr.Code)
	}

	out := logs.String()
	for _, want := range []string{
		"blocked=https://evil.example/x.js", "directive=script-src-elem", "line=42",
		"blocked=https://frames.example/", "directive=frame-src", "line=7",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "csp violation"); got != 2 {
		t.Errorf("logged %d violations, want 2 (the deprecation report is not one)", got)
	}
}

// TestCSPReportBoundsInput checks the limits that keep an anonymous endpoint
// from flooding or corrupting the log: body size, report count, field length
// and control characters.
func TestCSPReportBoundsInput(t *testing.T) {
	logs := captureLogs(t)

	if rr := postCSPReport(`{"csp-report":{"blocked-uri":"` + strings.Repeat("a", maxCSPReportBytes) + `"}}`); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: status %d, want 413", rr.Code)
	}
	if rr := postCSPReport(`not json`); rr.Code != http.StatusBadRequest {
		t.Errorf("malformed body: status %d, want 400", rr.Code)
	}

	entry := `{"type":"csp-violation","body":{"blockedURL":"https://x.example/"}}`
	postCSPReport("[" + strings.TrimSuffix(strings.Repeat(entry+",", 15), ",") + "]")
	if got := strings.Count(logs.String(), "csp violation"); got != maxCSPReportsPerRequest {
		t.Errorf("logged %d reports from one request, want %d", got, maxCSPReportsPerRequest)
	}

	field := cspReportField(map[string]any{"blocked-uri": "https://a.example/\nforged=1\x1b[31m" + strings.Repeat("b", 400)}, "blocked-uri")
	if strings.ContainsAny(field, "\n\x1b") {
		t.Errorf("control characters survived: %q", field)
	}
	if n := len([]rune(field)); n != maxCSPReportField {
		t.Errorf("field length %d, want %d", n, maxCSPReportField)
	}
}
