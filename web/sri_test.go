// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"crypto/sha512"
	"encoding/base64"
	"os"
	"regexp"
	"testing"
)

// sriPin is one <script> tag's local asset path and its pinned integrity hash.
type sriPin struct {
	src       string
	integrity string
}

var (
	scriptTagRe = regexp.MustCompile(`<script[^>]*>`)
	srcAttrRe   = regexp.MustCompile(`src="(/static/dist/[^"]+)"`)
	integrityRe = regexp.MustCompile(`integrity="(sha384-[^"]+)"`)
)

// extractSRIPins returns all (src, integrity) pairs for local /static/dist/
// scripts that carry an integrity attribute.
func extractSRIPins(html []byte) []sriPin {
	var pins []sriPin
	for _, tag := range scriptTagRe.FindAll(html, -1) {
		src := srcAttrRe.FindSubmatch(tag)
		integrity := integrityRe.FindSubmatch(tag)
		if src != nil && integrity != nil {
			pins = append(pins, sriPin{src: string(src[1]), integrity: string(integrity[1])})
		}
	}
	return pins
}

// TestSRIHashesMatchEmbeddedAssets fails when an integrity attribute in a
// layout template no longer matches the embedded asset it pins. This drift
// happens when `make assets` updates a vendored JS file (htmx, Alpine) but the
// sha384 pin in the layout is not regenerated: the browser then silently
// refuses to execute the script and the admin UI breaks without server errors.
func TestSRIHashesMatchEmbeddedAssets(t *testing.T) {
	sources := map[string][]byte{}

	baseHTML, err := Templates.ReadFile("templates/layouts/base.html")
	if err != nil {
		t.Fatalf("read base.html: %v", err)
	}
	sources["templates/layouts/base.html"] = baseHTML

	// The templ source is the authoritative authored file; its generated
	// counterpart carries the same literals into the binary.
	layoutTempl, err := os.ReadFile("../internal/views/admin/layout.templ")
	if err != nil {
		t.Fatalf("read layout.templ: %v", err)
	}
	sources["internal/views/admin/layout.templ"] = layoutTempl

	for name, html := range sources {
		pins := extractSRIPins(html)
		if len(pins) < 2 {
			t.Errorf("%s: expected at least 2 SRI-pinned scripts (htmx, Alpine), found %d", name, len(pins))
		}
		for _, pin := range pins {
			asset, err := Static.ReadFile(pin.src[1:]) // strip leading "/"
			if err != nil {
				t.Errorf("%s: pinned script %s not found in embedded static FS: %v", name, pin.src, err)
				continue
			}
			sum := sha512.Sum384(asset)
			want := "sha384-" + base64.StdEncoding.EncodeToString(sum[:])
			if pin.integrity != want {
				t.Errorf("%s: SRI drift for %s:\n  pinned: %s\n  actual: %s\nregenerate with: openssl dgst -sha384 -binary web%s | openssl base64 -A",
					name, pin.src, pin.integrity, want, pin.src)
			}
		}
	}
}
