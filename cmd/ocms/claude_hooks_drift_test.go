// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wiredClaudeHooks are the hook scripts in .claude/hooks that
// .claude/settings.json runs, with the event and tool matcher each must run
// under: the toolchain check can block a command only before a Bash call.
var wiredClaudeHooks = map[string]string{
	"validate-go-toolchain.sh": "PreToolUse Bash",
}

// optInClaudeHooks are the hook scripts in .claude/hooks that
// .claude/settings.json leaves unwired on purpose, with the reason.
var optInClaudeHooks = map[string]string{
	"setup-go-toolchain.sh": "replaces /usr/local/go, a machine-wide change an operator opts into",
	"validate-go-test.sh":   "advisory only: it always exits 0, so Claude never sees its -race note, and the test gate runs without -race on purpose (CI has a race step)",
}

// TestClaudeHooksAreWired fails when a hook script in .claude/hooks is
// neither wired into .claude/settings.json under its event and matcher
// (wiredClaudeHooks) nor listed in optInClaudeHooks, or when settings.json
// runs anything else. The Go toolchain hook sat in .claude/hooks unused,
// because settings.json had no hooks (2026-10-17 audit, I-11).
//
// The validate hooks are links into the .claude/shared submodule. Where the
// submodule is checked out each wired link must resolve to an executable
// script; CI and exported trees do not check it out, so there only the link
// itself is checked.
//
// Bug state: set "hooks" to {} in .claude/settings.json and this names the
// toolchain hook.
func TestClaudeHooksAreWired(t *testing.T) {
	claudeDir := filepath.Join("..", "..", ".claude")
	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatalf("reading .claude/settings.json: %v", err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing .claude/settings.json: %v", err)
	}
	_, err = os.Stat(filepath.Join(claudeDir, "shared", ".git"))
	sharedCheckedOut := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("checking for the .claude/shared submodule: %v", err)
	}

	const prefix = `"$CLAUDE_PROJECT_DIR"/.claude/hooks/`
	wired := map[string]string{} // script → "event matcher"
	for event, groups := range settings.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				name, ok := strings.CutPrefix(hook.Command, prefix)
				if !ok || strings.ContainsAny(name, `/ "`) {
					t.Errorf("%s hook %q does not run a script from .claude/hooks", event, hook.Command)
					continue
				}
				wired[name] = event + " " + group.Matcher
				script := filepath.Join(claudeDir, "hooks", name)
				info, err := os.Lstat(script)
				if err == nil && sharedCheckedOut {
					info, err = os.Stat(script) // a link must resolve
				}
				switch {
				case err != nil:
					t.Errorf("%s hook runs %s: %v", event, name, err)
				case info.Mode().IsRegular() && info.Mode().Perm()&0o111 == 0:
					t.Errorf("%s hook runs %s, which is not executable", event, name)
				}
			}
		}
	}
	for name, placement := range wired {
		if want, ok := wiredClaudeHooks[name]; !ok {
			t.Errorf("%s is wired in .claude/settings.json but not listed in wiredClaudeHooks", name)
		} else if placement != want {
			t.Errorf("%s is wired under %q, want %q", name, placement, want)
		}
	}

	entries, err := os.ReadDir(filepath.Join(claudeDir, "hooks"))
	if err != nil {
		t.Fatalf("listing .claude/hooks: %v", err)
	}
	scripts := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sh") {
			continue
		}
		scripts++
		_, optIn := optInClaudeHooks[name]
		_, isWired := wired[name]
		switch {
		case isWired && optIn:
			t.Errorf("%s is wired in .claude/settings.json but listed as opt-in", name)
		case !isWired && !optIn:
			t.Errorf("%s is not wired in .claude/settings.json; wire it and list it in wiredClaudeHooks, or add it to optInClaudeHooks with the reason", name)
		}
	}
	for name := range wiredClaudeHooks {
		if _, ok := wired[name]; !ok {
			t.Errorf("%s is listed in wiredClaudeHooks but not wired in .claude/settings.json", name)
		}
	}
	if scripts == 0 {
		t.Fatal("no hook scripts in .claude/hooks; the layout changed and this test is vacuous")
	}
}
