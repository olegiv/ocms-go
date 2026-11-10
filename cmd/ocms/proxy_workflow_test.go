// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type proxyWorkflowFixture struct {
	t   *testing.T
	dir string
	bin string
	env []string
}

func newProxyWorkflowFixture(t *testing.T) proxyWorkflowFixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "checkout with spaces")
	bin := t.TempDir()
	f := proxyWorkflowFixture{t: t, dir: dir, bin: bin, env: []string{
		"PATH=" + bin + ":" + os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
		"FIXTURE_ROOT=" + dir, "FIXTURE_BIN=" + bin,
	}}
	for _, name := range []string{
		"scripts/codex-commands", "scripts/codex/proxy-claude-command.sh",
		"scripts/codex/commit-prepare.sh", "scripts/codex/commit-do.sh",
		"scripts/codex/test-snapshot.sh", "scripts/codex/security-audit.sh",
		"scripts/codex/security-audit-proxy.md", "scripts/with-go-packages.sh",
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		f.write(name, string(data))
	}
	f.write(".gitignore", ".audit/\n.env\n")
	f.git("init", "-q", "--template=")
	f.script("claude", `#!/bin/sh
printf '%s\0' "$@" > "$FIXTURE_BIN/claude-args"
if [ "${1:-}" = --permission-mode ] && [ "${3:-}" = --tools ]; then
  cat > "$FIXTURE_BIN/draft-input"
  printf 'Update the fixture\n\nApply the tested edits.\n'
else
  printf '# Fixture audit report\n'
fi
exit "${FIXTURE_CLAUDE_EXIT:-0}"
`)
	return f
}

func (f proxyWorkflowFixture) write(name, contents string) {
	f.t.Helper()
	target := filepath.Join(f.dir, name)
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(contents), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

// script installs a fake command. It replaces the file by rename instead of
// rewriting it in place: dash reads a script as it runs, so a process still
// running the previous version would otherwise read the new contents from
// the middle and fail with a syntax error.
func (f proxyWorkflowFixture) script(name, contents string) {
	f.t.Helper()
	target := filepath.Join(f.bin, name)
	staged := target + ".new"
	if err := os.WriteFile(staged, []byte(contents), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(staged, target); err != nil {
		f.t.Fatal(err)
	}
}

func (f proxyWorkflowFixture) command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = f.dir
	cmd.Env = f.env
	return cmd
}

func (f proxyWorkflowFixture) git(args ...string) string {
	f.t.Helper()
	out, err := f.command("git", args...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f proxyWorkflowFixture) run(input string, want int, args ...string) string {
	f.t.Helper()
	cmd := f.command("bash", append([]string{"scripts/codex-commands"}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	status := 0
	if err != nil {
		if !errors.As(err, &exitErr) {
			f.t.Fatal(err)
		}
		status = exitErr.ExitCode()
	}
	if status != want {
		f.t.Fatalf("%v: exit %d, want %d\n%s", args, status, want, out)
	}
	return string(out)
}

func readWorkflowFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func requireWorkflowFileAbsent(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected %s to be absent, stat error: %v", name, err)
	}
}

func (f proxyWorkflowFixture) seedSnapshot() {
	f.t.Helper()
	f.write("go.mod", "module example.invalid/fixture\n\ngo 1.22\n")
	f.write("page with spaces.txt", "base\n")
	f.write("unrelated.txt", "base\n")
	f.write("scripts/build-assets.sh", `#!/bin/bash
set -euo pipefail
[[ ! "$PWD" -ef "$FIXTURE_ROOT" && ! -e .env && ! -e ignored.go && ! -e untracked.go ]]
[[ "$CI" == true && "$GOWORK" == off && "$OCMS_REQUIRE_SHARED_TOOLING" == 1 ]]
[[ $(cat .claude/shared/version) == pinned ]]
printf 'assets\n' >> "$FIXTURE_BIN/trace"
if [[ ${FIXTURE_MUTATE:-0} == 1 ]]; then echo changed > unrelated.txt; fi
`)
	f.write("scripts/test-gate.sh", `#!/bin/bash
set -euo pipefail
[[ ! "$PWD" -ef "$FIXTURE_ROOT" && $(cat unrelated.txt) == base ]]
[[ $(cat 'page with spaces.txt') == "$FIXTURE_EXPECTED" ]]
printf 'gate\n' >> "$FIXTURE_BIN/trace"
exit "${FIXTURE_GATE_EXIT:-0}"
`)
	f.write(".gitignore", ".audit/\n.env\nignored.go\n")
	f.git("add", "--all")
	f.git("init", "-q", "--template=", ".claude/shared")
	f.write(".claude/shared/version", "pinned\n")
	f.git("-C", ".claude/shared", "add", "--", "version")
	shared := f.git("-C", ".claude/shared", "commit-tree", f.git("-C", ".claude/shared", "write-tree"), "-m", "Fixture shared")
	f.git("-C", ".claude/shared", "read-tree", shared)
	f.write(".claude/shared/.git/HEAD", shared+"\n")
	f.git("update-index", "--add", "--cacheinfo", "160000,"+shared+",.claude/shared")
	base := f.git("commit-tree", f.git("write-tree"), "-m", "Fixture base")
	f.write(".git/HEAD", base+"\n")
	f.write(".claude/shared/version", "dirty shared checkout\n")
	f.write(".env", "not part of the candidate\n")
	f.write("ignored.go", "not part of the candidate\n")
	f.write("untracked.go", "not part of the candidate\n")
	f.write("unrelated.txt", "unrelated worktree edits\n")
}

func TestProxyCommitWorkflowTestsCandidateBeforeDrafting(t *testing.T) {
	for _, tt := range []struct {
		name   string
		args   []string
		env    []string
		trace  string
		status int
	}{
		{name: "staged only", args: []string{"commit-prepare"}, trace: "assets\ngate\n"},
		{name: "named file", args: []string{"commit-prepare", "--", "page with spaces.txt"}, trace: "assets\ngate\n"},
		{name: "gate failure", args: []string{"commit-prepare"}, env: []string{"FIXTURE_GATE_EXIT=7"}, trace: "assets\ngate\n", status: 7},
		{name: "tracked asset drift", args: []string{"commit-prepare"}, env: []string{"FIXTURE_MUTATE=1"}, trace: "assets\n", status: 2},
		{name: "local replacement", args: []string{"commit-prepare"}, status: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newProxyWorkflowFixture(t)
			f.seedSnapshot()
			f.write("page with spaces.txt", "intended\n")
			if tt.name != "named file" {
				f.git("add", "--", "page with spaces.txt")
				f.write("page with spaces.txt", "later worktree edit\n")
			}
			if tt.name == "local replacement" {
				f.write("go.mod", "module example.invalid/fixture\n\ngo 1.22\n\nreplace example.invalid/lib => ../outside\n")
				f.git("add", "--", "go.mod")
			}
			f.env = append(f.env, "FIXTURE_EXPECTED=intended")
			f.env = append(f.env, tt.env...)
			f.write(".git/.codex/tested-tree", "stale receipt\n")
			index := f.git("write-tree")
			indexFile := filepath.Join(f.dir, ".git/index")
			indexBytes := readWorkflowFile(t, indexFile)
			out := f.run("", tt.status, tt.args...)
			if !bytes.Equal(readWorkflowFile(t, indexFile), indexBytes) {
				t.Fatalf("preparation changed the index for tree %s", index)
			}
			if tt.trace == "" {
				requireWorkflowFileAbsent(t, filepath.Join(f.bin, "trace"))
			} else if trace := readWorkflowFile(t, filepath.Join(f.bin, "trace")); string(trace) != tt.trace {
				t.Fatalf("gate order: %q, want %q", trace, tt.trace)
			}
			receiptFile := filepath.Join(f.dir, ".git/.codex/tested-tree")
			if tt.status != 0 {
				requireWorkflowFileAbsent(t, receiptFile)
				requireWorkflowFileAbsent(t, filepath.Join(f.bin, "claude-args"))
				return
			}
			receipt := strings.Fields(string(readWorkflowFile(t, receiptFile)))
			if len(receipt) != 2 || !strings.Contains(out, "Verified tree "+receipt[1]) ||
				f.git("show", receipt[1]+":page with spaces.txt") != "intended" {
				t.Fatalf("wrong tested tree: %q\n%s", receipt, out)
			}
			args := readWorkflowFile(t, filepath.Join(f.bin, "claude-args"))
			if !bytes.Contains(args, []byte("--tools\x00\x00")) ||
				!bytes.Contains(args, []byte("--strict-mcp-config\x00")) || bytes.Contains(args, []byte("--allowedTools")) {
				t.Fatalf("draft session has unexpected tool permissions: %q", args)
			}
			diff := readWorkflowFile(t, filepath.Join(f.bin, "draft-input"))
			if !bytes.Contains(diff, []byte("+intended\n")) || bytes.Contains(diff, []byte("later worktree edit")) ||
				bytes.Contains(diff, []byte("unrelated worktree edits")) {
				t.Fatalf("draft did not receive the tested diff:\n%s", diff)
			}
		})
	}
}

func TestProxyCommitWorkflowRequiresTerminal(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	f.seedSnapshot()
	f.write("page with spaces.txt", "intended\n")
	f.git("add", "--", "page with spaces.txt")
	f.env = append(f.env, "FIXTURE_EXPECTED=intended")
	f.run("", 0, "commit-prepare-local")
	for _, workflow := range []string{"commit-do", "commit-do-local"} {
		if out := f.run("YES\n", 2, workflow); !strings.Contains(out, "requires an interactive terminal") {
			t.Fatal(out)
		}
	}
	f.write(".git/hooks/prepare-commit-msg", "#!/bin/sh\nexit 0\n")
	if out := f.run("", 2, "commit-do"); !strings.Contains(out, "active prepare-commit-msg hook") {
		t.Fatal(out)
	}
	f.write("unrelated.txt", "staged after preparation\n")
	f.git("add", "--", "unrelated.txt")
	if out := f.run("", 2, "commit-do"); !strings.Contains(out, "staged tree differs") {
		t.Fatal(out)
	}
	requireWorkflowFileAbsent(t, filepath.Join(f.bin, "claude-args"))
}

func TestProxyAuditOwnsReportDestination(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	const legacyReport = "security-audit-2026-11-05_20-08-27.md"
	const legacyLog = "govulncheck-2026-11-05_20-08-27.log"
	protected := map[string]string{
		"TODO.md":                           "[Previous audit](" + legacyReport + ")\n",
		legacyReport:                        "[Go scan](" + legacyLog + ")\n",
		legacyLog:                           "previous Go scan\n",
		"npm-audit-2026-11-05_20-08-27.log": "previous npm scan\n",
		"sections-2026-11-07/audit.md":      "section notes\n",
		"archive-older/history.md":          "archived audit\n",
		"run-ongoing/report.incomplete":     "partial report\n",
		"run-finished/report.md":            "[Go scan](govulncheck.log)\n",
		"run-finished/govulncheck.log":      "completed scan\n",
	}
	for name, contents := range protected {
		f.write(".audit/"+name, contents)
	}
	link := filepath.Join(f.dir, ".audit/security-audit-2026-11-08_01-00-00.md")
	if err := os.Symlink("TODO.md", link); err != nil {
		t.Fatal(err)
	}
	f.run("", 0, "security-audit", "scope note")
	reports, err := filepath.Glob(filepath.Join(f.dir, ".audit/run-????-*/report.md"))
	if err != nil || len(reports) != 1 || string(readWorkflowFile(t, reports[0])) != "# Fixture audit report\n" {
		t.Fatalf("new report: %v, %v", reports, err)
	}
	data := readWorkflowFile(t, filepath.Join(f.bin, "claude-args"))
	args := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	start, end := slices.Index(args, "--allowedTools"), slices.Index(args, "-p")
	if end < 0 || end+1 >= len(args) || !slices.Contains(args, "--disable-slash-commands") ||
		!strings.HasPrefix(args[end+1], "Perform the oCMS security audit.") || !strings.HasSuffix(args[end+1], "scope note") {
		t.Fatalf("expected a direct audit prompt with the caller's scope, got %q", args)
	}
	toolFlag := slices.Index(args, "--tools")
	if start < 0 || end <= start || toolFlag < 0 || toolFlag+1 >= len(args) || args[toolFlag+1] != "Bash,Read,Glob,Grep" {
		t.Fatalf("unexpected audit tools: %q", args)
	}
	allowed := args[start+1 : end]
	for _, rule := range allowed {
		if strings.HasPrefix(rule, "Write") || strings.HasPrefix(rule, "Edit") ||
			strings.HasPrefix(rule, "Bash(mkdir") || strings.HasPrefix(rule, "Bash(mv") {
			t.Errorf("audit must not grant filesystem writes: %q", rule)
		}
	}
	// Every shell command in the effective audit prompt is allowed exactly.
	// Other checks are explicitly manual, rather than silently skipped.
	promptIndex := slices.Index(args, "--append-system-prompt")
	if promptIndex < 0 || promptIndex+1 >= len(args) {
		t.Fatalf("missing proxy audit instructions: %q", args)
	}
	prompt := args[promptIndex+1]
	_, commands, found := strings.Cut(prompt, "```bash\n")
	body, _, closed := strings.Cut(commands, "\n```")
	if !found || !closed || !strings.Contains(prompt, "are manual") {
		t.Fatal("audit prompt must list executable checks and the manual fallback")
	}
	for _, command := range strings.Split(body, "\n") {
		if !slices.Contains(allowed, "Bash("+command+")") {
			t.Errorf("audit prompt requests a shell command without permission: %q", command)
		}
	}

	f.env = append(f.env, "FIXTURE_CLAUDE_EXIT=9")
	f.run("", 9, "security-audit")
	ongoing := filepath.Join(f.dir, ".audit/run-ongoing/report.incomplete")
	if pending, pendingErr := filepath.Glob(filepath.Join(f.dir, ".audit/run-*/report.incomplete")); pendingErr != nil || !slices.Equal(pending, []string{ongoing}) {
		t.Fatalf("failed audit left partial report: %v, %v", pending, pendingErr)
	}
	if current, currentErr := filepath.Glob(filepath.Join(f.dir, ".audit/run-????-*/report.md")); currentErr != nil || !slices.Equal(current, reports) {
		t.Fatalf("failed audit replaced a completed report: %v, %v", current, currentErr)
	}
	for name, want := range protected {
		if got := string(readWorkflowFile(t, filepath.Join(f.dir, ".audit", name))); got != want {
			t.Errorf("audit changed %s: %q", name, got)
		}
	}
	if target, err := os.Readlink(link); err != nil || target != "TODO.md" {
		t.Errorf("report-shaped symlink: target %q, error %v", target, err)
	}
}

// TestFixtureScriptReplacementKeepsRunningCopy pins the rename in script:
// replacing a fake command must not change what an already-open copy reads,
// as an interrupted scan may still be running when the next one is staged.
func TestFixtureScriptReplacementKeepsRunningCopy(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	const first = "#!/bin/sh\nprintf 'partial scan\\n'\n"
	f.script("govulncheck", first)
	running, err := os.Open(filepath.Join(f.bin, "govulncheck"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = running.Close() }()
	f.script("govulncheck", "#!/bin/sh\nprintf 'replacement scan output\\n'\n")
	got, err := io.ReadAll(running)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != first {
		t.Errorf("open copy read %q after replacement, want %q", got, first)
	}
	if replaced := string(readWorkflowFile(t, filepath.Join(f.bin, "govulncheck"))); !strings.Contains(replaced, "replacement scan output") {
		t.Errorf("fake command was not replaced: %q", replaced)
	}
}

func TestLocalAuditPreservesInProgressWork(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	const previous = "security-audit-2026-11-05_20-08-27.md"
	const notes = "active audit notes\n[Previous audit](" + previous + ")\n"
	f.write(".audit/TODO.md", notes)
	f.write(".audit/"+previous, "previous report\n")
	f.write("package.json", "{}\n")
	f.write("go.mod", "module example.invalid/auditfixture\n\ngo 1.27\n")
	f.write("fixture.go", "package auditfixture\n")
	f.write("output/old/broken.go", "package old\nfunc =\n")
	f.script("govulncheck", `#!/bin/sh
printf 'partial scan\n'
kill -TERM "$PPID"
`)
	f.run("", -1, "security-audit-local")
	interrupted, err := filepath.Glob(filepath.Join(f.dir, ".audit/run-*/govulncheck.log"))
	if err != nil || len(interrupted) != 1 || string(readWorkflowFile(t, interrupted[0])) != "partial scan\n" {
		t.Fatalf("interrupted scan: %v, %v", interrupted, err)
	}
	requireWorkflowFileAbsent(t, filepath.Join(filepath.Dir(interrupted[0]), "report.md"))

	f.script("govulncheck", `#!/bin/sh
[ "$*" = . ] || exit 2
printf 'Go scan output\n'
`)
	f.script("npm", `#!/bin/sh
[ "$*" = 'audit --json' ] || exit 2
printf 'npm scan output\n'
exit 1
`)
	f.run("", 1, "security-audit-local")
	if got := string(readWorkflowFile(t, filepath.Join(f.dir, ".audit/TODO.md"))); got != notes {
		t.Errorf("audit changed the active notes: %q", got)
	}
	if got := string(readWorkflowFile(t, filepath.Join(f.dir, ".audit", previous))); got != "previous report\n" {
		t.Errorf("audit changed the previous report: %q", got)
	}
	if got := string(readWorkflowFile(t, interrupted[0])); got != "partial scan\n" {
		t.Errorf("audit changed the interrupted scan: %q", got)
	}
	if archives, err := filepath.Glob(filepath.Join(f.dir, ".audit/archive-*")); err != nil || len(archives) != 0 {
		t.Errorf("audit moved earlier evidence: %v, %v", archives, err)
	}
	reports, err := filepath.Glob(filepath.Join(f.dir, ".audit/run-*/report.md"))
	if err != nil || len(reports) != 1 {
		t.Fatalf("local report: %v, %v", reports, err)
	}
	report := string(readWorkflowFile(t, reports[0]))
	if !strings.Contains(report, "govulncheck: ok") || !strings.Contains(report, "npm audit: issues found") {
		t.Errorf("unexpected scanner summary:\n%s", report)
	}
	for name, want := range map[string]string{"govulncheck.log": "Go scan output\n", "npm-audit.log": "npm scan output\n"} {
		if !strings.Contains(report, "["+name+"]("+name+")") {
			t.Errorf("report has no local link to %s", name)
		}
		if got := string(readWorkflowFile(t, filepath.Join(filepath.Dir(reports[0]), name))); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestAuditPreparationCreatesIndependentRuns(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	const legacy = ".audit/.archive.lock/keep"
	f.write(legacy, "legacy lock contents\n")
	f.script("date", "#!/bin/sh\nprintf '2026-11-08_12-00-00\\n'\n")
	first := f.command("bash", "scripts/codex/security-audit.sh", "--prepare")
	var firstOutput bytes.Buffer
	first.Stdout, first.Stderr = &firstOutput, &firstOutput
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	secondOutput, secondErr := f.command("bash", "scripts/codex/security-audit.sh", "--prepare").CombinedOutput()
	firstErr := first.Wait()
	if firstErr != nil || secondErr != nil {
		t.Fatalf("preparation failed: %v (%s), %v (%s)", firstErr, firstOutput.String(), secondErr, secondOutput)
	}
	firstPath, secondPath := strings.TrimSpace(firstOutput.String()), strings.TrimSpace(string(secondOutput))
	if firstPath == secondPath {
		t.Fatalf("two preparations shared %s", firstPath)
	}
	for _, path := range []string{firstPath, secondPath} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 ||
			!strings.HasPrefix(filepath.Base(path), "run-2026-11-08_12-00-00.") {
			t.Fatalf("invalid private run directory %q: %v, %v", path, info, err)
		}
	}
	if got := string(readWorkflowFile(t, filepath.Join(f.dir, legacy))); got != "legacy lock contents\n" {
		t.Errorf("preparation changed legacy lock data: %q", got)
	}
}

func TestProjectGoPackagesExcludeOnlyTopLevelOutput(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	cache, err := exec.Command("go", "env", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	f.env = append(f.env, "GOCACHE="+strings.TrimSpace(string(cache)))
	f.write("go.mod", "module example.invalid/pkgfixture\n\ngo 1.27\n")
	f.write("pkg/owned.go", "package pkg\n")
	f.write("pkg/owned_test.go", "package pkg\nimport \"testing\"\nfunc TestOwned(t *testing.T) {}\n")
	f.write("nested/output/owned.go", "package output\n")
	f.write("output/generated/broken.go", "package generated\nfunc =\n")
	f.write("output/generated/broken_test.go", "package generated\nimport \"testing\"\nfunc TestGenerated(t *testing.T) {}\n")
	output, err := f.command("bash", "scripts/with-go-packages.sh", "go", "test", "-count=1").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "example.invalid/pkgfixture/pkg") ||
		!strings.Contains(string(output), "example.invalid/pkgfixture/nested/output") || strings.Contains(string(output), "output/generated") {
		t.Fatalf("project test selected the wrong packages: %v\n%s", err, output)
	}
	f.write("pkg/owned.go", "package pkg\nvar _ = missingName\n")
	output, err = f.command("bash", "scripts/with-go-packages.sh", "go", "test", "-count=1").CombinedOutput()
	if err == nil || !strings.Contains(string(output), "missingName") {
		t.Fatalf("project test hid an owned package error: %v\n%s", err, output)
	}
}

func TestGoModuleExcludesTopLevelOutput(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	manifest, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	f.write("go.mod", string(manifest))
	f.write("nested/output/owned_test.go", "package output\nimport \"testing\"\nfunc TestOwned(t *testing.T) {}\n")
	f.write("output/generated/broken.go", "package generated\nfunc =\n")

	const owned = "github.com/olegiv/ocms-go/nested/output"
	for _, args := range [][]string{{"list", "./..."}, {"test", "-count=1", "./..."}} {
		cmd := exec.CommandContext(t.Context(), "go", args...)
		cmd.Dir = f.dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOPROXY=off")
		output, runErr := cmd.CombinedOutput()
		if runErr != nil || !strings.Contains(string(output), owned) || strings.Contains(string(output), "/output/generated") {
			t.Fatalf("go %v selected the wrong packages: %v\n%s", args, runErr, output)
		}
	}
}

func TestProxyAuditRefusesOutsideDestination(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(f.dir, ".audit")); err != nil {
		t.Fatal(err)
	}
	if out := f.run("", 2, "security-audit"); !strings.Contains(out, "real, gitignored .audit directory") {
		t.Fatal(out)
	}
	requireWorkflowFileAbsent(t, filepath.Join(f.bin, "claude-args"))
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("audit wrote outside its repository: %v, %v", entries, err)
	}
}
