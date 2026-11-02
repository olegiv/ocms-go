// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Run only in a disposable Linux PID/filesystem namespace. Ordinary test runs
// never switch accounts or exercise privileged process control.
func TestDeploymentCrossAccountBoundaries(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("OCMS_DEPLOY_PRIVILEGE_TEST") != "1" {
		t.Skip("requires explicit disposable-root deployment fixture")
	}
	for _, tool := range []string{"python3", "setpriv", "tar", "gzip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
	uidText, err := exec.Command("id", "-u", "nobody").Output()
	if err != nil {
		t.Fatal(err)
	}
	uid, err := strconv.Atoi(strings.TrimSpace(string(uidText)))
	if err != nil || uid == 0 {
		t.Fatalf("fixture user: %s %v", uidText, err)
	}
	gidText, err := exec.Command("id", "-g", "nobody").Output()
	if err != nil {
		t.Fatal(err)
	}
	gid, err := strconv.Atoi(strings.TrimSpace(string(gidText)))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	// The site account must traverse this test's private parent directory.
	for _, path := range []string{filepath.Dir(tmp), tmp} {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "scripts", "deploy", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	stubs, scripts := filepath.Join(tmp, "stubs"), filepath.Join(tmp, "scripts")
	if err := os.MkdirAll(stubs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{stubs, scripts} {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	calls, registry := filepath.Join(tmp, "calls"), filepath.Join(tmp, "sites.conf")
	writeStubs(t, stubs, map[string]string{
		// The real kernel changes credentials here; this is not a mock signal
		// or a stub that merely records an intended privilege drop.
		"sudo": `#!/bin/sh
[ "$1" != -n ] || shift
[ "$1" != systemctl ] || exec "$@"
[ "$1" = -u ] || exit 90
user=$2; shift 2
[ "$1" != -- ] || shift
exec /usr/bin/setpriv --reuid="$(id -u "$user")" --regid="$(id -g "$user")" --clear-groups -- "$@"
`,
		"date":      "#!/bin/sh\nprintf 20261102_030000\n",
		"systemctl": "#!/bin/sh\necho \"$*\" >> '" + calls + "'\n[ \"$1\" != is-active ] || [ \"$SYSTEMD_ACTIVE\" = 1 ]\n",
		// Python's standard SQLite backup API supplies the sqlite3 CLI seam.
		// Archive and permission behavior use the actual system utilities.
		"sqlite3": `#!/usr/bin/python3
import sqlite3,sys
source=sqlite3.connect(sys.argv[1])
if sys.argv[2].startswith(".backup "):
 target=sqlite3.connect(sys.argv[2][8:].strip("'"));source.backup(target);target.close()
else:
 for row in source.execute(sys.argv[2]): print(*row,sep="|")
source.close()
`,
	})
	redirect := func(source string) string {
		old := `SITES_CONF="/etc/ocms/sites.conf"`
		if strings.Count(source, old) != 1 {
			t.Fatal("site registry seam changed")
		}
		return strings.Replace(source, old, `SITES_CONF="`+registry+`"`, 1)
	}
	ctl := redirect(read("ocmsctl"))
	// Only the fixture's wait duration is shortened. The production duration
	// remains independently checked by the graceful-stop drift test.
	ctl = strings.ReplaceAll(ctl, "[ $waited -lt 60 ]", "[ $waited -lt 2 ]")
	writeStubs(t, scripts, map[string]string{"backup-multi.sh": redirect(read("backup-multi.sh")), "helper.sh": read("helper.sh"), "ocmsctl": ctl})
	for _, dir := range []string{stubs, scripts} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if err := os.Chmod(filepath.Join(dir, entry.Name()), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	env := []string{"PATH=" + stubs + ":/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + tmp}
	run := func(script string, args ...string) ([]byte, error) {
		command := exec.Command("bash", append([]string{filepath.Join(scripts, script)}, args...)...)
		command.Env = env
		return command.CombinedOutput()
	}
	makeSite := func(t *testing.T, name string) string {
		t.Helper()
		site := filepath.Join(tmp, name)
		for _, rel := range []string{"", "data", "backups", "uploads", "custom", "custom/themes"} {
			p := filepath.Join(site, rel)
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(p, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(p, uid, gid); err != nil {
				t.Fatal(err)
			}
		}
		for _, rel := range []string{"uploads/example.txt", "custom/themes/example.txt"} {
			if err := os.WriteFile(filepath.Join(site, rel), []byte("fixture content"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(filepath.Join(site, rel), uid, gid); err != nil {
				t.Fatal(err)
			}
		}
		command := exec.Command("python3", "-c", `import sqlite3,sys;db=sqlite3.connect(sys.argv[1]);db.executescript('CREATE TABLE sample(n); INSERT INTO sample VALUES(42);');db.close()`, filepath.Join(site, "data", "ocms.db"))
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("database fixture: %v %s", err, out)
		}
		if err := os.Chown(filepath.Join(site, "data", "ocms.db"), uid, gid); err != nil {
			t.Fatal(err)
		}
		return site
	}
	setRegistry := func(t *testing.T, site string) {
		t.Helper()
		if err := os.WriteFile(registry, []byte("fixture "+site+" nobody 8080\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("backup_symlink_outputs_and_directory", func(t *testing.T) {
		for _, target := range []string{"ocms_20261102_030000.db", "uploads_20261102_030000.tar.gz", "custom_20261102_030000.tar.gz", "directory"} {
			t.Run(target, func(t *testing.T) {
				site := makeSite(t, strings.ReplaceAll(target, ".", "_"))
				victim := filepath.Join(tmp, "protected-"+strings.ReplaceAll(target, ".", "_"))
				const sentinel = "root-owned sentinel"
				if err := os.WriteFile(victim, []byte(sentinel), 0o600); err != nil {
					t.Fatal(err)
				}
				if target == "directory" {
					protected := filepath.Join(tmp, "protected-dir")
					if err := os.Mkdir(protected, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(filepath.Join(site, "backups")); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(protected, filepath.Join(site, "backups")); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(victim, filepath.Join(site, "backups", target)); err != nil {
					t.Fatal(err)
				}
				setRegistry(t, site)
				out, err := run("backup-multi.sh", "fixture")
				if err == nil {
					t.Errorf("unsafe backup succeeded: %s", out)
				}
				if got, err := os.ReadFile(victim); err != nil || string(got) != sentinel {
					t.Errorf("backup modified foreign file: %q, %v", got, err)
				}
				if target == "directory" {
					entries, err := os.ReadDir(filepath.Join(tmp, "protected-dir"))
					if err != nil || len(entries) != 0 {
						t.Errorf("backup wrote through foreign directory: %v, %v", entries, err)
					}
				}
			})
		}
	})
	t.Run("normal_backup_and_partial_failure", func(t *testing.T) {
		site := makeSite(t, "normal")
		if err := os.WriteFile(registry, []byte("missing "+filepath.Join(tmp, "missing")+" nobody 8081\nfixture "+site+" nobody 8080\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := run("backup-multi.sh")
		if err == nil || !strings.Contains(string(out), "1/2 succeeded") {
			t.Fatalf("aggregate backup: %v %s", err, out)
		}
		for _, archive := range []string{"ocms_20261102_030000.db.gz", "uploads_20261102_030000.tar.gz", "custom_20261102_030000.tar.gz"} {
			p := filepath.Join(site, "backups", archive)
			info, err := os.Stat(p)
			if err != nil || info.Size() == 0 {
				t.Fatalf("normal backup missing: %s %v", archive, err)
			}
			if int(info.Sys().(*syscall.Stat_t).Uid) != uid {
				t.Errorf("backup is not site-owned: %s", archive)
			}
		}
		out, err = exec.Command("tar", "-xOzf", filepath.Join(site, "backups", "uploads_20261102_030000.tar.gz"), "uploads/example.txt").CombinedOutput()
		if err != nil || string(out) != "fixture content" {
			t.Fatalf("archive contents: %q, %v", out, err)
		}
	})
	start := func(t *testing.T, asSite bool, ignoreTERM bool) (*exec.Cmd, <-chan error) {
		t.Helper()
		args := []string{"sleep", "30"}
		if ignoreTERM {
			args = []string{"bash", "-c", `trap '' TERM; exec sleep 30`}
		}
		if asSite {
			args = append([]string{"setpriv", "--reuid=" + strconv.Itoa(uid), "--regid=" + strconv.Itoa(gid), "--clear-groups", "--"}, args...)
		}
		command := exec.Command(args[0], args[1:]...)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait(); close(done) }()
		t.Cleanup(func() { _ = command.Process.Kill(); <-done })
		// Establish credentials/TERM disposition before invoking the controller.
		time.Sleep(100 * time.Millisecond)
		return command, done
	}
	t.Run("foreign_pid_survives", func(t *testing.T) {
		site := makeSite(t, "pid-foreign")
		setRegistry(t, site)
		victim, _ := start(t, false, false)
		if err := os.WriteFile(filepath.Join(site, "ocms.pid"), []byte(strconv.Itoa(victim.Process.Pid)), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := run("ocmsctl", "stop", "fixture")
		if err == nil {
			t.Errorf("foreign stop succeeded: %s", out)
		}
		if err := syscall.Kill(victim.Process.Pid, 0); err != nil {
			t.Errorf("foreign process was terminated: %v", err)
		}
	})
	t.Run("own_pid_graceful_and_forced_stop", func(t *testing.T) {
		for _, force := range []bool{false, true} {
			site := makeSite(t, "pid-own-"+strconv.FormatBool(force))
			setRegistry(t, site)
			process, done := start(t, true, force)
			if err := os.WriteFile(filepath.Join(site, "ocms.pid"), []byte(strconv.Itoa(process.Process.Pid)), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := run("ocmsctl", "stop", "fixture"); err != nil {
				t.Fatalf("own stop: %v %s", err, out)
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("own process survived stop")
			}
		}
	})
	if os.Getenv("OCMS_DEPLOY_BASELINE") == "1" {
		return
	}
	t.Run("invalid_and_stale_pid_and_systemd", func(t *testing.T) {
		site := makeSite(t, "pid-invalid")
		setRegistry(t, site)
		victim, _ := start(t, false, false)
		for _, text := range []string{"-1", "0", "--help", "1\n2", "99999999", ""} {
			if err := os.WriteFile(filepath.Join(site, "ocms.pid"), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := run("ocmsctl", "stop", "fixture"); err != nil {
				t.Fatalf("invalid/stale PID: %v %s", err, out)
			}
			if err := syscall.Kill(victim.Process.Pid, 0); err != nil {
				t.Fatal("invalid PID affected another process")
			}
		}
		if err := os.WriteFile(filepath.Join(site, "ocms.pid"), []byte(strconv.Itoa(victim.Process.Pid)), 0o644); err != nil {
			t.Fatal(err)
		}
		env = append(env, "SYSTEMD_ACTIVE=1")
		if out, err := run("ocmsctl", "stop", "fixture"); err != nil {
			t.Fatalf("systemd delegation: %v %s", err, out)
		}
		data, err := os.ReadFile(calls)
		if err != nil || !strings.Contains(string(data), "stop ocms@fixture") {
			t.Fatalf("systemd stop not delegated: %s %v", data, err)
		}
		if err := syscall.Kill(victim.Process.Pid, 0); err != nil {
			t.Fatal("active systemd stop used PID fallback")
		}
	})
}
