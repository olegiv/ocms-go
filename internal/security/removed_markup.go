// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"slices"
	"strings"

	"golang.org/x/net/html"
)

// RemovedMarkup lists what sanitizing dropped on the way from before to
// after: whole elements ("<iframe>"), attributes (`<a onclick="…">`) and
// inline style declarations (`<p style="position: …">`).
//
// Counts are per tag name, not per element: once any element of a tag was
// dropped, attribute and style losses on that tag are not listed, because
// the counts cannot tell whether they belonged to the dropped element or to
// one that survived. The dropped element itself is always listed, so the
// result is never empty when markup was lost.
//
// A plain string comparison is not good enough. The sanitizer also rewrites
// markup in place: it adds rel="nofollow" to every link and normalizes
// character entities and declaration spacing, so "output != input" holds for
// almost every body and says nothing about content loss. Comparing element,
// attribute and style-declaration multisets reports only what disappeared.
func RemovedMarkup(before, after string) []string {
	if before == after {
		return nil
	}
	had, kept := markupInventory(before), markupInventory(after)

	droppedElements := map[string]bool{}
	var removed []string
	for tag, n := range had.elements {
		if kept.elements[tag] < n {
			droppedElements[tag] = true
			removed = append(removed, "<"+tag+">")
		}
	}
	for key, n := range had.attributes {
		if tag, attr, _ := strings.Cut(key, " "); !droppedElements[tag] && kept.attributes[key] < n {
			removed = append(removed, "<"+tag+" "+attr+`="…">`)
		}
	}
	for key, n := range had.styles {
		if tag, property, _ := strings.Cut(key, " "); !droppedElements[tag] && kept.styles[key] < n {
			removed = append(removed, "<"+tag+` style="`+property+`: …">`)
		}
	}
	slices.Sort(removed)
	return removed
}

// inventory tallies elements, element attributes and style declarations.
type inventory struct {
	elements   map[string]int // "p"
	attributes map[string]int // "a href"; style is counted per declaration when it has any
	styles     map[string]int // "p text-align"
}

func markupInventory(fragment string) inventory {
	inv := inventory{elements: map[string]int{}, attributes: map[string]int{}, styles: map[string]int{}}
	z := html.NewTokenizer(strings.NewReader(fragment))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return inv
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			inv.elements[tag]++
			for hasAttr {
				var key, val []byte
				key, val, hasAttr = z.TagAttr()
				if string(key) != "style" {
					inv.attributes[tag+" "+string(key)]++
					continue
				}
				// The policy keeps a style attribute that still holds an
				// allowlisted property, so count each declaration. A style
				// with no declaration at all counts as the attribute itself.
				declarations := 0
				for declaration := range strings.SplitSeq(string(val), ";") {
					property, _, found := strings.Cut(declaration, ":")
					if property = strings.ToLower(strings.TrimSpace(property)); found && property != "" {
						inv.styles[tag+" "+property]++
						declarations++
					}
				}
				if declarations == 0 {
					inv.attributes[tag+" style"]++
				}
			}
		}
	}
}
