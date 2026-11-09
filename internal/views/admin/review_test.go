// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
	"golang.org/x/net/html"
)

func TestReviewPanelKeepsSpoofedClaimsInsideReportedSection(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	details := review.Review{Fact: store.ContentProvenance{ID: 7, UserID: sql.NullInt64{Int64: 42, Valid: true}, Source: "oauth", Transport: "mcp", CreatedAt: time.Now(), OauthClientID: "https://client.example.test/metadata"}, ActorName: "Actual recorded editor", Reported: review.Report{Agent: draft.AgentReport{Model: draft.Set("FAKE_VERIFIED_MODEL"), Notes: draft.Set("<script>FAKE_VERIFIED_IDENTITY</script>")}}, Claims: []review.Claim{{Index: 0, Text: "FAKE_VERIFIED_CLAIM"}}}
	pc := &PageContext{AdminLang: "en"}
	var rendered bytes.Buffer
	if err := EditorialReviewPanel(pc, ReviewPanelData{PageID: 3, Revision: 5, BaseRevision: 6, Current: false, Details: details, PreviewURL: "/en/preview/pages/3/revisions/5"}).Render(context.Background(), &rendered); err != nil {
		t.Fatal(err)
	}
	doc, err := html.Parse(strings.NewReader(rendered.String()))
	if err != nil {
		t.Fatal(err)
	}
	sections := map[string]string{}
	var text func(*html.Node) string
	text = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var result strings.Builder
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			result.WriteString(text(child))
		}
		return result.String()
	}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		for _, a := range n.Attr {
			if a.Key == "data-provenance-class" {
				sections[a.Val] = text(n)
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	if len(sections) != 3 || !strings.Contains(sections["verified"], "Actual recorded editor") {
		t.Fatal("three evidence classes or authenticated principal missing")
	}
	for _, section := range []string{"verified", "validated"} {
		if strings.Contains(sections[section], "FAKE_VERIFIED") {
			t.Fatal("agent assertions promoted to verified/validated")
		}
	}
	if !strings.Contains(sections["reported"], "FAKE_VERIFIED_CLAIM") || strings.Contains(rendered.String(), "<script>FAKE_VERIFIED_IDENTITY</script>") {
		t.Fatal("reported claims were omitted or executable")
	}
	if !strings.Contains(rendered.String(), "disabled") || !strings.Contains(rendered.String(), `value="5"`) {
		t.Fatal("historical claim controls did not retain revision binding")
	}
}

func TestReviewIssueDisplayLocalizesWithoutChangingRecordedEvidence(t *testing.T) {
	stored := draft.NewIssue("external_link", "body_markdown", "en")
	localized := reviewLocalizedIssues([]draft.Issue{stored}, "ru")
	want := draft.NewIssue("external_link", "body_markdown", "ru")
	if len(localized) != 1 || localized[0].Message != want.Message || localized[0].Code != stored.Code || localized[0].Severity != stored.Severity || stored.Message == want.Message {
		t.Fatal("display localization changed recorded identity or lost Russian text")
	}
	unknown := draft.Issue{Code: "historical_unknown", Message: "Recorded historical issue", Severity: draft.Warning}
	if got := reviewLocalizedIssues([]draft.Issue{unknown}, "ru"); got[0] != unknown {
		t.Fatal("unknown historical issue was replaced")
	}
}

func TestTranslationReviewFormBindsBothRevisionsAndHidesHistoricalAction(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"en", "ru"} {
		for _, current := range []bool{true, false} {
			data := ReviewPanelData{PageID: 43, Revision: 5, Current: current, Details: review.Review{TranslationSource: &review.TranslationSource{PageID: 27, Revision: 7, Title: "<script>untrusted source</script>", Language: "en"}}}
			var body bytes.Buffer
			if err := EditorialReviewPanel(&PageContext{AdminLang: language}, data).Render(context.Background(), &body); err != nil {
				t.Fatal(err)
			}
			markup := body.String()
			if strings.Contains(markup, `action="/admin/pages/43/translation-review"`) != current {
				t.Fatal("historical action exposed or current action missing")
			}
			if !current {
				continue
			}
			for _, required := range []string{`name="base_revision" value="5"`, `name="source_page_id" value="27"`, `name="source_revision" value="7"`, `id="translation-acknowledge"`, `required`, `/en/preview/pages/27/revisions/7`, i18n.T(language, "review.translation_save"), i18n.T(language, "review.translation_acknowledge")} {
				if !strings.Contains(markup, required) {
					t.Fatalf("missing %q", required)
				}
			}
			if strings.Contains(markup, "<script>untrusted source</script>") {
				t.Fatal("source title executed as markup")
			}
		}
	}
}
