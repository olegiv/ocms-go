// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// pageStyleSamples holds one value per allowlisted property, in the form the
// admin editor writes it. Every property in pageStyleRules needs an entry, so
// a new rule cannot land without proof that its values survive sanitizing (a
// property bluemonday has no handler for would silently reject everything).
var pageStyleSamples = map[string]string{
	"text-align":       "center",
	"padding-left":     "40px",
	"padding-right":    "40px",
	"line-height":      "1.5",
	"color":            "#e03e2d",
	"background-color": "#fbeeb8",
	"font-family":      "'times new roman', times",
	"font-size":        "18pt",
	"font-weight":      "bold",
	"font-style":       "italic",
	"text-decoration":  "underline",
	"list-style-type":  "lower-alpha",
	"float":            "left",
	"display":          "block",
	"margin":           "10px 20px",
	"margin-left":      "auto",
	"margin-right":     "auto",
	"margin-top":       "10px",
	"margin-bottom":    "10px",
	"border":           "1px solid #000000",
	"border-width":     "1px",
	"border-style":     "solid",
	"border-color":     "#000000",
	"border-collapse":  "collapse",
	"border-spacing":   "2px",
	"width":            "100%",
	"height":           "40px",
	"vertical-align":   "top",
	"padding":          "4px",
}

// styledElement returns markup for one element carrying style. Images need a
// src to stay an image, links an href; col is a void element.
func styledElement(element, style string) string {
	switch element {
	case "img":
		return `<img src="/uploads/a.png" style="` + style + `">`
	case "col":
		return `<col style="` + style + `">`
	case "a":
		return `<a href="/x" style="` + style + `">x</a>`
	default:
		return `<` + element + ` style="` + style + `">x</` + element + `>`
	}
}

// styleAttr returns the style attribute of the first element named tag in
// fragment, and whether that element carries one. A missing element has no
// style.
func styleAttr(fragment, tag string) (string, bool) {
	z := html.NewTokenizer(strings.NewReader(fragment))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return "", false
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			if tok.Data != tag {
				continue
			}
			for _, attr := range tok.Attr {
				if attr.Key == "style" {
					return attr.Val, true
				}
			}
			return "", false
		}
	}
}

// styledElements lists every element with a style rule plus common elements
// without one.
func styledElements() []string {
	return append(slices.Sorted(maps.Keys(PageEditorValidStyles())), "strong", "em", "code", "section")
}

// TestPageStyleRulesSurviveSanitizer is the drift test for the editor/sanitizer
// contract: every element/property pair handed to TinyMCE as valid_styles must
// come back from SanitizePageHTML, so the editor never keeps a property that a
// save removes.
func TestPageStyleRulesSurviveSanitizer(t *testing.T) {
	validStyles := PageEditorValidStyles()
	if len(validStyles) == 0 {
		t.Fatal("PageEditorValidStyles is empty")
	}

	rulePairs := map[string]bool{}
	for _, rule := range pageStyleRules {
		for _, element := range rule.elements {
			rulePairs[element+" "+rule.property] = true
		}
	}
	exported := 0
	for element, properties := range validStyles {
		for property := range strings.SplitSeq(properties, ",") {
			exported++
			if !rulePairs[element+" "+property] {
				t.Errorf("valid_styles lists %s on <%s>, which no rule allows", property, element)
			}
			sample, ok := pageStyleSamples[property]
			if !ok {
				t.Errorf("no sample value for %q in pageStyleSamples", property)
				continue
			}
			declaration := property + ": " + sample
			got, ok := styleAttr(SanitizePageHTML(styledElement(element, declaration)), element)
			if !ok || got != declaration {
				t.Errorf("<%s style=%q> sanitized to style %q (present=%v), want it kept", element, declaration, got, ok)
			}
		}
	}
	if exported != len(rulePairs) {
		t.Errorf("valid_styles exports %d element/property pairs, rules define %d", exported, len(rulePairs))
	}
}

// TestPageStyleRulesRejectEmptyDecodedValue guards a bypass class: bluemonday
// checks values after decoding CSS escapes, and an invalid escape such as
// \d800 decodes to "". A check that accepts "" lets the original, unchecked
// value through, so every rule must reject it.
func TestPageStyleRulesRejectEmptyDecodedValue(t *testing.T) {
	for _, rule := range pageStyleRules {
		for _, element := range rule.elements {
			payload := rule.property + `: \d800 anything(x) 'y'`
			if got, ok := styleAttr(SanitizePageHTML(styledElement(element, payload)), element); ok {
				t.Errorf("<%s style=%q> kept style %q", element, payload, got)
			}
		}
	}
}

// TestPageStylesRejectDangerousCSS proves the style allowlist is value-checked
// on every element, so no page body can request a URL, run script, or cover
// the site's own chrome with an oversized or displaced box.
func TestPageStylesRejectDangerousCSS(t *testing.T) {
	rejected := []string{
		"position: fixed; inset: 0; z-index: 9999",
		"background: url(//evil.example/x.png)",
		"background-image: url(javascript:alert(1))",
		"color: expression(alert(1))",
		"behavior: url(x.htc)",
		"-moz-binding: url(x.xml#x)",
		`color: \75 rl(//x)`,
		"font-family: url(x)",
		"display: none",
		"width: calc(100% + 9999px)",
		// Displacement: negative offsets.
		"margin: -999px 0 0 0",
		"margin-left: -999px",
		"margin-top: -999px",
		"padding-left: -500px",
		"vertical-align: -9999px",
		// Oversized boxes: viewport units and unbounded lengths. A huge
		// font-size with a background inside a line-height below 1 paints over
		// the content above it, the header included.
		"font-size: 3000px",
		"font-size: 100vh",
		"font-size: 9em",
		"font-size: larger",
		"line-height: 0",
		"line-height: 0.5",
		"width: 1000vw",
		"width: 99999px",
		"height: 100vh",
		"padding: 99999px",
		"padding-left: 99999vw",
		"border: 100vh solid #fff",
		"border-width: 99999px",
		"border-spacing: 99999px",
		"width: 9999em",
		"width: 201%",
		"height: 200.5%",
		"padding: 9999%",
		"margin-left: 9999em",
	}
	for _, element := range styledElements() {
		for _, payload := range rejected {
			if got, ok := styleAttr(SanitizePageHTML(styledElement(element, payload)), element); ok {
				t.Errorf("<%s style=%q> kept style %q", element, payload, got)
			}
		}
	}

	for _, tc := range []struct{ fragment, tag, want string }{
		{`<span style="color: red; background: url(//x)">x</span>`, "span", "color: red"},
		{`<p style="text-align: center; position: absolute; top: 0">x</p>`, "p", "text-align: center"},
		{`<p style="line-height: 0"><span style="font-size: 3000px; background-color: #fff">x</span></p>`, "span", "background-color: #fff"},
	} {
		if got, _ := styleAttr(SanitizePageHTML(tc.fragment), tc.tag); got != tc.want {
			t.Errorf("%s: <%s> style = %q, want %q", tc.fragment, tc.tag, got, tc.want)
		}
	}
}

// TestPageStylesKeepTinyMCEDefaultMenus checks the bounded value patterns
// accept every entry of the editor's default font, size and line-height menus
// (TinyMCE 8 themes/silver defaults; pages.templ does not override them).
func TestPageStylesKeepTinyMCEDefaultMenus(t *testing.T) {
	// TinyMCE writes font formats on spans and line heights on blocks.
	var cases []struct{ tag, declaration string }
	add := func(tag, declaration string) {
		cases = append(cases, struct{ tag, declaration string }{tag, declaration})
	}
	for size := range strings.FieldsSeq("8pt 10pt 12pt 14pt 18pt 24pt 36pt") {
		add("span", "font-size: "+size)
	}
	for height := range strings.FieldsSeq("1 1.1 1.2 1.3 1.4 1.5 2") {
		add("p", "line-height: "+height)
	}
	fonts := "andale mono,monospace;arial,helvetica,sans-serif;arial black,sans-serif;book antiqua,palatino,serif;" +
		"comic sans ms,sans-serif;courier new,courier,monospace;georgia,palatino,serif;helvetica,arial,sans-serif;" +
		"impact,sans-serif;symbol;tahoma,arial,helvetica,sans-serif;terminal,monaco,monospace;" +
		"times new roman,times,serif;trebuchet ms,geneva,sans-serif;verdana,geneva,sans-serif;webdings;wingdings,zapf dingbats"
	for family := range strings.SplitSeq(fonts, ";") {
		// TinyMCE quotes names that contain a space and joins them with ", ".
		var names []string
		for name := range strings.SplitSeq(family, ",") {
			if strings.Contains(name, " ") {
				name = "'" + name + "'"
			}
			names = append(names, name)
		}
		add("span", "font-family: "+strings.Join(names, ", "))
	}
	for _, tc := range cases {
		fragment := styledElement(tc.tag, tc.declaration)
		if got, _ := styleAttr(SanitizePageHTML(fragment), tc.tag); got != tc.declaration {
			t.Errorf("%s sanitized to style %q, want it kept", fragment, got)
		}
	}
}

// TestPageStylesBorderShorthandKeepsColourFunctions checks the border check
// tokenizes outside parentheses: imported or API-written markup may spell the
// colour as rgb(0, 0, 0), which the separate border-color property accepts.
func TestPageStylesBorderShorthandKeepsColourFunctions(t *testing.T) {
	for _, tc := range []struct {
		declaration string
		kept        bool
	}{
		{"border: 1px solid rgb(0, 0, 0)", true},
		{"border: 2px dotted #000000", true},
		{"border: 1px solid rgb(0, 0, 0) url(x)", false},
		{"border: 1px solid rgb(0, 0, 0", false},
		{"border: 1px solid rgb(0, 0, 0))", false},
	} {
		got, _ := styleAttr(SanitizePageHTML(styledElement("td", tc.declaration)), "td")
		if kept := got == tc.declaration; kept != tc.kept {
			t.Errorf("<td style=%q> sanitized to style %q, want kept=%v", tc.declaration, got, tc.kept)
		}
	}
}

func TestPageStylesDroppedWhereNoRuleApplies(t *testing.T) {
	for _, fragment := range []struct{ html, tag string }{
		{`<a href="/x" style="font-size: 12pt">x</a>`, "a"},
		{`<strong style="color: red">x</strong>`, "strong"},
		{`<blockquote style="color: red">x</blockquote>`, "blockquote"},
		{`<p style="color: red">x</p>`, "p"},
		{`<img src="/uploads/a.png" style="text-align: center">`, "img"},
	} {
		if got, ok := styleAttr(SanitizePageHTML(fragment.html), fragment.tag); ok {
			t.Errorf("%s kept style %q", fragment.html, got)
		}
	}
}

func TestPageHTMLKeepsTableBorderAttribute(t *testing.T) {
	got := SanitizePageHTML(`<table border="1"><tbody><tr><td>x</td></tr></tbody></table><table border="x1"><tbody><tr><td>y</td></tr></tbody></table>`)
	if !strings.Contains(got, `<table border="1">`) {
		t.Errorf("border=\"1\" dropped: %q", got)
	}
	if strings.Contains(got, `border="x1"`) {
		t.Errorf("non-numeric border kept: %q", got)
	}
}

// TestSanitizePageHTMLKeepsEditorFormatting replays markup captured from the
// admin editor (TinyMCE 8.10, issue #202) through the page sanitizer: every
// format must come back with only the trailing semicolons dropped (and
// rel="noopener" added to links opening a new tab), and a second pass must not change
// it again. The drift tests compare the rule list with itself; only replayed
// editor output catches a style the editor writes that the list lacks.
func TestSanitizePageHTMLKeepsEditorFormatting(t *testing.T) {
	for _, tc := range editorReplayCases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizePageHTML(tc.editor)
			if got != tc.want {
				t.Fatalf("SanitizePageHTML(%q)\n got %q\nwant %q", tc.editor, got, tc.want)
			}
			if again := SanitizePageHTML(got); again != got {
				t.Fatalf("second pass changed %q to %q", got, again)
			}
		})
	}
}

// TestEditorOutputNeverReportsRemovals guards the save-time warning: markup
// the admin editor writes must sanitize without a removal, or every ordinary
// save would warn and editors would learn to ignore it.
func TestEditorOutputNeverReportsRemovals(t *testing.T) {
	for _, tc := range editorReplayCases {
		if removed := RemovedMarkup(tc.editor, SanitizePageHTML(tc.editor)); len(removed) > 0 {
			t.Errorf("%s: sanitizing editor output reports removals %q", tc.name, removed)
		}
	}
}

// editorReplayCases is markup captured from the admin editor (TinyMCE 8.10)
// with the policy's expected output.
var editorReplayCases = []struct{ name, editor, want string }{
	{"align center", `<p style="text-align: center;">Centered</p>`, `<p style="text-align: center">Centered</p>`},
	{"align right", `<p style="text-align: right;">Right</p>`, `<p style="text-align: right">Right</p>`},
	{"justify", `<p style="text-align: justify;">Justify</p>`, `<p style="text-align: justify">Justify</p>`},
	{"heading align", `<h2 style="text-align: center;">Heading</h2>`, `<h2 style="text-align: center">Heading</h2>`},
	{"text colour", `<p><span style="color: #e03e2d;">Colored</span></p>`, `<p><span style="color: #e03e2d">Colored</span></p>`},
	{"text colour rgb", `<p><span style="color: rgb(224, 62, 45);">Colored</span></p>`, `<p><span style="color: rgb(224, 62, 45)">Colored</span></p>`},
	{"highlight", `<p><span style="background-color: #fbeeb8;">Highlighted</span></p>`, `<p><span style="background-color: #fbeeb8">Highlighted</span></p>`},
	{"underline", `<p><span style="text-decoration: underline;">Underlined</span></p>`, `<p><span style="text-decoration: underline">Underlined</span></p>`},
	{"strikethrough", `<p><s>Struck</s></p>`, `<p><s>Struck</s></p>`},
	{"font size", `<p><span style="font-size: 18pt;">Sized</span></p>`, `<p><span style="font-size: 18pt">Sized</span></p>`},
	{"font family", `<p><span style="font-family: 'times new roman', times;">Family</span></p>`, `<p><span style="font-family: &#39;times new roman&#39;, times">Family</span></p>`},
	{"line height", `<p style="line-height: 1.5;">Spaced</p>`, `<p style="line-height: 1.5">Spaced</p>`},
	{"indent", `<p style="padding-left: 40px;">Indented</p>`, `<p style="padding-left: 40px">Indented</p>`},
	{"list style", `<ol style="list-style-type: lower-alpha;"><li>a</li></ol>`, `<ol style="list-style-type: lower-alpha"><li>a</li></ol>`},
	{"image float", `<img style="float: left;" src="/uploads/a.png" alt="f">`, `<img style="float: left" src="/uploads/a.png" alt="f">`},
	{"image centre", `<img style="display: block; margin-left: auto; margin-right: auto;" src="/uploads/a.png" alt="c">`, `<img style="display: block; margin-left: auto; margin-right: auto" src="/uploads/a.png" alt="c">`},
	{"image advanced tab", `<img style="border-style: solid; border-width: 2px; margin: 10px 20px;" src="/uploads/a.png" alt="b">`, `<img style="border-style: solid; border-width: 2px; margin: 10px 20px" src="/uploads/a.png" alt="b">`},
	{"image vertical space", `<img style="margin-bottom: 10px; margin-top: 10px;" src="/uploads/a.png" alt="v">`, `<img style="margin-bottom: 10px; margin-top: 10px" src="/uploads/a.png" alt="v">`},
	{"text colour on a link", `<p><span style="color: #e03e2d;">Before <a style="color: #e03e2d;" href="/x">linked</a> after</span></p>`, `<p><span style="color: #e03e2d">Before <a style="color: #e03e2d" href="/x">linked</a> after</span></p>`},
	{"highlight on a link", `<p><span style="background-color: #fbeeb8;">A <a style="background-color: #fbeeb8;" href="/y">link</a></span></p>`, `<p><span style="background-color: #fbeeb8">A <a style="background-color: #fbeeb8" href="/y">link</a></span></p>`},
	{
		"whole list item formatted",
		`<ul><li style="color: #2dc26b; font-family: georgia, palatino, serif; font-size: 18pt; font-style: italic; font-weight: bold;"><em><span style="color: #2dc26b; font-family: georgia, palatino, serif; font-size: 18pt;"><strong>Item</strong></span></em></li></ul>`,
		`<ul><li style="color: #2dc26b; font-family: georgia, palatino, serif; font-size: 18pt; font-style: italic; font-weight: bold"><em><span style="color: #2dc26b; font-family: georgia, palatino, serif; font-size: 18pt"><strong>Item</strong></span></em></li></ul>`,
	},
	{"right-to-left indent", `<p dir="rtl" style="padding-right: 40px;">rtl</p>`, `<p dir="rtl" style="padding-right: 40px">rtl</p>`},
	{"indented first list item", `<ul><li style="list-style-type: none;"><ul><li>first</li></ul></li><li>second</li></ul>`, `<ul><li style="list-style-type: none"><ul><li>first</li></ul></li><li>second</li></ul>`},
	{"twelve-column table", `<table style="border-collapse: collapse; width: 100%;" border="1"><colgroup><col style="width: 8.37563%;"></colgroup></table>`, `<table style="border-collapse: collapse; width: 100%" border="1"><colgroup><col style="width: 8.37563%"></colgroup></table>`},
	{
		"table and cell properties",
		`<table style="border-collapse: collapse; margin-left: auto; margin-right: auto; width: 100%;" border="1"><colgroup><col style="width: 50%;"><col style="width: 50%;"></colgroup><tbody><tr><td style="background-color: #fbeeb8; border: 2px dotted #000000; vertical-align: top;">a</td><td style="text-align: center;">b</td></tr></tbody></table>`,
		`<table style="border-collapse: collapse; margin-left: auto; margin-right: auto; width: 100%" border="1"><colgroup><col style="width: 50%"><col style="width: 50%"></colgroup><tbody><tr><td style="background-color: #fbeeb8; border: 2px dotted #000000; vertical-align: top">a</td><td style="text-align: center">b</td></tr></tbody></table>`,
	},
	{"resized table", `<table style="border-collapse: collapse; width: 100.035%;" border="1"><tbody><tr><td style="width: 49.9824%;">a</td></tr></tbody></table>`, `<table style="border-collapse: collapse; width: 100.035%" border="1"><tbody><tr><td style="width: 49.9824%">a</td></tr></tbody></table>`},
	{"link opening in a new tab", `<p><a href="https://example.com/x" target="_blank" rel="noopener noreferrer">ext</a></p>`, `<p><a href="https://example.com/x" target="_blank" rel="noopener noreferrer">ext</a></p>`},
}
