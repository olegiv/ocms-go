// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/util"
)

// Options comes from site policy and authenticated adapters, never draft input.
// Resolve current storage/visibility through Resolver; it performs no HTTP I/O.
type Options struct {
	Resolver        Resolver
	DefaultLanguage string
	UILanguage      string
	SiteURL         string
	PagePath        string
	LinkPolicy      LinkPolicy
}

func (o Options) linkPolicy() LinkPolicy {
	if o.LinkPolicy == "" {
		return Keep
	}
	return o.LinkPolicy
}

func (o Options) base() (*url.URL, error) {
	if o.Resolver == nil {
		return nil, errors.New("draft processing requires a site resolver")
	}
	switch o.linkPolicy() {
	case Keep, NoFollow, Strip:
	default:
		return nil, errors.New("unknown draft link policy")
	}
	return siteBase(o.SiteURL, o.PagePath)
}

// NormalizeCreate derives a bounded available slug and normalizes draft content.
// Failed validation returns issues and nil Content; storage failures return err.
func NormalizeCreate(ctx context.Context, input PageDraft, options Options) (Result, error) {
	result := Result{Issues: Validate(input, options.UILanguage), BodyChanged: true}
	if result.HasErrors() {
		return result, nil
	}
	if _, err := options.base(); err != nil {
		return result, err
	}
	if options.DefaultLanguage == "" {
		return result, errors.New("draft normalization requires the site's default language")
	}
	content := Content{Title: strings.TrimSpace(input.Title), Summary: strings.TrimSpace(input.Summary), PageType: input.PageType,
		LanguageCode: options.DefaultLanguage, MetaKeywords: unique(input.MetaKeywords.Value), Tags: unique(input.Tags.Value)}
	if content.PageType == "" {
		content.PageType = "post"
	}
	if input.LanguageCode.Present && !input.LanguageCode.Null {
		content.LanguageCode = input.LanguageCode.Value
	}
	if err := checkLanguage(ctx, content.LanguageCode, options, &result); err != nil {
		return result, err
	}
	slug, issue, err := chooseSlug(ctx, content.Title, input.Slug, options)
	if err != nil {
		return result, err
	}
	content.Slug = slug
	if issue != nil {
		result.Issues = append(result.Issues, *issue)
	}
	setMetadata(&content, input.MetaTitle, input.MetaDescription, options.UILanguage, &result)
	if err = resolveAssignments(ctx, &content, input.CategoryID, input.FeaturedImageID, true, options, &result); err != nil {
		return result, err
	}
	if result.HasErrors() {
		return result, nil
	}
	result.References, result.Reported = cloneReferences(input.References.Value), cloneReport(input.Reported.Value)
	return finishBody(ctx, content, input.BodyMarkdown, options, result)
}

// NormalizeUpdate preserves current HTML and omitted fields. It never converts
// stored HTML back to Markdown to apply a metadata-only update.
func NormalizeUpdate(ctx context.Context, input Changes, current Content, options Options) (Result, error) {
	result := Result{Issues: ValidateChanges(input, options.UILanguage), BodyChanged: input.BodyMarkdown.Present}
	if result.HasErrors() {
		return result, nil
	}
	if current.ID <= 0 || current.LanguageCode == "" || options.DefaultLanguage == "" {
		return result, errors.New("draft update requires current page identity and site language")
	}
	if _, err := options.base(); err != nil {
		return result, err
	}
	content := current
	content.Tags, content.MetaKeywords = slices.Clone(current.Tags), slices.Clone(current.MetaKeywords)
	content.CategoryID, content.FeaturedImageID = copyID(current.CategoryID), copyID(current.FeaturedImageID)
	if err := checkLanguage(ctx, content.LanguageCode, options, &result); err != nil {
		return result, err
	}
	if input.Title.Present {
		content.Title = strings.TrimSpace(input.Title.Value)
	}
	if input.Summary.Present {
		content.Summary = strings.TrimSpace(input.Summary.Value)
	}
	if input.Slug.Present {
		content.Slug = input.Slug.Value
		available, err := options.Resolver.SlugAvailable(ctx, content.Slug, content.ID)
		if err != nil {
			return result, fmt.Errorf("check draft slug: %w", err)
		}
		if !available {
			result.Issues = append(result.Issues, NewIssue("slug_taken", "slug", options.UILanguage))
		}
	}
	if input.Tags.Present {
		content.Tags = unique(input.Tags.Value)
	}
	if input.MetaKeywords.Present {
		content.MetaKeywords = unique(input.MetaKeywords.Value)
	}
	setMetadata(&content, input.MetaTitle, input.MetaDescription, options.UILanguage, &result)
	if err := resolveAssignments(ctx, &content, input.CategoryID, input.FeaturedImageID, input.Tags.Present, options, &result); err != nil {
		return result, err
	}
	if result.HasErrors() {
		return result, nil
	}
	result.References, result.Reported = cloneReferences(input.References.Value), cloneReport(input.Reported.Value)
	if input.BodyMarkdown.Present {
		return finishBody(ctx, content, input.BodyMarkdown.Value, options, result)
	}
	result.Content = &content
	return result, nil
}

func checkLanguage(ctx context.Context, language string, options Options, result *Result) error {
	active, err := options.Resolver.Language(ctx, language)
	if err != nil {
		return fmt.Errorf("check draft language: %w", err)
	}
	if !active || !util.IsRoutableLanguageCode(language) {
		result.Issues = append(result.Issues, NewIssue("language_inactive", "language_code", options.UILanguage))
	}
	return nil
}

func chooseSlug(ctx context.Context, title string, provided Nullable[string], options Options) (string, *Issue, error) {
	base := util.Slugify(title)
	if base == "" {
		base = "draft"
	}
	explicit := provided.Present && !provided.Null
	if explicit {
		base = provided.Value
	}
	for attempt := 1; attempt <= 10; attempt++ {
		suffix := ""
		if attempt > 1 {
			suffix = "-" + strconv.Itoa(attempt)
		}
		candidate := strings.TrimRight(base[:min(len(base), 255-len(suffix))], "-") + suffix
		available, err := options.Resolver.SlugAvailable(ctx, candidate, 0)
		if err != nil {
			return "", nil, fmt.Errorf("check derived draft slug: %w", err)
		}
		if available {
			return candidate, nil, nil
		}
		if explicit {
			break
		}
	}
	issue := NewIssue("slug_taken", "slug", options.UILanguage)
	return "", &issue, nil
}

func resolveAssignments(ctx context.Context, content *Content, category, image Nullable[int64], checkTags bool, options Options, result *Result) error {
	if checkTags {
		for _, slug := range content.Tags {
			if !util.IsValidSlug(slug) {
				result.Issues = append(result.Issues, NewIssue("unknown_tag", "tags", options.UILanguage))
				continue
			}
			found, err := options.Resolver.Tag(ctx, slug, content.LanguageCode)
			if err != nil {
				return fmt.Errorf("resolve draft tag: %w", err)
			}
			if !found {
				result.Issues = append(result.Issues, NewIssue("unknown_tag", "tags", options.UILanguage))
			}
		}
	}
	if category.Present {
		content.CategoryID = nil
		if !category.Null {
			found, err := options.Resolver.Category(ctx, category.Value, content.LanguageCode)
			if err != nil {
				return fmt.Errorf("resolve draft category: %w", err)
			}
			if !found {
				result.Issues = append(result.Issues, NewIssue("category_not_found", "category_id", options.UILanguage))
			}
			content.CategoryID = copyID(&category.Value)
		}
	}
	if image.Present {
		previous := content.FeaturedImageID
		content.FeaturedImageID = nil
		if !image.Null {
			media, err := options.Resolver.Media(ctx, image.Value)
			switch {
			case errors.Is(err, ErrUnavailable), err == nil && !media.validImage():
				result.Issues = append(result.Issues, NewIssue("image_not_found", "featured_image_id", options.UILanguage))
			case err != nil:
				return fmt.Errorf("resolve featured draft image: %w", err)
			case previous == nil || *previous != image.Value:
				if err := pagewrite.CheckFeaturedImage(media.MimeType, media.Width, media.Height, media.DimensionsKnown); err != nil {
					result.Issues = append(result.Issues, NewIssue("image_too_small", "featured_image_id", options.UILanguage))
				}
			}
			content.FeaturedImageID = copyID(&image.Value)
		}
	}
	return nil
}

func setMetadata(content *Content, title, description Nullable[string], language string, result *Result) {
	if title.Present {
		content.MetaTitle = ""
		if !title.Null {
			content.MetaTitle = strings.TrimSpace(title.Value)
			if utf8.RuneCountInString(content.MetaTitle) > 60 {
				result.Issues = append(result.Issues, NewIssue("meta_title_length", "meta_title", language))
			}
		}
	}
	if description.Present {
		content.MetaDescription = ""
		if !description.Null {
			content.MetaDescription = strings.TrimSpace(description.Value)
			if utf8.RuneCountInString(content.MetaDescription) > 160 {
				content.MetaDescription = trimAtWord(content.MetaDescription, 160)
				result.Issues = append(result.Issues, NewIssue("meta_description_trimmed", "meta_description", language))
			}
		}
	}
}

func trimAtWord(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	cut := maximum
	if !unicode.IsSpace(runes[maximum]) {
		for index := maximum - 1; index > 0; index-- {
			if unicode.IsSpace(runes[index]) {
				cut = index
				break
			}
		}
	}
	return strings.TrimSpace(string(runes[:cut]))
}

func finishBody(ctx context.Context, content Content, body string, options Options, result Result) (Result, error) {
	if options.PagePath == "" {
		options.PagePath = "/" + content.Slug
		if content.LanguageCode != options.DefaultLanguage {
			options.PagePath = "/" + content.LanguageCode + options.PagePath
		}
	}
	rendered, err := RenderMarkdown(ctx, body, options)
	if err != nil {
		return result, err
	}
	result.Issues, result.Links = append(result.Issues, rendered.Issues...), rendered.Links
	if !result.HasErrors() {
		content.BodyHTML = rendered.HTML
		result.Content = &content
	}
	return result, nil
}

func copyID(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneReferences(value []Reference) []Reference { return slices.Clone(value) }

func cloneReport(value AgentReport) AgentReport {
	value.SourcesUsed.Value, value.ClaimsToVerify.Value = slices.Clone(value.SourcesUsed.Value), slices.Clone(value.ClaimsToVerify.Value)
	return value
}
