// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// menuWithLink returns an archive whose only menu nests one link under a
// relative parent, so the check must walk children.
func menuWithLink(slug, url string) *ExportData {
	return &ExportData{Version: ExportVersion, Menus: []ExportMenu{{Name: slug, Slug: slug,
		Items: []ExportMenuItem{{Title: "Parent", URL: "/ok", IsActive: true,
			Children: []ExportMenuItem{{Title: "Link", URL: url, IsActive: true}}}}}}}
}

// TestImportRefusesUnsafeMenuURLs covers the preview and the import: a menu
// link with a scheme menus do not allow is refused, a deselected menu section
// is not checked, and relative links the menu form would now refuse still
// import, so older sites' exports round-trip.
func TestImportRefusesUnsafeMenuURLs(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())

	for i, raw := range []string{"javascript:alert(1)", "JaVaScRiPt:alert(1)", " javascript:alert(1)", "data:text/html,x", "vbscript:msgbox"} {
		data := menuWithLink(fmt.Sprintf("unsafe-%d", i), raw)
		preview, err := importer.ValidateData(ts.Ctx, data)
		if err != nil || preview.Valid {
			t.Errorf("ValidateData(%q) = (%+v, %v), want an invalid preview", raw, preview, err)
		}
		opts := DefaultImportOptions()
		if _, err := importer.Import(ts.Ctx, data, opts); err == nil || !strings.Contains(err.Error(), "URL must be relative") {
			t.Errorf("Import(%q) error = %v, want the menu URL refused", raw, err)
		}
		opts.ImportMenus = false
		if _, err := importer.Import(ts.Ctx, data, opts); err != nil {
			t.Errorf("Import(%q) with menus deselected error = %v", raw, err)
		}
	}

	for i, raw := range []string{"about", "?t=10:30", "#a:b", "MAILTO:team@example.com", "https://example.com/"} {
		slug := fmt.Sprintf("kept-%d", i)
		if _, err := importer.Import(ts.Ctx, menuWithLink(slug, raw), DefaultImportOptions()); err != nil {
			t.Errorf("Import(%q) error = %v, want it imported", raw, err)
			continue
		}
		menu, err := ts.Queries.GetMenuBySlug(ts.Ctx, slug)
		if err != nil {
			t.Fatalf("GetMenuBySlug(%q): %v", slug, err)
		}
		items, err := ts.Queries.ListMenuItems(ts.Ctx, menu.ID)
		if err != nil || len(items) != 2 {
			t.Fatalf("menu %q items = %d, %v; want 2", slug, len(items), err)
		}
	}
}

// TestImportConfigNormalizesURLValues applies the admin form's site_url and
// site_logo checks on import: valid values are stored normalized, and an
// invalid one is skipped with a notice while the import still succeeds.
func TestImportConfigNormalizesURLValues(t *testing.T) {
	for _, tc := range []struct {
		name, key, value, want string // want "" means skipped with a notice
	}{
		{"site_url origin", "site_url", " https://example.com/ ", "https://example.com"},
		{"site_url with path", "site_url", "https://example.com/blog", ""},
		{"site_url without scheme", "site_url", "example.com:8443", ""},
		{"site_logo path", "site_logo", " /uploads/logo.png ", "/uploads/logo.png"},
		{"site_logo data URI", "site_logo", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4="},
		{"site_logo javascript", "site_logo", "javascript:alert(1)", ""},
		{"other key unchanged", "site_tagline", " kept as is ", " kept as is "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			opts := DefaultImportOptions()
			opts.ConflictStrategy = ConflictOverwrite
			data := &ExportData{Version: ExportVersion, Config: map[string]string{tc.key: tc.value}}
			before, beforeErr := ts.Queries.GetConfigByKey(ts.Ctx, tc.key)
			result, err := importer.Import(ts.Ctx, data, opts)
			if err != nil {
				t.Fatalf("Import() error = %v", err)
			}
			cfg, lookupErr := ts.Queries.GetConfigByKey(ts.Ctx, tc.key)
			if tc.want != "" {
				if lookupErr != nil || cfg.Value != tc.want {
					t.Fatalf("stored %s = %q, %v; want %q", tc.key, cfg.Value, lookupErr, tc.want)
				}
				return
			}
			if cfg.Value != before.Value || errors.Is(lookupErr, sql.ErrNoRows) != errors.Is(beforeErr, sql.ErrNoRows) {
				t.Errorf("invalid %s changed the stored value from %q to %q", tc.key, before.Value, cfg.Value)
			}
			noticed := false
			for _, notice := range result.Notices {
				noticed = noticed || (notice.Entity == "config" && notice.ID == tc.key)
			}
			if !noticed {
				t.Errorf("no notice for the skipped %s value; notices = %+v", tc.key, result.Notices)
			}
		})
	}
}
