// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestProvisioningKeepsSiteAccountOutsideRootPaths(t *testing.T) {
	nobody, err := user.Lookup("nobody")
	if os.Geteuid() != 0 || err != nil {
		t.Skip("requires disposable root and a nobody account")
	}
	uid, err := strconv.Atoi(nobody.Uid)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := strconv.Atoi(nobody.Gid)
	if err != nil {
		t.Fatal(err)
	}
	for _, swapped := range []bool{false, true} {
		t.Run(strconv.FormatBool(swapped), func(t *testing.T) {
			w := newWorld(t)
			base := t.TempDir()
			for _, path := range []string{filepath.Dir(base), base} {
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			w.server.SitesBase = filepath.Join(base, "vhosts")
			vhost := filepath.Join(w.server.SitesBase, "example.com")
			parent := filepath.Join(vhost, "nested")
			protected := filepath.Join(base, "protected")
			if err := os.MkdirAll(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(protected, 0o700); err != nil {
				t.Fatal(err)
			}
			if swapped {
				for _, path := range []string{vhost, parent} {
					if err := os.Chown(path, uid, gid); err != nil {
						t.Fatal(err)
					}
				}
				w.server.Now = func() time.Time {
					// Existing clock seam places a real site-UID replacement after
					// canonical validation and before the first provisioning write.
					for _, argv := range [][]string{{"/bin/mv", parent, parent + ".saved"}, {"/bin/ln", "-s", protected, parent}} {
						cmd := exec.Command(argv[0], argv[1:]...)
						cmd.Env = []string{"PATH=/usr/bin:/bin"}
						cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("site-account replacement: %v: %s", err, out)
						}
					}
					return time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
				}
			}
			dir := filepath.Join(parent, "ocms")
			err := run(t.Context(), w.server, []string{"site", "add", "example.com", nobody.Username, "--dir", dir}, nil)
			if swapped {
				if err == nil {
					t.Error("provisioning followed the site account's replacement")
				}
				if entries, err := os.ReadDir(protected); err != nil || len(entries) != 0 {
					t.Errorf("root wrote through site-controlled ancestor: %v, %v", entries, err)
				}
				if len(w.systemctl) != 0 {
					t.Errorf("failed provisioning reconfigured systemd: %v", w.systemctl)
				}
				return
			}
			if err != nil {
				t.Fatalf("provision under a root-owned parent: %v", err)
			}
			// The selected group differs from nobody's primary group. Root
			// creation and explicit group ownership must remain supported.
			for path, mode := range map[string]os.FileMode{"": 0o750, ".env": 0o600, "backups": 0o700, "custom": 0o755, "custom/themes": 0o755} {
				info, err := os.Stat(filepath.Join(dir, path))
				if err != nil {
					t.Fatal(err)
				}
				owner := info.Sys().(*syscall.Stat_t)
				if int(owner.Uid) != uid || int(owner.Gid) != os.Getgid() || info.Mode().Perm() != mode {
					t.Errorf("%s: owner=%d:%d mode=%o", path, owner.Uid, owner.Gid, info.Mode().Perm())
				}
			}
		})
	}
}
