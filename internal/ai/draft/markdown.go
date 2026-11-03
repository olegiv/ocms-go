// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	gmutil "github.com/yuin/goldmark/util"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/security"
)

// Rendered contains safe HTML only when no blocking issue was found.
type Rendered struct {
	HTML   string
	Links  []Link
	Issues []Issue
}

type inlineImage struct {
	html    string
	caption string
}

// RenderMarkdown parses Markdown with raw HTML disabled, resolves media before
// sanitization, applies the link policy and bounds the resulting HTML too.
func RenderMarkdown(ctx context.Context, body string, options Options) (Rendered, error) {
	result := Rendered{Issues: validateBody(body, options.UILanguage)}
	if HasErrors(result.Issues) {
		return result, nil
	}
	base, err := options.base()
	if err != nil {
		return result, err
	}
	images := make(map[ast.Node]inlineImage)
	links := make(map[ast.Node]string)
	mediaCache := make(map[int64]Media)
	converter := goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.Linkify),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(gmutil.Prioritized(&markdownRenderer{images: images, links: links}, 100))))
	source := []byte(body)
	document := converter.Parser().Parse(text.NewReader(source))
	err = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ast.WalkStop, ctxErr
		}
		switch typed := node.(type) {
		case *ast.HTMLBlock, *ast.RawHTML:
			result.Issues = append(result.Issues, NewIssue("raw_html_not_allowed", "body_markdown", options.UILanguage))
			return ast.WalkStop, nil
		case *ast.Image:
			destination := markdownDestination(typed.Destination)
			id, valid := mediaID(destination)
			if !valid {
				result.Issues = append(result.Issues, NewIssue("media_ref_not_found", "body_markdown", options.UILanguage))
				return ast.WalkStop, nil
			}
			media, found := mediaCache[id]
			if !found {
				media, err = options.Resolver.Media(ctx, id)
				if errors.Is(err, ErrUnavailable) || err == nil && !media.validImage() {
					result.Issues = append(result.Issues, NewIssue("media_ref_not_found", "body_markdown", options.UILanguage))
					return ast.WalkStop, nil
				}
				if err != nil {
					return ast.WalkStop, fmt.Errorf("resolve draft inline image: %w", err)
				}
				mediaCache[id] = media
			}
			alt := strings.TrimSpace(imageAlt(typed, source))
			if alt == "" {
				alt = media.Alt
			}
			caption := string(typed.Title)
			if caption == "" {
				caption = media.Caption
			}
			images[node] = inlineImage{html: imageHTML(media, alt), caption: caption}
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			target, targetErr := targetURL(markdownDestination(typed.Destination), base)
			if targetErr != nil {
				result.Issues = append(result.Issues, NewIssue("link_not_allowed", "body_markdown", options.UILanguage))
				return ast.WalkStop, nil
			}
			links[node] = target.url
		case *ast.AutoLink:
			value := string(typed.URL(source))
			if typed.AutoLinkType == ast.AutoLinkEmail {
				value = "mailto:" + value
			}
			target, targetErr := targetURL(markdownDestination([]byte(value)), base)
			if targetErr != nil {
				result.Issues = append(result.Issues, NewIssue("link_not_allowed", "body_markdown", options.UILanguage))
				return ast.WalkStop, nil
			}
			links[node] = target.url
		}
		return ast.WalkContinue, nil
	})
	if err != nil || HasErrors(result.Issues) {
		return result, err
	}
	var rendered bytes.Buffer
	if err = converter.Renderer().Render(&rendered, source, document); err != nil {
		return result, fmt.Errorf("render draft Markdown: %w", err)
	}
	safe := security.SanitizePageHTML(rendered.String())
	if len(safe) > MaxBodyBytes {
		result.Issues = append(result.Issues, NewIssue("body_too_large", "body_markdown", options.UILanguage))
		return result, nil
	}
	bodyHTML, linkResults, issues, err := applyLinks(ctx, safe, base, options.linkPolicy(), options.Resolver, options.UILanguage)
	if err != nil {
		return result, err
	}
	result.Links, result.Issues = linkResults, append(result.Issues, issues...)
	if len(bodyHTML) > MaxBodyBytes {
		result.Issues = append(result.Issues, NewIssue("body_too_large", "body_markdown", options.UILanguage))
	}
	if !HasErrors(result.Issues) {
		result.HTML = bodyHTML
	}
	return result, nil
}

func markdownDestination(value []byte) string {
	return html.UnescapeString(string(gmutil.UnescapePunctuations(value)))
}

func mediaID(value string) (int64, bool) {
	digits, ok := strings.CutPrefix(value, "media:")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(digits, 10, 64)
	return id, err == nil && id > 0 && strconv.FormatInt(id, 10) == digits
}

func imageAlt(node ast.Node, source []byte) string {
	var value strings.Builder
	var walk func(ast.Node)
	walk = func(parent ast.Node) {
		for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
			switch typed := child.(type) {
			case *ast.Text:
				value.Write(typed.Value(source))
			case *ast.String:
				value.Write(typed.Value)
			default:
				walk(child)
			}
		}
	}
	walk(node)
	return html.UnescapeString(value.String())
}

func imageHTML(media Media, alt string) string {
	var variants []Variant
	for _, variant := range media.Variants {
		if (variant.Type == model.VariantSmall || variant.Type == model.VariantMedium || variant.Type == model.VariantLarge) && variant.Width > 0 && variant.Height > 0 {
			variants = append(variants, variant)
		}
	}
	slices.SortFunc(variants, func(a, b Variant) int {
		if a.Width < b.Width {
			return -1
		}
		if a.Width > b.Width {
			return 1
		}
		return strings.Compare(a.Type, b.Type)
	})
	variants = slices.CompactFunc(variants, func(a, b Variant) bool { return a.Width == b.Width })
	src := model.MediaURL(model.VariantOriginal, media.UUID, media.Filename)
	width, height := media.Width, media.Height
	if len(variants) > 0 {
		main := variants[len(variants)-1]
		src = model.MediaURL(main.Type, media.UUID, media.Filename)
		width, height = main.Width, main.Height
	}
	var output strings.Builder
	_, _ = fmt.Fprintf(&output, `<img src="%s" alt="%s" data-media-id="%d" loading="lazy" decoding="async"`, html.EscapeString(src), html.EscapeString(alt), media.ID)
	if width > 0 && height > 0 {
		_, _ = fmt.Fprintf(&output, ` width="%d" height="%d"`, width, height)
	}
	if len(variants) > 1 {
		var candidates []string
		for _, variant := range variants {
			candidates = append(candidates, fmt.Sprintf("%s %dw", model.MediaURL(variant.Type, media.UUID, media.Filename), variant.Width))
		}
		_, _ = fmt.Fprintf(&output, ` srcset="%s" sizes="(max-width: 800px) 100vw, 800px"`, html.EscapeString(strings.Join(candidates, ", ")))
	}
	output.WriteString(">")
	return output.String()
}

type markdownRenderer struct {
	images map[ast.Node]inlineImage
	links  map[ast.Node]string
}

func (r *markdownRenderer) RegisterFuncs(registry renderer.NodeRendererFuncRegisterer) {
	registry.Register(ast.KindImage, r.renderImage)
	registry.Register(ast.KindParagraph, r.renderParagraph)
	registry.Register(ast.KindLink, r.renderLink)
	registry.Register(ast.KindAutoLink, r.renderAutoLink)
}

func (r *markdownRenderer) renderImage(writer gmutil.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	_, err := writer.WriteString(r.images[node].html)
	return ast.WalkSkipChildren, err
}

func (r *markdownRenderer) renderParagraph(writer gmutil.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	image, standalone := r.images[node.FirstChild()]
	standalone = standalone && node.FirstChild().NextSibling() == nil
	tag := "p"
	if standalone {
		tag = "figure"
	}
	if entering {
		_, err := writer.WriteString("<" + tag + ">")
		return ast.WalkContinue, err
	}
	if standalone && image.caption != "" {
		if _, err := writer.WriteString("<figcaption>" + html.EscapeString(image.caption) + "</figcaption>"); err != nil {
			return ast.WalkStop, err
		}
	}
	_, err := writer.WriteString("</" + tag + ">\n")
	return ast.WalkContinue, err
}

func (r *markdownRenderer) renderLink(writer gmutil.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, err := writer.WriteString("</a>")
		return ast.WalkContinue, err
	}
	// The destination was parsed and scheme-checked before rendering. Escape it
	// as an attribute, not as a whole URI: escaping IPv6 host brackets as path
	// characters produces an invalid host that the final sanitizer removes.
	opening := `<a href="` + html.EscapeString(r.links[node]) + `"`
	link := node.(*ast.Link)
	if len(link.Title) > 0 {
		opening += ` title="` + html.EscapeString(html.UnescapeString(string(link.Title))) + `"`
	}
	_, err := writer.WriteString(opening + ">")
	return ast.WalkContinue, err
}

func (r *markdownRenderer) renderAutoLink(writer gmutil.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	link := node.(*ast.AutoLink)
	_, err := writer.WriteString(`<a href="` + html.EscapeString(r.links[node]) + `">` + html.EscapeString(string(link.Label(source))) + `</a>`)
	return ast.WalkSkipChildren, err
}
