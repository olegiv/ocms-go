// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// firstPartyScripts ship with the repository rather than with an npm upgrade,
// so pinning a hash to a file we edit ourselves would be noise on every commit.
//
// This list is the ONLY thing exempt from the integrity requirement, and that
// inversion is the point. The previous design listed the *protected* files, so
// a newly added vendored script was unprotected AND invisible — which is how
// tinymce, klaro and both swagger-ui bundles shipped with no integrity
// attribute through two audit passes. Failing closed means the cost of
// forgetting is a red test, not a silent gap.
var firstPartyScripts = map[string]string{
	"/static/dist/js/admin-core.js":     "hand-written admin behaviour",
	"/static/dist/js/media-dropzone.js": "hand-written upload widget",
	"/static/dist/js/menu-builder.js":   "hand-written menu builder",
	"/static/dist/js/lightbox.js":       "hand-written image lightbox",
	// templUI component bundles, vendored into the repo by the templui CLI and
	// edited in place rather than tracked as an npm dependency.
	"/static/dist/js/avatar.min.js":    "templUI component",
	"/static/dist/js/checkbox.min.js":  "templUI component",
	"/static/dist/js/dialog.min.js":    "templUI component",
	"/static/dist/js/dropdown.min.js":  "templUI component",
	"/static/dist/js/input.min.js":     "templUI component",
	"/static/dist/js/label.min.js":     "templUI component",
	"/static/dist/js/popover.min.js":   "templUI component",
	"/static/dist/js/selectbox.min.js": "templUI component",
	"/static/dist/js/sidebar.min.js":   "templUI component",
	"/static/dist/js/tabs.min.js":      "templUI component",
	"/static/dist/js/textarea.min.js":  "templUI component",
	"/static/dist/js/toast.min.js":     "templUI component",
}

// anyScriptTag matches an opening <script> tag across .templ, .html and Go
// string literals. Klaro is emitted from a Go string, which is why scanning
// templates alone was not enough to see it.
var anyScriptTag = regexp.MustCompile(`(?s)<script[^>]*>`)

// anyDistAsset finds a /static/dist asset path inside a tag, including nested
// directories such as js/tinymce/tinymce.min.js and the swagger-ui folder.
var anyDistAsset = regexp.MustCompile(`/static/dist/[a-zA-Z0-9._/-]+\.js`)

// scannedExtensions are the file types that can emit a script tag.
var scannedExtensions = map[string]bool{".templ": true, ".html": true, ".go": true}

// TestEveryVendoredScriptIsProtected fails when any script under /static/dist/
// is loaded without an integrity hash unless it is explicitly declared
// first-party.
//
// This replaces two hand-maintained allowlists that had to be remembered. The
// residual maintenance burden — adding a genuinely new first-party file to
// firstPartyScripts — fails safe: forgetting produces a test failure demanding
// a hash, never a silently unprotected third-party script.
func TestEveryVendoredScriptIsProtected(t *testing.T) {
	root := repoRoot(t)

	type hit struct {
		file  string
		line  int
		asset string
	}
	var unprotected []hit
	var stale []string
	scanned := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "bin", "data", "uploads", "wiki", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !scannedExtensions[filepath.Ext(path)] || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Generated templ output duplicates its .templ source.
		if strings.HasSuffix(path, "_templ.go") {
			return nil
		}

		data, readErr := os.ReadFile(path) // #nosec G304 -- repo walk
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}

		for _, tag := range anyScriptTag.FindAllIndex(data, -1) {
			snippet := string(data[tag[0]:tag[1]])
			asset := anyDistAsset.FindString(snippet)
			if asset == "" {
				continue
			}
			scanned++
			if _, ok := firstPartyScripts[asset]; ok {
				continue
			}
			line := 1 + strings.Count(string(data[:tag[0]]), "\n")

			declared := integrityPattern.FindStringSubmatch(snippet)
			if declared == nil {
				unprotected = append(unprotected, hit{rel, line, asset})
				continue
			}
			// Presence alone is not enough. Checking only that the attribute
			// exists would let a hash go stale across an npm upgrade, and the
			// browser then silently refuses to execute the script — which is
			// the failure this suite exists to catch.
			want, hashErr := embeddedScriptIntegrity(asset)
			if hashErr != nil {
				stale = append(stale, rel+":"+strconv.Itoa(line)+"  "+asset+
					" (cannot read from the embedded FS: "+hashErr.Error()+")")
				continue
			}
			if declared[1] != want {
				stale = append(stale, rel+":"+strconv.Itoa(line)+"  "+asset+
					"\n      declared: "+declared[1]+"\n      actual:   "+want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking repo: %v", err)
	}

	if scanned == 0 {
		t.Fatal("no /static/dist script tags found at all; the scanner is broken and this test is vacuous")
	}

	if len(unprotected) > 0 {
		var lines []string
		for _, h := range unprotected {
			lines = append(lines, h.file+":"+strconv.Itoa(h.line)+"  "+h.asset)
		}
		t.Errorf("these scripts load without an integrity hash. Add one (and "+
			"crossorigin=\"anonymous\"), or declare the file in firstPartyScripts "+
			"with a reason if it is not an npm dependency:\n  %s",
			strings.Join(lines, "\n  "))
	}

	if len(stale) > 0 {
		t.Errorf("these integrity hashes no longer match the shipped bytes, so the "+
			"browser will refuse to execute them:\n  %s", strings.Join(stale, "\n  "))
	}
}

// TestFirstPartyExemptionsAreNotNpmDependencies cross-checks the exemption list
// against what the copy-deps npm script actually writes into web/static/dist.
//
// Without this, the exemption list is self-certifying: someone could silence a
// failure on a genuinely vendored file by declaring it first-party, which is
// precisely the mistake the inversion exists to prevent.
func TestFirstPartyExemptionsAreNotNpmDependencies(t *testing.T) {
	root := repoRoot(t)

	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatalf("reading package.json: %v", err)
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatalf("parsing package.json: %v", err)
	}
	copyDeps, ok := pkg.Scripts["copy-deps"]
	if !ok {
		t.Fatal("package.json has no copy-deps script; this test can no longer tell " +
			"vendored files from first-party ones")
	}

	for asset, reason := range firstPartyScripts {
		base := filepath.Base(asset)
		// copy-deps names its destinations explicitly, so a first-party file
		// must not appear as one.
		if strings.Contains(copyDeps, "dist/js/"+base) || strings.Contains(copyDeps, "/"+base+" ") {
			t.Errorf("%s is declared first-party (%q) but copy-deps writes it from node_modules; "+
				"it is an npm dependency and needs an integrity hash", asset, reason)
		}
	}
}
