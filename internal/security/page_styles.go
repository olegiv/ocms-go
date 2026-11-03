// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"regexp"
	"slices"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/microcosm-cc/bluemonday/css"
)

// pageStyleRule allows one CSS property in the style attribute of the listed
// page-body elements. It is the single source for both sides of the editor
// contract: buildPageHTMLSanitizer turns the rules into bluemonday style
// policies, and PageEditorValidStyles hands the same element/property pairs
// to TinyMCE as valid_styles, so the editor drops every property a save would
// remove. Values are only checked here, on the server: a value the editor
// accepts but check rejects (a negative margin typed into a dialog, say) is
// still dropped on save.
//
// check validates the value bluemonday hands over: lowercased, with CSS
// escapes decoded. A nil check falls back to bluemonday's handler for the
// property (css.GetDefaultHandler), which rejects every value of a property it
// does not know.
type pageStyleRule struct {
	property string
	elements []string
	check    func(string) bool
}

var (
	headingElements   = []string{"h1", "h2", "h3", "h4", "h5", "h6"}
	textBlockElements = slices.Concat([]string{"p", "div", "pre"}, headingElements)
	cellElements      = []string{"td", "th"}
	tablePartElements = []string{"table", "tr", "td", "th"}
)

// lengthToken is a non-negative length: up to 9999px/pt, 99em/rem or 100%,
// with up to six decimals (TinyMCE writes unrounded table percentages such as
// 8.37563%). bluemonday's own length handler also accepts negative values,
// viewport units and any magnitude. Negative offsets would pull an image over
// the site header; the size caps keep page content in normal flow at sizes a
// page layout can still contain, so nothing here can cover content above it.
const lengthToken = `(?:0|[0-9]{1,4}(?:\.[0-9]{1,6})?(?:px|pt)|[0-9]{1,2}(?:\.[0-9]{1,6})?(?:em|rem)|(?:100|[0-9]{1,2}(?:\.[0-9]{1,6})?)%)`

const (
	lengthOrAutoToken = `(?:auto|` + lengthToken + `)`
	borderWidthToken  = `(?:thin|medium|thick|` + lengthToken + `)`
	// familyToken is one font name, bare or quoted. It needs at least one
	// character: bluemonday decodes an invalid CSS escape such as \d800 to an
	// empty string, which its own font-family handler accepts.
	familyToken = `(?:'[a-z0-9 -]+'|"[a-z0-9 -]+"|[a-z0-9 -]+)`
)

var (
	lengthValue        = pattern(`^` + lengthToken + `$`)
	lengthOrAutoValue  = pattern(`^` + lengthOrAutoToken + `$`)
	lengthBoxValue     = pattern(`^` + lengthToken + `(?: ` + lengthToken + `){0,3}$`)
	marginBoxValue     = pattern(`^` + lengthOrAutoToken + `(?: ` + lengthOrAutoToken + `){0,3}$`)
	borderWidthsValue  = pattern(`^` + borderWidthToken + `(?: ` + borderWidthToken + `){0,3}$`)
	borderSpacingValue = pattern(`^` + lengthToken + `(?: ` + lengthToken + `)?$`)
	fontFamilyValue    = pattern(`^` + familyToken + `(?:, ?` + familyToken + `){0,9}$`)
	// fontSizeValue allows absolute sizes only, up to 99px/99pt. Relative
	// units (em, %, larger) multiply when spans nest, and an unbounded size
	// lets one span's background cover the page.
	fontSizeValue = pattern(`^(?:[0-9]{1,2}(?:\.[0-9]{1,2})?(?:px|pt)|xx-small|x-small|small|medium|large|x-large|xx-large)$`)
	// lineHeightValue is a unitless factor of at least 1. A smaller line
	// height lets a large span's background overflow its line onto the
	// content above, as far as the site header.
	lineHeightValue = pattern(`^(?:normal|[1-9](?:\.[0-9]{1,3})?)$`)

	borderStyles = []string{"none", "hidden", "dotted", "dashed", "solid", "double", "groove", "ridge", "inset", "outset"}
)

// pageStyleRules lists the inline styles the admin page editor (TinyMCE 8 with
// the plugins and toolbar configured in internal/views/admin/pages.templ)
// writes: its default alignment, colour, font, line-height and indent formats
// (including the copies TinyMCE puts on links and fully selected list items),
// the advlist list styles, the image dialog, and the table plugin. TinyMCE
// merges longhand border, padding and margin declarations into the shorthand
// when every side is set, so the shorthands are listed beside the longhands.
// Nothing here can carry url(), expression(), a position change, a negative
// offset or a viewport-sized length.
var pageStyleRules = []pageStyleRule{
	// Alignment buttons (alignleft, aligncenter, alignright, alignjustify).
	{"text-align", slices.Concat(textBlockElements, []string{"figure", "ul", "ol", "li", "tr"}, cellElements), oneOf("left", "center", "right", "justify")},
	// Indent/outdent on plain blocks and selected table cells (padding-right
	// for right-to-left content); lists nest instead.
	{"padding-left", slices.Concat(textBlockElements, []string{"blockquote"}, cellElements), lengthValue},
	{"padding-right", slices.Concat(textBlockElements, []string{"blockquote"}, cellElements), lengthValue},
	{"line-height", slices.Concat([]string{"p", "div", "li"}, headingElements, cellElements), lineHeightValue},

	// Text colour and highlight (also applied to links inside the selection),
	// font family and size, underline and strikethrough. Formatting a whole
	// list item copies colour, font, size, bold and italic onto the <li>.
	{"color", []string{"span", "a", "li"}, nil},
	{"background-color", slices.Concat([]string{"span", "a"}, tablePartElements), nil},
	{"font-family", []string{"span", "li"}, fontFamilyValue},
	{"font-size", []string{"span", "li"}, fontSizeValue},
	{"font-weight", []string{"li"}, oneOf("bold", "normal")},
	{"font-style", []string{"li"}, oneOf("italic", "normal")},
	{"text-decoration", []string{"span"}, nil},

	// Numbered and bulleted list styles (advlist). Indenting a first list item
	// wraps the nested list in an <li style="list-style-type: none">.
	{"list-style-type", []string{"ul", "ol", "li"}, nil},

	// Image alignment: float left/right, or display:block with auto side
	// margins to centre. The image dialog's advanced tab sets margin (or only
	// its vertical sides) and border.
	{"float", []string{"img"}, oneOf("left", "right", "none")},
	{"display", []string{"img"}, oneOf("block")},
	{"margin", []string{"img", "table"}, marginBoxValue},
	{"margin-left", []string{"img", "table"}, lengthOrAutoValue},
	{"margin-right", []string{"img", "table"}, lengthOrAutoValue},
	{"margin-top", []string{"img"}, lengthOrAutoValue},
	{"margin-bottom", []string{"img"}, lengthOrAutoValue},

	// Table, row and cell properties.
	{"border", slices.Concat([]string{"img"}, tablePartElements), borderShorthand},
	{"border-width", slices.Concat([]string{"img"}, tablePartElements), borderWidthsValue},
	{"border-style", slices.Concat([]string{"img"}, tablePartElements), nil},
	{"border-color", slices.Concat([]string{"img"}, tablePartElements), nil},
	{"border-collapse", []string{"table"}, nil},
	{"border-spacing", []string{"table"}, borderSpacingValue},
	{"width", slices.Concat([]string{"table", "col", "colgroup"}, cellElements), lengthOrAutoValue},
	{"height", tablePartElements, lengthOrAutoValue},
	{"vertical-align", cellElements, oneOf("top", "middle", "bottom", "baseline")},
	{"padding", cellElements, lengthBoxValue},
}

// tableBorderAttr matches the border attribute TinyMCE puts on new tables
// (border="1"); the UGC policy drops it, leaving inserted tables without
// cell borders.
var tableBorderAttr = regexp.MustCompile(`^(?:0|[1-9][0-9]?)$`)

// allowPageStyles adds pageStyleRules to p.
//
// It never calls AllowAttrs("style"). bluemonday routes a style attribute
// through its CSS sanitizer only on elements that have a style policy
// (sanitize.go: `if htmlAttr.Key == "style" && hasStylePolicies`); an allowed
// style attribute on any other element would be copied through verbatim,
// position:fixed overlays and url() requests included. With style policies
// alone, every element without a rule loses its style attribute.
func allowPageStyles(p *bluemonday.Policy) {
	for _, rule := range pageStyleRules {
		builder := p.AllowStyles(rule.property)
		if rule.check != nil {
			builder = builder.MatchingHandler(rule.check)
		}
		builder.OnElements(rule.elements...)
	}
	p.AllowAttrs("border").Matching(tableBorderAttr).OnElements("table")
}

// borderShorthand accepts one to three border tokens: a bounded width, a line
// style and a colour, in any order.
func borderShorthand(value string) bool {
	tokens := cssTokens(value)
	if len(tokens) == 0 || len(tokens) > 3 {
		return false
	}
	for _, token := range tokens {
		if !borderWidthsValue(token) && !slices.Contains(borderStyles, token) && !css.ColorHandler(token) {
			return false
		}
	}
	return true
}

// cssTokens splits a declaration value on spaces outside parentheses, so a
// colour function such as rgb(0, 0, 0) stays one token. It returns nil for a
// value with unbalanced parentheses.
func cssTokens(value string) []string {
	var tokens []string
	depth, start := 0, 0
	for i, r := range value {
		switch {
		case r == '(':
			depth++
		case r == ')':
			if depth == 0 {
				return nil
			}
			depth--
		case r == ' ' && depth == 0:
			if i > start {
				tokens = append(tokens, value[start:i])
			}
			start = i + 1
		}
	}
	if depth != 0 {
		return nil
	}
	if start < len(value) {
		tokens = append(tokens, value[start:])
	}
	return tokens
}

func pattern(expr string) func(string) bool {
	return regexp.MustCompile(expr).MatchString
}

func oneOf(values ...string) func(string) bool {
	return func(value string) bool { return slices.Contains(values, value) }
}

// PageEditorValidStyles returns the page style allowlist in TinyMCE's
// valid_styles shape: element name → comma-separated CSS properties. The
// admin editor passes it to tinymce.init while OCMS_SANITIZE_PAGE_HTML is on,
// so the editor drops the same properties a save removes.
func PageEditorValidStyles() map[string]string {
	byElement := map[string][]string{}
	for _, rule := range pageStyleRules {
		for _, element := range rule.elements {
			if !slices.Contains(byElement[element], rule.property) {
				byElement[element] = append(byElement[element], rule.property)
			}
		}
	}
	validStyles := make(map[string]string, len(byElement))
	for element, properties := range byElement {
		slices.Sort(properties)
		validStyles[element] = strings.Join(properties, ",")
	}
	return validStyles
}
