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

	"github.com/olegiv/ocms-go/internal/testutil/repotree"
)

// alertOpen matches the line that opens a templUI alert block and captures
// its indentation.
var alertOpen = regexp.MustCompile(`^(\t*)@alert\.Alert\(.*\) \{$`)

// TestAlertContentSitsInTheContentColumn fails when a templUI alert has a
// child that its grid puts in the icon column. The alert is a two-column
// grid, 0 wide (1rem after a leading icon) and 1fr: alert.Title and
// alert.Description place themselves in the second column, and after a
// leading icon one more child fills it. Any other child lands in the first
// column and wraps at every word, as the two-factor page's notices and the
// form builder's captcha notice did.
//
// Bug state: make { pc.T("mfa.not_configured") } the only child of its alert
// in security.templ again and this reports it.
func TestAlertContentSitsInTheContentColumn(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	alerts := 0
	var offenders []string
	for _, tree := range []string{"internal", "modules", "custom"} {
		treeRoot := filepath.Join(root, tree)
		err := filepath.WalkDir(treeRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if repotree.Skip(treeRoot, path, d) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".templ") {
				return nil
			}
			source, err := os.ReadFile(path) // #nosec G304 -- paths come from walking the repository tree
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			lines := strings.Split(string(source), "\n")
			for i, line := range lines {
				if !strings.Contains(line, "@alert.Alert(") {
					continue
				}
				alerts++
				where := rel + ":" + strconv.Itoa(i+1)
				m := alertOpen.FindStringSubmatch(line)
				if m == nil {
					offenders = append(offenders, where+" (opens the alert in a form this test cannot read)")
					continue
				}
				if !alertChildrenFit(alertChildren(lines[i+1:], m[1])) {
					offenders = append(offenders, where)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}
	if alerts == 0 {
		t.Fatal("no alerts found; the templates moved and this test is vacuous")
	}
	if len(offenders) > 0 {
		t.Errorf("these alerts put content in the icon column, where it wraps at every word; "+
			"wrap it in alert.Description, or lead with an icon and one element:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// alertChildren returns the first line of each direct child of an alert
// whose opening line is indented by indent: the lines one tab deeper, up to
// the brace that closes the alert, leaving out closing tags and braces.
func alertChildren(body []string, indent string) []string {
	var children []string
	for _, line := range body {
		if line == indent+"}" {
			break
		}
		if !strings.HasPrefix(line, indent+"\t") || strings.HasPrefix(line, indent+"\t\t") {
			continue
		}
		child := strings.TrimSpace(line)
		if child == "" || strings.HasPrefix(child, "</") || strings.HasPrefix(child, "}") || strings.HasPrefix(child, "//") {
			continue
		}
		children = append(children, child)
	}
	return children
}

// alertChildrenFit reports whether every child lands in the content column:
// a leading icon followed by one child, or only alert.Title and
// alert.Description, with or without a leading icon.
func alertChildrenFit(children []string) bool {
	if len(children) > 0 && strings.HasPrefix(children[0], "@icon.") {
		children = children[1:]
		if len(children) == 1 {
			return true
		}
	}
	if len(children) == 0 {
		return false
	}
	for _, child := range children {
		if !strings.HasPrefix(child, "@alert.Title(") && !strings.HasPrefix(child, "@alert.Description(") {
			return false
		}
	}
	return true
}
