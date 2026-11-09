// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupSiteCreatesPrivateBootstrapCredentials(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable")
	}
	root := t.TempDir()
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "deploy", "setup-site.sh"))
	if err != nil {
		t.Fatal(err)
	}
	stubs := filepath.Join(root, "stubs")
	if err := os.Mkdir(stubs, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStubs(t, stubs, map[string]string{
		"cat": `#!/bin/sh
for env_file in "${MODE_LOG%/*}/ocms"/.env*; do
    if [ -f "$env_file" ]; then
        LC_ALL=C ls -ld -- "$env_file" | cut -d ' ' -f 1 >> "$MODE_LOG"
    fi
done
exec /bin/cat "$@"
`,
		"chown": `#!/bin/sh
LC_ALL=C ls -ld -- "$3/.env" | cut -d ' ' -f 1 >> "$MODE_LOG"
`,
		"id":        "#!/bin/sh\nexit 0\n",
		"systemctl": "#!/bin/sh\nexit 0\n",
		// macOS readlink lacks GNU -m on some versions. The fixture paths
		// are absolute, contain no symlinks or dot segments and need no rewrite.
		"readlink": "#!/bin/sh\n[ \"$1\" = -m ] || exit 1\nprintf '%s\\n' \"$2\"\n",
	})
	source := string(script)
	for from, to := range map[string]string{
		`if [ "$EUID" -ne 0 ]; then`: "if false; then",
		"/var/www/vhosts":            filepath.Join(root, "vhosts"),
		"/etc/ocms/sites.conf":       filepath.Join(root, "sites.conf"),
		"/etc/systemd/system":        filepath.Join(root, "systemd"),
		"/opt/ocms/bin/ocms":         filepath.Join(root, "ocms"),
	} {
		if !strings.Contains(source, from) {
			t.Fatalf("missing fixture seam %q", from)
		}
		source = strings.ReplaceAll(source, from, to)
	}
	setup := filepath.Join(root, "setup-site.sh")
	if err := os.WriteFile(setup, []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, host := range []string{"first.example.test", "second.example.test"} {
		vhost := filepath.Join(root, "vhosts", host)
		if err := os.MkdirAll(vhost, 0o755); err != nil {
			t.Fatal(err)
		}
		modeLog := filepath.Join(vhost, "mode.log")
		cmd := exec.Command(bash, "-c", `umask 022; exec "$@"`, "_", bash, setup, host, "site-user") // #nosec G204 -- disposable copy and synthetic arguments
		cmd.Env = []string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"), "MODE_LOG=" + modeLog}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture setup failed: %v", err)
		}
		mode, err := os.ReadFile(modeLog)
		modes := strings.Fields(string(mode))
		if err != nil || len(modes) < 2 {
			t.Fatalf("could not inspect dotenv at creation and before chown: %q, %v", mode, err)
		}
		for _, observed := range modes {
			if !strings.HasPrefix(observed, "-rw-------") {
				t.Fatalf("dotenv was not private at creation: %q", modes)
			}
		}
		instance := filepath.Join(vhost, "ocms")
		env, err := os.ReadFile(filepath.Join(instance, ".env"))
		if err != nil {
			t.Fatal(err)
		}
		var password string
		for line := range strings.SplitSeq(string(env), "\n") {
			if value, ok := strings.CutPrefix(line, "OCMS_SEED_ADMIN_PASSWORD="); ok {
				password = value
			}
		}
		if len(password) != 48 || seen[password] || strings.Contains(string(out), password) || strings.Contains(string(out), "changeme1234") {
			t.Fatal("bootstrap must generate a unique password without printing it")
		}
		seen[password] = true
		if !strings.Contains(string(env), "\nOCMS_DO_SEED=false\nOCMS_REQUIRE_SEED_ADMIN_PASSWORD=true\n") {
			t.Fatal("generated config must require the password and keep seeding disabled")
		}
		info, err := os.Stat(filepath.Join(instance, "backups"))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("backups directory is not owner-only: %v, %v", info, err)
		}
	}
}

func TestBackupMultiMakesOutputsPrivateAfterSudo(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable")
	}
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 is unavailable")
	}
	root := t.TempDir()
	for _, name := range []string{"data", "uploads", "custom/themes", "backups", "stubs"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"uploads/content.txt", "custom/themes/content.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(root, "data", "ocms.db")
	cmd := exec.Command(sqlite, db, "CREATE TABLE fixture (value TEXT); INSERT INTO fixture VALUES ('private');") // #nosec G204 -- scratch database
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture database: %v\n%s", err, out)
	}
	tar, err := exec.LookPath("tar")
	if err != nil {
		t.Skip("tar is unavailable")
	}
	stubs := filepath.Join(root, "stubs")
	writeStubs(t, stubs, map[string]string{
		// Simulate an inherited default ACL producing a wider archive mode
		// after a correctly restricted umask has been applied.
		"tar": `#!/bin/sh
case "$(umask)" in *077|77) ;; *) exit 91 ;; esac
"$REAL_TAR" "$@" && chmod 640 "$2"
`,
		"id": "#!/bin/sh\nprintf '999999\\n'\n",
		"sudo": `#!/bin/sh
[ "$1" = -n ] && [ "$2" = -u ] && [ "$3" = fixture-user ] && [ "$4" = -- ] || exit 1
shift 4
umask 022
exec "$@"
`,
	})
	registry := filepath.Join(root, "sites.conf")
	if err := os.WriteFile(registry, []byte("fixture "+root+" fixture-user 8199\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "scripts", "deploy", "backup-multi.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "backup-multi.sh")
	if err := os.WriteFile(script, []byte(strings.Replace(string(source), "/etc/ocms/sites.conf", registry, 1)), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(bash, script, "fixture") // #nosec G204 -- sandboxed script paths and mocked sudo
	cmd.Env = []string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"), "REAL_TAR=" + tar}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("backup fixture failed: %v\n%s", err, out)
	}
	dir := filepath.Join(root, "backups")
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("backup directory is not owner-only: %v, %v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		t.Fatalf("want database, uploads and custom backups: %v, %v", entries, err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s is not owner-only: %v, %v", entry.Name(), info, err)
		}
	}
}
