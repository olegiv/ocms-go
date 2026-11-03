// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/olegiv/ocms-go/internal/contentpath"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// LinkPolicy applies only to external text links, never inline image sources.
type LinkPolicy string

const (
	Keep     LinkPolicy = "keep"
	NoFollow LinkPolicy = "nofollow"
	Strip    LinkPolicy = "strip"
)

// Link records a submitted URL and the limited check performed by oCMS.
// External destinations are never checked or fetched.
type Link struct {
	URL    string `json:"url"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type linkTarget struct {
	url      string
	path     string
	external bool
	fragment bool
}

func siteBase(siteURL, pagePath string) (*url.URL, error) {
	base, err := url.Parse(siteURL)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("draft processing requires an HTTP(S) site origin")
	}
	if _, err = origin(base); err != nil {
		return nil, err
	}
	if pagePath == "" {
		pagePath = "/"
	}
	page, err := url.Parse(pagePath)
	if err != nil || !strings.HasPrefix(pagePath, "/") || page.IsAbs() || page.Host != "" || page.User != nil {
		return nil, errors.New("draft page path must be local")
	}
	return base.ResolveReference(page), nil
}

func origin(value *url.URL) (string, error) { return contentpath.Origin(value) }

func targetURL(raw string, base *url.URL) (linkTarget, error) {
	if strings.IndexFunc(raw, unicode.IsControl) >= 0 || strings.Contains(raw, "\\") {
		return linkTarget{}, errors.New("unsafe link characters")
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil {
		return linkTarget{}, errors.New("invalid link URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "mailto":
		if parsed.Opaque == "" || parsed.Host != "" {
			return linkTarget{}, errors.New("invalid email link")
		}
		return linkTarget{url: parsed.String(), external: true}, nil
	case "", "http", "https":
	default:
		return linkTarget{}, errors.New("unsupported link scheme")
	}
	if parsed.Scheme == "" && parsed.Host == "" && parsed.Path == "" && parsed.RawQuery == "" {
		return linkTarget{url: parsed.String(), fragment: true}, nil
	}
	if parsed.Scheme != "" && parsed.Host == "" {
		return linkTarget{}, errors.New("HTTP link requires a host")
	}
	resolved := base.ResolveReference(parsed)
	ours, err := origin(base)
	if err != nil {
		return linkTarget{}, err
	}
	theirs, err := origin(resolved)
	if err != nil {
		return linkTarget{}, err
	}
	path := resolved.EscapedPath()
	if path == "" {
		path = "/"
	}
	if resolved.RawQuery != "" {
		path += "?" + resolved.RawQuery
	}
	return linkTarget{url: parsed.String(), path: path, external: ours != theirs}, nil
}

func parseFragment(value string) (*html.Node, error) {
	root := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(value), root)
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		root.AppendChild(node)
	}
	return root, nil
}

func renderFragment(root *html.Node) (string, error) {
	var output bytes.Buffer
	for node := root.FirstChild; node != nil; node = node.NextSibling {
		if err := html.Render(&output, node); err != nil {
			return "", err
		}
	}
	return output.String(), nil
}

func applyLinks(ctx context.Context, body string, base *url.URL, policy LinkPolicy, resolver Resolver, language string) (string, []Link, []Issue, error) {
	root, err := parseFragment(body)
	if err != nil {
		return "", nil, nil, err
	}
	var links []Link
	var issues []Issue
	seen := make(map[string]Link)
	// Issues carry no URL, so a repeat of the same code and field is a
	// duplicate; the per-link detail is in links. Reporting each kind once
	// keeps the stored and returned feedback bounded.
	reported := make(map[string]bool)
	report := func(issue Issue) {
		key := issue.Code + "\x00" + issue.Field
		if !reported[key] {
			reported[key] = true
			issues = append(issues, issue)
		}
	}
	var walk func(*html.Node) error
	walk = func(parent *html.Node) error {
		for node := parent.FirstChild; node != nil; {
			next := node.NextSibling
			if err := ctx.Err(); err != nil {
				return err
			}
			if node.Type == html.ElementNode && node.Data == "a" {
				raw := attribute(node, "href")
				target, targetErr := targetURL(raw, base)
				if targetErr != nil {
					report(NewIssue("link_not_allowed", "body_markdown", language))
					unwrap(node)
					node = next
					continue
				}
				link, exists := seen[raw]
				if !exists {
					if len(seen) == MaxLinks {
						return errTooManyLinks
					}
					link = Link{URL: target.url, Kind: "internal", Status: "existing"}
					switch {
					case target.external:
						link.Kind, link.Status = "external", "not_checked"
						if policy == Strip {
							link.Status = "removed"
						}
						report(NewIssue("external_link", "body_markdown", language))
					case target.fragment:
						link.Kind, link.Status = "fragment", "not_checked"
					default:
						found, err := resolver.InternalLink(ctx, target.path)
						if err != nil {
							return fmt.Errorf("check internal draft link: %w", err)
						}
						if !found {
							link.Status = "unresolved"
							report(NewIssue("internal_link_broken", "body_markdown", language))
						}
					}
					seen[raw] = link
					links = append(links, link)
				}
				if target.external && policy == Strip {
					unwrap(node)
					node = next
					continue
				}
				removeAttribute(node, "rel")
				if target.external {
					rel := "noopener noreferrer"
					if policy == NoFollow {
						rel += " nofollow"
					}
					node.Attr = append(node.Attr, html.Attribute{Key: "rel", Val: rel})
				}
			}
			if err := walk(node); err != nil {
				return err
			}
			node = next
		}
		return nil
	}
	if err = walk(root); errors.Is(err, errTooManyLinks) {
		// A blocking issue: no HTML is returned, so nothing is saved.
		report(NewIssue("too_many_links", "body_markdown", language))
		return "", links, issues, nil
	}
	if err != nil {
		return "", nil, nil, err
	}
	result, err := renderFragment(root)
	return result, links, issues, err
}

// errTooManyLinks stops the walk at the first destination past MaxLinks.
var errTooManyLinks = errors.New("too many distinct links")

// InspectHTMLLinks reviews an existing body without returning replacement HTML.
// Metadata-only edits preserve legacy markup; unsafe legacy anchors therefore
// produce a conversion warning instead of pretending the old body was validated.
func InspectHTMLLinks(ctx context.Context, body string, options Options) ([]Link, []Issue, error) {
	base, err := options.base()
	if err != nil {
		return nil, nil, err
	}
	_, links, issues, err := applyLinks(ctx, body, base, Keep, options.Resolver, options.UILanguage)
	if err != nil {
		return nil, nil, err
	}
	for i, issue := range issues {
		if issue.Severity == Error {
			issues[i] = NewIssue("html_conversion_lossy", "body_html", options.UILanguage)
		}
	}
	return links, issues, nil
}

func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func removeAttribute(node *html.Node, name string) {
	kept := node.Attr[:0]
	for _, attr := range node.Attr {
		if attr.Key != name {
			kept = append(kept, attr)
		}
	}
	node.Attr = kept
}

func unwrap(node *html.Node) {
	for node.FirstChild != nil {
		child := node.FirstChild
		node.RemoveChild(child)
		node.Parent.InsertBefore(child, node)
	}
	node.Parent.RemoveChild(node)
}
