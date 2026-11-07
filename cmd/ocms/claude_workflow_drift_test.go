// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
)

func TestClaudeWorkflowExamplesUseValidSecretsAndFreshTests(t *testing.T) {
	developmentCommand := regexp.MustCompile(`(?m)^ *(?:(OCMS_ENV=development) +)?OCMS_SESSION_SECRET=['"]?([^\s'"]+)['"]? +(go (?:run|test) .+)$`)
	for _, name := range []string{
		".claude/commands/test.md", ".claude/agents/test-runner.md",
		".claude/agents/api-developer.md", "README.md",
	} {
		t.Run(name, func(t *testing.T) {
			body := string(readWorkflowFile(t, filepath.Join("..", "..", name)))
			matches := developmentCommand.FindAllStringSubmatch(body, -1)
			if len(matches) == 0 {
				t.Fatal("missing development session secret example")
			}
			for _, match := range matches {
				if len(match[2]) < config.MinSessionSecretLength {
					t.Errorf("development session secret is %d bytes, need at least %d", len(match[2]), config.MinSessionSecretLength)
				}
				if strings.HasPrefix(match[3], "go run ") && match[1] == "" {
					t.Error("development server example must opt into OCMS_ENV=development")
				}
				if strings.HasPrefix(match[3], "go test ") && !slices.Contains(strings.Fields(match[3]), "-count=1") {
					t.Error("Go test example can reuse cached results")
				}
			}
		})
	}
}

func TestClaudeCommandsRouteValidatedInputs(t *testing.T) {
	for _, tt := range []struct {
		name      string
		required  []string
		forbidden []string
	}{
		{
			name: "templui-add",
			required: []string{"$ARGUMENTS", `^[a-z][a-z0-9-]*$`, "a separate shell argument",
				"templui --version", "templui --force add button card dialog", `templui --force add "*"`},
			forbidden: []string{"templui -f ", "templui -v`"},
		},
		{
			name:     "templui-list",
			required: []string{"$ARGUMENTS", `^[a-z][a-z0-9-]*$`, "templui list", "https://templui.io/docs/components/<name>"},
		},
		{
			name: "commit-prepare",
			required: []string{"$ARGUMENTS", "exact `quality` or `q`", "Reject unknown options such as `--quiet`",
				"\n./scripts/codex/commit-prepare.sh\n", "\n./scripts/codex/commit-prepare.sh quality\n"},
			forbidden: []string{`contains "quality"`, "git commit "},
		},
		{
			name:      "commit-do",
			required:  []string{"\n./scripts/codex-commands commit-do\n", "effective Git hooks", "the user to type `YES`"},
			forbidden: []string{"git commit ", "--no-verify"},
		},
		{
			name:      "security-audit",
			required:  []string{"\n./scripts/codex-commands security-audit\n", "preserves archived reports"},
			forbidden: []string{"Move all existing audit files", "launch the security-auditor agent"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := string(readWorkflowFile(t, filepath.Join("..", "..", ".claude", "commands", tt.name+".md")))
			for _, required := range tt.required {
				if !strings.Contains(body, required) {
					t.Errorf("missing required workflow step %q", required)
				}
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(body, forbidden) {
					t.Errorf("found obsolete workflow step %q", forbidden)
				}
			}
		})
	}
}

func TestClaudeAuditAndFrontendToolsStaySeparated(t *testing.T) {
	for _, tt := range []struct {
		name string
		want string
	}{
		{"security-auditor", "Read, Grep, Glob"},
		{"frontend-developer", "Read, Grep, Glob, Bash, Write, Edit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := string(readWorkflowFile(t, filepath.Join("..", "..", ".claude", "agents", tt.name+".md")))
			frontmatter := strings.SplitN(body, "---\n", 3)
			if len(frontmatter) != 3 || !slices.Contains(strings.Split(frontmatter[1], "\n"), "tools: "+tt.want) {
				t.Errorf("expected explicit tool list %q", tt.want)
			}
			if tt.name == "security-auditor" {
				for _, command := range []string{"npm audit fix", "npm update", "go get ", "go mod tidy"} {
					if strings.Contains(body, command) {
						t.Errorf("audit agent suggests running %q", command)
					}
				}
			}
		})
	}
}
