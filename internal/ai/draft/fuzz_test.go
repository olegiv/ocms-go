// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/security"
)

func FuzzDraftParser(f *testing.F) {
	for _, seed := range []string{`{}`, `null`, `{"title":"A","summary":"","body_markdown":"Hello"}`, `{"title":null}`, `{"body_markdown":"![x](media:1)"}`, `{"status":null}`, `{"reported":{"claims_to_verify":["unverified"]}}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > draft.MaxInputBytes {
			t.Skip()
		}
		if create, err := draft.DecodeCreate([]byte(raw), "en"); err == nil {
			encoded, err := json.Marshal(create)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = draft.DecodeCreate(encoded, "en"); err != nil {
				t.Fatalf("create round trip: %v", err)
			}
		}
		if changes, err := draft.DecodeChanges([]byte(raw), "en"); err == nil {
			encoded, err := json.Marshal(changes)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = draft.DecodeChanges(encoded, "en"); err != nil {
				t.Fatalf("update round trip: %v", err)
			}
		}
	})
}

func FuzzMarkdownSafety(f *testing.F) {
	for _, seed := range []string{"Plain text", "![alt](media:1)", "<script>alert(1)</script>", "[x](javascript:alert(1))", "[x](https://outside.test)", "<svg/onload=alert(1)>"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > draft.MaxBodyBytes {
			t.Skip()
		}
		opt, _ := options()
		rendered, err := draft.RenderMarkdown(context.Background(), body, opt)
		if err != nil {
			return
		}
		if draft.HasErrors(rendered.Issues) {
			if rendered.HTML != "" {
				t.Fatal("invalid Markdown produced saveable HTML")
			}
			return
		}
		if len(rendered.HTML) > draft.MaxBodyBytes {
			t.Fatalf("unsafe output: %s", rendered.HTML)
		}
		assertSafeHTML(t, rendered.HTML)
		if twice := security.SanitizePageHTML(rendered.HTML); twice != rendered.HTML {
			t.Fatal("output changed on repeated sanitization")
		}
	})
}

func assertSafeHTML(t *testing.T, value string) {
	t.Helper()
	nodes, err := html.ParseFragment(strings.NewReader(value), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		t.Fatal(err)
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "iframe", "object", "embed", "svg", "math", "base", "style":
				t.Fatalf("unsafe element %s", node.Data)
			}
			for _, attr := range node.Attr {
				if strings.HasPrefix(attr.Key, "on") {
					t.Fatalf("event handler %s", attr.Key)
				}
				if attr.Key == "src" && (node.Data != "img" || !strings.HasPrefix(attr.Val, "/uploads/")) {
					t.Fatalf("unexpected image source %s", attr.Val)
				}
				if attr.Key == "href" {
					target, err := url.Parse(attr.Val)
					if err != nil {
						t.Fatal(err)
					}
					switch target.Scheme {
					case "", "http", "https", "mailto":
					default:
						t.Fatalf("unsafe link scheme %s", target.Scheme)
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	for _, node := range nodes {
		walk(node)
	}
}

func BenchmarkValidate(b *testing.B) {
	input := draft.PageDraft{Title: "Benchmark", Summary: "A valid draft", BodyMarkdown: "Paragraph", Tags: draft.Set([]string{"go"})}
	b.ReportAllocs()
	for b.Loop() {
		if draft.HasErrors(draft.Validate(input, "en")) {
			b.Fatal("invalid fixture")
		}
	}
}

func BenchmarkMarkdownPipeline(b *testing.B) {
	opt, resolver := options()
	body := strings.Repeat("## Heading\n\nA paragraph with **emphasis** and [a link](/existing).\n\n", 50) + "![Image](media:1)"
	b.ReportAllocs()
	for b.Loop() {
		resolver.linkCalls = nil
		result, err := draft.RenderMarkdown(context.Background(), body, opt)
		if err != nil || draft.HasErrors(result.Issues) {
			b.Fatalf("%+v %v", result, err)
		}
	}
}
