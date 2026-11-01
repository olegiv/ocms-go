// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/olegiv/ocms-go/internal/ai/draft"
	"github.com/olegiv/ocms-go/internal/ai/grounding"
	"github.com/olegiv/ocms-go/internal/ai/prompts"
	"github.com/olegiv/ocms-go/internal/contentpath"
	"github.com/olegiv/ocms-go/internal/editorial"
	"github.com/olegiv/ocms-go/internal/model"
)

// AuthoringResult has one data envelope and the matching prompt instructions, without duplicate content.
type AuthoringResult struct {
	Context       grounding.Context `json:"context"`
	Instructions  string            `json:"instructions"`
	PromptVersion string            `json:"prompt_version"`
}

func (m *Module) authoringResolver(ctx context.Context, call *toolCall) (*grounding.Resolver, error) {
	siteURL, _, err := m.resolveSiteURL(ctx)
	if err != nil {
		return nil, err
	}
	if siteURL == "" {
		return nil, newValidationError("site_url", "Configure the site's public HTTP(S) origin before requesting authoring context")
	}
	resolver, err := grounding.NewResolver(m.svc.queries, siteURL, grounding.Access{UserID: call.identity.userID, AllDrafts: draftsVisible(call.actor), OwnDrafts: slices.Contains(call.identity.scopes, model.PermissionMCPDrafts)})
	if err != nil {
		return nil, newValidationError("site_url", "Configure the site's public HTTP(S) origin without a path or query")
	}
	return resolver, nil
}

func (m *Module) getAuthoringContext(ctx context.Context, call *toolCall, in grounding.Input) (AuthoringResult, error) {
	resolver, err := m.authoringResolver(ctx, call)
	if err != nil {
		return AuthoringResult{}, err
	}
	value, err := resolver.Build(ctx, in)
	if errors.Is(err, grounding.ErrInput) || errors.Is(err, editorial.ErrInvalid) || errors.Is(err, sql.ErrNoRows) {
		return AuthoringResult{}, newValidationError("context", "Use active languages, a known preset and at most eight valid references; reduce oversized input")
	}
	if err != nil {
		return AuthoringResult{}, err
	}
	instructions, err := prompts.Instructions(value.Task)
	if err != nil {
		return AuthoringResult{}, err
	}
	return AuthoringResult{Context: value, Instructions: instructions, PromptVersion: prompts.Version}, nil
}

type DraftSchemaInput struct{}
type DraftSchemaResult struct {
	Create map[string]any    `json:"create"`
	Update map[string]any    `json:"update"`
	Issues []IssueDefinition `json:"issues"`
}

func (m *Module) getPageDraftSchema(_ context.Context, _ *toolCall, _ DraftSchemaInput) (DraftSchemaResult, error) {
	create, err := draft.CreateSchema()
	if err != nil {
		return DraftSchemaResult{}, err
	}
	update, err := draft.UpdateSchema()
	if err != nil {
		return DraftSchemaResult{}, err
	}
	raw, err := json.Marshal(struct{ Create, Update any }{create, update})
	if err != nil {
		return DraftSchemaResult{}, err
	}
	var decoded struct {
		Create map[string]any
		Update map[string]any
	}
	if err = json.Unmarshal(raw, &decoded); err != nil {
		return DraftSchemaResult{}, err
	}
	return DraftSchemaResult{Create: decoded.Create, Update: decoded.Update, Issues: issueDefinitions()}, nil
}

type GuidanceInput struct {
	LanguageCode string `json:"language_code,omitempty" minLength:"2" maxLength:"10" doc:"Active language; omitted uses the site default."`
}
type StyleGuideResult struct {
	Guide editorial.Guide `json:"style_guide"`
}
type PresetsResult struct {
	LanguageCode string             `json:"language_code"`
	Presets      []editorial.Preset `json:"presets"`
}

func (m *Module) guidanceLanguage(ctx context.Context, input GuidanceInput) (string, error) {
	lang, err := contentpath.RouteLanguage(ctx, m.svc.queries, input.LanguageCode)
	if errors.Is(err, sql.ErrNoRows) {
		return "", newValidationError("language_code", "Choose an active routable language")
	}
	return lang.Code, err
}
func (m *Module) getStyleGuide(ctx context.Context, _ *toolCall, in GuidanceInput) (StyleGuideResult, error) {
	language, err := m.guidanceLanguage(ctx, in)
	if err != nil {
		return StyleGuideResult{}, err
	}
	guide, err := editorial.New(m.svc.queries).Guide(ctx, language)
	return StyleGuideResult{Guide: guide}, err
}
func (m *Module) listPresets(ctx context.Context, _ *toolCall, in GuidanceInput) (PresetsResult, error) {
	language, err := m.guidanceLanguage(ctx, in)
	if err != nil {
		return PresetsResult{}, err
	}
	presets, err := editorial.New(m.svc.queries).Presets(ctx, language)
	return PresetsResult{LanguageCode: language, Presets: presets}, err
}

// IssueDefinition exposes the closed Go catalog with explicit language keys.
type IssueDefinition struct {
	Code     string         `json:"code"`
	Severity draft.Severity `json:"severity"`
	English  IssueText      `json:"en"`
	Russian  IssueText      `json:"ru"`
}
type IssueText struct {
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

func issueDefinitions() []IssueDefinition {
	result := make([]IssueDefinition, 0, len(draft.Catalog()))
	for _, entry := range draft.Catalog() {
		result = append(result, IssueDefinition{Code: entry.Code, Severity: entry.Severity, English: IssueText{Message: entry.English.Message, Hint: entry.English.Hint}, Russian: IssueText{Message: entry.Russian.Message, Hint: entry.Russian.Hint}})
	}
	return result
}
