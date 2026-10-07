// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package seo

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFeedSummary(t *testing.T) {
	for _, tc := range []struct{ name, summary, body, want string }{
		{"saved summary", "<b>Saved &amp; ready</b>", "Ignored body", "Saved & ready"},
		{"block boundaries", "", "<p>Hello <strong>world</strong></p><p>Next<br>line</p>", "Hello world Next line"},
		{"scripts and styles", "", "<p>Visible</p><script>secret()</script><style>hidden {}</style><p>After</p>", "Visible After"},
		{"self-closing script spelling", "", "<p>Visible</p><script/>secret()</script><style/>hidden {}</style><p>After</p>", "Visible After"},
		{"empty saved summary falls back", "<style>hidden {}</style>", "<p>Fallback</p>", "Fallback"},
		{"unicode and entities", "", "<p>Привет&nbsp;Луна 🐕 &amp; друзья</p>", "Привет Луна 🐕 & друзья"},
		{"inline boundaries", "", "Hel<b>lo</b> <!-- hidden -->world", "Hello world"},
		{"literal encoded markup", "", "&lt;b&gt;literal&lt;/b&gt;", "<b>literal</b>"},
		{"empty", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FeedSummary(tc.summary, tc.body); got != tc.want {
				t.Errorf("FeedSummary = %q, want %q", got, tc.want)
			}
		})
	}
	for _, text := range []string{strings.Repeat("Я🐕", 200), strings.Repeat("word ", 100)} {
		got := FeedSummary("", text)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 300 || !strings.HasSuffix(got, "…") {
			t.Errorf("excerpt must be valid UTF-8 and bounded to 300 characters: %q", got)
		}
	}
	if saved := strings.Repeat("Я", 400); FeedSummary(saved, "") != saved {
		t.Error("saved summary must not be truncated")
	}
}

func TestNormalizeFeedOrigin(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{" https://example.com/// ", "https://example.com"},
		{"http://localhost:8080/", "http://localhost:8080"},
		{"https://[::1]:443", "https://[::1]:443"},
	} {
		got, err := NormalizeFeedOrigin(tc.raw)
		if err != nil || got != tc.want {
			t.Errorf("NormalizeFeedOrigin(%q) = %q, %v; want %q", tc.raw, got, err, tc.want)
		}
	}
	for _, raw := range []string{
		"", "example.com", "ftp://example.com", "https://:443", "https://example.com/path",
		"https://user:secret@example.com", "https://example.com?", "https://example.com?a=1",
		"https://example.com#", "https://example.com#fragment", "https://example.com:0", "https://example.com:65536",
	} {
		if got, err := NormalizeFeedOrigin(raw); err == nil {
			t.Errorf("NormalizeFeedOrigin(%q) unexpectedly accepted %q", raw, got)
		}
	}
}

func TestFeedXML(t *testing.T) {
	published := time.Date(2026, 10, 1, 12, 0, 0, 0, time.FixedZone("CEST", 7200))
	feed := Feed{
		Title: "News & Луна", Description: "A <plain> description", URL: "https://example.com/rss.xml",
		SiteURL: "https://example.com", Language: "ru", Author: "News & Луна", UpdatedAt: published.Add(time.Hour),
		Entries: []FeedEntry{{
			ID: "https://example.com/page/42", Title: "Привет <Луна>", URL: "https://example.com/ru/hello",
			Summary: "Fish & chips 🐕 <script>literal</script>", Author: "Олег",
			PublishedAt: published, UpdatedAt: published.Add(time.Hour),
		}},
	}
	t.Run("RSS", func(t *testing.T) {
		body, err := BuildRSSFeed(feed)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Version string `xml:"version,attr"`
			Channel struct {
				Title       string   `xml:"title"`
				Description string   `xml:"description"`
				Language    string   `xml:"language"`
				Self        feedLink `xml:"http://www.w3.org/2005/Atom link"`
				Items       []struct {
					Title       string  `xml:"title"`
					Description string  `xml:"description"`
					GUID        rssGUID `xml:"guid"`
					Published   string  `xml:"pubDate"`
					Updated     string  `xml:"http://www.w3.org/2005/Atom updated"`
					Creator     string  `xml:"http://purl.org/dc/elements/1.1/ creator"`
				} `xml:"item"`
			} `xml:"channel"`
		}
		if err := xml.Unmarshal(body, &doc); err != nil {
			t.Fatalf("invalid RSS: %v\n%s", err, body)
		}
		if doc.Version != "2.0" || doc.Channel.Self.Href != feed.URL || len(doc.Channel.Items) != 1 ||
			doc.Channel.Title != feed.Title || doc.Channel.Description != feed.Description || doc.Channel.Language != feed.Language {
			t.Fatalf("RSS metadata or items missing: %s", body)
		}
		item := doc.Channel.Items[0]
		if item.Title != feed.Entries[0].Title || item.Description != feed.Entries[0].Summary ||
			item.GUID.Value != feed.Entries[0].ID || item.GUID.IsPermaLink || item.Creator != "Олег" {
			t.Errorf("RSS entry does not preserve public text and ID: %+v", item)
		}
		if _, err := time.Parse(time.RFC1123Z, item.Published); err != nil {
			t.Errorf("invalid RSS publication date: %v", err)
		}
		if _, err := time.Parse(time.RFC3339, item.Updated); err != nil {
			t.Errorf("invalid RSS update date: %v", err)
		}
		assertDeterministicFeed(t, body, func() ([]byte, error) { return BuildRSSFeed(feed) })
	})
	t.Run("Atom", func(t *testing.T) {
		feed.URL = "https://example.com/atom.xml"
		body, err := BuildAtomFeed(feed)
		if err != nil {
			t.Fatal(err)
		}
		var doc atomDocument
		if err := xml.Unmarshal(body, &doc); err != nil {
			t.Fatalf("invalid Atom: %v\n%s", err, body)
		}
		if doc.XMLName.Space != atomNamespace || doc.ID != feed.URL || doc.Author.Name != feed.Author ||
			doc.Title.Value != feed.Title || len(doc.Entries) != 1 {
			t.Fatalf("required Atom feed fields missing: %s", body)
		}
		entry := doc.Entries[0]
		if entry.Summary.Type != "text" || entry.Summary.Value != feed.Entries[0].Summary ||
			entry.Author.Name != "Олег" || entry.ID != feed.Entries[0].ID || entry.Link.Href != feed.Entries[0].URL {
			t.Errorf("Atom entry does not preserve text and public links: %+v", entry)
		}
		for _, date := range []string{doc.Updated, entry.Published, entry.Updated} {
			if _, err := time.Parse(time.RFC3339, date); err != nil {
				t.Errorf("invalid Atom date: %v", err)
			}
		}
		assertDeterministicFeed(t, body, func() ([]byte, error) { return BuildAtomFeed(feed) })
	})
	t.Run("empty Atom and author fallback", func(t *testing.T) {
		feed.Entries = nil
		body, err := BuildAtomFeed(feed)
		if err != nil {
			t.Fatal(err)
		}
		var doc atomDocument
		if err := xml.Unmarshal(body, &doc); err != nil || doc.Author.Name == "" || doc.Updated == "" || len(doc.Entries) != 0 {
			t.Fatalf("empty feed must retain required metadata: %v\n%s", err, body)
		}
		feed.Entries = []FeedEntry{{ID: "https://example.com/page/1", PublishedAt: published, UpdatedAt: published}}
		body, err = BuildAtomFeed(feed)
		if err != nil {
			t.Fatal(err)
		}
		if err := xml.Unmarshal(body, &doc); err != nil || doc.Entries[0].Author.Name != feed.Author {
			t.Fatalf("empty author must fall back to site name: %v\n%s", err, body)
		}
	})
}

func assertDeterministicFeed(t *testing.T, body []byte, build func() ([]byte, error)) {
	t.Helper()
	again, err := build()
	if err != nil || !bytes.Equal(body, again) || !bytes.HasPrefix(body, []byte(xml.Header)) {
		t.Errorf("XML must have a declaration and remain deterministic: %v", err)
	}
}
