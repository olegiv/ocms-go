// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package seo

import (
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Feed media types identify the two supported syndication formats.
const (
	RSSMediaType  = "application/rss+xml"
	AtomMediaType = "application/atom+xml"
	atomNamespace = "http://www.w3.org/2005/Atom"
)

// Feed contains the public metadata and entries shared by RSS and Atom.
type Feed struct {
	Title       string
	Description string
	URL         string // Self URL of this feed.
	SiteURL     string // Corresponding homepage or taxonomy archive.
	Language    string
	Author      string // Site name, also the fallback Atom author.
	UpdatedAt   time.Time
	Entries     []FeedEntry
}

// FeedEntry contains public post data, never an author's email address.
type FeedEntry struct {
	ID          string // Stable absolute identifier independent of the post slug.
	Title       string
	URL         string
	Summary     string
	Author      string
	PublishedAt time.Time
	UpdatedAt   time.Time
}

// NormalizeFeedOrigin validates and normalizes a configured HTTP(S) origin.
// Feeds must not derive their absolute URLs from the incoming Host header.
func NormalizeFeedOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") ||
		strings.Trim(u.EscapedPath(), "/") != "" {
		return "", fmt.Errorf("site_url must be a valid HTTP(S) origin")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("site_url port must be in the range 1-65535")
		}
	}
	return u.Scheme + "://" + u.Host, nil
}

// FeedSummary prefers saved summary text, otherwise excerpts the HTML body.
// Only the fallback is truncated, to at most 300 Unicode characters.
func FeedSummary(summary, body string) string {
	if text := feedPlainText(summary); text != "" {
		return text
	}
	text := feedPlainText(body)
	const maxRunes = 300
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)[:maxRunes-1]
	// Prefer a nearby word boundary, keeping unspaced text useful as well.
	for i := len(runes) - 1; i >= len(runes)/2; i-- {
		if runes[i] == ' ' {
			runes = runes[:i]
			break
		}
	}
	return strings.TrimSpace(string(runes)) + "…"
}

func feedPlainText(raw string) string {
	z := html.NewTokenizer(strings.NewReader(raw))
	var text strings.Builder
	var skipTag string
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(text.String()), " ")
		case html.TextToken:
			if skipTag == "" {
				text.Write(z.Text())
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			token := z.Token()
			kind, name := token.Type, token.Data
			if skipTag != "" {
				if kind == html.EndTagToken && name == skipTag {
					skipTag = ""
				}
				continue
			}
			if name == "script" || name == "style" {
				if kind != html.EndTagToken {
					skipTag = name
				}
				continue
			}
			if feedBlockTag(name) {
				text.WriteByte(' ')
			}
		}
	}
}

func feedBlockTag(name string) bool {
	switch name {
	case "address", "article", "aside", "blockquote", "br", "dd", "div", "dl", "dt",
		"fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4",
		"h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section",
		"table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}

type feedLink struct {
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
	Href string `xml:"href,attr"`
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	AtomNS  string     `xml:"xmlns:atom,attr"`
	DCNS    string     `xml:"xmlns:dc,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	Language      string    `xml:"language"`
	LastBuildDate string    `xml:"lastBuildDate"`
	Self          feedLink  `xml:"atom:link"`
	Items         []rssItem `xml:"item"`
}

type rssGUID struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type rssItem struct {
	Title       string  `xml:"title"`
	Link        string  `xml:"link"`
	Description string  `xml:"description"`
	GUID        rssGUID `xml:"guid"`
	Published   string  `xml:"pubDate"`
	Updated     string  `xml:"atom:updated"`
	Creator     string  `xml:"dc:creator,omitempty"`
}

// BuildRSSFeed serializes RSS 2.0 with Atom self/update and Dublin Core author extensions.
func BuildRSSFeed(feed Feed) ([]byte, error) {
	channel := rssChannel{
		Title: feed.Title, Link: feed.SiteURL, Description: feed.Description,
		Language: feed.Language, LastBuildDate: feed.UpdatedAt.UTC().Format(time.RFC1123Z),
		Self: feedLink{Rel: "self", Type: RSSMediaType, Href: feed.URL},
	}
	for _, entry := range feed.Entries {
		channel.Items = append(channel.Items, rssItem{
			Title: entry.Title, Link: entry.URL, Description: html.EscapeString(entry.Summary),
			GUID: rssGUID{Value: entry.ID}, Published: entry.PublishedAt.UTC().Format(time.RFC1123Z),
			Updated: entry.UpdatedAt.UTC().Format(time.RFC3339), Creator: entry.Author,
		})
	}
	return marshalFeed(rssDocument{
		Version: "2.0", AtomNS: atomNamespace, DCNS: "http://purl.org/dc/elements/1.1/", Channel: channel,
	})
}

type atomText struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomDocument struct {
	XMLName  xml.Name    `xml:"feed"`
	XMLNS    string      `xml:"xmlns,attr"`
	Language string      `xml:"xml:lang,attr"`
	ID       string      `xml:"id"`
	Title    atomText    `xml:"title"`
	Subtitle atomText    `xml:"subtitle"`
	Updated  string      `xml:"updated"`
	Author   atomAuthor  `xml:"author"`
	Links    []feedLink  `xml:"link"`
	Entries  []atomEntry `xml:"entry"`
}

type atomEntry struct {
	ID        string     `xml:"id"`
	Title     atomText   `xml:"title"`
	Summary   atomText   `xml:"summary"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
	Author    atomAuthor `xml:"author"`
	Link      feedLink   `xml:"link"`
}

// BuildAtomFeed serializes an Atom 1.0 feed with plain-text summaries.
func BuildAtomFeed(feed Feed) ([]byte, error) {
	doc := atomDocument{
		XMLNS: atomNamespace, Language: feed.Language, ID: feed.URL,
		Title:    atomText{Type: "text", Value: feed.Title},
		Subtitle: atomText{Type: "text", Value: feed.Description},
		Updated:  feed.UpdatedAt.UTC().Format(time.RFC3339), Author: atomAuthor{Name: feed.Author},
		Links: []feedLink{
			{Rel: "self", Type: AtomMediaType, Href: feed.URL},
			{Rel: "alternate", Type: "text/html", Href: feed.SiteURL},
		},
	}
	for _, entry := range feed.Entries {
		author := entry.Author
		if author == "" {
			author = feed.Author
		}
		doc.Entries = append(doc.Entries, atomEntry{
			ID: entry.ID, Title: atomText{Type: "text", Value: entry.Title},
			Summary:   atomText{Type: "text", Value: entry.Summary},
			Published: entry.PublishedAt.UTC().Format(time.RFC3339), Updated: entry.UpdatedAt.UTC().Format(time.RFC3339),
			Author: atomAuthor{Name: author}, Link: feedLink{Rel: "alternate", Type: "text/html", Href: entry.URL},
		})
	}
	return marshalFeed(doc)
}

func marshalFeed(doc any) ([]byte, error) {
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal feed: %w", err)
	}
	return append([]byte(xml.Header), body...), nil
}
