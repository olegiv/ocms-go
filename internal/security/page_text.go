// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// inlineTextElements join their text to the surrounding words; every other
// element boundary separates words, so "<p>a</p><p>b</p>" reads "a b" while
// "<strong>B</strong>old" stays "Bold".
var inlineTextElements = map[atom.Atom]bool{
	atom.A: true, atom.Abbr: true, atom.Acronym: true, atom.B: true, atom.Bdi: true,
	atom.Bdo: true, atom.Big: true, atom.Cite: true, atom.Code: true, atom.Data: true,
	atom.Del: true, atom.Dfn: true, atom.Em: true, atom.Font: true, atom.I: true,
	atom.Ins: true, atom.Kbd: true, atom.Label: true, atom.Mark: true, atom.Nobr: true,
	atom.Q: true, atom.Rb: true, atom.Rp: true, atom.Rt: true, atom.Rtc: true,
	atom.Ruby: true, atom.S: true, atom.Samp: true, atom.Small: true, atom.Span: true,
	atom.Strike: true, atom.Strong: true, atom.Sub: true, atom.Sup: true, atom.Time: true,
	atom.Tt: true, atom.U: true, atom.Var: true, atom.Wbr: true,
}

// hiddenTextElements hold content a reader does not see as page text: code,
// embeds and their fallbacks, and metadata such as a <title> or an SVG
// <desc>, which a browser shows at most as a tooltip.
var hiddenTextElements = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Template: true, atom.Noscript: true,
	atom.Iframe: true, atom.Object: true, atom.Noembed: true, atom.Noframes: true,
	atom.Head: true, atom.Title: true, atom.Desc: true,
}

// pageBody is the context page HTML is parsed in: the inside of <body>.
var pageBody = &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}

// PageHTMLText returns the text a visitor reads in page HTML: entities
// decoded, markup and hidden elements (script, style, embeds) dropped, words
// separated at block boundaries, whitespace collapsed to single spaces and
// control characters removed. Search indexes this instead of the stored HTML,
// so tag names, attribute values and inline styles neither match a query nor
// show up in a result excerpt.
//
// It parses the HTML as a browser does, so unclosed or misnested elements
// (an unclosed <svg>, a self-closing <script/>) cover what a browser shows
// them covering, not the rest of the page.
func PageHTMLText(fragment string) string {
	nodes, err := html.ParseFragment(strings.NewReader(fragment), pageBody)
	if err != nil {
		// Only a failing reader makes ParseFragment fail; a strings.Reader
		// never does.
		return ""
	}

	var b strings.Builder
	space := false
	write := func(text string) {
		for _, r := range text {
			switch {
			case unicode.IsSpace(r):
				space = true
			case unicode.IsControl(r):
				// Dropped: search uses control characters as excerpt markers.
			default:
				if space && b.Len() > 0 {
					b.WriteByte(' ')
				}
				space = false
				b.WriteRune(r)
			}
		}
	}

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			write(n.Data)
		case html.ElementNode:
			if hiddenTextElements[n.DataAtom] {
				return
			}
			block := !inlineTextElements[n.DataAtom]
			if block {
				space = true
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			if block {
				space = true
			}
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return b.String()
}
