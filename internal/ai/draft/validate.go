// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package draft

import (
	"encoding/json"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/olegiv/ocms-go/internal/util"
)

// Validate checks direct Go create calls too; parsing a schema is not authority
// to skip these constraints. Resolution of site entities is done by Normalize.
func Validate(input PageDraft, language string) []Issue {
	issues := validateText(input.Title, input.Summary, input.BodyMarkdown, language)
	if input.PageType != "" && input.PageType != "page" && input.PageType != "post" {
		issues = append(issues, NewIssue("invalid_input", "page_type", language))
	}
	if input.Slug.Present && !input.Slug.Null {
		issues = append(issues, validateSlug(input.Slug.Value, language)...)
	}
	issues = append(issues, validateCollections(input.Tags, input.MetaKeywords, input.Reported, language)...)
	if !HasErrors(issues) {
		issues = append(issues, validateTyped(input, 0, language)...)
	}
	return issues
}

// ValidateChanges validates only fields being changed; legacy stored body and
// metadata do not get rewritten or revalidated by an unrelated edit.
func ValidateChanges(input Changes, language string) []Issue {
	var issues []Issue
	if input.Title.Present {
		issues = append(issues, validateTitle(input.Title.Value, language)...)
	}
	if input.Summary.Present && (!utf8.ValidString(input.Summary.Value) || utf8.RuneCountInString(input.Summary.Value) > MaxSummaryRunes) {
		issues = append(issues, NewIssue("summary_length", "summary", language))
	}
	if input.BodyMarkdown.Present {
		issues = append(issues, validateBody(input.BodyMarkdown.Value, language)...)
	}
	if input.Slug.Present {
		issues = append(issues, validateSlug(input.Slug.Value, language)...)
	}
	issues = append(issues, validateCollections(input.Tags, input.MetaKeywords, input.Reported, language)...)
	if !HasErrors(issues) {
		issues = append(issues, validateTyped(input, 1, language)...)
	}
	return issues
}

func validateTyped(input any, index int, language string) []Issue {
	if !validStrings(reflect.ValueOf(input)) {
		return []Issue{NewIssue("invalid_input", "$", language)}
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > MaxInputBytes {
		return []Issue{NewIssue("invalid_input", "$", language)}
	}
	var ignored any
	if err = decode(raw, index, language, &ignored); err != nil {
		return []Issue{NewIssue("invalid_input", "$", language)}
	}
	return nil
}

func validStrings(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.String:
		return utf8.ValidString(value.String())
	case reflect.Struct:
		for index := range value.NumField() {
			if !validStrings(value.Field(index)) {
				return false
			}
		}
	case reflect.Slice:
		for index := range value.Len() {
			if !validStrings(value.Index(index)) {
				return false
			}
		}
	}
	return true
}

func validateTitle(title, language string) []Issue {
	length := utf8.RuneCountInString(strings.TrimSpace(title))
	if !utf8.ValidString(title) || length < 1 || length > MaxTitleRunes {
		return []Issue{NewIssue("title_length", "title", language)}
	}
	return nil
}

func validateBody(body, language string) []Issue {
	if len(body) > MaxBodyBytes {
		return []Issue{NewIssue("body_too_large", "body_markdown", language)}
	}
	if !utf8.ValidString(body) {
		return []Issue{NewIssue("invalid_input", "body_markdown", language)}
	}
	return nil
}

func validateText(title, summary, body, language string) []Issue {
	issues := validateTitle(title, language)
	if !utf8.ValidString(summary) || utf8.RuneCountInString(summary) > MaxSummaryRunes {
		issues = append(issues, NewIssue("summary_length", "summary", language))
	}
	return append(issues, validateBody(body, language)...)
}

func validateSlug(slug, language string) []Issue {
	if len(slug) > 255 || !util.IsValidSlug(slug) {
		return []Issue{NewIssue("slug_invalid", "slug", language)}
	}
	return nil
}

func validateCollections(tags, keywords Field[[]string], report Field[AgentReport], language string) []Issue {
	var issues []Issue
	if tags.Present && len(unique(tags.Value)) > MaxTags {
		issues = append(issues, NewIssue("tags_limit", "tags", language))
	}
	for _, keyword := range keywords.Value {
		if !utf8.ValidString(keyword) || utf8.RuneCountInString(keyword) > 200 {
			issues = append(issues, NewIssue("invalid_input", "meta_keywords", language))
			break
		}
	}
	for _, values := range [][]string{report.Value.SourcesUsed.Value, report.Value.ClaimsToVerify.Value} {
		for _, text := range values {
			if !utf8.ValidString(text) || utf8.RuneCountInString(text) > 2048 {
				issues = append(issues, NewIssue("invalid_input", "reported", language))
				return issues
			}
		}
	}
	return issues
}

func unique(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result
}
