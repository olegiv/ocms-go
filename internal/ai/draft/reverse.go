// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	mdneg "github.com/olegiv/ocms-go/internal/seo/markdown"
)

// DerivedMarkdown is a read representation, never a replacement for stored HTML.
type DerivedMarkdown struct {
	Markdown string
	Issues   []Issue
}

// DeriveMarkdown preserves verified existing media:ID references and warns when
// rich editor HTML cannot round-trip. It never fetches an image or link URL.
func DeriveMarkdown(ctx context.Context, storedHTML string, options Options) (DerivedMarkdown, error) {
	var result DerivedMarkdown
	if len(storedHTML) > mdneg.MaxHTMLBytes {
		return result, mdneg.ErrBodyTooLarge
	}
	base, err := options.base()
	if err != nil {
		return result, err
	}
	root, err := parseFragment(storedHTML)
	if err != nil {
		return result, err
	}
	lossy, imageLossy := false, false
	var walk func(*html.Node) error
	walk = func(node *html.Node) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if node.Type == html.ElementNode {
			if !markdownElement(node.Data) {
				lossy = true
			}
			for _, attr := range node.Attr {
				if attr.Key == "style" || attr.Key == "class" || attr.Key == "id" || attr.Key == "rowspan" || attr.Key == "colspan" || attr.Key == "dir" || attr.Key == "lang" {
					lossy = true
				}
			}
			if node.Data == "img" {
				media, imageErr := imageFromHTML(ctx, node, base, options.Resolver)
				switch {
				case errors.Is(imageErr, ErrUnavailable):
					imageLossy = true
				case imageErr != nil:
					return imageErr
				default:
					removeAttribute(node, "src")
					removeAttribute(node, "srcset")
					removeAttribute(node, "sizes")
					node.Attr = append(node.Attr, html.Attribute{Key: "src", Val: "media:" + strconv.FormatInt(media.ID, 10)})
					if caption := standaloneCaption(node); caption != nil {
						for child := caption.FirstChild; child != nil; child = child.NextSibling {
							if child.Type == html.ElementNode {
								lossy = true
							}
						}
						removeAttribute(node, "title")
						node.Attr = append(node.Attr, html.Attribute{Key: "title", Val: nodeText(caption)})
						caption.Parent.RemoveChild(caption)
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if walkErr := walk(child); walkErr != nil {
				return walkErr
			}
		}
		return nil
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if err = walk(child); err != nil {
			return result, fmt.Errorf("derive draft Markdown: %w", err)
		}
	}
	prepared, err := renderFragment(root)
	if err != nil {
		return result, err
	}
	result.Markdown, err = mdneg.HTMLToMarkdown(prepared)
	if err != nil {
		return result, err
	}
	if lossy || len(result.Markdown) > MaxBodyBytes {
		result.Issues = append(result.Issues, NewIssue("html_conversion_lossy", "body_markdown", options.UILanguage))
	}
	if imageLossy {
		result.Issues = append(result.Issues, NewIssue("media_conversion_lossy", "body_markdown", options.UILanguage))
	}
	return result, nil
}

func imageFromHTML(ctx context.Context, node *html.Node, base *url.URL, resolver Resolver) (Media, error) {
	raw := attribute(node, "src")
	target, err := targetURL(raw, base)
	if err != nil || target.external || target.fragment {
		return Media{}, ErrUnavailable
	}
	parsed, err := url.Parse(target.path)
	if err != nil {
		return Media{}, ErrUnavailable
	}
	path := parsed.EscapedPath()
	if id, valid := mediaID("media:" + attribute(node, "data-media-id")); valid {
		media, err := resolver.Media(ctx, id)
		if err != nil && !errors.Is(err, ErrUnavailable) {
			return Media{}, err
		}
		if err == nil && media.validImage() && media.matchesURL(path) {
			return media, nil
		}
	}
	if lookup, ok := resolver.(MediaURLResolver); ok {
		return lookup.MediaForURL(ctx, path)
	}
	return Media{}, ErrUnavailable
}

func standaloneCaption(image *html.Node) *html.Node {
	if image.Parent == nil || image.Parent.Data != "figure" {
		return nil
	}
	var caption *html.Node
	for child := image.Parent.FirstChild; child != nil; child = child.NextSibling {
		if child == image || child.Type == html.TextNode && strings.TrimSpace(child.Data) == "" {
			continue
		}
		if child.Type != html.ElementNode || child.Data != "figcaption" || caption != nil {
			return nil
		}
		caption = child
	}
	return caption
}

func nodeText(node *html.Node) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(parent *html.Node) {
		if parent.Type == html.TextNode {
			text.WriteString(parent.Data)
		}
		for child := parent.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return text.String()
}

func markdownElement(tag string) bool {
	switch tag {
	case "p", "br", "h1", "h2", "h3", "h4", "h5", "h6", "strong", "b", "em", "i", "s", "del", "blockquote", "code", "pre", "ul", "ol", "li", "a", "img", "hr", "table", "thead", "tbody", "tr", "th", "td", "figure", "figcaption":
		return true
	}
	return false
}
