// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin_test

import (
	"crypto/sha512"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/web"
)

// layoutsWithVendoredScripts are the layouts that load vendored JavaScript with
// a Subresource Integrity hash. Paths are relative to the repository root.
// Every layout that loads vendored JavaScript belongs here. Listing only the
// two admin layouts is what let the public frontend and three themes ship
// third-party scripts with no integrity attribute at all: the test passed
// because it never looked at them.
var layoutsWithVendoredScripts = []string{
	"internal/views/admin/layout.templ",
	"web/templates/layouts/base.html",
	"internal/handler/frontend_layout.templ",
	"internal/themes/default/templates/layouts/base.html",
	"internal/themes/developer/templates/layouts/base.html",
	"custom/themes/starter/templates/layouts/base.html",
}

// vendoredScripts are the third-party libraries copied out of node_modules by
// the copy-deps npm script. Their bytes change whenever the dependency is
// upgraded, which is exactly the drift this test exists to catch, so each must
// carry an integrity hash and a cache-busted URL.
//
// First-party scripts (admin-core.js, media-dropzone.js, the templUI component
// bundles) are deliberately excluded: they ship with the repo rather than with
// an npm upgrade, and pinning a hash to a file we edit ourselves would be noise.
var vendoredScripts = map[string]bool{
	"/static/dist/js/htmx.min.js":            true,
	"/static/dist/js/alpine.min.js":          true,
	"/static/dist/js/alpine-sort.min.js":     true,
	"/static/dist/js/alpine-collapse.min.js": true,
	// Concatenated from node_modules/prismjs by the copy-deps script, so its
	// bytes change on a prismjs upgrade exactly like the others.
	"/static/dist/js/prism-bundle.min.js": true,
}

// scriptTagPattern matches an opening <script> tag. The asset path is located
// inside the tag separately, because the three layouts spell it three ways:
// a bare src="/path", templ's src={ utils.ScriptURL("/path") }, and
// html/template's src="{{ scriptURL "/path" }}".
var scriptTagPattern = regexp.MustCompile(`<script[^>]*>`)

// assetPathPattern finds a dist asset path anywhere inside a script tag.
var assetPathPattern = regexp.MustCompile(`/static/dist/js/[a-zA-Z0-9._-]+`)

// integrityPattern extracts an sha384 SRI value from a script tag.
var integrityPattern = regexp.MustCompile(`integrity="(sha384-[A-Za-z0-9+/=]+)"`)

// TestVendoredScriptIntegrityHashes is the guard against a stale Subresource
// Integrity hash silently disabling a vendored library.
//
// This is not hypothetical. The htmx hash was set when package.json pinned
// 2.0.8 and was never regenerated when the dependency was upgraded to 2.0.10,
// so browsers refused to execute htmx for about five weeks — every hx-* feature
// in the admin UI was inert, and nothing failed loudly because HTTP-level tests
// do not enforce SRI. Alpine had the identical bug before it (ba7b310).
//
// The hashes are recomputed from web.Static, the same embedded bytes the server
// serves, so this fails the moment `make assets` pulls a different build.
func TestVendoredScriptIntegrityHashes(t *testing.T) {
	root := repoRoot(t)
	checked := 0

	for _, layout := range layoutsWithVendoredScripts {
		path := filepath.Join(root, layout)
		source, err := os.ReadFile(path) // #nosec G304 -- fixed list of repo files
		if err != nil {
			t.Fatalf("failed to read %s: %v", layout, err)
		}

		tags := scriptTagPattern.FindAllString(string(source), -1)
		if len(tags) == 0 {
			t.Errorf("%s: found no script tags at all; has the layout moved?", layout)
			continue
		}

		for _, tag := range tags {
			assetPath := assetPathPattern.FindString(tag)
			if assetPath == "" || !vendoredScripts[assetPath] {
				continue
			}

			declared := integrityPattern.FindStringSubmatch(tag)
			if declared == nil {
				t.Errorf("%s: vendored script %q has no integrity attribute; "+
					"pin it, or remove it from vendoredScripts if it is no longer third-party",
					layout, assetPath)
				continue
			}

			want, err := embeddedScriptIntegrity(assetPath)
			if err != nil {
				t.Errorf("%s: %v", layout, err)
				continue
			}

			if declared[1] != want {
				t.Errorf("%s: integrity for %s is stale — the browser will refuse to "+
					"execute it and every feature using that library goes silently dead.\n"+
					"  declared: %s\n  actual:   %s\nReplace the declared value with the actual one.",
					layout, assetPath, declared[1], want)
			}
			checked++
		}
	}

	if checked == 0 {
		t.Fatal("no integrity hashes were checked; the regex or the layout list is broken")
	}
}

// embeddedScriptIntegrity returns the SRI value for an asset as served, read
// from the embedded filesystem rather than from disk.
func embeddedScriptIntegrity(assetPath string) (string, error) {
	embedded := "static" + strings.TrimPrefix(assetPath, "/static")

	data, err := web.Static.ReadFile(embedded)
	if err != nil {
		return "", err
	}

	sum := sha512.Sum384(data)
	return "sha384-" + base64.StdEncoding.EncodeToString(sum[:]), nil
}

// TestVendoredScriptsAreCacheBusted keeps a corrected hash from being defeated
// by a year-old cached copy.
//
// /static/dist/* is served with a one-year max-age and the URL carries no
// version, so a browser that cached the previous build keeps using it. Every
// vendored script must therefore go through utils.ScriptURL, which appends a
// per-process version. Without this, fixing an integrity hash does nothing for
// anyone who already loaded the old file.
func TestVendoredScriptsAreCacheBusted(t *testing.T) {
	root := repoRoot(t)

	for _, layout := range layoutsWithVendoredScripts {
		source, err := os.ReadFile(filepath.Join(root, layout)) // #nosec G304 -- fixed list of repo files
		if err != nil {
			t.Fatalf("failed to read %s: %v", layout, err)
		}

		for _, line := range strings.Split(string(source), "\n") {
			if !strings.Contains(line, "<script") {
				continue
			}
			asset := ""
			for path := range vendoredScripts {
				if strings.Contains(line, path) {
					asset = path
					break
				}
			}
			if asset == "" {
				continue
			}
			// templ uses utils.ScriptURL(...); html/template uses the scriptURL func.
			if !strings.Contains(line, "ScriptURL(") && !strings.Contains(line, "scriptURL ") {
				t.Errorf("%s: %s is loaded without cache busting, so a browser holding the "+
					"previous build keeps it — and then fails the new integrity hash:\n  %s",
					layout, asset, strings.TrimSpace(line))
			}
		}
	}
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to determine working directory: %v", err)
	}

	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate the repository root")
	return ""
}
