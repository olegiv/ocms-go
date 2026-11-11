// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/olegiv/ocms-go/internal/security"
)

// TestPageEditorValidStylesMatchSanitizer checks the rendered page form hands
// TinyMCE exactly the page sanitizer's style allowlist while
// OCMS_SANITIZE_PAGE_HTML is on, and nothing while it is off. With the list
// missing, the editor would show formatting a sanitizing save removes (issue
// #202); with it present while sanitizing is off, the editor would drop styles
// the server keeps.
func TestPageEditorValidStylesMatchSanitizer(t *testing.T) {
	renderScripts := func(sanitizes bool) string {
		t.Helper()
		var buf bytes.Buffer
		if err := pageFormScripts(nil, PageFormViewData{SanitizesPageHTML: sanitizes}).Render(context.Background(), &buf); err != nil {
			t.Fatalf("render pageFormScripts: %v", err)
		}
		return buf.String()
	}

	rendered := renderScripts(true)
	encoded, ok := tinymceConfigAttr(rendered, "data-valid-styles")
	if !ok {
		t.Fatal("#tinymce-config has no data-valid-styles attribute while sanitizing")
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatalf("data-valid-styles = %q is not a JSON object: %v", encoded, err)
	}
	want := security.PageEditorValidStyles()
	if len(want) == 0 || !maps.Equal(got, want) {
		t.Errorf("data-valid-styles = %v, want security.PageEditorValidStyles() = %v", got, want)
	}
	const initOption = "{ valid_styles: JSON.parse(document.getElementById('tinymce-config').dataset.validStyles) }"
	if !strings.Contains(rendered, initOption) {
		t.Errorf("tinymce.init does not pass the allowlist; want %q in the page form scripts", initOption)
	}

	if unexpected, present := tinymceConfigAttr(renderScripts(false), "data-valid-styles"); present {
		t.Errorf("data-valid-styles = %q rendered while page sanitizing is off", unexpected)
	}
}

// tinymceConfigAttr returns the value of attribute key on the #tinymce-config
// element, and whether the element carries it.
func tinymceConfigAttr(fragment, key string) (string, bool) {
	z := html.NewTokenizer(strings.NewReader(fragment))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return "", false
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			attrs := map[string]string{}
			for _, attr := range tok.Attr {
				attrs[attr.Key] = attr.Val
			}
			if attrs["id"] != "tinymce-config" {
				continue
			}
			val, ok := attrs[key]
			return val, ok
		}
	}
}

// The editor configuration security.pageStyleRules was last checked against.
// The plugins, toolbar, menus and image dialog decide which inline styles
// TinyMCE writes.
const (
	reviewedTinyMCEVersion = "8.10.2"
	reviewedEditorSurface  = "menubar: true,\n" +
		"plugins: ['advlist','autolink','lists','link','image','charmap','preview','anchor','searchreplace','visualblocks','code','fullscreen','insertdatetime','media','table','help','wordcount','codesample'],\n" +
		"toolbar: 'undo redo | blocks | bold italic forecolor | alignleft aligncenter alignright alignjustify | bullist numlist outdent indent | link customimage media codesample | code | removeformat | help',\n" +
		"image_advtab: true,"
)

// TestPageEditorSurfaceMatchesReviewedStyleRules fails when the editor's
// plugins, toolbar, menus, image dialog or TinyMCE version change. Any of
// those can make the editor write inline styles pageStyleRules does not list,
// which a sanitizing save then strips without a word (issue #202). When it
// fails: capture what the new controls write, add it to
// TestSanitizePageHTMLKeepsEditorFormatting, extend pageStyleRules, then
// update the constants above.
func TestPageEditorSurfaceMatchesReviewedStyleRules(t *testing.T) {
	var buf bytes.Buffer
	if err := pageFormScripts(nil, PageFormViewData{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render pageFormScripts: %v", err)
	}
	rendered := buf.String()
	for line := range strings.SplitSeq(reviewedEditorSurface, "\n") {
		if !strings.Contains(rendered, line) {
			t.Errorf("tinymce.init no longer contains %q; review security.pageStyleRules against the new editor configuration", line)
		}
	}

	// go test runs in the package directory, three levels below the module root.
	lockFile, err := os.ReadFile(filepath.Join("..", "..", "..", "package-lock.json")) // #nosec G304 -- fixed repo file
	if err != nil {
		t.Fatalf("read package-lock.json: %v", err)
	}
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(lockFile, &lock); err != nil {
		t.Fatalf("parse package-lock.json: %v", err)
	}
	if got := lock.Packages["node_modules/tinymce"].Version; got != reviewedTinyMCEVersion {
		t.Errorf("TinyMCE is %q, pageStyleRules was reviewed against %q; review the rules against the new version's formats", got, reviewedTinyMCEVersion)
	}
}
