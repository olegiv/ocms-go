// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	"node": true, "govulncheck": true, "scripts/test-gate.sh": true, "./scripts/test-gate.sh": true,
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

// TestProxyReadRulesKeepCommandBoundaries inspects the permissions actually
// passed to Claude by proxy entry points that use Bash. A-T2's git diff* rule also
// authorized git difftool --extcmd, and the old code-running-tool inventory
// did not cover Git or date. Wildcards may vary arguments, not command names.
// N-18 also requires exact list-only formatter rules and denies for write flags
// that the shared command or user settings could otherwise allow.
func TestProxyReadRulesKeepCommandBoundaries(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, workflow := range []string{"code-quality", "security-audit"} {
		t.Run(workflow, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name string, data []byte) {
				t.Helper()
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"scripts/codex-commands", "scripts/codex/proxy-claude-command.sh"} {
				data, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				write(name, data)
			}
			// The strict-lint gate has its own real-hook regression test below.
			write(".claude/hooks/go-strict-lint.sh", []byte("#!/bin/sh\nexit 0\n"))
			// The audit's report lifecycle is covered by the real helper test.
			write("scripts/codex/security-audit.sh", []byte("#!/bin/sh\npwd\n"))
			write("scripts/codex/security-audit-proxy.md", []byte("Audit prompt\n"))
			write("bin/claude", []byte("#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$PROXY_TEST_ARGS\"\necho 'Fixture audit report'\n"))
			argsFile := filepath.Join(dir, "args")
			command := exec.Command("bash", filepath.Join(dir, "scripts", "codex-commands"), workflow)
			command.Dir = dir
			command.Env = []string{"PATH=" + filepath.Join(dir, "bin") + ":/usr/bin:/bin", "PROXY_TEST_ARGS=" + argsFile}
			if out, err := command.CombinedOutput(); err != nil {
				t.Fatalf("proxy dispatch: %v\n%s", err, out)
			}
			data, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
			start, end := slices.Index(args, "--allowedTools"), slices.Index(args, "-p")
			if start < 0 || end <= start+1 {
				t.Fatalf("proxy passed no permission list: %q", args)
			}
			allowed := args[start+1 : end]
			for _, name := range []string{"git status", "git diff", "git log", "git show", "git rev-parse", "git ls-files", "git write-tree", "date"} {
				for _, suffix := range []string{"", " *"} {
					rule := "Bash(" + name + suffix + ")"
					if !slices.Contains(allowed, rule) {
						t.Errorf("missing %s; preserve bare commands and space-delimited arguments", rule)
					}
				}
			}
			for _, rule := range allowed {
				pattern, bash := strings.CutPrefix(rule, "Bash(")
				if !bash {
					continue
				}
				fields := strings.Fields(strings.TrimSuffix(pattern, ")"))
				if len(fields) == 0 {
					t.Errorf("empty Bash permission: %s", rule)
					continue
				}
				if strings.Contains(fields[0], "*") || (fields[0] == "git" && len(fields) > 1 && strings.Contains(fields[1], "*")) {
					t.Errorf("%s lets a wildcard change the command name; put it after a complete command and a space", rule)
				}
			}
			for _, formatter := range []string{"gofmt", "goimports"} {
				want := "Bash(" + formatter + " -l .)"
				if !slices.Contains(allowed, want) {
					t.Errorf("missing list-only formatter rule %s", want)
				}
				for _, rule := range allowed {
					if strings.HasPrefix(rule, "Bash("+formatter) && rule != want {
						t.Errorf("unexpected formatter allow rule %s; allow only %s", rule, want)
					}
				}
			}
			denyFlag := slices.Index(args, "--disallowedTools")
			if denyFlag <= end {
				t.Fatalf("missing explicit denials after the prompt flag: %q", args)
			}
			denied := args[denyFlag+1:]
			if nextFlag := slices.IndexFunc(denied, func(arg string) bool { return strings.HasPrefix(arg, "--") }); nextFlag >= 0 {
				denied = denied[:nextFlag]
			}
			wantDenials := []string{"Edit(//**)", "Bash(gofmt *-w*)", "Bash(goimports *-w*)"}
			if workflow == "security-audit" {
				wantDenials = append(wantDenials, "Bash(mkdir *)", "Bash(mv *)")
			}
			for _, want := range wantDenials {
				if !slices.Contains(denied, want) {
					t.Errorf("missing explicit deny rule %s", want)
				}
			}
			checkProxyGitOptions(t, allowed, denied)
		})
	}
}

func checkProxyGitOptions(t *testing.T, allowed, denied []string) {
	t.Helper()
	matches := func(rule, invocation string) bool {
		pattern := strings.ReplaceAll(regexp.QuoteMeta(rule), `\*`, ".*")
		return regexp.MustCompile("^" + pattern + "$").MatchString(invocation)
	}
	for _, rule := range allowed {
		command, ok := strings.CutPrefix(rule, "Bash(git ")
		if !ok {
			continue
		}
		for _, option := range []string{
			"--output=fixture/path", "--output fixture/path",
			`"--output" fixture/path`, `'--output'=fixture/path`,
			"-s --format=format:fixture --output=fixture/path HEAD",
			"--ext-diff", "--ext-diff HEAD", `"--ext-diff" HEAD`,
			"--no-index --ext-dif", "--no-index --ext",
		} {
			invocation := "Bash(git " + strings.ReplaceAll(command, "*", option)
			if matches(rule, invocation) && strings.Contains(invocation, option) &&
				!slices.ContainsFunc(denied, func(deny string) bool { return matches(deny, invocation) }) {
				t.Errorf("%s permits %s without an explicit deny", rule, invocation)
			}
		}
	}
	for _, subcommand := range []string{"diff", "log", "show"} {
		for _, options := range []string{"-o fixture/path", "--stat -o fixture/path"} {
			invocation := "Bash(git " + subcommand + " " + options + ")"
			if slices.ContainsFunc(allowed, func(rule string) bool { return matches(rule, invocation) }) &&
				!slices.ContainsFunc(denied, func(rule string) bool { return matches(rule, invocation) }) {
				t.Errorf("%s has no explicit deny", invocation)
			}
		}
	}
	for _, command := range []string{
		"Bash(git log --oneline -3)", "Bash(git diff --no-ext-diff HEAD)",
		"Bash(git show --stat HEAD)", "Bash(git ls-files -o)",
		"Bash(git diff --output-indicator-new=> HEAD)", "Bash(git log --extended-regexp --grep=fixture HEAD)",
		"Bash(git log --oneline --grep=--output)",
	} {
		if !slices.ContainsFunc(allowed, func(rule string) bool { return matches(rule, command) }) ||
			slices.ContainsFunc(denied, func(rule string) bool { return matches(rule, command) }) {
			t.Errorf("ordinary read %s must be permitted", command)
		}
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

// TestCodeQualityProxyRequiresStrictLint exercises the real proxy and stack
// hook with a stub linter and Claude executable. A-T1 let a proxy session
// report success without ever running strict lint. Findings, unavailable
// tooling and broken installation must stop before Claude can report a pass.
func TestCodeQualityProxyRequiresStrictLint(t *testing.T) {
	root := filepath.Join("..", "..")
	proxy, err := os.ReadFile(filepath.Join(root, "scripts", "codex", "proxy-claude-command.sh"))
	if err != nil {
		t.Fatal(err)
	}
	hook, err := os.ReadFile(filepath.Join(root, ".claude", "hooks", "go-strict-lint.sh"))
	if errors.Is(err, fs.ErrNotExist) && !sharedToolingRequired() {
		if _, sharedErr := os.Stat(filepath.Join(root, ".claude", "shared", ".git")); errors.Is(sharedErr, fs.ErrNotExist) {
			t.Skip("the .claude/shared submodule is not checked out")
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name       string
		command    string
		linterExit int
		claudeExit int
		damage     string
		wantExit   int
		wantCalls  string
		wantOutput string
	}{
		{name: "clean", command: "/code-quality", wantCalls: "lint\nclaude\n"},
		{name: "alias", command: "code-quality", wantCalls: "lint\nclaude\n"},
		{name: "findings", command: "/code-quality", linterExit: 1, wantExit: 1, wantCalls: "lint\n", wantOutput: "strict fixture finding"},
		{name: "linter failure", command: "/code-quality", linterExit: 7, wantExit: 3, wantCalls: "lint\n", wantOutput: "could not run"},
		{name: "missing link", command: "/code-quality", damage: "link", wantExit: 3, wantOutput: "requires executable"},
		{name: "missing target", command: "/code-quality", damage: "target", wantExit: 3, wantOutput: "requires executable"},
		{name: "nonexecutable", command: "/code-quality", damage: "mode", wantExit: 3, wantOutput: "requires executable"},
		{name: "unexpected hook exit", command: "/code-quality", damage: "exit", wantExit: 3, wantOutput: "exit 127"},
		{name: "Claude failure", command: "/code-quality", claudeExit: 9, wantExit: 9, wantCalls: "lint\nclaude\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "repository with spaces")
			write := func(name, content string, mode fs.FileMode) {
				t.Helper()
				path := filepath.Join(dir, name)
				if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o750); mkdirErr != nil {
					t.Fatal(mkdirErr)
				}
				if writeErr := os.WriteFile(path, []byte(content), mode); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			write("scripts/codex/proxy-claude-command.sh", string(proxy), 0o755)
			write(".claude/shared/stacks/go/hooks/go-strict-lint.sh", string(hook), 0o755)
			write("go.mod", "module example.test/strict\n\ngo 1.22\n", 0o600)
			write("fixture.go", "package strict\n", 0o600)
			write("calls", "", 0o600)
			write("args", "", 0o600)
			write("bin/linter", `#!/bin/sh
[ "$PWD" -ef "$STRICT_TEST_REPO" ] || exit 8
printf 'lint\n' >> "$STRICT_TEST_REPO/calls"
if [ "$STRICT_TEST_LINT_EXIT" = 1 ]; then echo 'strict fixture finding (errcheck)'; fi
exit "$STRICT_TEST_LINT_EXIT"
`, 0o755)
			write("bin/claude", `#!/bin/sh
if [ "$STRICT_TEST_COMMAND" = code-quality ]; then
  [ "$PWD" -ef "$STRICT_TEST_REPO" ] || exit 8
fi
printf 'claude\n' >> "$STRICT_TEST_REPO/calls"
printf '%s\n' "$@" > "$STRICT_TEST_REPO/args"
exit "$STRICT_TEST_CLAUDE_EXIT"
`, 0o755)
			link := filepath.Join(dir, ".claude", "hooks", "go-strict-lint.sh")
			if mkdirErr := os.MkdirAll(filepath.Dir(link), 0o750); mkdirErr != nil {
				t.Fatal(mkdirErr)
			}
			if linkErr := os.Symlink("../shared/stacks/go/hooks/go-strict-lint.sh", link); linkErr != nil {
				t.Fatal(linkErr)
			}
			target := filepath.Join(dir, ".claude", "shared", "stacks", "go", "hooks", "go-strict-lint.sh")
			switch tt.damage {
			case "link":
				if removeErr := os.Remove(link); removeErr != nil {
					t.Fatal(removeErr)
				}
			case "target":
				if removeErr := os.Remove(target); removeErr != nil {
					t.Fatal(removeErr)
				}
			case "mode":
				if modeErr := os.Chmod(target, 0o600); modeErr != nil {
					t.Fatal(modeErr)
				}
			case "exit":
				write(".claude/shared/stacks/go/hooks/go-strict-lint.sh", "#!/bin/sh\nexit 127\n", 0o755)
			}
			env := []string{
				"PATH=" + filepath.Join(dir, "bin") + ":/usr/bin:/bin",
				"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
				"GO_STRICT_LINT_BIN=" + filepath.Join(dir, "bin", "linter"),
				"STRICT_TEST_REPO=" + dir,
				"STRICT_TEST_COMMAND=" + strings.TrimPrefix(tt.command, "/"),
				"STRICT_TEST_LINT_EXIT=" + strconv.Itoa(tt.linterExit),
				"STRICT_TEST_CLAUDE_EXIT=" + strconv.Itoa(tt.claudeExit),
			}
			initRepo := exec.Command("git", "init", "--quiet", dir)
			initRepo.Env = env
			if out, initErr := initRepo.CombinedOutput(); initErr != nil {
				t.Fatalf("initializing fixture: %v\n%s", initErr, out)
			}
			command := exec.Command("bash", filepath.Join(dir, "scripts", "codex", "proxy-claude-command.sh"), tt.command, "scope note")
			command.Dir = t.TempDir() // the proxy must find its own checkout
			command.Env = env
			out, runErr := command.CombinedOutput()
			exitCode := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				if !errors.As(runErr, &exitErr) {
					t.Fatal(runErr)
				}
				exitCode = exitErr.ExitCode()
			}
			if exitCode != tt.wantExit || !strings.Contains(string(out), tt.wantOutput) {
				t.Errorf("exit %d, want %d and output containing %q:\n%s", exitCode, tt.wantExit, tt.wantOutput, out)
			}
			calls, readErr := os.ReadFile(filepath.Join(dir, "calls"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(calls) != tt.wantCalls {
				t.Errorf("execution order %q, want %q", calls, tt.wantCalls)
			}
			args, argsErr := os.ReadFile(filepath.Join(dir, "args"))
			if argsErr != nil {
				t.Fatal(argsErr)
			}
			if strings.Contains(tt.wantCalls, "claude") {
				if !strings.Contains(string(args), "--permission-mode\ndontAsk\n") || !strings.Contains(string(args), "-p\n/"+strings.TrimPrefix(tt.command, "/")+" scope note\n") {
					t.Errorf("proxy lost the permission mode or request arguments: %s", args)
				}
				if strings.TrimPrefix(tt.command, "/") == "code-quality" && !strings.Contains(string(args), "--append-system-prompt\nThe repository proxy already ran .claude/hooks/go-strict-lint.sh --check successfully (exit 0).") {
					t.Error("Claude was not given the completed strict-lint result")
				}
			}
		})
	}
}
