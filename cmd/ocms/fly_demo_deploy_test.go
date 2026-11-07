// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestFlyDemoDeployUsesCommittedContext runs the actual deploy script against
// a disposable repository. Docker and Fly are stubs that record the source,
// arguments and config they would receive. The original script passed ignored
// modules and a dirty config to the build and deployment.
func TestFlyDemoDeployUsesCommittedContext(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	checkout := filepath.Join(t.TempDir(), "checkout with spaces")
	write := func(name, contents string) {
		t.Helper()
		path := filepath.Join(checkout, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const script = ".fly/scripts/deploy.sh"
	for _, name := range []string{script, ".gitignore", ".dockerignore"} {
		write(name, string(readWorkflowFile(t, filepath.Join("..", "..", name))))
	}
	write("fly.toml", "app = 'ocms-demo'\n")
	write("custom/modules/doc.go", "package modules\n")
	write("custom/modules/imports_bookmarks.go", "package modules\n")
	write("custom/modules/bookmarks/register.go", "package bookmarks\n")
	gitEnv := []string{
		"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
	}
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(git, args...) // #nosec G204 -- fixed Git operations in a disposable checkout
		cmd.Dir, cmd.Env = checkout, gitEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q", "--template=")
	runGit("add", "--all")
	commit := runGit("commit-tree", runGit("write-tree"), "-m", "Demo fixture")
	write(".git/HEAD", commit+"\n")
	short := runGit("rev-parse", "--short", commit)
	write("custom/modules/weather/register.go", "package weather\n")
	write("custom/modules/imports_weather.go", "package modules\n")
	write("custom/modules/bookmarks/register.go", "package local_edit\n")
	write("fly.toml", "app = 'worktree-app'\n")
	runGit("add", "--", "fly.toml")
	runGit("check-ignore", "custom/modules/weather/register.go", "custom/modules/imports_weather.go")

	for _, tc := range []struct {
		name, failure string
		args          []string
		want          int
	}{
		{name: "default"},
		{name: "reset and logs", args: []string{"--reset", "--logs"}},
		{name: "build failure", failure: "build", want: 23},
		{name: "archive failure", failure: "archive", want: 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := t.TempDir()
			stubs := filepath.Join(fixture, "bin")
			tmp := filepath.Join(fixture, "tmp with spaces")
			for _, dir := range []string{stubs, tmp} {
				if err := os.Mkdir(dir, 0o750); err != nil {
					t.Fatal(err)
				}
			}
			writeStubs(t, stubs, map[string]string{
				"docker": `#!/bin/sh
set -e
printf '%s\n' "$@" > "$FIXTURE/build-args"
for context do :; done
cd "$context"
find custom/modules -type f | LC_ALL=C sort > "$FIXTURE/module-files"
cp custom/modules/bookmarks/register.go "$FIXTURE/bookmarks"
[ "$FAILURE" != build ] || exit 23
`,
				"fly": `#!/bin/sh
set -e
printf '%s\n' "$*" >> "$FIXTURE/fly-calls"
case "$1 $2" in
  'deploy --local-only') cp fly.toml "$FIXTURE/deploy-config" ;;
  'machines list') printf 'fixture-machine\n' ;;
esac
`,
				"git": `#!/bin/sh
set -e
"$REAL_GIT" "$@"
# Return a failure even after writing a valid archive. Extraction succeeds
# and the script must still stop before it starts Docker.
if [ "$FAILURE" = archive ] && [ "${3:-}" = archive ]; then exit 17; fi
`,
				"curl":  "#!/bin/sh\nexit 0\n",
				"sleep": "#!/bin/sh\nexit 0\n",
			})
			cmd := exec.Command(bash, append([]string{filepath.Join(checkout, script)}, tc.args...)...) // #nosec G204 -- repository script in a disposable checkout
			cmd.Dir = checkout
			if slices.Contains(tc.args, "--reset") {
				cmd.Dir = fixture // resolution must work outside the checkout too
			}
			cmd.Env = append(slices.Clone(gitEnv), "PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
				"HOME="+fixture, "TMPDIR="+tmp, "FIXTURE="+fixture, "REAL_GIT="+git, "FAILURE="+tc.failure)
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatal(err)
				}
				code = exitErr.ExitCode()
			}
			if code != tc.want {
				t.Fatalf("deploy exit = %d, want %d\n%s", code, tc.want, out)
			}
			if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
				t.Errorf("temporary build context was left behind: %v, %v", entries, err)
			}
			if tc.failure != "" {
				requireWorkflowFileAbsent(t, filepath.Join(fixture, "fly-calls"))
			}
			if tc.failure == "archive" {
				requireWorkflowFileAbsent(t, filepath.Join(fixture, "build-args"))
				return
			}
			files := readWorkflowFile(t, filepath.Join(fixture, "module-files"))
			const wanted = "custom/modules/bookmarks/register.go\ncustom/modules/doc.go\ncustom/modules/imports_bookmarks.go\n"
			if string(files) != wanted {
				t.Errorf("Docker saw the wrong module files:\n%s", files)
			}
			if got := readWorkflowFile(t, filepath.Join(fixture, "bookmarks")); string(got) != "package bookmarks\n" {
				t.Errorf("Docker saw modified Bookmarks source: %q", got)
			}
			args := strings.Split(strings.TrimSpace(string(readWorkflowFile(t, filepath.Join(fixture, "build-args")))), "\n")
			for _, want := range []string{"VERSION=" + short, "GIT_COMMIT=" + short, "linux/amd64", "ocms-test"} {
				if !slices.Contains(args, want) {
					t.Errorf("Docker did not get %q in %q", want, args)
				}
			}
			reset := slices.Contains(tc.args, "--reset")
			if slices.Contains(args, "--no-cache") != reset {
				t.Errorf("Docker cache arguments differ from the reset contract: %q", args)
			}
			if tc.failure == "build" {
				return
			}
			if got := readWorkflowFile(t, filepath.Join(fixture, "deploy-config")); string(got) != "app = 'ocms-demo'\n" {
				t.Errorf("Fly saw modified config: %q", got)
			}
			calls := "deploy --local-only --image ocms-test\n"
			if reset {
				calls += "machines list -a ocms-demo -q\nssh console -C /app/scripts/reset-demo.sh -a ocms-demo\nmachines restart fixture-machine -a ocms-demo\nlogs -a ocms-demo --no-tail\n"
			}
			if got := readWorkflowFile(t, filepath.Join(fixture, "fly-calls")); string(got) != calls {
				t.Errorf("Fly commands = %q, want %q", got, calls)
			}
		})
	}
}
