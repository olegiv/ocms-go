// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package authoring coordinates draft normalization and the shared atomic page writer.
package authoring

import (
	"time"

	"github.com/olegiv/ocms-go/internal/ai/draft"
)

type CreateInput struct {
	RequestID string          `json:"request_id" required:"true" minLength:"36" maxLength:"36" format:"uuid"`
	Draft     draft.PageDraft `json:"draft" required:"true"`
}
type UpdateInput struct {
	RequestID    string        `json:"request_id" required:"true" minLength:"36" maxLength:"36" format:"uuid"`
	PageID       int64         `json:"page_id" required:"true" minimum:"1"`
	BaseRevision int64         `json:"base_revision" required:"true" minimum:"1"`
	Changes      draft.Changes `json:"changes" required:"true"`
}
type TranslateInput struct {
	RequestID      string          `json:"request_id" required:"true" minLength:"36" maxLength:"36" format:"uuid"`
	SourcePageID   int64           `json:"source_page_id" required:"true" minimum:"1"`
	SourceRevision int64           `json:"source_revision" required:"true" minimum:"1"`
	LanguageCode   string          `json:"language_code" required:"true" minLength:"2" maxLength:"10"`
	Draft          draft.PageDraft `json:"draft" required:"true"`
}
type PageInput struct {
	PageID int64 `json:"page_id" required:"true" minimum:"1"`
}
type ListInput struct {
	LanguageCode string `json:"language_code,omitempty" minLength:"2" maxLength:"10"`
	Page         int    `json:"page,omitempty" default:"1" minimum:"1" maximum:"21474836"`
	PerPage      int    `json:"per_page,omitempty" default:"20" minimum:"1" maximum:"100"`
}
type PreviewInput struct {
	PageID   int64 `json:"page_id" required:"true" minimum:"1"`
	Revision int64 `json:"revision" required:"true" minimum:"1"`
}
type SaveResult struct {
	PageID            int64         `json:"page_id"`
	Revision          int64         `json:"revision"`
	AdminURL          string        `json:"admin_url"`
	PreviewURL        string        `json:"preview_url" doc:"Recorded revision URL requiring an editor session; no bearer token is issued."`
	Issues            []draft.Issue `json:"issues"`
	TranslationStatus string        `json:"translation_status" enum:"current,stale,not_tracked,unavailable"`
}
type Reference struct {
	PageID   int64  `json:"page_id"`
	Revision int64  `json:"revision"`
	Role     string `json:"role"`
}
type Basis struct {
	SourcePageID   int64  `json:"source_page_id"`
	SourceRevision int64  `json:"source_revision"`
	Fingerprint    string `json:"fingerprint"`
}
type Validation struct {
	Issues                []draft.Issue `json:"issues"`
	References            []Reference   `json:"references"`
	Links                 []draft.Link  `json:"links"`
	LinksTruncated        bool          `json:"links_truncated"`
	TranslationBasis      *Basis        `json:"translation_basis,omitempty"`
	TranslationStatus     string        `json:"translation_status,omitempty"`
	SiteURL               string        `json:"site_url,omitempty"`
	PromptContractVersion string        `json:"prompt_contract_version,omitempty" doc:"Server contract version, not proof that the agent used the prompt."`
}
type Reported struct {
	Agent            draft.AgentReport `json:"agent"`
	MCPClientName    string            `json:"mcp_client_name,omitempty"`
	MCPClientVersion string            `json:"mcp_client_version,omitempty"`
}
type Verified struct {
	UserID        int64     `json:"user_id,omitempty"`
	Source        string    `json:"source"`
	APIKeyID      int64     `json:"api_key_id,omitempty"`
	OAuthGrantID  string    `json:"oauth_grant_id,omitempty"`
	OAuthClientID string    `json:"registered_oauth_client_id,omitempty"`
	Transport     string    `json:"transport"`
	SavedAt       time.Time `json:"saved_at"`
}
type DraftSummary struct {
	PageID       int64     `json:"page_id"`
	Revision     int64     `json:"revision"`
	Title        string    `json:"title"`
	LanguageCode string    `json:"language_code"`
	AdminURL     string    `json:"admin_url"`
	PreviewURL   string    `json:"preview_url"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type DraftDetail struct {
	DraftSummary
	Slug               string        `json:"slug"`
	Summary            string        `json:"summary"`
	PageType           string        `json:"page_type"`
	BodyHTML           string        `json:"body_html"`
	BodyMarkdown       string        `json:"body_markdown"`
	MetaTitle          string        `json:"meta_title"`
	MetaDescription    string        `json:"meta_description"`
	MetaKeywords       []string      `json:"meta_keywords"`
	Tags               []string      `json:"tags"`
	CategoryIDs        []int64       `json:"category_ids"`
	FeaturedImageID    *int64        `json:"featured_image_id,omitempty"`
	Verified           Verified      `json:"verified"`
	Validated          Validation    `json:"validated"`
	Reported           Reported      `json:"reported"`
	TranslationBasis   *Basis        `json:"translation_basis,omitempty"`
	TranslationStatus  string        `json:"translation_status"`
	ConversionWarnings []draft.Issue `json:"conversion_warnings"`
}
type ListResult struct {
	Drafts  []DraftSummary `json:"drafts"`
	Total   int64          `json:"total"`
	Page    int            `json:"page"`
	PerPage int            `json:"per_page"`
}
type Translation struct {
	LanguageCode string `json:"language_code"`
	Status       string `json:"status" enum:"missing,published,ai_draft,draft,stale,unavailable"`
	PageID       int64  `json:"page_id,omitempty"`
	Revision     int64  `json:"revision,omitempty"`
	Freshness    string `json:"freshness,omitempty"`
}
type TranslationResult struct {
	PageID       int64         `json:"page_id"`
	Translations []Translation `json:"translations"`
}

// Error carries curated validation feedback, never storage diagnostics.
type Error struct {
	Code              string
	Message           string
	Issues            []draft.Issue
	Retryable         bool
	RetryAfterSeconds int64
}

func (e *Error) Error() string { return e.Message }
