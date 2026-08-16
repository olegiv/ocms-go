// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"database/sql"
	"errors"
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

// TestImportRejectsUnusableCanonicalURL covers the third write path into
// pages.canonical_url. An export archive is an untrusted payload that bypasses
// both the admin form and the v2 API, and the pre-existing import checks only
// resolved embedded media UUIDs — a scripting scheme or a reference with no host
// went straight into the table.
//
// Bug state: drop the validateImportedPageCanonicalURLs call from
// importWithPreCommit and every subtest reports a page that should not exist.
func TestImportRejectsUnusableCanonicalURL(t *testing.T) {
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
			if err == nil {
				t.Fatalf("Import(canonical_url=%q) succeeded, want it refused", raw)
			}
			if result == nil || result.Success {
				t.Fatalf("Import(canonical_url=%q) result = %+v, want an unsuccessful result", raw, result)
			}
			if !strings.Contains(err.Error(), "canonical URL") {
				t.Errorf("Import(canonical_url=%q) error = %v, want it to name the offending field", raw, err)
			}
			if _, lookupErr := ts.Queries.GetPageBySlug(ts.Ctx, slug); !errors.Is(lookupErr, sql.ErrNoRows) {
				t.Errorf("page lookup after a refused import = %v, want sql.ErrNoRows", lookupErr)
			}
		})
	}
}

// TestImportAcceptsAbsoluteCanonicalURL pins that the check does not block the
// archives an operator actually restores, including one whose pages carry no
// canonical URL at all.
func TestImportAcceptsAbsoluteCanonicalURL(t *testing.T) {
	accepted := map[string]string{
		"empty": "",
		"https": "https://example.com/original",
		"http":  "http://example.com/original",
	}

	for name, raw := range accepted {
		t.Run(name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()

			slug := "ok-" + name
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
			page, lookupErr := ts.Queries.GetPageBySlug(ts.Ctx, slug)
			if lookupErr != nil {
				t.Fatalf("page lookup after an accepted import = %v", lookupErr)
			}
			if page.CanonicalUrl != raw {
				t.Errorf("stored canonical_url = %q, want %q", page.CanonicalUrl, raw)
			}
		})
	}
}
