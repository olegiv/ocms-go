// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/idna"
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

func origin(value *url.URL) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(value.Hostname()), ".")
	if address, err := netip.ParseAddr(host); err == nil {
		host = address.String()
	} else {
		var convertErr error
		host, convertErr = idna.Lookup.ToASCII(host)
		if convertErr != nil || host == "" {
			return "", errors.New("invalid site/link host")
		}
	}
	port := value.Port()
	if port == "" {
		switch strings.ToLower(value.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		default:
			return "", errors.New("unsupported site/link scheme")
		}
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", errors.New("invalid site/link port")
	}
	port = strconv.FormatUint(parsedPort, 10)
	return strings.ToLower(value.Scheme) + "://" + net.JoinHostPort(host, port), nil
}

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
					issues = append(issues, NewIssue("link_not_allowed", "body_markdown", language))
					unwrap(node)
					node = next
					continue
				}
				link, exists := seen[raw]
				if !exists {
					link = Link{URL: target.url, Kind: "internal", Status: "existing"}
					switch {
					case target.external:
						link.Kind, link.Status = "external", "not_checked"
						if policy == Strip {
							link.Status = "removed"
						}
						issues = append(issues, NewIssue("external_link", "body_markdown", language))
					case target.fragment:
						link.Kind, link.Status = "fragment", "not_checked"
					default:
						found, err := resolver.InternalLink(ctx, target.path)
						if err != nil {
							return fmt.Errorf("check internal draft link: %w", err)
						}
						if !found {
							link.Status = "unresolved"
							issues = append(issues, NewIssue("internal_link_broken", "body_markdown", language))
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
	if err = walk(root); err != nil {
		return "", nil, nil, err
	}
	result, err := renderFragment(root)
	return result, links, issues, err
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
