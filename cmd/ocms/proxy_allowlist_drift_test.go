// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// proxyBashRule matches a Bash(...) permission rule in the proxy script.
var proxyBashRule = regexp.MustCompile(`"Bash\(([^)]*)\)"`)

// codeRunningCommands are the programs that run project or package code, so
// an allowlist rule for them must name the exact command line.
var codeRunningCommands = map[string]bool{
	"go": true, "staticcheck": true, "errcheck": true, "golangci-lint": true, "nilaway": true,
	"npm": true, "npx": true, "make": true, "sh": true, "bash": true, "python": true, "python3": true,
	"node": true, "scripts/test-gate.sh": true, "./scripts/test-gate.sh": true,
}

// TestProxyRulesForCodeRunningToolsAreExact fails when the Codex proxy allows
// a code-running tool by prefix.
//
// The proxied commands read diffs, logs and scan output with permission
// prompts off, so a prefix such as "go test*" lets text they read run
// "go test -exec <program>", "go env -w" or "npm audit fix" (review of the
// 2026-10-17 audit fixes).
//
// Bug state: change "Bash(go vet ./...)" in
// scripts/codex/proxy-claude-command.sh to "Bash(go vet*)", or
// "Bash(staticcheck ./...)" to "Bash(staticcheck*)", and this names it.
func TestProxyRulesForCodeRunningToolsAreExact(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "codex", "proxy-claude-command.sh"))
	if err != nil {
		t.Fatalf("reading the proxy script: %v", err)
	}
	checked := 0
	for _, match := range proxyBashRule.FindAllStringSubmatch(string(data), -1) {
		rule := match[1]
		// The command name ends at the first space or wildcard, so
		// "staticcheck*" is checked as staticcheck.
		command := rule
		if i := strings.IndexAny(rule, " *"); i >= 0 {
			command = rule[:i]
		}
		if !codeRunningCommands[command] {
			continue
		}
		checked++
		if strings.Contains(rule, "*") {
			t.Errorf("Bash(%s) allows %s by prefix; list the exact command line instead", rule, command)
		}
	}
	if checked == 0 {
		t.Fatal("no code-running rule found in the proxy script; the rule format changed and this test is vacuous")
	}
}
