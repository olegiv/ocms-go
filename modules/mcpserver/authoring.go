// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/ai/grounding"
	"github.com/olegiv/ocms-go/internal/ai/prompts"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/util"
)

type resourceSpec struct {
	URI, Name, Kind, Tool string
	Template              bool
}

func authoringResources() []resourceSpec {
	return []resourceSpec{
		{URI: "ocms://style-guide", Name: "Site style guide", Kind: "style-guide", Tool: "get_style_guide"},
		{URI: "ocms://style-guide/{lang}", Name: "Language style guide", Kind: "style-guide", Tool: "get_style_guide", Template: true},
		{URI: "ocms://presets", Name: "Authoring presets", Kind: "presets", Tool: "list_presets"},
		{URI: "ocms://presets/{lang}", Name: "Language presets", Kind: "presets", Tool: "list_presets", Template: true},
		{URI: "ocms://taxonomy/{lang}", Name: "Language taxonomy and authoring choices", Kind: "taxonomy", Tool: "get_authoring_context", Template: true},
		{URI: "ocms://languages", Name: "Site languages", Kind: "languages", Tool: "get_site_info"},
		{URI: "ocms://draft-schema", Name: "Page draft contracts", Kind: "draft-schema", Tool: "get_page_draft_schema"},
		{URI: "ocms://issue-catalog", Name: "Draft issue catalog", Kind: "issue-catalog", Tool: "get_page_draft_schema"},
	}
}

func (m *Module) registerAuthoring(srv *mcp.Server) {
	for _, definition := range prompts.Definitions() {
		srv.AddPrompt(&mcp.Prompt{Name: definition.Name, Title: definition.Title, Description: definition.Description, Arguments: []*mcp.PromptArgument{
			{Name: "preset", Description: "One preset slug, such as blog-post."}, {Name: "language", Description: "One active language code."}, {Name: "refs", Description: "One comma-joined token, such as 12:example,/about:source. Topic comes from the conversation; use get_authoring_context for a multi-word brief."},
		}}, m.getAuthoringPrompt)
	}
	for _, spec := range authoringResources() {
		if spec.Template {
			srv.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: spec.URI, Name: spec.Name, MIMEType: "application/json"}, m.readAuthoringResource)
		} else {
			srv.AddResource(&mcp.Resource{URI: spec.URI, Name: spec.Name, MIMEType: "application/json"}, m.readAuthoringResource)
		}
	}
}

func identityFromMCPRequest(req mcp.Request) (*identity, bool) {
	if req == nil || req.GetExtra() == nil {
		return nil, false
	}
	return identityFromTokenInfo(req.GetExtra().TokenInfo)
}

func (m *Module) authoringCall(ctx context.Context, req mcp.Request) (*toolCall, error) {
	rec, _ := callRecordFrom(ctx)
	id, ok := identityFromMCPRequest(req)
	rec.identity = id
	if !ok {
		rec.outcome, rec.errorCode = outcomeUnauthenticated, codeUnauthorized
		return nil, &toolError{Code: codeUnauthorized, Message: "Authentication required"}
	}
	if !slices.Contains(id.scopes, model.PermissionMCPAccess) {
		return nil, &toolError{Code: codeForbidden, Message: "mcp:access permission required"}
	}
	settings, ok := ctx.Value(requestSettingsKey{}).(Settings)
	if !ok {
		settings = m.currentSettings()
	}
	return &toolCall{identity: id, actor: actorFor(id, settings), settings: settings}, nil
}

func (m *Module) getAuthoringPrompt(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	call, err := m.authoringCall(ctx, req)
	if err != nil {
		return nil, err
	}
	if req.Params == nil {
		return nil, newValidationError("arguments", "Choose a known authoring prompt")
	}
	in, err := prompts.Arguments(req.Params.Name, req.Params.Arguments)
	if err != nil {
		return nil, newValidationError("arguments", "Use optional single-token preset, language and refs arguments; use the context tool for a full brief")
	}
	result, err := m.getAuthoringContext(ctx, call, in)
	if err != nil {
		return nil, err
	}
	rendered, err := prompts.Render(result.Context)
	if err != nil {
		return nil, err
	}
	return &mcp.GetPromptResult{Description: "oCMS authoring prompt v" + prompts.Version, Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: rendered}}}}, nil
}

func resourceTarget(raw string) (string, string, error) {
	uri, err := url.Parse(raw)
	if err != nil || uri.Scheme != "ocms" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" || uri.Opaque != "" {
		return "", "", newValidationError("uri", "Choose a listed oCMS resource")
	}
	language := strings.TrimPrefix(uri.Path, "/")
	if language != "" && (!util.IsRoutableLanguageCode(language) || strings.Contains(language, "/")) {
		return "", "", newValidationError("language_code", "Choose one active language code")
	}
	switch uri.Host {
	case "style-guide", "presets":
		if uri.Path == "/" {
			return "", "", newValidationError("language_code", "A template language cannot be empty")
		}
	case "taxonomy":
		if language == "" {
			return "", "", newValidationError("language_code", "Taxonomy requires a language")
		}
	case "languages", "draft-schema", "issue-catalog":
		if uri.Path != "" {
			return "", "", newValidationError("uri", "Choose a listed oCMS resource")
		}
	default:
		return "", "", newValidationError("uri", "Choose a listed oCMS resource")
	}
	return uri.Host, language, nil
}

func (m *Module) readAuthoringResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	call, err := m.authoringCall(ctx, req)
	if err != nil {
		return nil, err
	}
	if req.Params == nil {
		return nil, newValidationError("uri", "Choose a listed oCMS resource")
	}
	kind, language, err := resourceTarget(req.Params.URI)
	if err != nil {
		return nil, err
	}
	var value any
	switch kind {
	case "style-guide":
		value, err = m.getStyleGuide(ctx, call, GuidanceInput{LanguageCode: language})
	case "presets":
		value, err = m.listPresets(ctx, call, GuidanceInput{LanguageCode: language})
	case "taxonomy":
		value, err = m.getAuthoringContext(ctx, call, grounding.Input{LanguageCode: language})
	case "languages":
		value, err = m.getSiteInfo(ctx, call, SiteInfoInput{})
	case "draft-schema", "issue-catalog":
		value, err = m.getPageDraftSchema(ctx, call, DraftSchemaInput{})
	}
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(raw)}}}, nil
}

// finishAuthoringRead gives prompt/resource calls the same bounded, scrubbed, single-record lifecycle as tools.
func (m *Module) finishAuthoringRead(ctx context.Context, method string, req mcp.Request, rec *callRecord, result mcp.Result, err error, elapsed time.Duration) (mcp.Result, error) {
	switch {
	case ctx.Err() != nil || isCancellation(err):
		rec.outcome, rec.errorCode = outcomeCancelled, codeTimeout
		err = &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "Authoring read cancelled or timed out"}
	case err != nil:
		var te *toolError
		var wire *jsonrpc.Error
		switch {
		case errors.As(err, &te):
			rec.outcome, rec.errorCode = outcomeToolError, te.Code
			if te.Code == codeInternal {
				rec.outcome = outcomeInternalError
				err = internalRPCError()
			} else {
				err = &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: te.Error()}
			}
		case errors.As(err, &wire):
			rec.outcome, rec.errorCode = outcomeRejected, rpcErrorCode(wire.Code)
			if wire.Code == jsonrpc.CodeInternalError {
				rec.outcome = outcomeInternalError
				err = internalRPCError()
			} else {
				err = &jsonrpc.Error{Code: wire.Code, Message: "Invalid authoring request"}
			}
		default:
			rec.outcome, rec.errorCode = outcomeInternalError, codeInternal
			err = internalRPCError()
		}
	}
	if rec.outcome == "" {
		rec.outcome = outcomeOK
	}
	if rec.identity == nil {
		rec.identity, _ = identityFromMCPRequest(req)
	}
	name := "unknown"
	switch typed := req.(type) {
	case *mcp.GetPromptRequest:
		if typed.Params != nil {
			for _, def := range prompts.Definitions() {
				if typed.Params.Name == def.Name {
					name = def.Name
				}
			}
		}
	case *mcp.ReadResourceRequest:
		if typed.Params != nil {
			if kind, _, parseErr := resourceTarget(typed.Params.URI); parseErr == nil {
				name = kind
			}
		}
	}
	attrs := []any{"method", method, "name", name, "outcome", rec.outcome, "duration_ms", elapsed.Milliseconds()}
	if rec.identity != nil {
		attrs = append(attrs, "user_id", rec.identity.userID)
		if rec.identity.grantID != "" {
			attrs = append(attrs, "auth_type", "oauth", "grant_id", rec.identity.grantID)
		} else {
			attrs = append(attrs, "auth_type", "api_key", "api_key_id", rec.identity.key.ID)
		}
	}
	if rec.errorCode != "" {
		attrs = append(attrs, "error_code", rec.errorCode)
	}
	level := slog.LevelInfo
	switch rec.outcome {
	case outcomeInternalError:
		level = slog.LevelError
	case outcomeCancelled:
		level = slog.LevelWarn
	}
	m.logger.Log(context.Background(), level, "MCP authoring read", attrs...)
	return result, err
}
