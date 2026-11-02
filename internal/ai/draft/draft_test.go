// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/idempotency"
	"github.com/olegiv/ocms-go/internal/security"
)

type fixtureResolver struct {
	occupied   map[string]bool
	media      map[int64]draft.Media
	linkCalls  []string
	mediaErr   error
	mediaCalls int
}

func (f *fixtureResolver) Language(_ context.Context, code string) (bool, error) {
	return code == "en" || code == "ru", nil
}
func (f *fixtureResolver) SlugAvailable(_ context.Context, slug string, _ int64) (bool, error) {
	return !f.occupied[slug], nil
}
func (f *fixtureResolver) Tag(_ context.Context, slug, language string) (bool, error) {
	return slug == "go" && language == "en", nil
}
func (f *fixtureResolver) Category(_ context.Context, id int64, language string) (bool, error) {
	return id == 3 && language == "en", nil
}
func (f *fixtureResolver) Media(_ context.Context, id int64) (draft.Media, error) {
	f.mediaCalls++
	if f.mediaErr != nil {
		return draft.Media{}, f.mediaErr
	}
	media, ok := f.media[id]
	if !ok {
		return draft.Media{}, draft.ErrUnavailable
	}
	return media, nil
}

func TestInvalidBodyStopsResolutionAndExpandedHTMLIsBounded(t *testing.T) {
	opt, resolver := options()
	result, err := draft.RenderMarkdown(context.Background(), strings.Repeat("![Missing](media:99)\n\n", 1000), opt)
	if err != nil || !draft.HasErrors(result.Issues) || result.HTML != "" || resolver.mediaCalls != 1 {
		t.Fatalf("invalid body caused repeated database work: calls=%d result=%+v err=%v", resolver.mediaCalls, result, err)
	}
	result, err = draft.RenderMarkdown(context.Background(), strings.Repeat("& ", 40_000), opt)
	if err != nil || !has(result.Issues, "body_too_large") || result.HTML != "" {
		t.Fatalf("expanded HTML exceeded its cap: %+v %v", result, err)
	}
}

func TestReturnedSchemasCannotChangeCachedValidation(t *testing.T) {
	schema, err := draft.CreateSchema()
	if err != nil {
		t.Fatal(err)
	}
	schema.Required = nil
	title := schema.Properties["title"]
	if title == nil {
		t.Fatal("create schema has no title property")
	}
	title.MaxLength = nil
	delete(schema.Properties, "title")
	if _, err := draft.DecodeCreate([]byte(`{"summary":"","body_markdown":""}`), "en"); err == nil {
		t.Fatal("caller mutated the server's required fields")
	}
	if _, err := draft.DecodeCreate([]byte(`{"title":"`+strings.Repeat("x", 256)+`","summary":"","body_markdown":""}`), "en"); err == nil {
		t.Fatal("caller mutated the server's title limit")
	}
}
func (f *fixtureResolver) InternalLink(_ context.Context, path string) (bool, error) {
	f.linkCalls = append(f.linkCalls, path)
	return path == "/existing" || path == "/ru/existing", nil
}

func options() (draft.Options, *fixtureResolver) {
	resolver := &fixtureResolver{occupied: map[string]bool{"test": true}, media: map[int64]draft.Media{1: {
		ID: 1, UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Filename: "image with space.jpg", MimeType: "image/jpeg", Alt: "Library alt", Caption: "Library caption", Width: 1536, Height: 1024, DimensionsKnown: true,
		Variants: []draft.Variant{{Type: "small", Width: 400, Height: 267}, {Type: "medium", Width: 800, Height: 533}, {Type: "large", Width: 1536, Height: 1024}},
	}}}
	return draft.Options{Resolver: resolver, DefaultLanguage: "en", UILanguage: "en", SiteURL: "https://example.test"}, resolver
}

func has(issues []draft.Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestDistinctSchemasRejectUnknownForbiddenAndNullFields(t *testing.T) {
	validCreate := `{"title":"Test","summary":"","body_markdown":""}`
	for _, raw := range []string{validCreate, `{"title":"Test","summary":"","body_markdown":"","slug":null,"category_id":null,"tags":[]}`} {
		if _, err := draft.DecodeCreate([]byte(raw), "en"); err != nil {
			t.Fatalf("valid create %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{}`, `null`, `[]`, validCreate + `{}`, `{"title":"A","title":"B","summary":"","body_markdown":""}`, `{"title":"A","summary":null,"body_markdown":""}`, `{"title":"A","summary":"","body_markdown":"","tags":null}`, `{"title":"A","summary":"","body_markdown":"","typo":true}`, `{"title":"A","summary":"","body_markdown":"","reported":{"verified":true}}`} {
		if _, err := draft.DecodeCreate([]byte(raw), "en"); err == nil {
			t.Fatalf("invalid create accepted: %s", raw)
		}
	}
	for _, raw := range []string{`{"title":"New"}`, `{"meta_title":null,"tags":[],"category_id":null}`, `{"summary":"","body_markdown":""}`} {
		if _, err := draft.DecodeChanges([]byte(raw), "en"); err != nil {
			t.Fatalf("valid update %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{}`, `null`, `{"title":null}`, `{"body_markdown":null}`, `{"summary":null}`, `{"slug":null}`, `{"tags":null}`, `{"reported":null}`, `{"typo":"value"}`} {
		if _, err := draft.DecodeChanges([]byte(raw), "en"); err == nil {
			t.Fatalf("invalid update accepted: %s", raw)
		}
	}
	for _, field := range []string{"status", "scheduled_at", "published_at", "author_id", "ai_editable", "verified", "revision", "page_type", "language_code"} {
		_, err := draft.DecodeChanges(fmt.Appendf(nil, `{"%s":null}`, field), "ru")
		var rejected *draft.ValidationError
		if !errors.As(err, &rejected) || !has(rejected.Issues, "forbidden_field") {
			t.Fatalf("forbidden update %s: %v", field, err)
		}
		if rejected.Issues[0].Message == draft.NewIssue("forbidden_field", field, "en").Message {
			t.Fatal("Russian error was not localized")
		}
	}
	create, err := draft.CreateSchema()
	if err != nil {
		t.Fatal(err)
	}
	update, err := draft.UpdateSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		raw            string
		create, update bool
	}{{validCreate, true, true}, {`{"title":"New"}`, false, true}, {`{"meta_title":null}`, false, true}, {`{}`, false, false}, {`{"status":"published"}`, false, false}, {`{"title":null}`, false, false}, {`{"body_markdown":null}`, false, false}, {`{"tags":null}`, false, false}} {
		var value any
		if err := json.Unmarshal([]byte(tc.raw), &value); err != nil {
			t.Fatal(err)
		}
		resolvedCreate, err := create.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		resolvedUpdate, err := update.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		if (resolvedCreate.Validate(value) == nil) != tc.create || (resolvedUpdate.Validate(value) == nil) != tc.update {
			t.Fatalf("schema drift for %s", tc.raw)
		}
	}
}

func TestPresenceSurvivesJSONAndIdempotencyHashing(t *testing.T) {
	inputs := []string{`{"title":"A"}`, `{"title":"A","meta_title":null}`, `{"title":"A","meta_title":""}`, `{"title":"A","tags":[]}`,
		`{"title":"A","reported":{}}`, `{"title":"A","reported":{"model":""}}`, `{"title":"A","reported":{"sources_used":[]}}`,
		`{"title":"A","reported":{"claims_to_verify":[]}}`, `{"title":"A","reported":{"notes":""}}`}
	hashes := make(map[string]bool)
	for _, raw := range inputs {
		changes, err := draft.DecodeChanges([]byte(raw), "en")
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(changes)
		if err != nil {
			t.Fatal(err)
		}
		request, err := idempotency.New(1, "update_draft", "e693277d-58cf-4403-9820-ac6a45e0f754", changes)
		if err != nil {
			t.Fatal(err)
		}
		if hashes[request.Hash] {
			t.Fatalf("omitted, null and empty collapsed: %s -> %s", raw, encoded)
		}
		hashes[request.Hash] = true
	}
	first, _ := draft.DecodeChanges([]byte(`{"title":"A","meta_title":null}`), "en")
	second, _ := draft.DecodeChanges([]byte(`{"meta_title":null,"title":"A"}`), "en")
	a, _ := idempotency.New(1, "update_draft", "e693277d-58cf-4403-9820-ac6a45e0f754", first)
	b, _ := idempotency.New(1, "update_draft", "e693277d-58cf-4403-9820-ac6a45e0f754", second)
	if a.Hash != b.Hash {
		t.Fatal("property order changed the typed hash")
	}
}

func TestNormalizationMetadataMediaAndOriginalInput(t *testing.T) {
	opt, _ := options()
	input := draft.PageDraft{Title: "  Test  ", Summary: "Summary", BodyMarkdown: "# Heading\n\n![Chosen alt](media:1)", MetaTitle: draft.SetNullable(strings.Repeat("M", 61)), MetaDescription: draft.SetNullable(strings.Repeat("слово ", 40)), Tags: draft.Set([]string{"go", "go"}), CategoryID: draft.SetNullable(int64(3)), FeaturedImageID: draft.SetNullable(int64(1))}
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := draft.NormalizeCreate(context.Background(), input, opt)
	if err != nil || result.HasErrors() || result.Content == nil {
		t.Fatalf("normalize: %+v %v", result, err)
	}
	page := result.Content
	if page.Slug != "test-2" || page.Title != "Test" || page.PageType != "post" || len(page.Tags) != 1 || utf8.RuneCountInString(page.MetaDescription) > 160 || strings.HasSuffix(page.MetaDescription, "сл") {
		t.Fatalf("normalization: %+v", page)
	}
	for _, required := range []string{"<figure>", "<figcaption>Library caption</figcaption>", `alt="Chosen alt"`, `data-media-id="1"`, `srcset=`, `loading="lazy"`, `width="1536"`, "image%20with%20space.jpg"} {
		if !strings.Contains(page.BodyHTML, required) {
			t.Fatalf("image lost %s: %s", required, page.BodyHTML)
		}
	}
	if !has(result.Issues, "meta_title_length") || !has(result.Issues, "meta_description_trimmed") {
		t.Fatal("normalization issues missing")
	}
	after, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("normalization mutated the input used for idempotency")
	}
	if twice := security.SanitizePageHTML(page.BodyHTML); twice != page.BodyHTML {
		t.Fatalf("normal save/render strips draft markup: %s", twice)
	}
	derived, err := draft.DeriveMarkdown(context.Background(), page.BodyHTML, opt)
	if err != nil || !strings.Contains(derived.Markdown, "media:1") || !strings.Contains(derived.Markdown, "Chosen alt") {
		t.Fatalf("reverse media: %+v %v", derived, err)
	}
}

func TestMetadataOnlyUpdatePreservesHumanHTML(t *testing.T) {
	opt, resolver := options()
	resolver.mediaErr = errors.New("media must not be consulted for omitted fields")
	current := draft.Content{ID: 7, Title: "Human title", Slug: "human", LanguageCode: "en", PageType: "page", Summary: "Old", BodyHTML: `<div class="editor-grid"><table><tr><td colspan="2">Rich HTML</td></tr></table></div>`, Tags: []string{"legacy-tag"}}
	changes := draft.Changes{Summary: draft.Set("New metadata"), MetaTitle: draft.Clear[string]()}
	result, err := draft.NormalizeUpdate(context.Background(), changes, current, opt)
	if err != nil || result.Content == nil || result.HasErrors() {
		t.Fatalf("metadata update: %+v %v", result, err)
	}
	if result.BodyChanged || result.Content.BodyHTML != current.BodyHTML || result.Content.Tags[0] != "legacy-tag" || len(resolver.linkCalls) != 0 {
		t.Fatal("metadata update rewrote or revalidated omitted content")
	}
	derived, err := draft.DeriveMarkdown(context.Background(), current.BodyHTML, opt)
	if err != nil || !has(derived.Issues, "html_conversion_lossy") {
		t.Fatalf("lossy conversion warning missing: %+v %v", derived, err)
	}
}

func TestLinkPoliciesAndInternalVisibility(t *testing.T) {
	for _, policy := range []draft.LinkPolicy{draft.Keep, draft.NoFollow, draft.Strip} {
		t.Run(string(policy), func(t *testing.T) {
			opt, resolver := options()
			opt.LinkPolicy = policy
			rendered, err := draft.RenderMarkdown(context.Background(), `[Existing](/existing) [Hidden](/hidden) [External](https://outside.test/article) [Email](mailto:hello@example.test) [Here](#part)`, opt)
			if err != nil || draft.HasErrors(rendered.Issues) {
				t.Fatalf("links: %+v %v", rendered, err)
			}
			if !has(rendered.Issues, "external_link") || !has(rendered.Issues, "internal_link_broken") || len(resolver.linkCalls) != 2 {
				t.Fatalf("link evidence: %+v calls=%v", rendered, resolver.linkCalls)
			}
			switch policy {
			case draft.Keep:
				if !strings.Contains(rendered.HTML, `rel="noopener noreferrer"`) || strings.Contains(rendered.HTML, "nofollow") {
					t.Fatal(rendered.HTML)
				}
			case draft.NoFollow:
				if !strings.Contains(rendered.HTML, `rel="noopener noreferrer nofollow"`) {
					t.Fatal(rendered.HTML)
				}
			case draft.Strip:
				if strings.Contains(rendered.HTML, "outside.test") || strings.Contains(rendered.HTML, "mailto:") || !strings.Contains(rendered.HTML, "External") {
					t.Fatal(rendered.HTML)
				}
			}
			if security.SanitizePageHTML(rendered.HTML) != rendered.HTML {
				t.Fatal("save/render changed the selected link policy")
			}
		})
	}
}

func TestXSSAndRemoteImageCorpus(t *testing.T) {
	for _, body := range []string{`<script>alert(1)</script>`, `<img src=x onerror=alert(1)>`, `[x](javascript:alert(1))`, `[x](jav&#x61;script:alert(1))`, `[x](data:text/html,attack)`, `[x](https://user:secret@example.test/)`, `![x](https://outside.test/x.jpg)`, `![x](data:image/svg+xml;base64,xxx)`, `![x](file:///etc/passwd)`, `![x](/uploads/originals/x.jpg)`, `![x](media:01)`, `![x](media:0)`, `![x](media:9223372036854775808)`} {
		t.Run(body, func(t *testing.T) {
			opt, _ := options()
			result, err := draft.RenderMarkdown(context.Background(), body, opt)
			if err != nil || !draft.HasErrors(result.Issues) || result.HTML != "" {
				t.Fatalf("unsafe input accepted: %+v %v", result, err)
			}
		})
	}
	opt, _ := options()
	result, err := draft.RenderMarkdown(context.Background(), "```html\n<script>alert(1)</script>\n```\n\n&lt;script&gt;\n\n<svg/onload=alert(1)>", opt)
	if err != nil || draft.HasErrors(result.Issues) || strings.Contains(result.HTML, "<script>") {
		t.Fatalf("safe code example: %+v %v", result, err)
	}
}

func TestValidationLimitsAndReferenceFailures(t *testing.T) {
	for _, tc := range []struct {
		input draft.PageDraft
		code  string
	}{
		{draft.PageDraft{Title: " "}, "title_length"}, {draft.PageDraft{Title: strings.Repeat("я", 256)}, "title_length"},
		{draft.PageDraft{Title: "OK", Summary: strings.Repeat("я", 501)}, "summary_length"},
		{draft.PageDraft{Title: "OK", BodyMarkdown: strings.Repeat("я", 100001)}, "body_too_large"},
		{draft.PageDraft{Title: "OK", Tags: draft.Set([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"})}, "tags_limit"},
		{draft.PageDraft{Title: "OK", FeaturedImageID: draft.SetNullable(int64(99))}, "image_not_found"},
		{draft.PageDraft{Title: "OK", LanguageCode: draft.SetNullable("zz")}, "language_inactive"},
		{draft.PageDraft{Title: "OK", Tags: draft.Set([]string{"unknown"})}, "unknown_tag"},
		{draft.PageDraft{Title: "OK", CategoryID: draft.SetNullable(int64(99))}, "category_not_found"},
	} {
		opt, _ := options()
		result, err := draft.NormalizeCreate(context.Background(), tc.input, opt)
		if err != nil || result.Content != nil || !has(result.Issues, tc.code) {
			t.Fatalf("want %s: %+v %v", tc.code, result, err)
		}
	}
}
