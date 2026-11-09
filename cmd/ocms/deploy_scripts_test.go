// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestDeployBinaryCommands runs the commands scripts/deploy/helper.sh builds
// for the deploy scripts. install_binary_command must replace the
// destination's content, leave it executable and leave no copy behind: it
// renames the staged upload over the destination, which leaves the old inode
// alone even when a process still runs it.
// backup_binary_command must copy the binary to .backup, and succeed with a
// note when there is no binary yet. wait_healthy_command must succeed once ocmsctl
// health does, and fail after the given number of attempts when it never does.
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
		if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("staged upload still exists after install: %v", err)
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

		// cp treats an existing destination directory as a target directory.
		// Make the resulting file path a directory too, so cp fails even as root.
		blocked := filepath.Join(binary+".backup.new", filepath.Base(binary))
		if err := os.MkdirAll(blocked, 0o750); err != nil {
			t.Fatal(err)
		}
		write(t, binary, "replacement binary")
		if out, err := try("backup_binary_command", binary); err == nil {
			t.Errorf("backing up over a directory succeeded:\n%s", out)
		}
		if data, err := os.ReadFile(binary + ".backup"); err != nil || string(data) != "running binary" { // #nosec G304 -- test path
			t.Errorf("backup after failed copy = %q, %v; want the previous backup", data, err)
		}
	})

	t.Run("wait healthy", func(t *testing.T) {
		dir := t.TempDir()
		// stub writes an ocmsctl that logs each call, prints the call's
		// number, and answers from the given call on, or never when answerOn
		// is 0.
		stub := func(name string, answerOn int) (string, string) {
			path, calls := filepath.Join(dir, name), filepath.Join(dir, name+".calls")
			script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\nn=$(wc -l < '" + calls + "')\necho \"FAILED (call $((n)))\"\n" +
				"[ " + strconv.Itoa(answerOn) + " -gt 0 ] && [ \"$n\" -ge " + strconv.Itoa(answerOn) + " ]\n"
			if err := os.WriteFile(path, []byte(script), 0o755); err != nil { // #nosec G306 -- executable test stub
				t.Fatal(err)
			}
			return path, calls
		}
		attempts := func(calls string) int {
			data, err := os.ReadFile(calls) // #nosec G304 -- test path
			if err != nil {
				t.Fatal(err)
			}
			return strings.Count(string(data), "health site_a\n")
		}

		late, lateCalls := stub("ocmsctl-late", 3)
		run(t, "wait_healthy_command", late, "site_a", "5", "0")
		if got := attempts(lateCalls); got != 3 {
			t.Errorf("an instance healthy on the third check was checked %d times, want 3", got)
		}

		down, downCalls := stub("ocmsctl-down", 0)
		out, err := try("wait_healthy_command", down, "site_a", "4", "0")
		if err == nil {
			t.Errorf("waiting for an instance that never answers succeeded:\n%s", out)
		}
		if got := attempts(downCalls); got != 4 {
			t.Errorf("an instance that never answers was checked %d times, want 4", got)
		}
		// Only the last attempt's output is shown, so the operator sees why.
		if out != "FAILED (call 4)\n" {
			t.Errorf("a failed wait printed %q, want the last attempt's output", out)
		}
	})
}

// TestDeployScriptsInstallTheUpload fails when a deploy script visibly copies
// a binary straight onto the path ocmsctl and ocms@.service run, or when
// deploy-multi.sh stops only the systemd units. Copying onto a
// running binary fails with "Text file busy", which aborted deploy-multi.sh
// after it had stopped the units; a site started with ocmsctl kept serving
// on its port. The SSH scripts' backup and install order is checked by
// TestDeployScriptsDryRunOrder.
//
// Bug state: have deploy-multi.sh cp "$NEW_BINARY" "$BINARY_PATH", or stop its
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
// stop and start the instance, wait for it to report healthy with the
// configured HEALTH_RETRIES and HEALTH_INTERVAL, and ask it once more. String
// checks of the scripts cannot see a wrong value behind a right-looking line,
// such as REMOTE_RUN_BINARY pointing at the upload itself.
//
// Bug state: set REMOTE_RUN_BINARY="${REMOTE_BIN_DIR}/${REMOTE_BINARY}" in
// deploy-binary.sh, move its install after the stop, or hard-code the health
// retries, and this fails.
func TestDeployScriptsDryRunOrder(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	order := func(name, retries, interval string) []string {
		stage := "/opt/ocms/bin/.ocms-upload.DRYRUN00"
		upload := stage + "/" + name
		return []string{
			"cp -p '/opt/ocms/bin/ocms' '/opt/ocms/bin/ocms.backup.new'",
			"mktemp -d -- '/opt/ocms/bin/.ocms-upload.XXXXXXXX'",
			"scp bin/ocms-linux-amd64 root@server.example.com:" + upload,
			"chmod 755 '" + upload + "' && mv -f '" + upload + "' '/opt/ocms/bin/ocms'",
			"rm -rf -- '" + stage + "'",
			"/opt/ocms/bin/ocmsctl stop site_a",
			"/opt/ocms/bin/ocmsctl start site_a",
			"'/opt/ocms/bin/ocmsctl' health 'site_a' 2>&1); do [ \"$n\" -lt " + retries + " ] || { echo \"$out\"; exit 1; }; n=$((n + 1)); sleep " + interval + "; done",
			"/opt/ocms/bin/ocmsctl status site_a",
			"/opt/ocms/bin/ocmsctl health site_a",
		}
	}
	cases := []struct {
		name string
		env  []string
		want []string
	}{
		{"default upload name", nil, order("ocms-linux-amd64", "15", "2")},
		{"REMOTE_BINARY=ocms", []string{"REMOTE_BINARY=./ocms"}, order("ocms", "15", "2")},
		{"health settings", []string{"HEALTH_RETRIES=1", "HEALTH_INTERVAL=0"}, order("ocms-linux-amd64", "1", "0")},
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

// sshStub stands in for ssh in the deploy and sync script tests. It appends
// the remote command to $SSH_LOG and succeeds, except for the failures the
// instance names call for: site_bad never reports healthy, site_flaky fails
// the check after the status, site_nostart fails to start and site_nostop to
// stop, and the connection drops (exit 255) while waiting for site_lost,
// while checking site_lostcheck again and while stopping site_loststop. With
// $SIGNAL_ON set, the first `ocmsctl start` ("start", which then fails, or
// "start-ok") or health wait ("wait") sends SIGTERM to the calling script.
const sshStub = `#!/bin/sh
printf '%s\n' "$2" >> "$SSH_LOG"
case "$SIGNAL_ON:$2" in
  "start:/opt/ocms/bin/ocmsctl start "* | "start-ok:/opt/ocms/bin/ocmsctl start "* | "wait:"*"' health '"*)
    if [ ! -e "$SSH_LOG.signalled" ]; then
      : > "$SSH_LOG.signalled"
      kill -TERM "$PPID"
      [ "$SIGNAL_ON" = start ] && exit 1
      exit 0
    fi ;;
esac
case "$2" in
  "mktemp -d -- "*) echo /opt/ocms/bin/.ocms-upload.TEST0000 ;;
  *"health 'site_bad'"*) echo "FAILED (HTTP 503)"; exit 1 ;;
  *"health 'site_lost'"*) exit 255 ;;
  "/opt/ocms/bin/ocmsctl health site_flaky" | "/opt/ocms/bin/ocmsctl start site_nostart" | "/opt/ocms/bin/ocmsctl stop site_nostop") exit 1 ;;
  "/opt/ocms/bin/ocmsctl health site_lostcheck" | "/opt/ocms/bin/ocmsctl stop site_loststop") exit 255 ;;
esac
`

// ctl is the remote ocmsctl command the scripts run for verb and instance.
func ctl(verb, instance string) string { return "/opt/ocms/bin/ocmsctl " + verb + " " + instance }

// waitFor is in the health wait for instance, and in no other command.
func waitFor(instance string) string { return "'/opt/ocms/bin/ocmsctl' health '" + instance + "'" }

// writeStubs writes each script to dir as an executable named by its key.
func writeStubs(t *testing.T, dir string, scripts map[string]string) {
	t.Helper()
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil { // #nosec G306 -- executable test stub
			t.Fatal(err)
		}
	}
}

// remoteCommands returns what sshStub logged to path, and its lines; none
// when the script never ran a remote command.
func remoteCommands(t *testing.T, path string) (string, []string) {
	t.Helper()
	logged, err := os.ReadFile(path) // #nosec G304 -- test path
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(logged), strings.Split(strings.TrimSuffix(string(logged), "\n"), "\n")
}

func TestDeployScriptsStageAwayFromSymlinkTarget(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	for _, script := range []string{"deploy.sh", "deploy-binary.sh"} {
		for _, fault := range []string{"", "backup", "upload", "install"} {
			t.Run(script+"/fault="+fault, func(t *testing.T) {
				dir := t.TempDir()
				bin := filepath.Join(dir, "bin")
				if err := os.Mkdir(bin, 0o750); err != nil {
					t.Fatal(err)
				}
				source := filepath.Join(dir, "release")
				target := filepath.Join(bin, "ocms-linux-amd64")
				installed := filepath.Join(bin, "ocms")
				backup := installed + ".backup"
				for path, content := range map[string]string{source: "new", target: "old", backup: "prior backup"} {
					if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(filepath.Base(target), installed); err != nil {
					t.Fatal(err)
				}
				if fault == "backup" {
					if err := os.MkdirAll(filepath.Join(backup+".new", "ocms"), 0o750); err != nil {
						t.Fatal(err)
					}
				}
				writeStubs(t, dir, map[string]string{
					"ssh": `#!/bin/sh
case "$DEPLOY_FAULT:$2" in install:chmod\ *) exit 25;; esac
exec sh -c "$2"
`,
					"scp": `#!/bin/sh
dest=${2#*:}
if [ "$DEPLOY_FAULT" = upload ]; then printf partial > "$dest"; exit 23; fi
cp "$1" "$dest"
`,
				})
				calls := filepath.Join(dir, "calls.log")
				writeStubs(t, bin, map[string]string{"ocmsctl": "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CALLS\"\n"})
				cmd := exec.Command(bash, filepath.Join("..", "..", "scripts", "deploy", script), "server.example.com", "site_a", "--skip-build") // #nosec G204 -- fixed repository script
				cmd.Env = []string{
					"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + dir,
					"LOCAL_BINARY=" + source, "REMOTE_BIN_DIR=" + bin, "CALLS=" + calls, "DEPLOY_FAULT=" + fault,
				}
				out, err := cmd.CombinedOutput()
				if (err == nil) != (fault == "") {
					t.Fatalf("deploy with fault %q: %v\n%s", fault, err, out)
				}
				wantInstalled, wantBackup, wantCalls := "old", "old", ""
				switch fault {
				case "":
					wantInstalled, wantCalls = "new", "stop site_a\nstart site_a\nhealth site_a\nstatus site_a\nhealth site_a\n"
				case "backup":
					wantBackup = "prior backup"
				}
				for path, want := range map[string]string{installed: wantInstalled, target: "old", backup: wantBackup} {
					if got, err := os.ReadFile(path); err != nil || string(got) != want { // #nosec G304 -- test path
						t.Errorf("%s = %q, %v; want %q", path, got, err, want)
					}
				}
				if got, _ := remoteCommands(t, calls); got != wantCalls {
					t.Errorf("ocmsctl calls = %q, want %q", got, wantCalls)
				}
				if leftovers, err := filepath.Glob(filepath.Join(bin, ".ocms-upload.*")); err != nil || len(leftovers) != 0 {
					t.Errorf("upload directories left behind: %v, %v", leftovers, err)
				}
			})
		}
	}
}

// TestDeployScriptsFailOnUnhealthyInstance runs deploy.sh and deploy-binary.sh
// against sshStub and a stub scp. A deploy of healthy instances must report
// success; any other must still start, check and report on every instance it
// could, then exit 1 and say why. Both scripts only printed `ocmsctl status` and reported
// success, so on 2026-10-23 a binary that every instance refused to run
// (OCMS_MFA_KEY unset) deployed "successfully".
//
// Bug state: replace either script's health check with the plain
// `ocmsctl status` it had, let deploy-binary.sh stop at the first failed
// start, or let deploy.sh end at a failed start without its summary, and
// this fails.
func TestDeployScriptsFailOnUnhealthyInstance(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		script    string
		args      []string
		env       []string
		ok        bool
		out       []string // the output must contain each
		notOut    []string
		ran       []string // remote commands that must have run
		notRan    []string
		waited    []string // instances whose health wait ran
		notWaited []string
	}{
		{name: "healthy", script: "deploy.sh", args: []string{"site_ok"}, ok: true,
			out: []string{"Deployment complete!"},
			ran: []string{ctl("start", "site_ok"), ctl("status", "site_ok"), ctl("health", "site_ok")}, waited: []string{"site_ok"}},
		{name: "unhealthy", script: "deploy.sh", args: []string{"site_bad"},
			out:    []string{"FAILED (HTTP 503)", "Deployment failed for: site_bad", "Backup: /opt/ocms/bin/ocms.backup"},
			ran:    []string{ctl("start", "site_bad"), ctl("status", "site_bad")},
			notRan: []string{ctl("health", "site_bad")}, waited: []string{"site_bad"}},
		// --skip-binary makes no backup, so the one on the server is not this deploy's.
		{name: "unhealthy without a binary", script: "deploy.sh", args: []string{"site_bad", "--skip-binary"},
			out: []string{"Deployment failed for: site_bad"}, notOut: []string{"Backup:"}},
		{name: "healthy", script: "deploy-binary.sh", args: []string{"site_ok", "site_two"}, ok: true,
			out: []string{"Deployment complete!"},
			ran: []string{ctl("start", "site_ok"), ctl("start", "site_two"), ctl("status", "site_ok"), ctl("status", "site_two"),
				ctl("health", "site_ok"), ctl("health", "site_two")},
			waited: []string{"site_ok", "site_two"}},
		{name: "unhealthy", script: "deploy-binary.sh", args: []string{"site_bad", "site_ok"},
			out:    []string{"FAILED (HTTP 503)", "Deployment failed for: site_bad", "Backup: /opt/ocms/bin/ocms.backup"},
			notOut: []string{"Deployment failed for: site_bad site_ok"},
			ran:    []string{ctl("start", "site_bad"), ctl("start", "site_ok"), ctl("status", "site_bad"), ctl("status", "site_ok"), ctl("health", "site_ok")},
			waited: []string{"site_bad", "site_ok"}},
		{name: "failed start", script: "deploy-binary.sh", args: []string{"site_nostart", "site_ok"},
			out:    []string{"Could not start instance 'site_nostart'", "Deployment failed for: site_nostart"},
			ran:    []string{ctl("start", "site_ok"), ctl("status", "site_ok"), ctl("health", "site_ok")},
			waited: []string{"site_ok"}, notWaited: []string{"site_nostart"}},
		{name: "failed stop", script: "deploy-binary.sh", args: []string{"site_nostop", "site_ok"},
			out:    []string{"Could not stop instance 'site_nostop'", "Deployment failed for: site_nostop"},
			ran:    []string{ctl("start", "site_ok")},
			notRan: []string{ctl("start", "site_nostop")}, waited: []string{"site_ok"}, notWaited: []string{"site_nostop"}},
		{name: "lost connection", script: "deploy-binary.sh", args: []string{"site_lost"},
			out: []string{"Lost the connection to the server while waiting for 'site_lost'", "Deployment failed for: site_lost"}},
		{name: "fails after starting", script: "deploy-binary.sh", args: []string{"site_flaky"},
			out: []string{"Instance 'site_flaky' stopped reporting healthy", "Deployment failed for: site_flaky"}},
		{name: "bad retries", script: "deploy-binary.sh", args: []string{"site_ok"}, env: []string{"HEALTH_RETRIES=0"},
			out: []string{"HEALTH_RETRIES must be a positive whole number"}, notRan: []string{ctl("stop", "site_ok")}},
		{name: "bad interval", script: "deploy.sh", args: []string{"site_ok"}, env: []string{"HEALTH_INTERVAL=0.5"},
			out: []string{"HEALTH_INTERVAL must be a whole number of seconds"}, notRan: []string{ctl("stop", "site_ok")}},
		// printf %d reads 08 as an invalid octal number and waits 0 seconds.
		{name: "octal interval", script: "deploy-binary.sh", args: []string{"site_ok"}, env: []string{"HEALTH_INTERVAL=08"},
			out: []string{"HEALTH_INTERVAL must be a whole number of seconds"}, notRan: []string{ctl("stop", "site_ok")}},
		{name: "connection lost on the second check", script: "deploy-binary.sh", args: []string{"site_lostcheck"},
			out:    []string{"Lost the connection to the server while checking 'site_lostcheck'", "Deployment failed for: site_lostcheck"},
			notOut: []string{"stopped reporting healthy"}},
		{name: "connection lost while stopping", script: "deploy-binary.sh", args: []string{"site_loststop", "site_ok"},
			out:    []string{"Lost the connection to the server while stopping 'site_loststop'", "Deployment failed for: site_loststop"},
			notOut: []string{"Could not stop"}, ran: []string{ctl("start", "site_ok")}, notRan: []string{ctl("start", "site_loststop")}},
		{name: "failed start", script: "deploy.sh", args: []string{"site_nostart"},
			out: []string{"Could not start instance 'site_nostart'", "Deployment failed for: site_nostart"}, notWaited: []string{"site_nostart"}},
		{name: "failed stop", script: "deploy.sh", args: []string{"site_nostop"},
			out:    []string{"Could not stop instance 'site_nostop'", "Deployment failed for: site_nostop"},
			notRan: []string{ctl("start", "site_nostop")}},
	}
	for _, tc := range cases {
		t.Run(tc.script+"/"+tc.name, func(t *testing.T) {
			work, stubs := t.TempDir(), t.TempDir()
			if err := os.Mkdir(filepath.Join(work, "bin"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, "bin", "ocms-linux-amd64"), []byte("binary"), 0o600); err != nil {
				t.Fatal(err)
			}
			writeStubs(t, stubs, map[string]string{"ssh": sshStub, "scp": "#!/bin/sh\nexit 0\n"})
			sshLog := filepath.Join(work, "ssh.log")

			args := append([]string{filepath.Join(root, "scripts", "deploy", tc.script), "server.example.com"}, tc.args...)
			cmd := exec.Command(bash, append(args, "--skip-build")...) // #nosec G204 -- fixed repository script, test arguments
			cmd.Dir = work
			cmd.Env = append([]string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + work,
				"SSH_LOG=" + sshLog, "HEALTH_RETRIES=1", "HEALTH_INTERVAL=0"}, tc.env...)
			out, err := cmd.CombinedOutput()
			logged, commands := remoteCommands(t, sshLog)

			if tc.ok {
				if err != nil {
					t.Fatalf("deploy: %v, want success\n%s", err, out)
				}
			} else {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
					t.Fatalf("deploy: %v, want exit status 1\n%s", err, out)
				}
				if strings.Contains(string(out), "Deployment complete!") {
					t.Errorf("a failed deploy reported success:\n%s", out)
				}
			}
			for _, want := range tc.out {
				if !strings.Contains(string(out), want) {
					t.Errorf("the output does not say %q:\n%s", want, out)
				}
			}
			for _, unwanted := range tc.notOut {
				if strings.Contains(string(out), unwanted) {
					t.Errorf("the output says %q:\n%s", unwanted, out)
				}
			}
			for _, want := range tc.ran {
				if !slices.Contains(commands, want) {
					t.Errorf("the deploy never ran %q; it ran:\n%s", want, logged)
				}
			}
			for _, unwanted := range tc.notRan {
				if slices.Contains(commands, unwanted) {
					t.Errorf("the deploy ran %q:\n%s", unwanted, logged)
				}
			}
			waited := func(instance string) bool {
				return slices.ContainsFunc(commands, func(c string) bool { return strings.Contains(c, waitFor(instance)) })
			}
			for _, instance := range tc.waited {
				if !waited(instance) {
					t.Errorf("the deploy never waited for %s; it ran:\n%s", instance, logged)
				}
			}
			for _, instance := range tc.notWaited {
				if waited(instance) {
					t.Errorf("the deploy waited for %s, which it did not start:\n%s", instance, logged)
				}
			}
		})
	}
}

// TestDeployMultiKeepsGoingAfterAFailure runs deploy-multi.sh, with its paths
// moved to a temporary directory and stubs for systemctl, ocmsctl, curl,
// sleep and file, over three sites: site_a fails to start and site_c fails
// to stop. The script must still start site_b, leave site_c alone, report
// both failures and exit 1. It also checks that aliased sources are rejected
// before any deployment work and that a successful deploy cleans up the upload.
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
	installed := filepath.Join(bin, "ocms")
	backup := installed + ".backup"
	write(installed, "old", 0o755)
	write(backup, "prior backup", 0o755)
	write(installed+".new", "prior staging", 0o755)
	newBinary := filepath.Join(tmp, "ocms-new")
	write(newBinary, "new", 0o755)

	run := func(source string) ([]byte, error) {
		cmd := exec.Command(bash, filepath.Join(scripts, "deploy-multi.sh"), source) // #nosec G204 -- copy of a repository script
		cmd.Env = []string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + tmp}
		return cmd.CombinedOutput()
	}
	symlink, hardlink := filepath.Join(tmp, "symlink"), filepath.Join(tmp, "hardlink")
	if linkErr := os.Symlink(installed, symlink); linkErr != nil {
		t.Fatal(linkErr)
	}
	if linkErr := os.Link(installed, hardlink); linkErr != nil {
		t.Fatal(linkErr)
	}
	for _, source := range []string{installed, symlink, hardlink, backup, installed + ".new"} {
		out, deployErr := run(source)
		if deployErr == nil || !strings.Contains(string(out), "New binary must be separate from") {
			t.Errorf("aliased source %s: %v, want refusal\n%s", source, deployErr, out)
		}
	}
	if _, statErr := os.Stat(calls); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("deployment touched services for an aliased source: %v", statErr)
	}
	for path, want := range map[string]string{installed: "old", backup: "prior backup", installed + ".new": "prior staging"} {
		if got, readErr := os.ReadFile(path); readErr != nil || string(got) != want { // #nosec G304 -- test path
			t.Errorf("%s after refusal = %q, %v; want %q", path, got, readErr, want)
		}
	}

	out, err := run(newBinary)
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
	if got, err := os.ReadFile(installed); err != nil || string(got) != "new" {
		t.Errorf("installed binary = %q, %v; want the new one", got, err)
	}
	// Retry the retained upload with only a healthy site registered.
	write(filepath.Join(tmp, "sites.conf"), "site_b /srv/b ocms 8002\n", 0o644)
	if out, err := run(newBinary); err != nil || !strings.Contains(string(out), "deployed successfully") {
		t.Fatalf("retry with a separate source: %v\n%s", err, out)
	}
	if got, err := os.ReadFile(installed); err != nil || string(got) != "new" {
		t.Errorf("installed binary after retry = %q, %v; want the new one", got, err)
	}
	if _, err := os.Stat(newBinary); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("separate upload still exists after successful deploy: %v", err)
	}
}

// TestSyncProdToDevRestartsProduction runs sync-prod-to-dev.sh against
// sshStub and stubs for rsync (which logs to $SSH_LOG too) and lsof,
// answering its prompt. The script stops the production instance while it
// copies the data. Whether the copy works, fails or is interrupted, it must
// start the instance again exactly once, wait for it to report healthy, and
// exit non-zero unless the copy worked and the instance came back. A stop
// that ocmsctl refuses must end the script before anything is copied, and
// without starting an instance that is still running. It used to start
// production only after every step had worked, so a failed copy ended the
// script under set -e with the site down, it never checked that the site
// came up, and it copied the database of an instance it had not stopped
// (issue #194).
//
// A second interrupt must not cut the trap's start short, an interrupt
// before step 5's start succeeded must still make the trap start the
// instance, and one during the health wait must not start it twice.
//
// The script must also trap INT, TERM and HUP itself, turning them into a
// normal exit: left untrapped, bash runs the EXIT trap from inside its
// handling of the fatal signal, and on a CI runner it skipped it ("interrupted
// during the start" failed there once and never locally). No stub reproduces
// that race, so the traps are checked in the source.
//
// Bug state: drop the EXIT trap, the signal traps, the line in
// restart_remote_on_exit that ignores INT, TERM and HUP, the health check in
// start_remote_server, or the exit after a failed stop, or clear
// REMOTE_STOPPED before step 5's start, and this fails.
func TestSyncProdToDevRestartsProduction(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "deploy", "sync-prod-to-dev.sh"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(script) // #nosec G304 -- fixed repository path
	if err != nil {
		t.Fatal(err)
	}
	for _, trap := range []string{"trap 'exit 129' HUP", "trap 'exit 130' INT", "trap 'exit 143' TERM", "trap restart_remote_on_exit EXIT"} {
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(trap) + `$`).Match(source) {
			t.Errorf("sync-prod-to-dev.sh no longer sets %q, so an interrupt can skip restarting production", trap)
		}
	}
	const anyFailure = -1
	cases := []struct {
		name     string
		instance string
		answer   string
		rsync    string // the stub's exit status, or "term" to send SIGTERM to the script
		signalOn string // sshStub's $SIGNAL_ON
		exit     int    // 0, an exit status, or anyFailure
		starts   int
		copied   bool
		out      []string
		notOut   []string
	}{
		{name: "synced", instance: "site_ok", answer: "y", rsync: "0", exit: 0, starts: 1, copied: true,
			out: []string{"Sync complete!"}},
		{name: "answered no", instance: "site_ok", answer: "n", rsync: "0", exit: 0,
			out: []string{"Aborted."}, notOut: []string{"Sync complete!"}},
		{name: "copy failed", instance: "site_ok", answer: "y", rsync: "23", exit: 23, starts: 1, copied: true,
			out:    []string{"The sync stopped before it finished (status 23) while the remote instance was stopped; starting it again", "Instance 'site_ok' is healthy"},
			notOut: []string{"Sync complete!"}},
		{name: "interrupted", instance: "site_ok", answer: "y", rsync: "term", exit: anyFailure, starts: 1, copied: true,
			out:    []string{"The sync stopped before it finished", "Instance 'site_ok' is healthy"},
			notOut: []string{"Sync complete!"}},
		{name: "unhealthy after the sync", instance: "site_bad", answer: "y", rsync: "0", exit: 1, starts: 1, copied: true,
			out:    []string{"FAILED (HTTP 503)", "The data was synced, but the remote instance 'site_bad' is not healthy"},
			notOut: []string{"Sync complete!", "stopped before it finished"}},
		{name: "copy failed, then unhealthy", instance: "site_bad", answer: "y", rsync: "23", exit: 23, starts: 1, copied: true,
			out:    []string{"The sync stopped before it finished (status 23)", "The remote instance 'site_bad' did not come back"},
			notOut: []string{"Sync complete!"}},
		{name: "stop refused", instance: "site_nostop", answer: "y", rsync: "0", exit: 1,
			out:    []string{"Could not stop remote instance 'site_nostop'; nothing was synced"},
			notOut: []string{"Sync complete!", "stopped before it finished"}},
		{name: "connection lost while stopping", instance: "site_loststop", answer: "y", rsync: "0", exit: 1, starts: 1,
			out:    []string{"Lost the connection to the server while stopping 'site_loststop'", "Instance 'site_loststop' is healthy"},
			notOut: []string{"Sync complete!"}},
		{name: "interrupted during the restart", instance: "site_ok", answer: "y", rsync: "23", signalOn: "start-ok", exit: 23, starts: 1, copied: true,
			out:    []string{"The sync stopped before it finished (status 23)", "Instance 'site_ok' is healthy"},
			notOut: []string{"Sync complete!"}},
		{name: "interrupted during the start", instance: "site_ok", answer: "y", rsync: "0", signalOn: "start", exit: anyFailure, starts: 2, copied: true,
			out:    []string{"The sync stopped before it finished", "Instance 'site_ok' is healthy"},
			notOut: []string{"Sync complete!"}},
		{name: "interrupted during the health wait", instance: "site_ok", answer: "y", rsync: "0", signalOn: "wait", exit: anyFailure, starts: 1, copied: true,
			out:    []string{"The remote instance 'site_ok' was started, but its health check was cut short"},
			notOut: []string{"Sync complete!", "stopped before it finished"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work, stubs := t.TempDir(), t.TempDir()
			writeStubs(t, stubs, map[string]string{
				"ssh": sshStub,
				"rsync": `#!/bin/sh
echo rsync >> "$SSH_LOG"
if [ "$RSYNC_EXIT" = term ]; then kill -TERM "$PPID"; exit 0; fi
if [ "$RSYNC_EXIT" != 0 ]; then exit "$RSYNC_EXIT"; fi
for dest do :; done
case "$dest" in */data/) printf 'SQLite format 3\000' > "$dest/ocms.db";; esac
`,
				"lsof":    "#!/bin/sh\nexit 1\n",
				"sqlite3": "#!/bin/sh\nexit 0\n",
			})
			sshLog := filepath.Join(work, "ssh.log")

			cmd := exec.Command(bash, script, "server.example.com", tc.instance, "-v", "/srv/site") // #nosec G204 -- fixed repository script, test arguments
			cmd.Dir = work
			cmd.Stdin = strings.NewReader(tc.answer)
			cmd.Env = []string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + work,
				"SSH_LOG=" + sshLog, "RSYNC_EXIT=" + tc.rsync, "SIGNAL_ON=" + tc.signalOn, "HEALTH_RETRIES=1", "HEALTH_INTERVAL=0"}
			out, err := cmd.CombinedOutput()
			logged, commands := remoteCommands(t, sshLog)

			var exitErr *exec.ExitError
			switch {
			case tc.exit == 0 && err != nil:
				t.Fatalf("sync: %v, want success\n%s", err, out)
			case tc.exit == anyFailure && err == nil:
				t.Fatalf("sync succeeded, want a failure\n%s", out)
			case tc.exit > 0 && (!errors.As(err, &exitErr) || exitErr.ExitCode() != tc.exit):
				t.Fatalf("sync: %v, want exit status %d\n%s", err, tc.exit, out)
			}
			for _, want := range tc.out {
				if !strings.Contains(string(out), want) {
					t.Errorf("the output does not say %q:\n%s", want, out)
				}
			}
			for _, unwanted := range tc.notOut {
				if strings.Contains(string(out), unwanted) {
					t.Errorf("the output says %q:\n%s", unwanted, out)
				}
			}
			if copied := slices.Contains(commands, "rsync"); copied != tc.copied {
				t.Errorf("the sync copied data: %v, want %v; it ran:\n%s", copied, tc.copied, logged)
			}
			if starts := strings.Count(logged, ctl("start", tc.instance)+"\n"); starts != tc.starts {
				t.Fatalf("the sync started %s %d times, want %d:\n%s", tc.instance, starts, tc.starts, logged)
			}
			if tc.starts == 0 {
				return
			}
			stop, start := slices.Index(commands, ctl("stop", tc.instance)), slices.Index(commands, ctl("start", tc.instance))
			if stop < 0 || start < stop {
				t.Fatalf("the sync did not stop and then start %s; it ran:\n%s", tc.instance, logged)
			}
			if !slices.ContainsFunc(commands[start:], func(c string) bool { return strings.Contains(c, waitFor(tc.instance)) }) {
				t.Errorf("the sync never waited for %s after starting it; it ran:\n%s", tc.instance, logged)
			}
		})
	}
}

func TestSyncProdToDevScrubsDatabaseBeforePublishing(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 is not installed")
	}
	rsync, err := exec.LookPath("rsync")
	if err != nil {
		t.Skip("rsync is not installed")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "deploy", "sync-prod-to-dev.sh"))
	if err != nil {
		t.Fatal(err)
	}
	sqliteQuery := func(path, query string) string {
		t.Helper()
		out, err := exec.Command(sqlite, "-batch", "-bail", "-init", "/dev/null", path, query).CombinedOutput() // #nosec G204 -- fixed SQLite tool and fixture
		if err != nil {
			t.Fatalf("sqlite fixture: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	for _, scenario := range []string{"current schema", "legacy schema", "custom database with WAL", "scrub failure", "--no-db", "--dry-run"} {
		t.Run(scenario, func(t *testing.T) {
			work, stubs, source := t.TempDir(), t.TempDir(), t.TempDir()
			data := filepath.Join(work, "data")
			if err := os.Mkdir(data, 0o700); err != nil {
				t.Fatal(err)
			}
			name := "ocms.db"
			if scenario == "custom database with WAL" {
				name = "site.db"
			}
			dst, src := filepath.Join(data, name), filepath.Join(source, name)
			sqliteQuery(dst, "CREATE TABLE content(value); INSERT INTO content VALUES ('local');")
			sqliteQuery(src, "CREATE TABLE content(value); INSERT INTO content VALUES ('production');")
			tables := []string{"sessions"}
			if scenario != "legacy schema" {
				tables = append(tables, "oauth_tokens", "oauth_codes", "preview_links")
			}
			for _, table := range tables {
				sqliteQuery(src, "CREATE TABLE "+table+"(token); INSERT INTO "+table+" VALUES ('synthetic-credential-fixture');")
			}
			if scenario == "scrub failure" {
				sqliteQuery(src, "CREATE TRIGGER prevent_scrub BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'scrub fault'); END;")
			}
			wantContent := "production"
			if scenario == "custom database with WAL" {
				// Keep the source connection open so these writes only exist in the WAL.
				walDB, err := sql.Open("sqlite3", src)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = walDB.Close() })
				if _, err := walDB.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;
					UPDATE content SET value='from WAL';
					UPDATE sessions SET token='synthetic-wal-credential';`); err != nil {
					t.Fatal(err)
				}
				wantContent = "from WAL"
			}
			writeStubs(t, stubs, map[string]string{
				"ssh":  sshStub,
				"lsof": "#!/bin/sh\nexit 1\n",
				"rsync": `#!/bin/bash
args=("$@")
src=$(($# - 2))
if [[ "${args[src]}" == *:/srv/site/data/ ]]; then
    args[src]="$FIXTURE/"
fi
exec "$REAL_RSYNC" "${args[@]}"
`,
			})
			args := []string{script, "server.example.com", "site_ok", "-v", "/srv/site", "--no-uploads", "--no-logs"}
			if strings.HasPrefix(scenario, "--") {
				args = append(args, scenario)
			}
			sshLog := filepath.Join(work, "ssh.log")
			cmd := exec.Command(bash, args...) // #nosec G204 -- fixed repository script, mocked production commands
			cmd.Dir = work
			cmd.Stdin = strings.NewReader("y")
			cmd.Env = []string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + work,
				"TMPDIR=" + work, "SSH_LOG=" + sshLog, "HEALTH_RETRIES=1", "HEALTH_INTERVAL=0", "REAL_RSYNC=" + rsync, "FIXTURE=" + source}
			out, err := cmd.CombinedOutput()
			if scenario == "scrub failure" {
				if err == nil || !strings.Contains(string(out), "scrub fault") || !strings.Contains(string(out), "Instance 'site_ok' is healthy") {
					t.Fatalf("scrub failure did not restore the remote instance and fail the sync: %v\n%s", err, out)
				}
			} else if err != nil {
				t.Fatalf("sync: %v\n%s", err, out)
			}
			if staging, err := filepath.Glob(filepath.Join(work, "ocms-sync-db.*")); err != nil || len(staging) != 0 {
				t.Fatalf("private copy remained after exit: %v, %v", staging, err)
			}
			if scenario == "scrub failure" || strings.HasPrefix(scenario, "--") {
				if got := sqliteQuery(dst, "SELECT value FROM content;"); got != "local" {
					t.Fatalf("local database was replaced: %s", got)
				}
				return
			}
			if got := sqliteQuery(dst, "SELECT value FROM content;"); got != wantContent {
				t.Fatalf("copied content = %s", got)
			}
			for _, table := range tables {
				if got := sqliteQuery(dst, "SELECT COUNT(*) FROM "+table+";"); got != "0" {
					t.Errorf("synced %s contains %s credential rows", table, got)
				}
				if got := sqliteQuery(src, "SELECT COUNT(*) FROM "+table+";"); got != "1" {
					t.Errorf("production fixture %s was modified", table)
				}
			}
			b, err := os.ReadFile(dst) // #nosec G304 -- local fixture path
			if err != nil || strings.Contains(string(b), "synthetic-credential-fixture") ||
				strings.Contains(string(b), "synthetic-wal-credential") {
				t.Fatal("credential fixture is still present in the copied database bytes", err)
			}
			if _, err := os.Stat(dst + "-wal"); !os.IsNotExist(err) {
				t.Fatalf("published database retained a WAL: %v", err)
			}
		})
	}
}

// TestHealthcheckMultiReportsCrashLoopingUnits runs healthcheck-multi.sh, with
// its paths moved to a temporary directory and stubs for systemctl, curl,
// mail, sleep and hostname, five times over five sites, then twice more once
// site_sick answers again. site_ok is active and healthy, site_sick active but
// failing /health/ready, site_loop crash-looping (systemd reports
// "activating" between restarts), site_failed "failed", and site_stopped
// "inactive". The three that are down must count in the exit status, each
// alerting once per outage. Only site_sick may be restarted, at most
// MAX_RESTART_ATTEMPTS times, with one CRITICAL alert when they run out and
// one "recovered" alert when it answers again. The script skipped every unit
// that was not active, so a crash loop passed the cron check, and its restart
// cooldown, as long as the cron interval, let a dead site be restarted and
// alerted on every run for ever (issue #194).
//
// Bug state: skip units that are not active again, restart activating or
// failed units, alert on every run, or let the restart count reset while the
// site is still down, and this fails.
func TestHealthcheckMultiReportsCrashLoopingUnits(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	scripts, stubs := filepath.Join(tmp, "scripts"), filepath.Join(tmp, "stubs")
	for _, dir := range []string{scripts, stubs} {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "scripts", "deploy", name)) // #nosec G304 -- fixed repository paths
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	sitesConf, stateDir := filepath.Join(tmp, "sites.conf"), filepath.Join(tmp, "state")
	// redirect copies healthcheck-multi.sh with its paths in tmp.
	redirect := func(stateDir string) string {
		t.Helper()
		script := read("healthcheck-multi.sh")
		for old, replacement := range map[string]string{
			`SITES_CONF="/etc/ocms/sites.conf"`: `SITES_CONF="` + sitesConf + `"`,
			`STATE_DIR="/run/ocms-health"`:      `STATE_DIR="` + stateDir + `"`,
		} {
			if strings.Count(script, old) != 1 {
				t.Fatalf("healthcheck-multi.sh no longer has exactly one %q to redirect for the test", old)
			}
			script = strings.Replace(script, old, replacement, 1)
		}
		return script
	}
	writeStubs(t, scripts, map[string]string{"healthcheck-multi.sh": redirect(stateDir), "helper.sh": read("helper.sh")})

	calls := filepath.Join(tmp, "calls.log")
	// systemctl takes the unit from its last argument, so `is-active --quiet
	// <unit>` works too, and prints the state unless --quiet is given. curl
	// answers 200 for the ports in $HEALTHY. mail logs the subject.
	writeStubs(t, stubs, map[string]string{
		"systemctl": `#!/bin/sh
echo "systemctl $*" >> "` + calls + `"
for unit; do :; done
case "$unit" in
  ocms@site_ok|ocms@site_sick) state=active ;;
  ocms@site_loop) state=activating ;;
  ocms@site_failed) state=failed ;;
  *) state=inactive ;;
esac
if [ "$1" = is-active ]; then
  [ "$2" = --quiet ] || echo "$state"
  [ "$state" = active ]
fi
`,
		"curl":     "#!/bin/sh\nfor port in $HEALTHY; do case \"$*\" in *:$port/*) printf 200; exit 0 ;; esac; done\nprintf 503\n",
		"mail":     "#!/bin/sh\necho \"mail $2\" >> \"" + calls + "\"\ncat > /dev/null\n",
		"sleep":    "#!/bin/sh\nexit 0\n",
		"hostname": "#!/bin/sh\necho testhost\n",
	})
	if err := os.WriteFile(sitesConf, []byte("site_ok /srv/ok ocms 8001\nsite_sick /srv/sick ocms 8002\n"+
		"site_loop /srv/loop ocms 8003\nsite_failed /srv/failed ocms 8004\nsite_stopped /srv/stopped ocms 8005\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	check := func(healthy string, wantDown int) string {
		t.Helper()
		cmd := exec.Command(bash, filepath.Join(scripts, "healthcheck-multi.sh")) // #nosec G204 -- copy of a repository script
		cmd.Env = []string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + tmp,
			"ALERT_EMAIL=ops@example.com", "HEALTHY=" + healthy}
		out, err := cmd.CombinedOutput()
		got := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			got = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("healthcheck-multi.sh: %v\n%s", err, out)
		}
		if got != wantDown {
			t.Fatalf("healthcheck-multi.sh exited %d, want %d (sites down)\n%s", got, wantDown, out)
		}
		return string(out)
	}
	first := check("8001", 3)
	for _, want := range []string{"[site_sick] Health check failed (port 8002)",
		"[site_loop] systemd reports ocms@site_loop as activating",
		"[site_failed] systemd reports ocms@site_failed as failed"} {
		if !strings.Contains(first, want) {
			t.Errorf("the first check does not say %q:\n%s", want, first)
		}
	}
	for range 4 {
		check("8001", 3)
	}
	logged, commands := remoteCommands(t, calls)
	count := func(line string) int {
		n := 0
		for _, c := range commands {
			if c == line {
				n++
			}
		}
		return n
	}
	for line, want := range map[string]int{
		"systemctl restart ocms@site_sick":                                3, // MAX_RESTART_ATTEMPTS, however many runs
		"systemctl restart ocms@site_loop":                                0,
		"systemctl restart ocms@site_failed":                              0,
		"systemctl restart ocms@site_ok":                                  0,
		"systemctl restart ocms@site_stopped":                             0,
		"mail ALERT: oCMS site site_sick is down":                         1,
		"mail ALERT: oCMS site site_loop is down":                         1,
		"mail ALERT: oCMS site site_failed is down":                       1,
		"mail CRITICAL: oCMS site site_sick requires manual intervention": 1,
		"mail ALERT: oCMS site site_ok is down":                           0,
		"mail ALERT: oCMS site site_stopped is down":                      0,
	} {
		if got := count(line); got != want {
			t.Errorf("after five checks, %q ran %d times, want %d:\n%s", line, got, want, logged)
		}
	}

	// site_sick answers again: one "recovered" alert, and its state is reset.
	check("8001 8002", 2)
	check("8001 8002", 2)
	logged, commands = remoteCommands(t, calls)
	if got := count("mail oCMS site site_sick recovered"); got != 1 {
		t.Errorf("site_sick's recovery was alerted %d times, want once:\n%s", got, logged)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "site_sick")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("site_sick's restart count was kept after it recovered: %v", err)
	}

	// Other users must not reach the state: in /tmp, a planted .down marker
	// or count silenced a site's alerts and restarts.
	if info, err := os.Stat(stateDir); err != nil || info == nil {
		t.Fatalf("state directory is unavailable: %v", err)
	} else if info.Mode().Perm() != 0o700 {
		t.Errorf("state directory mode = %v; want 0700", info.Mode().Perm())
	}
	linked := filepath.Join(tmp, "linked-state")
	if err := os.Symlink(stateDir, linked); err != nil {
		t.Fatal(err)
	}
	writeStubs(t, scripts, map[string]string{"healthcheck-multi.sh": redirect(linked)})
	cmd := exec.Command(bash, filepath.Join(scripts, "healthcheck-multi.sh")) // #nosec G204 -- copy of a repository script
	cmd.Env = []string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + tmp, "HEALTHY=8001"}
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "Cannot use "+linked+" for the health state") {
		t.Errorf("a symlinked state directory was used: %v\n%s", err, out)
	}
}
