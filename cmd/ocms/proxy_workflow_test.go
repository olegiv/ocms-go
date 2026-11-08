// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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

func (f proxyWorkflowFixture) script(name, contents string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.bin, name), []byte(contents), 0o755); err != nil {
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
	legacy := []string{
		"security-audit-2026-11-05_20-08-27.md",
		"govulncheck-2026-11-05_20-08-27.log",
		"npm-audit-2026-11-05_20-08-27.log",
	}
	for _, name := range legacy {
		f.write(".audit/"+name, "previous audit artifact\n")
	}
	protected := []string{
		"TODO.md", "sections-2026-11-07/audit.md", "codex-2026-11-07/HANDOFF.md",
		"govulncheck-2026-11-07_18-53-57.log", "npm-audit-2026-11-07_18-53-57.log",
		"security-audit-in-progress.md", "security-audit-2026-11-08_00-00-00.md/draft.md",
		"2026-11-05_20-00-00/history.md", "archive-older/history.md", "run-ongoing/report.incomplete",
	}
	for _, name := range protected {
		f.write(".audit/"+name, "preserved audit artifact\n")
	}
	link := filepath.Join(f.dir, ".audit/security-audit-2026-11-08_01-00-00.md")
	if err := os.Symlink("TODO.md", link); err != nil {
		t.Fatal(err)
	}
	f.run("", 0, "security-audit", "scope note")
	reports, err := filepath.Glob(filepath.Join(f.dir, ".audit/run-*/report.md"))
	if err != nil || len(reports) != 1 || string(readWorkflowFile(t, reports[0])) != "# Fixture audit report\n" {
		t.Fatalf("new report: %v, %v", reports, err)
	}
	for _, name := range protected {
		if got := string(readWorkflowFile(t, filepath.Join(f.dir, ".audit", name))); got != "preserved audit artifact\n" {
			t.Errorf("audit changed %s: %q", name, got)
		}
	}
	if target, err := os.Readlink(link); err != nil || target != "TODO.md" {
		t.Errorf("report-shaped symlink: target %q, error %v", target, err)
	}
	for _, name := range legacy {
		archived, err := filepath.Glob(filepath.Join(f.dir, ".audit/archive-*", name))
		if err != nil || len(archived) != 1 || string(readWorkflowFile(t, archived[0])) != "previous audit artifact\n" {
			t.Fatalf("archived %s: %v, %v", name, archived, err)
		}
		requireWorkflowFileAbsent(t, filepath.Join(f.dir, ".audit", name))
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
	if current, currentErr := filepath.Glob(filepath.Join(f.dir, ".audit/run-*/report.md")); currentErr != nil || !slices.Equal(current, reports) {
		t.Fatalf("failed audit replaced a completed report: %v, %v", current, currentErr)
	}
}

func TestLocalAuditPreservesInProgressWork(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	f.write(".audit/TODO.md", "active audit notes\n")
	f.write("package.json", "{}\n")
	f.write("go.mod", "module example.invalid/auditfixture\n\ngo 1.27\n")
	f.write("fixture.go", "package auditfixture\n")
	f.write("output/old/broken.go", "package old\nfunc =\n")
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
	if notes := string(readWorkflowFile(t, filepath.Join(f.dir, ".audit/TODO.md"))); notes != "active audit notes\n" {
		t.Errorf("audit changed the active notes: %q", notes)
	}
	if archives, err := filepath.Glob(filepath.Join(f.dir, ".audit/archive-*")); err != nil || len(archives) != 0 {
		t.Errorf("audit without a legacy report made an archive: %v, %v", archives, err)
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
		if got := string(readWorkflowFile(t, filepath.Join(filepath.Dir(reports[0]), name))); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestAuditPreparationHandlesLegacyLock(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	lock := filepath.Join(f.dir, ".audit", ".archive.lock")
	if err := os.MkdirAll(lock, 0o750); err != nil {
		t.Fatal(err)
	}
	if out := f.run("", 2, "security-audit-local"); !strings.Contains(out, "no audit is active") || !strings.Contains(out, "rmdir -- .audit/.archive.lock") {
		t.Fatalf("audit lock diagnostic lacks recovery instructions: %s", out)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatalf("contending launcher changed the lock: %v", err)
	}
	cmd := f.command("bash", "scripts/codex/security-audit.sh", "--prepare")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), ".audit/run-") {
		t.Fatalf("audit did not recover after lock removal: %v\n%s", err, out)
	}
}

func TestAuditArchiveLockReleasesAfterOwnerDies(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	f.write(".audit/TODO.md", "active notes\n")
	lock := filepath.Join(f.dir, ".audit", ".archive.lock")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	holder := exec.CommandContext(ctx, "bash", "-c", `set -e
exec 9>>"$1"
if command -v lockf >/dev/null 2>&1; then lockf -s -t 0 9; else flock -n -E 75 9; fi
printf 'locked\n'
read -r release
`, "holder", lock)
	holder.Dir, holder.Env = f.dir, f.env
	in, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if holder.ProcessState == nil {
			_ = holder.Process.Kill()
			_ = holder.Wait()
		}
	})
	if line, readErr := bufio.NewReader(out).ReadString('\n'); readErr != nil || line != "locked\n" {
		t.Fatalf("lock holder did not start: %q, %v", line, readErr)
	}
	prepare := func() ([]byte, error) {
		return f.command("bash", "scripts/codex/security-audit.sh", "--prepare").CombinedOutput()
	}
	var exitErr *exec.ExitError
	if output, prepareErr := prepare(); !errors.As(prepareErr, &exitErr) || exitErr.ExitCode() != 2 || !strings.Contains(string(output), "Another audit is preparing") {
		t.Fatalf("preparation entered a locked archive: %v\n%s", prepareErr, output)
	}
	if err = holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = holder.Wait(); err == nil {
		t.Fatal("lock owner unexpectedly exited normally")
	}
	if info, statErr := os.Stat(lock); statErr != nil || !info.Mode().IsRegular() {
		t.Fatalf("the lock file did not survive the killed owner: %v", statErr)
	}
	if output, prepareErr := prepare(); prepareErr != nil || !strings.Contains(string(output), ".audit/run-") {
		t.Fatalf("preparation did not recover from the killed owner: %v\n%s", prepareErr, output)
	}
}

func TestAuditPreparationRefusesSymlinkedLock(t *testing.T) {
	f := newProxyWorkflowFixture(t)
	f.write(".audit/TODO.md", "active notes\n")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Symlink(outside, filepath.Join(f.dir, ".audit", ".archive.lock")); err != nil {
		t.Fatal(err)
	}
	cmd := f.command("bash", "scripts/codex/security-audit.sh", "--prepare")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "Use a regular file") {
		t.Fatalf("audit accepted a symlinked lock: %v\n%s", err, out)
	}
	requireWorkflowFileAbsent(t, outside)
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
