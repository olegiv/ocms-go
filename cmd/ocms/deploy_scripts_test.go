// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
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
// note when there is no binary yet. remote_upload_path must move an upload
// whose name is a file the deploy uses (ocms, its staging copies, the backup,
// ocmsctl) to ocms.upload.
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

	t.Run("upload path", func(t *testing.T) {
		const dir = "/opt/ocms/bin"
		for name, want := range map[string]string{
			"ocms-linux-amd64": dir + "/ocms-linux-amd64",
			"ocms":             dir + "/ocms.upload",
			"./ocms":           dir + "/ocms.upload",
			"ocms.new":         dir + "/ocms.upload",
			"ocms.backup":      dir + "/ocms.upload",
			"ocms.backup.new":  dir + "/ocms.upload",
			"ocmsctl":          dir + "/ocms.upload",
		} {
			out, err := exec.Command(bash, "-c", `source "$1" && remote_upload_path "$2" "$3"`, "bash", helper, dir, name).CombinedOutput() // #nosec G204 -- fixed script, test values
			if err != nil || string(out) != want {
				t.Errorf("remote_upload_path %s %s = %q, %v; want %s", dir, name, out, err, want)
			}
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
		install := strings.Index(src, `install_binary_command "${REMOTE_UPLOAD}" "${REMOTE_RUN_BINARY}"`)
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
	order := func(upload string) []string {
		return []string{
			"cp -p '/opt/ocms/bin/ocms' '/opt/ocms/bin/ocms.backup.new'",
			"scp bin/ocms-linux-amd64 root@server.example.com:" + upload,
			"cp '" + upload + "' '/opt/ocms/bin/ocms.new' && chmod 755 '/opt/ocms/bin/ocms.new' && mv -f '/opt/ocms/bin/ocms.new' '/opt/ocms/bin/ocms'",
			"/opt/ocms/bin/ocmsctl stop site_a",
			"/opt/ocms/bin/ocmsctl start site_a",
		}
	}
	cases := []struct {
		name string
		env  []string
		want []string
	}{
		{"default upload name", nil, order("/opt/ocms/bin/ocms-linux-amd64")},
		// The upload happens while the instance runs: scp onto the running
		// binary failed with "Text file busy" (local Codex review).
		{"REMOTE_BINARY=ocms", []string{"REMOTE_BINARY=ocms"}, order("/opt/ocms/bin/ocms.upload")},
	}
	for _, tc := range cases {
		for _, script := range []string{"deploy.sh", "deploy-binary.sh"} {
			t.Run(tc.name+"/"+script, func(t *testing.T) {
				want := tc.want
				cmd := exec.Command(bash, filepath.Join("scripts", "deploy", script), "server.example.com", "site_a", "--skip-build", "--dry-run") // #nosec G204 -- fixed repository script
				cmd.Dir = root
				cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, tc.env...)
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
}

// TestDeployMultiKeepsGoingAfterAFailure runs deploy-multi.sh, with its paths
// moved to a temporary directory and stubs for systemctl, ocmsctl, curl,
// sleep and file, over three sites: site_a fails to start and site_c fails
// to stop. The script must still start site_b, leave site_c alone, report
// both failures and exit 1. Under set -e alone, the first failure ended the
// script, leaving every site it had stopped but not yet started down.
//
// Bug state: drop the `if !` around `systemctl start` or `"$OCMSCTL" stop`
// and this fails.
func TestDeployMultiKeepsGoingAfterAFailure(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	scripts, stubs, bin := filepath.Join(tmp, "scripts"), filepath.Join(tmp, "stubs"), filepath.Join(tmp, "bin")
	for _, dir := range []string{scripts, stubs, bin} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	src, err := os.ReadFile(filepath.Join(root, "scripts", "deploy", "deploy-multi.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(src)
	for old, replacement := range map[string]string{
		`BINARY_PATH="/opt/ocms/bin/ocms"`:        `BINARY_PATH="` + filepath.Join(bin, "ocms") + `"`,
		`BACKUP_PATH="/opt/ocms/bin/ocms.backup"`: `BACKUP_PATH="` + filepath.Join(bin, "ocms.backup") + `"`,
		`OCMSCTL="/opt/ocms/bin/ocmsctl"`:         `OCMSCTL="` + filepath.Join(stubs, "ocmsctl") + `"`,
		`SITES_CONF="/etc/ocms/sites.conf"`:       `SITES_CONF="` + filepath.Join(tmp, "sites.conf") + `"`,
		`HEALTH_RETRIES=10`:                       `HEALTH_RETRIES=1`,
		`if [ "$EUID" -ne 0 ]; then`:              `if false; then`,
	} {
		if strings.Count(script, old) != 1 {
			t.Fatalf("deploy-multi.sh no longer has exactly one %q to redirect for the test", old)
		}
		script = strings.Replace(script, old, replacement, 1)
	}
	write(filepath.Join(scripts, "deploy-multi.sh"), script, 0o755)
	helper, err := os.ReadFile(filepath.Join(root, "scripts", "deploy", "helper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(scripts, "helper.sh"), string(helper), 0o644)

	calls := filepath.Join(tmp, "calls.log")
	write(filepath.Join(stubs, "systemctl"), `#!/bin/sh
echo "systemctl $*" >> "`+calls+`"
[ "$2" != "ocms@site_a" ]
`, 0o755)
	write(filepath.Join(stubs, "ocmsctl"), `#!/bin/sh
echo "ocmsctl $*" >> "`+calls+`"
[ "$2" != "site_c" ]
`, 0o755)
	write(filepath.Join(stubs, "curl"), "#!/bin/sh\nexit 0\n", 0o755)
	write(filepath.Join(stubs, "sleep"), "#!/bin/sh\nexit 0\n", 0o755)
	write(filepath.Join(stubs, "file"), "#!/bin/sh\necho \"$1: ELF 64-bit LSB executable\"\n", 0o755)
	write(filepath.Join(tmp, "sites.conf"), "site_a /srv/a ocms 8001\nsite_b /srv/b ocms 8002\nsite_c /srv/c ocms 8003\n", 0o644)
	write(filepath.Join(bin, "ocms"), "old", 0o755)
	newBinary := filepath.Join(tmp, "ocms-new")
	write(newBinary, "new", 0o755)

	cmd := exec.Command(bash, filepath.Join(scripts, "deploy-multi.sh"), newBinary) // #nosec G204 -- copy of a repository script
	cmd.Env = []string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + tmp}
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("deploy-multi.sh: %v, want exit status 1\n%s", err, out)
	}
	logged, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"systemctl start ocms@site_a", "systemctl start ocms@site_b"} {
		if !strings.Contains(string(logged), want) {
			t.Errorf("the script never ran %q; it ran:\n%s\noutput:\n%s", want, logged, out)
		}
	}
	if strings.Contains(string(logged), "ocms@site_c") {
		t.Errorf("the script started site_c, which did not stop:\n%s", logged)
	}
	for _, site := range []string{"site_a", "site_c"} {
		if !strings.Contains(string(out), "  - "+site+"\n") {
			t.Errorf("the failure report does not list %s:\n%s", site, out)
		}
	}
	if installed, err := os.ReadFile(filepath.Join(bin, "ocms")); err != nil || string(installed) != "new" {
		t.Errorf("installed binary = %q, %v; want the new one", installed, err)
	}
}
