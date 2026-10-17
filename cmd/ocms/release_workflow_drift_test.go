// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
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
			Name string            `yaml:"name"`
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
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
