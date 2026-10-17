// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// htmlComment matches an HTML comment, which templ and html/template both send
// to the browser.
var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// TestRenderedCommentsDoNotNameSourceFiles fails when a template carries an
// HTML comment that names a Go source file. Such notes are written for
// maintainers but ship in every response; the admin shell's note about
// sri_test.go reached public-role users once /account shared the shell
// (security audit 2026-10-17, I-1). Put maintainer notes in Go comments.
//
// Bug state: put the "checked mechanically by sri_test.go" note back into
// layout.templ as an HTML comment and this names the file.
func TestRenderedCommentsDoNotNameSourceFiles(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	scanned := 0
	var offenders []string
	for _, tree := range []string{"internal", "web", "modules"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "dist" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".templ") && !strings.HasSuffix(path, ".html") {
				return nil
			}
			source, err := os.ReadFile(path) // #nosec G304 -- paths come from walking the repository tree
			if err != nil {
				return err
			}
			scanned++
			text := string(source)
			for _, loc := range htmlComment.FindAllStringIndex(text, -1) {
				comment := text[loc[0]:loc[1]]
				if strings.Contains(comment, ".go") {
					rel, _ := filepath.Rel(root, path)
					line := strings.Count(text[:loc[0]], "\n") + 1
					offenders = append(offenders, rel+":"+strconv.Itoa(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}
	if scanned == 0 {
		t.Fatal("no templates scanned; the layout moved and this test is vacuous")
	}
	if len(offenders) > 0 {
		t.Errorf("these HTML comments name Go source files and are sent to every visitor; "+
			"move the note into a Go comment:\n  %s", strings.Join(offenders, "\n  "))
	}
}
