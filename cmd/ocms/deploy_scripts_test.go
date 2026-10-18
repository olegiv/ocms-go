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
)

// TestDeployBinaryCommands runs the commands scripts/deploy/helper.sh builds
// for the deploy scripts. install_binary_command must replace the
// destination's content, leave it executable and leave no copy behind: it
// copies beside the destination and renames over it, so a running binary is
// replaced instead of failing the copy with "Text file busy".
// backup_binary_command must copy the binary to .backup, and succeed with a
// note when there is no binary yet.
func TestDeployBinaryCommands(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	helper, err := filepath.Abs(filepath.Join("..", "..", "scripts", "deploy", "helper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	try := func(command string, args ...string) (string, error) {
		script := `source "$1" && shift && eval "$(` + command + ` "$@")"`
		out, err := exec.Command(bash, append([]string{"-c", script, "bash", helper}, args...)...).CombinedOutput() // #nosec G204 -- fixed script, test paths
		return string(out), err
	}
	run := func(t *testing.T, command string, args ...string) string {
		t.Helper()
		out, err := try(command, args...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", command, err, out)
		}
		return out
	}
	write := func(t *testing.T, path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("install", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "opt ocms") // a space must not break it
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		src, dst := filepath.Join(dir, "ocms-linux-amd64"), filepath.Join(dir, "ocms")
		write(t, src, "new binary")
		write(t, dst, "old binary")
		before, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		run(t, "install_binary_command", src, dst)
		data, err := os.ReadFile(dst) // #nosec G304 -- test path
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "new binary" || info.Mode().Perm() != 0o755 {
			t.Errorf("installed %q with mode %v, want the new binary with mode 0755", data, info.Mode().Perm())
		}
		if _, err := os.Stat(dst + ".new"); err == nil {
			t.Error("the copy beside the destination was left behind")
		}
		if os.SameFile(before, info) {
			t.Error("the install wrote into the existing file, as cp onto a running binary would, instead of renaming a new one over it")
		}
	})

	t.Run("backup", func(t *testing.T) {
		dir := t.TempDir()
		binary := filepath.Join(dir, "ocms")
		write(t, binary, "running binary")
		run(t, "backup_binary_command", binary)
		if data, err := os.ReadFile(binary + ".backup"); err != nil || string(data) != "running binary" { // #nosec G304 -- test path
			t.Errorf("backup = %q, %v; want the running binary", data, err)
		}
		missing := filepath.Join(dir, "absent")
		if out := run(t, "backup_binary_command", missing); !strings.Contains(out, "nothing to back up") {
			t.Errorf("backing up a missing binary printed %q, want a note that there is nothing to back up", out)
		}
		if _, err := os.Stat(missing + ".backup"); err == nil {
			t.Error("a backup was made of a binary that does not exist")
		}

		// A backup that cannot be written fails the command, so set -e stops
		// the deploy before anything is replaced.
		locked := t.TempDir()
		write(t, filepath.Join(locked, "ocms"), "running binary")
		if err := os.Chmod(locked, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o750) }) // #nosec G302 -- test directory
		if out, err := try("backup_binary_command", filepath.Join(locked, "ocms")); err == nil {
			t.Errorf("backing up into a read-only directory succeeded:\n%s", out)
		}
	})
}

// TestDeployScriptsInstallTheUpload fails when a deploy script visibly copies
// a binary straight onto the path ocmsctl and ocms@.service run, when deploy.sh or
// deploy-binary.sh restart the instances without installing the upload
// there, or when deploy-multi.sh stops only the systemd units. Copying onto a
// running binary fails with "Text file busy", which aborted deploy-multi.sh
// after it had stopped the units; a site started with ocmsctl kept serving
// on its port; and deploy.sh and deploy-binary.sh uploaded to
// ocms-linux-amd64, then restarted the instances on the old binary (review
// of the 2026-10-17 audit fixes).
//
// The scripts must also back up the running binary in a way that fails
// loudly, and install the upload before stopping any instance, so a failed
// install leaves them running.
//
// Bug state: drop the install step from deploy-binary.sh, move it after the
// stop, have deploy-multi.sh cp "$NEW_BINARY" "$BINARY_PATH", or stop its
// sites with systemctl stop, and this fails.
func TestDeployScriptsInstallTheUpload(t *testing.T) {
	dir := filepath.Join("..", "..", "scripts", "deploy")
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- fixed repository paths
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	// Best effort: a cp, with any flags, whose last argument is the run binary.
	// The helper's install_binary_command is covered by TestDeployBinaryCommands.
	copyOntoRunBinary := regexp.MustCompile(`(?m)\bcp(?:\s+-\S+)*\s+\S+\s+"?\$\{?(REMOTE_RUN_BINARY|BINARY_PATH)\}?"?\s*($|&&|;|")`)
	for _, name := range []string{"deploy.sh", "deploy-binary.sh", "deploy-multi.sh", "helper.sh"} {
		if m := copyOntoRunBinary.FindString(read(name)); m != "" {
			t.Errorf("scripts/deploy/%s copies onto the running binary (%q); copy beside it and rename", name, strings.TrimSpace(m))
		}
	}
	for _, name := range []string{"deploy.sh", "deploy-binary.sh"} {
		src := read(name)
		install := strings.Index(src, `install_binary_command "${REMOTE_BIN_DIR}/${REMOTE_BINARY}" "${REMOTE_RUN_BINARY}"`)
		stop := strings.Index(src, "ocmsctl stop")
		switch {
		case install < 0:
			t.Errorf("scripts/deploy/%s does not install the upload as ${REMOTE_RUN_BINARY}, so the instances restart on the old binary", name)
		case stop < 0:
			t.Errorf("scripts/deploy/%s stops no instance; the script changed and this test is vacuous", name)
		case install > stop:
			t.Errorf("scripts/deploy/%s installs the upload only after stopping the instances, so a failed install leaves them down", name)
		}
		if !strings.Contains(src, `backup_binary_command "${REMOTE_RUN_BINARY}"`) {
			t.Errorf("scripts/deploy/%s does not back up ${REMOTE_RUN_BINARY} with backup_binary_command", name)
		}
	}
	multi := read("deploy-multi.sh")
	if !regexp.MustCompile(`mv -f "\$\{BINARY_PATH\}\.new" "\$BINARY_PATH"`).MatchString(multi) {
		t.Error("scripts/deploy/deploy-multi.sh no longer renames the new binary over $BINARY_PATH")
	}
	if !strings.Contains(multi, `"$OCMSCTL" stop "$site_id"`) || regexp.MustCompile(`(?m)^\s*systemctl stop`).MatchString(multi) {
		t.Error("scripts/deploy/deploy-multi.sh does not stop its sites with ocmsctl stop, which also reaches a site started without systemd")
	}
	if strings.Index(multi, `mv -f "${BINARY_PATH}.new"`) > strings.Index(multi, `"$OCMSCTL" stop`) {
		t.Error("scripts/deploy/deploy-multi.sh installs the binary only after stopping the sites, so a failed install leaves them down")
	}
}

// TestDeployScriptsDryRunOrder runs deploy.sh and deploy-binary.sh with
// --dry-run and checks the commands they would run, in order: back up the
// running binary, upload, install the upload over /opt/ocms/bin/ocms, then
// stop and start the instance. String checks of the scripts cannot see a
// wrong value behind a right-looking line, such as REMOTE_RUN_BINARY pointing
// at the upload itself.
//
// Bug state: set REMOTE_RUN_BINARY="${REMOTE_BIN_DIR}/${REMOTE_BINARY}" in
// deploy-binary.sh, or move its install after the stop, and this fails.
func TestDeployScriptsDryRunOrder(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"cp -p '/opt/ocms/bin/ocms' '/opt/ocms/bin/ocms.backup.new'",
		"scp bin/ocms-linux-amd64 root@server.example.com:/opt/ocms/bin/ocms-linux-amd64",
		"cp '/opt/ocms/bin/ocms-linux-amd64' '/opt/ocms/bin/ocms.new' && chmod 755 '/opt/ocms/bin/ocms.new' && mv -f '/opt/ocms/bin/ocms.new' '/opt/ocms/bin/ocms'",
		"/opt/ocms/bin/ocmsctl stop site_a",
		"/opt/ocms/bin/ocmsctl start site_a",
	}
	for _, script := range []string{"deploy.sh", "deploy-binary.sh"} {
		t.Run(script, func(t *testing.T) {
			cmd := exec.Command(bash, filepath.Join("scripts", "deploy", script), "server.example.com", "site_a", "--skip-build", "--dry-run") // #nosec G204 -- fixed repository script
			cmd.Dir = root
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("dry run: %v\n%s", err, out)
			}
			var commands []string
			for line := range strings.Lines(string(out)) {
				if _, command, ok := strings.Cut(line, "[DRY-RUN] "); ok {
					commands = append(commands, strings.TrimSpace(command))
				}
			}
			next := 0
			for _, command := range commands {
				if next < len(want) && strings.Contains(command, want[next]) {
					next++
				}
			}
			if next < len(want) {
				t.Errorf("the dry run never reached %q in order; it ran:\n%s", want[next], strings.Join(commands, "\n"))
			}
		})
	}
}
