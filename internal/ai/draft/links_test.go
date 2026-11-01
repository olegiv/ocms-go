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
