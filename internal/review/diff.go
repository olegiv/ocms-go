// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package review

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/olegiv/ocms-go/internal/store"
	"golang.org/x/net/html"
)

type Difference struct {
	BeforeRecorded, AfterRecorded bool
	Field, Before, After          string
	Truncated                     bool
}

// Compare marks unknown legacy fields rather than filling them from the current page.
func Compare(before, after store.PageVersion) ([]Difference, error) {
	left, err := versionFields(before)
	if err != nil {
		return nil, err
	}
	right, err := versionFields(after)
	if err != nil {
		return nil, err
	}
	var differences []Difference
	for _, field := range []string{"title", "summary", "body", "meta_title", "meta_description", "meta_keywords", "slug", "language_code", "featured_image_id", "category_ids", "tag_ids"} {
		a, aok := left[field]
		b, bok := right[field]
		if !aok && !bok {
			continue
		}
		if !aok {
			a = "[not recorded]"
		}
		if !bok {
			b = "[not recorded]"
		}
		if field == "body" {
			a = plainBody(a)
			b = plainBody(b)
		}
		if a == b {
			continue
		}
		cut := false
		if len([]rune(a)) > 32768 {
			a = string([]rune(a)[:32768])
			cut = true
		}
		if len([]rune(b)) > 32768 {
			b = string([]rune(b)[:32768])
			cut = true
		}
		differences = append(differences, Difference{Field: field, Before: a, After: b, BeforeRecorded: aok, AfterRecorded: bok, Truncated: cut})
	}
	return differences, nil
}

func versionFields(v store.PageVersion) (map[string]string, error) {
	result := map[string]string{"title": v.Title, "body": v.Body}
	if v.ID == 0 {
		return map[string]string{}, nil
	}
	if v.Legacy || !v.SnapshotJson.Valid {
		return result, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(v.SnapshotJson.String), &fields); err != nil {
		return nil, err
	}
	for key, raw := range fields {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			result[key] = text
		} else {
			result[key] = string(raw)
		}
	}
	return result, nil
}

func plainBody(body string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(body))
	var out strings.Builder
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			if !errors.Is(tokenizer.Err(), io.EOF) {
				return body
			}
			return strings.TrimSpace(out.String())
		case html.TextToken:
			out.Write(tokenizer.Text())
		case html.EndTagToken:
			out.WriteByte('\n')
		}
	}
}
