// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"log/slog"
	"strings"
	"testing"
)

// canonicalURLArchive builds a one-page archive carrying the given canonical URL
// in its SEO block.
func canonicalURLArchive(authorEmail, slug, canonicalURL string) *ExportData {
	return &ExportData{
		Version: ExportVersion,
		Pages: []ExportPage{{
			ID: 1, Title: "Canonical", Slug: slug, Status: "draft",
			AuthorEmail: authorEmail,
			SEO:         &ExportPageSEO{CanonicalURL: canonicalURL},
		}},
	}
}

// TestImportClearsUnusableCanonicalURL covers the third write path into
// pages.canonical_url. An export archive bypasses both the admin form and the
// v2 API, and the pre-existing import checks only resolved embedded media UUIDs
// — a scripting scheme or a reference with no host went straight into the table.
//
// The archive still imports. Every release before this rule shipped let the
// admin form store any string and the exporter writes the column out verbatim,
// so refusing would make an instance's own backups unrestorable. The offending
// value is cleared and reported as a warning instead.
//
// Bug state: drop the normalizeImportedPageCanonicalURLs call from
// importWithPreCommit and every subtest reports the value that got through.
func TestImportClearsUnusableCanonicalURL(t *testing.T) {
	rejected := map[string]string{
		"javascript scheme": "javascript:alert(1)",
		"data scheme":       "data:text/html;base64,PHNjcmlwdD4=",
		"relative path":     "/about",
		"scheme relative":   "//cdn.example.com/a",
		"credentials":       "https://user:pass@example.com/a",
	}

	for name, raw := range rejected {
		t.Run(name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()

			slug := "canonical-" + strings.ReplaceAll(name, " ", "-")
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			result, err := importer.Import(ts.Ctx,
				canonicalURLArchive(ts.User.Email, slug, raw),
				ImportOptions{ConflictStrategy: ConflictSkip, ImportPages: true},
			)
			if err != nil {
				t.Fatalf("Import(canonical_url=%q) error = %v, want the archive to land", raw, err)
			}
			if result == nil || !result.Success {
				t.Fatalf("Import(canonical_url=%q) result = %+v, want a successful result", raw, result)
			}

			page, lookupErr := ts.Queries.GetPageBySlug(ts.Ctx, slug)
			if lookupErr != nil {
				t.Fatalf("page lookup after import = %v", lookupErr)
			}
			if page.CanonicalUrl != "" {
				t.Errorf("stored canonical_url = %q, want it cleared", page.CanonicalUrl)
			}

			// A silent clear would be data loss the operator never sees.
			var reported bool
			for _, warning := range result.Warnings {
				if warning.ID == slug && strings.Contains(warning.Message, "canonical URL") {
					reported = true
				}
			}
			if !reported {
				t.Errorf("clearing canonical_url=%q produced no warning; warnings = %+v", raw, result.Warnings)
			}
		})
	}
}

// TestImportAcceptsAbsoluteCanonicalURL pins that the check does not disturb the
// archives an operator actually restores, and that a valid value is stored
// exactly as validated — trimmed. Storing the untrimmed input while validating
// the trimmed one is the defect this whole change set exists to remove, so the
// whitespace case is the point of this test, not decoration.
func TestImportAcceptsAbsoluteCanonicalURL(t *testing.T) {
	accepted := map[string]string{
		"empty":                 "",
		"https":                 "https://example.com/original",
		"http":                  "http://example.com/original",
		"whitespace is trimmed": "  https://example.com/padded  ",
	}

	for name, raw := range accepted {
		t.Run(name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()

			slug := "ok-" + strings.ReplaceAll(name, " ", "-")
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			result, err := importer.Import(ts.Ctx,
				canonicalURLArchive(ts.User.Email, slug, raw),
				ImportOptions{ConflictStrategy: ConflictSkip, ImportPages: true},
			)
			if err != nil {
				t.Fatalf("Import(canonical_url=%q) error = %v, want it accepted", raw, err)
			}
			if result == nil || !result.Success {
				t.Fatalf("Import(canonical_url=%q) result = %+v, want a successful result", raw, result)
			}
			if len(result.Warnings) != 0 {
				t.Errorf("Import(canonical_url=%q) warned about a valid value: %+v", raw, result.Warnings)
			}
			page, lookupErr := ts.Queries.GetPageBySlug(ts.Ctx, slug)
			if lookupErr != nil {
				t.Fatalf("page lookup after an accepted import = %v", lookupErr)
			}
			if want := strings.TrimSpace(raw); page.CanonicalUrl != want {
				t.Errorf("stored canonical_url = %q, want %q", page.CanonicalUrl, want)
			}
		})
	}
}
