// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// htmxConfigPattern extracts the JSON object from the HTMX 4 configuration
// meta tag in either the HTML template or templ source.
var htmxConfigPattern = regexp.MustCompile(`name="htmx-config"[^\n]*?(\{"noSwap"[^}]*\})`)

// TestHTMX4ConfigSuppressesErrorSwaps guards the HTMX 2 compatibility mode.
//
// HTMX 4 swaps response bodies for 4xx and 5xx statuses by default, but the
// admin relies on the pre-4 behavior: error responses are surfaced as toast
// notifications and do not replace the target element.
func TestHTMX4ConfigSuppressesErrorSwaps(t *testing.T) {
	root := repoRoot(t)

	for _, layout := range []string{
		"internal/views/admin/layout.templ",
		"web/templates/layouts/base.html",
	} {
		t.Run(layout, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(root, layout)) // #nosec G304 -- fixed repo file
			if err != nil {
				t.Fatalf("read %s: %v", layout, err)
			}

			match := htmxConfigPattern.FindSubmatch(source)
			if match == nil {
				t.Fatalf("%s is missing the HTMX 4 noSwap configuration", layout)
			}

			got := string(match[1])
			want := `{"noSwap": [204, 304, "4xx", "5xx"]}`
			if got != want {
				t.Fatalf("%s has HTMX 4 noSwap config %q; want %q", layout, got, want)
			}
		})
	}
}

// TestPublicLayoutDoesNotLoadHTMX keeps HTMX scoped to the admin UI.
//
// The public frontend previously loaded HTMX even though no public template
// used an hx-* attribute. After the HTMX 4 upgrade, that unnecessary download
// is removed; this test keeps it from returning silently.
func TestPublicLayoutDoesNotLoadHTMX(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "internal/handler/frontend_layout.templ")
	source, err := os.ReadFile(path) // #nosec G304 -- fixed repo file
	if err != nil {
		t.Fatalf("read public layout: %v", err)
	}

	if containsHTMX := regexp.MustCompile(`/static/dist/js/htmx\.min\.js`).Match(source); containsHTMX {
		t.Fatal("public frontend layout still loads HTMX; it should remain admin-only")
	}
}
