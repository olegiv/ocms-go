// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// commandBashRule matches a Bash(...) rule in a command's allowed-tools line.
var commandBashRule = regexp.MustCompile(`Bash\(([^)]*)\)`)

// TestSharedCodeQualityRulesMatchProxy fails when the shared /code-quality
// command allows a code-running tool by prefix, or with a different command
// line than the Codex proxy, which runs the same command. The shared command
// allowed "errcheck ./..." while the project documents, and the proxy
// allows, "errcheck -ignoregenerated ./..." (review of the 2026-10-17 audit
// fixes), so following the project instructions inside /code-quality
// triggered a permission prompt.
//
// The command is a link into the .claude/shared submodule. Exported trees,
// the release workflow and clones without --recurse-submodules do not check
// it out, so the link dangles and the test skips, unless
// OCMS_REQUIRE_SHARED_TOOLING=1 (set by the Go CI workflow) makes that a
// failure; a missing link fails. A submodule checked out at an older commit
// fails this test until its pointer is bumped.
//
// Bug state: change "Bash(errcheck -ignoregenerated ./...)" back to
// "Bash(errcheck ./...)" in .claude/shared/stacks/go/commands/code-quality.md
// and this names it.
func TestSharedCodeQualityRulesMatchProxy(t *testing.T) {
	link := filepath.Join("..", "..", ".claude", "commands", "code-quality.md")
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("the /code-quality command link is missing: %v", err)
	}
	command, err := os.ReadFile(link)
	if errors.Is(err, fs.ErrNotExist) {
		if sharedToolingRequired() {
			t.Fatal("the .claude/shared submodule is not checked out, and OCMS_REQUIRE_SHARED_TOOLING=1 requires it")
		}
		t.Skip("the .claude/shared submodule is not checked out")
	}
	if err != nil {
		t.Fatalf("reading the /code-quality command: %v", err)
	}
	proxy, err := os.ReadFile(filepath.Join("..", "..", "scripts", "codex", "proxy-claude-command.sh"))
	if err != nil {
		t.Fatalf("reading the proxy script: %v", err)
	}
	var allowed []string
	for _, line := range strings.Split(string(command), "\n") {
		if rest, ok := strings.CutPrefix(line, "allowed-tools:"); ok {
			for _, match := range commandBashRule.FindAllStringSubmatch(rest, -1) {
				allowed = append(allowed, match[1])
			}
			break
		}
	}
	var proxied []string
	for _, match := range proxyBashRule.FindAllStringSubmatch(string(proxy), -1) {
		proxied = append(proxied, match[1])
	}
	shared, ours := rulesByTool(allowed), rulesByTool(proxied)
	compared := 0
	for tool, rules := range shared {
		for _, rule := range rules {
			if strings.Contains(rule, "*") {
				t.Errorf("/code-quality allows %s by prefix with Bash(%s); list the exact command line instead", tool, rule)
			}
		}
		want, ok := ours[tool]
		if !ok {
			continue
		}
		compared++
		if !slices.Equal(rules, want) {
			t.Errorf("/code-quality allows %s as %q, the proxy as %q; use the same command line", tool, rules, want)
		}
	}
	if compared == 0 {
		t.Fatal("no code-running tool is allowed by both; the rule format changed and this test is vacuous")
	}
}

// rulesByTool groups the rules for code-running tools by tool, sorted. A
// tool is the command name, with its subcommand for go, npm and make.
func rulesByTool(rules []string) map[string][]string {
	byTool := map[string][]string{}
	for _, rule := range rules {
		fields := strings.Fields(rule)
		if len(fields) == 0 {
			continue
		}
		tool := strings.TrimRight(fields[0], "*")
		if !codeRunningCommands[tool] {
			continue
		}
		if (tool == "go" || tool == "npm" || tool == "make") && len(fields) > 1 {
			tool += " " + strings.TrimRight(fields[1], "*")
		}
		byTool[tool] = append(byTool[tool], rule)
	}
	for tool := range byTool {
		slices.Sort(byTool[tool])
	}
	return byTool
}

// sharedToolingRequired reports whether the tests must find the
// .claude/shared submodule checked out instead of skipping without it; the
// Go CI workflow checks it out and sets OCMS_REQUIRE_SHARED_TOOLING=1.
func sharedToolingRequired() bool {
	return os.Getenv("OCMS_REQUIRE_SHARED_TOOLING") == "1"
}
