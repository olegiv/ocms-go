// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/ai/grounding"
	"github.com/olegiv/ocms-go/internal/model"
)

func TestAuthoringContextMissingRowsAreClassifiedAtTheirSource(t *testing.T) {
	for _, tc := range []struct {
		name, language, change, repair, code string
	}{
		{"missing default guide", "en", "UPDATE editorial_style_guides SET language_code='sf11-unavailable' WHERE language_code=''", "UPDATE editorial_style_guides SET language_code='' WHERE language_code='sf11-unavailable'", codeInternal},
		{"missing default language", "", "UPDATE languages SET is_default=0 WHERE code='en'", "UPDATE languages SET is_default=1 WHERE code='en'", codeInternal},
		{"unknown explicit language", "zz", "", "", codeValidation},
		{"inactive explicit language", "en", "UPDATE languages SET is_active=0 WHERE code='en'", "UPDATE languages SET is_active=1 WHERE code='en'", codeValidation},
		{"language guide falls back to default", "en", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.setConfig(model.ConfigKeySiteURL, "https://example.test")
			bearer := env.createKey(model.PermissionMCPAccess)
			session := env.connect(bearer)
			if tc.change != "" {
				if _, err := env.db.Exec(tc.change); err != nil {
					t.Fatal(err)
				}
			}
			if tc.code == codeValidation {
				resolver, err := grounding.NewResolver(env.q, "https://example.test", grounding.Access{})
				if err != nil {
					t.Fatal(err)
				}
				_, err = resolver.Build(t.Context(), grounding.Input{LanguageCode: tc.language})
				if !errors.Is(err, grounding.ErrInput) || errors.Is(err, sql.ErrNoRows) {
					t.Errorf("explicit unavailable language lacks domain classification: %v", err)
				}
			}
			args := map[string]any{"language_code": tc.language, "brief": "SF11_PRIVATE_BRIEF"}
			result := call(t, session, "get_authoring_context", args)
			if tc.code == "" {
				decoded := decodeResult[AuthoringResult](t, result)
				if decoded.Context.LanguageCode != "en" || !decoded.Context.Guide.Inherited {
					t.Fatal("missing language override did not use the default guide")
				}
			} else {
				assertDraftPolicyError(t, result, tc.code, false)
				if strings.Contains(resultText(result), sql.ErrNoRows.Error()) || strings.Contains(resultText(result), "SF11_PRIVATE_BRIEF") {
					t.Error("tool error exposed internal cause or request input")
				}
			}
			toolLogs := env.logs.find("MCP tool call")
			if len(toolLogs) != 1 {
				t.Fatalf("tool log count=%d", len(toolLogs))
			}
			entry := toolLogs[0]
			cause, hasCause := recordAttr(entry, "error")
			if tc.code == codeInternal {
				userID, _ := recordAttr(entry, "user_id")
				if entry.Level != slog.LevelError || !hasCause || !strings.Contains(cause.String(), sql.ErrNoRows.Error()) || userID.Int64() != env.userID {
					t.Errorf("missing fault diagnostic: %s", env.logs.text())
				}
			} else if entry.Level == slog.LevelError || hasCause {
				t.Errorf("normal/refused context logged as a fault: %s", env.logs.text())
			}
			// Prompts and the taxonomy resource consume the same context
			// adapter; their wire format is JSON-RPC, not a tool envelope.
			read := func(kind string) error {
				if kind == "prompt" {
					_, err := session.GetPrompt(t.Context(), &mcp.GetPromptParams{Name: "write_page", Arguments: map[string]string{"language": tc.language}})
					return err
				}
				_, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "ocms://taxonomy/" + tc.language})
				return err
			}
			for _, kind := range []string{"prompt", "resource"} {
				if kind == "resource" && tc.language == "" {
					continue // Taxonomy resources require an explicit language.
				}
				readErr := read(kind)
				if tc.code == "" {
					if readErr != nil {
						t.Fatalf("%s fallback failed: %v", kind, readErr)
					}
					continue
				}
				want := int64(jsonrpc.CodeInvalidParams)
				if tc.code == codeInternal {
					want = jsonrpc.CodeInternalError
				}
				var wire *jsonrpc.Error
				if !errors.As(readErr, &wire) || wire.Code != want {
					t.Errorf("%s error=%v, want RPC %d", kind, readErr, want)
				}
				if readErr != nil && (strings.Contains(readErr.Error(), sql.ErrNoRows.Error()) || strings.Contains(readErr.Error(), "SF11_PRIVATE_BRIEF")) {
					t.Errorf("%s exposed internal cause/input", kind)
				}
			}
			for _, record := range env.logs.find("MCP authoring read") {
				loggedCode := ""
				if code, ok := recordAttr(record, "error_code"); ok {
					loggedCode = code.String()
				}
				if loggedCode != tc.code || (record.Level == slog.LevelError) != (tc.code == codeInternal) {
					t.Errorf("prompt/resource classification mismatch: %s", env.logs.text())
				}
			}
			if tc.repair != "" {
				if _, err := env.db.Exec(tc.repair); err != nil {
					t.Fatal(err)
				}
			}
			repaired := decodeResult[AuthoringResult](t, call(t, session, "get_authoring_context", map[string]any{"language_code": "en"}))
			if repaired.Context.LanguageCode != "en" {
				t.Fatal("repaired context was not restored")
			}
			for _, private := range []string{bearer, "SF11_PRIVATE_BRIEF"} {
				if strings.Contains(env.logs.text(), private) {
					t.Error("context logs exposed private input")
				}
			}
		})
	}
}
