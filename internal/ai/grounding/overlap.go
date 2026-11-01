// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package grounding

import (
	"strings"
	"unicode"

	"github.com/olegiv/ocms-go/internal/ai/draft"
)

// Overlap identifies substantial reuse of an example excerpt, not plagiarism or factual verification.
type Overlap struct {
	PageID   int64   `json:"page_id"`
	Revision int64   `json:"revision"`
	Fraction float64 `json:"fraction"`
}

// CheckOverlap compares bounded word shingles against example references only.
// Short quotations and source/parent references do not trigger this style warning.
func CheckOverlap(body string, references []Reference, language string) ([]Overlap, []draft.Issue) {
	hits := []Overlap{}
	issues := []draft.Issue{}
	candidate := shingles(body)
	if len(candidate) < 40 {
		return hits, issues
	}
	seen := map[int64]bool{}
	for _, ref := range references {
		if ref.Role != "example" || ref.PageID <= 0 || seen[ref.PageID] {
			continue
		}
		seen[ref.PageID] = true
		sampleText := ref.Markdown
		if ref.DuplicateOf != nil && *ref.DuplicateOf >= 0 && *ref.DuplicateOf < len(references) {
			sampleText = references[*ref.DuplicateOf].Markdown
		}
		sample := shingles(sampleText)
		matches := 0
		for phrase := range candidate {
			if sample[phrase] {
				matches++
			}
		}
		fraction := float64(matches) / float64(len(candidate))
		if fraction >= 0.6 {
			hits = append(hits, Overlap{PageID: ref.PageID, Revision: ref.Revision, Fraction: fraction})
			issues = append(issues, draft.NewIssue("overlap_with_example", "body_markdown", language))
		}
	}
	return hits, issues
}

func shingles(body string) map[string]bool {
	body = clip(body, draft.MaxBodyBytes)
	words := strings.FieldsFunc(strings.ToLower(body), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	result := map[string]bool{}
	for i := 0; i+5 <= len(words); i++ {
		result[strings.Join(words[i:i+5], " ")] = true
	}
	return result
}
