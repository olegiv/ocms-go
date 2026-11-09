// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package draft validates and normalizes agent-authored content without writing
// pages or making outbound requests. Persistence belongs to the shared v2 path.
package draft

import "github.com/danielgtaylor/huma/v2"

const (
	MaxTitleRunes   = 255
	MaxSummaryRunes = 500
	MaxBodyBytes    = 200_000
	MaxTags         = 10
	MaxInputBytes   = 2 * 1024 * 1024
	// MaxLinks bounds the distinct link destinations in one body. Each one is
	// resolved, listed and stored with the revision, so an unbounded count let
	// a single save store megabytes of link evidence (2026-11-03 audit B-1).
	MaxLinks = 500
)

// PageDraft is the create contract. Optional values retain omission and null
// before normalization so idempotency can hash the original typed request.
type PageDraft struct {
	Title           string             `json:"title" minLength:"1" maxLength:"255"`
	Summary         string             `json:"summary" maxLength:"500"`
	BodyMarkdown    string             `json:"body_markdown" maxLength:"200000" doc:"Markdown; images must use existing media:ID references."`
	PageType        string             `json:"page_type,omitempty" enum:"post,page" default:"post"`
	Slug            Nullable[string]   `json:"slug,omitzero" minLength:"1" maxLength:"255" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$"`
	LanguageCode    Nullable[string]   `json:"language_code,omitzero" minLength:"2" maxLength:"10"`
	MetaTitle       Nullable[string]   `json:"meta_title,omitzero"`
	MetaDescription Nullable[string]   `json:"meta_description,omitzero"`
	MetaKeywords    Field[[]string]    `json:"meta_keywords,omitzero" maxItems:"50"`
	Tags            Field[[]string]    `json:"tags,omitzero" doc:"Existing tag slugs in the selected language; at most ten after deduplication."`
	CategoryID      Nullable[int64]    `json:"category_id,omitzero" minimum:"1"`
	FeaturedImageID Nullable[int64]    `json:"featured_image_id,omitzero" minimum:"1"`
	References      Field[[]Reference] `json:"references,omitzero" maxItems:"50"`
	Reported        Field[AgentReport] `json:"reported,omitzero"`
}

// Changes is a non-empty update object. Identity, language, page type, lifecycle,
// AI access and verified provenance cannot be supplied through this contract.
type Changes struct {
	Title           Field[string]      `json:"title,omitzero" minLength:"1" maxLength:"255"`
	Summary         Field[string]      `json:"summary,omitzero" maxLength:"500"`
	BodyMarkdown    Field[string]      `json:"body_markdown,omitzero" maxLength:"200000"`
	Slug            Field[string]      `json:"slug,omitzero" minLength:"1" maxLength:"255" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$"`
	MetaTitle       Nullable[string]   `json:"meta_title,omitzero"`
	MetaDescription Nullable[string]   `json:"meta_description,omitzero"`
	MetaKeywords    Field[[]string]    `json:"meta_keywords,omitzero" maxItems:"50"`
	Tags            Field[[]string]    `json:"tags,omitzero"`
	CategoryID      Nullable[int64]    `json:"category_id,omitzero" minimum:"1"`
	FeaturedImageID Nullable[int64]    `json:"featured_image_id,omitzero" minimum:"1"`
	References      Field[[]Reference] `json:"references,omitzero" maxItems:"50"`
	Reported        Field[AgentReport] `json:"reported,omitzero"`
}

// TransformSchema keeps empty changes invalid in generated MCP schemas too.
func (Changes) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	minimum := 1
	schema.MinProperties = &minimum
	return schema
}

// Reference is an input to R1.4 grounding, not proof that an agent used a source.
type Reference struct {
	Ref  string `json:"ref" minLength:"1" maxLength:"2048"`
	Role string `json:"role" enum:"example,source,parent"`
}

// AgentReport is untrusted information stored separately from server evidence.
type AgentReport struct {
	Model          Field[string]   `json:"model,omitzero" maxLength:"200"`
	SourcesUsed    Field[[]string] `json:"sources_used,omitzero" maxItems:"50"`
	ClaimsToVerify Field[[]string] `json:"claims_to_verify,omitzero" maxItems:"50"`
	Notes          Field[string]   `json:"notes,omitzero" maxLength:"10000"`
}

// Content contains values ready for a later draft-only persistence adapter.
// BodyHTML is authoritative. A metadata-only update preserves it byte for byte.
type Content struct {
	ID              int64
	Title           string
	Slug            string
	LanguageCode    string
	PageType        string
	Summary         string
	BodyHTML        string
	MetaTitle       string
	MetaDescription string
	MetaKeywords    []string
	Tags            []string
	CategoryID      *int64
	FeaturedImageID *int64
}

// Result has no saveable content when validation fails. Grounding in R1.4 must
// resolve References before a later R1.5 authoring adapter persists them.
type Result struct {
	Content     *Content
	Issues      []Issue
	Links       []Link
	References  []Reference
	Reported    AgentReport
	BodyChanged bool
}

// HasErrors identifies blocking feedback; warnings and information do not block.
func (r Result) HasErrors() bool { return HasErrors(r.Issues) }
