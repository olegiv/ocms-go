// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDockerEntrypointTMPDIR runs docker-entrypoint.sh with a TMPDIR it can
// create and one it cannot. The first must exist for the command it starts;
// the second must be reported on stderr and dropped, so the app falls back to
// /tmp instead of failing its first upload. The root branch runs with stub
// id and su-exec commands, since the test does not run as root.
func TestDockerEntrypointTMPDIR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the entrypoint is a POSIX shell script")
	}
	if os.Geteuid() == 0 {
		t.Skip("an unwritable directory is writable for root")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "docker-entrypoint.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	readOnly := filepath.Join(dir, "read-only")
	if err := os.Mkdir(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	stubs := filepath.Join(dir, "stubs")
	if err := os.Mkdir(stubs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"id":      "#!/bin/sh\necho 0\n",
		"su-exec": "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$SU_EXEC_LOG\"\nshift\nexec \"$@\"\n",
	} {
		if err := os.WriteFile(filepath.Join(stubs, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	suExecLog := filepath.Join(dir, "su-exec.log")
	run := func(tmpdir string, asRoot bool) (stdout, stderr string) {
		t.Helper()
		cmd := exec.Command("sh", script, "sh", "-c", `printf 'TMPDIR=%s' "${TMPDIR-unset}"`)
		path := os.Getenv("PATH")
		if asRoot {
			path = stubs + string(os.PathListSeparator) + path
		}
		cmd.Env = []string{"PATH=" + path, "TMPDIR=" + tmpdir, "SU_EXEC_LOG=" + suExecLog}
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			t.Fatalf("entrypoint failed: %v\nstdout: %s\nstderr: %s", err, out.String(), errOut.String())
		}
		return out.String(), errOut.String()
	}

	for _, asRoot := range []bool{false, true} {
		created := filepath.Join(dir, "data", "tmp")
		if asRoot {
			created = filepath.Join(dir, "root-data", "tmp")
		}
		stdout, stderr := run(created, asRoot)
		if info, err := os.Stat(created); err != nil || !info.IsDir() {
			t.Errorf("asRoot=%v: TMPDIR %s was not created (%v)", asRoot, created, err)
		}
		if !strings.Contains(stdout, "TMPDIR="+created) || stderr != "" {
			t.Errorf("asRoot=%v, creatable TMPDIR: stdout %q, stderr %q", asRoot, stdout, stderr)
		}
		if asRoot {
			// Root has no DAC_OVERRIDE in the compose setup, so the
			// directory must be created as the app user.
			logged, err := os.ReadFile(suExecLog)
			if err != nil || !strings.Contains(string(logged), "ocms mkdir -p "+created) {
				t.Errorf("TMPDIR was not created through su-exec ocms; su-exec calls: %q (%v)", logged, err)
			}
		}

		stdout, stderr = run(filepath.Join(readOnly, "tmp"), asRoot)
		if !strings.Contains(stdout, "TMPDIR=unset") {
			t.Errorf("asRoot=%v: an uncreatable TMPDIR was passed on: %q", asRoot, stdout)
		}
		if !strings.Contains(stderr, "warning: could not create TMPDIR") || !strings.Contains(stderr, "using /tmp instead") {
			t.Errorf("asRoot=%v: stderr %q, want the TMPDIR warning", asRoot, stderr)
		}
	}
}
