// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflowFile is the part of a GitHub Actions workflow these tests read.
type workflowFile struct {
	Jobs map[string]struct {
		// Permissions is a scope map or a shorthand such as "read-all".
		Permissions any `yaml:"permissions"`
		Steps       []struct {
			Name            string            `yaml:"name"`
			Uses            string            `yaml:"uses"`
			Run             string            `yaml:"run"`
			With            map[string]string `yaml:"with"`
			Env             map[string]string `yaml:"env"`
			If              any               `yaml:"if"`
			ContinueOnError any               `yaml:"continue-on-error"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func readWorkflow(t *testing.T, name string) workflowFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name)) // #nosec G304 -- fixed repository path
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	var wf workflowFile
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	return wf
}

// TestReleaseVersionPolicy checks that preparation and CI use the same
// stable version check, including the command's optional leading v.
func TestReleaseVersionPolicy(t *testing.T) {
	const prepare = `scripts/release-version.sh "v${input#v}"`
	const validate = `version_no_v=$(scripts/release-version.sh "${GITHUB_REF_NAME}" 2>&1)`
	root := filepath.Join("..", "..")
	command := string(readWorkflowFile(t, filepath.Join(root, ".claude", "commands", "release-gh-prepare.md")))
	preflight, _, found := strings.Cut(command, "## Hard preconditions")
	if !found || !strings.Contains(preflight, "\n```bash\n"+prepare+"\n```\n") {
		t.Error("/release-gh-prepare must run the shared validator before the release preconditions")
	}
	wf := readWorkflow(t, "release.yml")
	validated := false
	for _, step := range wf.Jobs["validate"].Steps {
		if step.Name == "Validate release tag" {
			gate := strings.Index(step.Run, "\nif ! "+validate+"; then\n")
			changelog := strings.Index(step.Run, "CHANGELOG.md")
			validated = gate >= 0 && gate < changelog
		}
	}
	if !validated {
		t.Error("release.yml must validate the tag and use the normalized version before checking CHANGELOG.md")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	for _, tc := range []struct {
		input, prepared, tag string
	}{
		{"0.21.0", "0.21.0", ""},
		{"v10.21.30", "10.21.30", "10.21.30"},
		{"v0.21.0-rc.1", "", ""},
		{"0.21.0-rc.1", "", ""},
		{"v0.21.0+build.1", "", ""},
		{"v0.21", "", ""},
		{"vv0.21.0", "", ""},
		{" v0.21.0", "", ""},
		{"--delete", "", ""},
		{"", "", ""},
	} {
		t.Run("input="+tc.input, func(t *testing.T) {
			check := func(source, snippet, want string) {
				t.Helper()
				cmd := exec.CommandContext(t.Context(), bash, "-ec", snippet, "release-version", tc.input) // #nosec G204 -- fixed snippets and fixture input
				cmd.Dir = root
				got, runErr := cmd.Output()
				if want == "" {
					if runErr == nil || len(got) != 0 {
						t.Errorf("%s accepted invalid input %q: stdout %q, error %v", source, tc.input, got, runErr)
					}
				} else if runErr != nil || string(got) != want+"\n" {
					t.Errorf("%s input %q: stdout %q, error %v, want %q", source, tc.input, got, runErr, want+"\n")
				}
			}
			check("prepare", "input=$1\n"+prepare, tc.prepared)
			check("tag validation", "GITHUB_REF_NAME=$1\n"+validate+"\nprintf '%s\\n' \"$version_no_v\"", tc.tag)
		})
	}
}

// TestReleaseArchivesAreAttested fails when the release workflow stops
// signing build provenance for the archives it publishes, or loses the
// permissions that signing needs.
//
// Release archives used to ship with checksums.txt only, which proves
// integrity against the release page but not where the archive was built
// (security audit 2026-10-17, I-9). The attestation lets a user check an
// archive with `gh attestation verify <archive> --repo olegiv/ocms-go`.
//
// Bug state: delete the "Attest release archives" step and this fails.
func TestReleaseArchivesAreAttested(t *testing.T) {
	wf := readWorkflow(t, "release.yml")
	build, ok := wf.Jobs["build"]
	if !ok {
		t.Fatal("release.yml has no build job")
	}
	perms, _ := build.Permissions.(map[string]any)
	for _, perm := range []string{"id-token", "attestations"} {
		if perms[perm] != "write" {
			t.Errorf("build job permission %s = %v, want write", perm, perms[perm])
		}
	}

	attested := ""
	builtBeforeAttest := false
	for _, step := range build.Steps {
		if strings.HasPrefix(step.Uses, "actions/attest-build-provenance@") {
			attested = step.With["subject-path"]
			break
		}
		if strings.Contains(step.Run, `tar -C "${workdir}" -czf "release/${archive}.tar.gz"`) {
			builtBeforeAttest = true
		}
	}
	if attested == "" {
		t.Fatal("build job has no actions/attest-build-provenance step with a subject-path")
	}
	if !builtBeforeAttest {
		t.Error("the attestation runs before the release archives are built")
	}

	// The archives attested must be the archives published.
	published := false
	for _, step := range wf.Jobs["publish"].Steps {
		if strings.Contains(step.Run, attested) {
			published = true
		}
	}
	if !published {
		t.Errorf("publish job does not upload the attested archives %q", attested)
	}
}

// TestReleaseNotesAreUnwrapped fails when the release workflow publishes
// CHANGELOG.md's wrapped lines as they are, or when scripts/release-notes.sh
// stops joining them. GitHub renders every newline of a release description
// as a line break, so the v0.30.0 notes showed 83 sentences broken mid-line.
// The script must join each paragraph and list item onto one line, keep
// headings, nested list items, table rows and fenced code as they are, stop
// at the next version, and change nothing but line breaks in the newest
// CHANGELOG.md section.
//
// Bug state: put the plain awk extraction back into the "Prepare release
// notes" step, or let the script print continuation lines as they are, and
// this fails.
func TestReleaseNotesAreUnwrapped(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	wf := readWorkflow(t, "release.yml")
	prepared := false
	for _, step := range wf.Jobs["build"].Steps {
		if step.Name == "Prepare release notes" {
			prepared = strings.Contains(step.Run, `scripts/release-notes.sh "$version_no_v"`) && strings.Contains(step.Run, "> release/notes.md")
		}
	}
	if !prepared {
		t.Error(`the build job's "Prepare release notes" step does not write release/notes.md with scripts/release-notes.sh`)
	}

	notes := func(t *testing.T, version, changelog string) string {
		t.Helper()
		out, err := exec.Command(bash, filepath.Join(root, "scripts", "release-notes.sh"), version, changelog).CombinedOutput() // #nosec G204 -- fixed repository script, test arguments
		if err != nil {
			t.Fatalf("release-notes.sh %s: %v\n%s", version, err, out)
		}
		return string(out)
	}

	t.Run("fixture", func(t *testing.T) {
		changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
		fixture := "## [Unreleased]\n\n## [1.2.30] - 2026-01-02\n\n- not this one\n\n" +
			"## [1.2.3] - 2026-01-01\n\nA paragraph that is\nwrapped twice\nhere.\n\n### Fixed\n" +
			"- **An item** wrapped\n  over two lines.\n  - A nested item,\n    wrapped too.\n- A short item.\n\n" +
			"| a | b |\n|---|---|\n\n```sh\nkeep this\n  as it is\n```\n\n## [1.2.2] - 2025-12-31\n\n- older\n"
		if err := os.WriteFile(changelog, []byte(fixture), 0o600); err != nil {
			t.Fatal(err)
		}
		want := "\nA paragraph that is wrapped twice here.\n\n### Fixed\n" +
			"- **An item** wrapped over two lines.\n  - A nested item, wrapped too.\n- A short item.\n\n" +
			"| a | b |\n|---|---|\n\n```sh\nkeep this\n  as it is\n```\n\n"
		if got := notes(t, "1.2.3", changelog); got != want {
			t.Errorf("release notes for 1.2.3 =\n%q\nwant\n%q", got, want)
		}
	})

	t.Run("newest CHANGELOG section", func(t *testing.T) {
		changelog, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md")) // #nosec G304 -- fixed repository path
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`).FindSubmatch(changelog)
		if m == nil {
			t.Fatal("CHANGELOG.md has no released version")
		}
		version := string(m[1])
		got := notes(t, version, filepath.Join(root, "CHANGELOG.md"))
		section := regexp.MustCompile(`(?s)## \[` + regexp.QuoteMeta(version) + `\][^\n]*\n(.*?)(\n## \[|$)`).FindSubmatch(changelog)
		if section == nil {
			t.Fatalf("CHANGELOG.md has no %s section", version)
		}
		if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(string(section[1])), " ") {
			t.Errorf("the %s notes do not have the same words as the CHANGELOG.md section", version)
		}
		for i, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(line, "  ") && !regexp.MustCompile(`^\s+([-*+]|\d+\.)\s`).MatchString(line) {
				t.Errorf("line %d of the %s notes is a wrapped continuation: %q", i+1, version, line)
			}
		}
	})
}

// TestReleaseTitleComesFromChangelog fails when the release workflow titles
// a GitHub release with the bare tag name, or when scripts/release-title.sh
// stops reading the title from the version's CHANGELOG.md heading
// ("## [X.Y.Z] - YYYY-MM-DD - <title>") or accepts a heading without one.
// Releases up to v0.30.0 were titled "vX.Y.Z" alone.
//
// Bug state: put `--title "${GITHUB_REF_NAME}"` back into the publish step,
// or drop the title check from the validate job, and this fails.
func TestReleaseTitleComesFromChangelog(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	wf := readWorkflow(t, "release.yml")
	const titleCmd = `scripts/release-title.sh "$version_no_v" CHANGELOG.md`
	validated := false
	for _, step := range wf.Jobs["validate"].Steps {
		if strings.Contains(step.Run, "if ! "+titleCmd+"; then") {
			validated = true
		}
	}
	if !validated {
		t.Error("the validate job does not reject a CHANGELOG heading without a release title")
	}
	written := false
	for _, step := range wf.Jobs["build"].Steps {
		if step.Name == "Prepare release notes" && strings.Contains(step.Run, titleCmd+" > release/title.txt") {
			written = true
		}
	}
	if !written {
		t.Error(`the build job's "Prepare release notes" step does not write release/title.txt with scripts/release-title.sh`)
	}
	for _, step := range wf.Jobs["publish"].Steps {
		if !strings.Contains(step.Run, "gh release") {
			continue
		}
		if !strings.Contains(step.Run, `title="$(cat release/title.txt)"`) {
			t.Error("the publish step does not read release/title.txt")
		}
		if strings.Contains(step.Run, `--title "${GITHUB_REF_NAME}"`) {
			t.Error("the publish step titles the release with the bare tag name")
		}
		for _, cmd := range []string{"gh release create", "gh release edit"} {
			if !regexp.MustCompile(regexp.QuoteMeta(cmd) + `[^\n]*(\\\n[^\n]*)*--title "\$title"`).MatchString(step.Run) {
				t.Errorf(`%s does not pass --title "$title"`, cmd)
			}
		}
	}

	changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
	fixture := "## [Unreleased]\n\n## [1.2.30] - 2026-01-02 - Not this one\n\n- a\n\n" +
		"## [1.2.3] - 2026-01-01 - Editor formatting and search\n\n- b\n\n" +
		"## [1.2.2] - 2025-12-31\n\n- c\n"
	if err = os.WriteFile(changelog, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "release-title.sh")
	out, err := exec.Command(bash, script, "1.2.3", changelog).Output() // #nosec G204 -- fixed repository script, test arguments
	if err != nil {
		t.Fatalf("release-title.sh 1.2.3: %v", err)
	}
	if got, want := string(out), "v1.2.3 - Editor formatting and search\n"; got != want {
		t.Errorf("release title for 1.2.3 = %q, want %q", got, want)
	}
	for _, version := range []string{"1.2.2", "1.2.4", "1.2"} {
		if got, runErr := exec.Command(bash, script, version, changelog).Output(); runErr == nil { // #nosec G204 -- fixed repository script, test arguments
			t.Errorf("release-title.sh %s printed %q, want an error: the heading has no title", version, got)
		}
	}
}

// pinnedAction matches an action reference pinned to a full commit SHA.
var pinnedAction = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}$`)

// TestWorkflowActionsArePinned fails when a workflow step uses an action by
// tag or branch instead of a full commit SHA. A tag can be moved to other
// code after review; a SHA cannot.
//
// Bug state: change the attest step to actions/attest-build-provenance@v4
// and this names it.
func TestWorkflowActionsArePinned(t *testing.T) {
	var names []string
	for _, pattern := range []string{"*.yml", "*.yaml"} {
		matches, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", pattern))
		if err != nil {
			t.Fatalf("listing workflows: %v", err)
		}
		names = append(names, matches...)
	}
	if len(names) == 0 {
		t.Fatal("no workflows found")
	}
	checked := 0
	for _, path := range names {
		wf := readWorkflow(t, filepath.Base(path))
		for jobName, job := range wf.Jobs {
			for _, step := range job.Steps {
				if step.Uses == "" || strings.HasPrefix(step.Uses, "./") || strings.HasPrefix(step.Uses, "docker://") {
					continue
				}
				checked++
				if !pinnedAction.MatchString(step.Uses) {
					t.Errorf("%s job %s: %q is not pinned to a commit SHA", filepath.Base(path), jobName, step.Uses)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no action references found; the workflow layout changed and this test is vacuous")
	}
}

// TestCIChecksOutSharedTooling fails when the Go workflow stops checking out
// the .claude/shared submodule before it runs the tests, makes that step
// conditional or allowed to fail, or stops requiring the submodule in the
// Test step. Without it the drift tests that read the shared Claude tooling
// (the /code-quality rules, the hook targets) skip or check only links, so
// CI never ran them (review of the 2026-10-17 audit fixes). The requirement
// is what makes CI fail when the checkout did not happen, whatever form a
// disabled step takes.
//
// Bug state: delete the "Check out the shared Claude tooling" step, or the
// OCMS_REQUIRE_SHARED_TOOLING setting of the Test step, and this fails.
func TestCIChecksOutSharedTooling(t *testing.T) {
	wf := readWorkflow(t, "go.yml")
	build, ok := wf.Jobs["build"]
	if !ok {
		t.Fatal("go.yml has no build job")
	}
	checkoutCommand := regexp.MustCompile(`(?m)^\s*git submodule update --init\b.*\.claude/shared\s*$`)
	checkout, tests := -1, -1
	for i, step := range build.Steps {
		if checkout < 0 && checkoutCommand.MatchString(step.Run) {
			checkout = i
			if step.If != nil || step.ContinueOnError != nil {
				t.Errorf("go.yml step %q is conditional or allowed to fail", step.Name)
			}
		}
		if tests < 0 && strings.HasPrefix(strings.TrimSpace(step.Run), "go test ") {
			tests = i
			if step.Env["OCMS_REQUIRE_SHARED_TOOLING"] != "1" {
				t.Error("go.yml's Test step does not set OCMS_REQUIRE_SHARED_TOOLING=1, so a missing submodule only skips tests")
			}
		}
	}
	switch {
	case tests < 0:
		t.Fatal("go.yml runs no go test step; the workflow changed and this test is vacuous")
	case checkout < 0:
		t.Error("go.yml does not check out the .claude/shared submodule")
	case checkout > tests:
		t.Error("go.yml checks out the .claude/shared submodule only after it runs the tests")
	}
}

// TestCIRacesConcurrentCode fails when the Go workflow stops running the
// race detector over the packages with concurrent code: the server stop
// (cmd/ocms), the request timeout and its panic recovery
// (internal/middleware) and the Event Log writer (internal/logging), which
// run work in goroutines of their own, and the settings retry gate
// (internal/module), which concurrent requests call. CI ran it only over
// the module settings tests (review of the 2026-10-17 audit fixes).
//
// Bug state: drop ./internal/logging/ from the race step in go.yml and this
// names it.
func TestCIRacesConcurrentCode(t *testing.T) {
	wf := readWorkflow(t, "go.yml")
	build, ok := wf.Jobs["build"]
	if !ok {
		t.Fatal("go.yml has no build job")
	}
	raced := map[string]bool{}
	for _, step := range build.Steps {
		run := strings.TrimSpace(step.Run)
		if !strings.HasPrefix(run, "go test ") || !strings.Contains(run, " -race") || step.If != nil || step.ContinueOnError != nil {
			continue
		}
		if strings.Contains(run, " -run ") {
			continue // a filtered run does not cover a package's other tests
		}
		for _, field := range strings.Fields(run) {
			raced[field] = true
		}
	}
	for _, pkg := range []string{"./cmd/ocms/", "./internal/middleware/", "./internal/logging/", "./internal/module/"} {
		if !raced[pkg] {
			t.Errorf("go.yml runs no unfiltered go test -race step over %s", pkg)
		}
	}
}
