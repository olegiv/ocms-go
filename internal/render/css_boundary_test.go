// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestSafeCSSKeepsImportedMarkupInsideStyle(t *testing.T) {
	tmpl := template.Must(template.New("page").Funcs((&Renderer{}).TemplateFuncs()).Parse(
		`<!doctype html><html><head><style>{{. | safeCSS}}</style></head><body><p>normal</p></body></html>`))
	for _, css := range []string{
		`body{color:red}</st</styleyle><div x-init="alert(1)">injected</div>`,
		`</sty</STYLEle ><script>alert(1)</script>`,
		`</style` + "\t" + `><img src=x onerror=alert(1)>`,
		strings.Repeat(`</st`, 2000) + `</style>` + strings.Repeat(`yle>`, 2000),
	} {
		var out bytes.Buffer
		if err := tmpl.Execute(&out, css); err != nil {
			t.Fatal(err)
		}
		doc, err := html.Parse(&out)
		if err != nil {
			t.Fatal(err)
		}
		var walk func(*html.Node)
		walk = func(node *html.Node) {
			if node.Type == html.ElementNode {
				switch node.Data {
				case "script", "div", "img":
					t.Errorf("CSS escaped its style element: <%s>", node.Data)
				}
				for _, attr := range node.Attr {
					if strings.HasPrefix(attr.Key, "x-") || strings.HasPrefix(attr.Key, "on") {
						t.Errorf("CSS created executable attribute %s", attr.Key)
					}
				}
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
		walk(doc)
	}
	// CSS range comparisons and ordinary selectors must retain their syntax.
	css := `@media (width < 600px) { .button:hover { color: red; } }`
	if got := string(sanitizeCustomCSS(css)); got != css {
		t.Fatalf("ordinary stylesheet changed: %q", got)
	}
}
