// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/ai/draft"
)

func TestOriginClassificationUsesHostIDNAAndPorts(t *testing.T) {
	for _, tc := range []struct {
		site, target string
		external     bool
	}{
		{"https://example.test", "https://EXAMPLE.test:0443/existing", false},
		{"https://example.test", "https://example.test.:443/existing", false},
		{"https://bücher.example", "https://xn--bcher-kva.example/existing", false},
		{"http://[::1]", "http://[::1]:80/existing", false},
		{"https://example.test", "http://example.test/existing", true},
		{"https://example.test", "https://example.test:8443/existing", true},
		{"https://example.test", "https://example.test.evil.test/existing", true},
		{"https://example.test", "//other.test/existing", true},
	} {
		t.Run(tc.target, func(t *testing.T) {
			opt, resolver := options()
			opt.SiteURL = tc.site
			result, err := draft.RenderMarkdown(context.Background(), "[Link](<"+tc.target+">)", opt)
			if err != nil || draft.HasErrors(result.Issues) || has(result.Issues, "external_link") != tc.external {
				t.Fatalf("classification: %+v %v", result, err)
			}
			if (!tc.external && len(resolver.linkCalls) != 1) || (tc.external && len(resolver.linkCalls) != 0) {
				t.Fatalf("link check drift: calls=%v rendered=%+v", resolver.linkCalls, result)
			}
		})
	}
	opt, _ := options()
	rendered, err := draft.RenderMarkdown(context.Background(), "<hello@example.test>", opt)
	if err != nil || draft.HasErrors(rendered.Issues) || !has(rendered.Issues, "external_link") || !strings.Contains(rendered.HTML, "mailto:hello@example.test") {
		t.Fatalf("email autolink: %+v %v", rendered, err)
	}
}

func TestSlugDerivationIsBoundedAndExplicitSlugsStayExplicit(t *testing.T) {
	opt, resolver := options()
	for n := 2; n <= 10; n++ {
		resolver.occupied[fmt.Sprintf("test-%d", n)] = true
	}
	result, err := draft.NormalizeCreate(context.Background(), draft.PageDraft{Title: "Test"}, opt)
	if err != nil || result.Content != nil || !has(result.Issues, "slug_taken") {
		t.Fatalf("collision exhaustion: %+v %v", result, err)
	}
	delete(resolver.occupied, "test-2")
	result, err = draft.NormalizeCreate(context.Background(), draft.PageDraft{Title: "Different", Slug: draft.SetNullable("test")}, opt)
	if err != nil || result.Content != nil || !has(result.Issues, "slug_taken") {
		t.Fatalf("explicit occupied slug was changed: %+v %v", result, err)
	}
	result, err = draft.NormalizeCreate(context.Background(), draft.PageDraft{Title: "🚀"}, opt)
	if err != nil || result.Content == nil || result.Content.Slug != "draft" {
		t.Fatalf("ASCII fallback: %+v %v", result, err)
	}
	result, err = draft.NormalizeCreate(context.Background(), draft.PageDraft{Title: strings.Repeat("中", 200)}, opt)
	if err != nil || result.Content == nil || len(result.Content.Slug) > 255 || strings.HasSuffix(result.Content.Slug, "-") {
		t.Fatalf("transliteration exceeded slug limit: %+v %v", result, err)
	}
}

func countIssues(issues []draft.Issue, code string) int {
	n := 0
	for _, issue := range issues {
		if issue.Code == code {
			n++
		}
	}
	return n
}

// TestLinkEvidenceIsBounded checks that one body cannot make a save resolve,
// list and store an unbounded number of links. A 70 KB body of distinct
// links produced about 6,000 issues and 1.6 MB of stored evidence per
// revision (2026-11-03 audit B-1).
//
// Bug state: remove the MaxLinks check or the per-kind issue deduplication
// from applyLinks and this fails.
func TestLinkEvidenceIsBounded(t *testing.T) {
	body := func(n int, format string) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, format, i)
		}
		return b.String()
	}

	opt, resolver := options()
	rendered, err := draft.RenderMarkdown(context.Background(), body(draft.MaxLinks/2, "[a](/p%d) ")+body(draft.MaxLinks/2, "[b](https://outside.test/%d) "), opt)
	if err != nil || draft.HasErrors(rendered.Issues) || rendered.HTML == "" {
		t.Fatalf("body at the link limit rejected: %v %+v", err, rendered.Issues)
	}
	if got := countIssues(rendered.Issues, "internal_link_broken"); got != 1 {
		t.Errorf("internal_link_broken reported %d times, want once", got)
	}
	if got := countIssues(rendered.Issues, "external_link"); got != 1 {
		t.Errorf("external_link reported %d times, want once", got)
	}
	if len(rendered.Links) != draft.MaxLinks || len(resolver.linkCalls) != draft.MaxLinks/2 {
		t.Errorf("links listed %d, resolver calls %d", len(rendered.Links), len(resolver.linkCalls))
	}

	opt, resolver = options()
	rendered, err = draft.RenderMarkdown(context.Background(), body(draft.MaxLinks+1, "[a](/p%d) "), opt)
	if err != nil || !has(rendered.Issues, "too_many_links") || rendered.HTML != "" {
		t.Fatalf("body past the link limit accepted: %v %+v", err, rendered.Issues)
	}
	if len(rendered.Links) > draft.MaxLinks || len(resolver.linkCalls) > draft.MaxLinks || len(rendered.Issues) > 3 {
		t.Errorf("rejected body still produced links=%d calls=%d issues=%d", len(rendered.Links), len(resolver.linkCalls), len(rendered.Issues))
	}

	// Repeats of one destination count once toward the limit.
	opt, _ = options()
	rendered, err = draft.RenderMarkdown(context.Background(), strings.Repeat("[same](/existing) ", draft.MaxLinks+50), opt)
	if err != nil || draft.HasErrors(rendered.Issues) || len(rendered.Links) != 1 {
		t.Fatalf("repeated link counted per occurrence: %v %+v links=%d", err, rendered.Issues, len(rendered.Links))
	}
}
