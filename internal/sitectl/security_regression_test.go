// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAddSiteRejectsAncestorReplacedAfterValidation(t *testing.T) {
	env := newTestEnv(t)
	vhost := filepath.Join(env.SitesBase, "example.com")
	parent := filepath.Join(vhost, "nested")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	// Now is called after canonicalization and before creating the site.
	// Replacing this entry needs only write access to the vhost directory.
	env.Now = func() time.Time {
		if err := os.Rename(parent, parent+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, parent); err != nil {
			t.Fatal(err)
		}
		return env.now
	}
	err := env.AddSite(t.Context(), SiteSpec{Domain: "example.com", User: currentUser(t), Dir: filepath.Join(parent, "app")})
	if err == nil {
		t.Error("provisioning accepted an ancestor redirected after validation")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Errorf("provisioning wrote outside the vhost: %v, %v", entries, err)
	}
	if len(env.host.calls) != 0 {
		t.Errorf("failed provisioning reached server configuration: %v", env.host.calls)
	}
}

func TestAddSiteRejectsUnsafeCanonicalDirectory(t *testing.T) {
	for _, name := range []string{"safe\nUser=root\nExecStart=/bin/false\n#", "safe\rGroup=root", "safe%h"} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			env := newTestEnv(t)
			vhost := filepath.Join(env.SitesBase, "example.com")
			target := filepath.Join(vhost, name)
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			parent := filepath.Join(vhost, "ordinary")
			if err := os.Symlink(target, parent); err != nil {
				t.Fatal(err)
			}
			err := env.AddSite(t.Context(), SiteSpec{Domain: "example.com", User: currentUser(t), Dir: filepath.Join(parent, "app")})
			if err == nil {
				t.Error("accepted a canonical path containing configuration syntax")
			}
			for _, path := range []string{filepath.Join(target, "app"), filepath.Join(env.SystemdDir, "app@example_com.service.d"), env.registry()} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Errorf("unsafe path changed %q: %v", path, err)
				}
			}
			if len(env.host.calls) != 0 {
				t.Errorf("unsafe path reached systemd: %v", env.host.calls)
			}
		})
	}
}

func TestDeployRefusesUnsafeThemeLinksBeforeUpload(t *testing.T) {
	for _, kind := range []string{"relative file", "absolute file", "directory", "chained", "module source", "uppercase module source", "mixed-case module source"} {
		t.Run(kind, func(t *testing.T) {
			env := newTestEnv(t)
			work := t.TempDir()
			custom := filepath.Join(work, "custom")
			themes := filepath.Join(custom, "themes")
			const secret = "developer-only-fixture"
			writeFiles(t, work, map[string]string{".env": secret, "private/fixture": secret, "custom/themes/mine/theme.json": "{}", "custom/modules/private.go": secret})
			link := filepath.Join(themes, "mine", "leak")
			target := "../../../.env"
			switch kind {
			case "absolute file":
				target = filepath.Join(work, ".env")
			case "directory":
				target = filepath.Join(work, "private")
			case "chained":
				if err := os.Symlink(filepath.Join(work, ".env"), filepath.Join(custom, "shared")); err != nil {
					t.Fatal(err)
				}
				target = "../../shared"
			case "module source":
				target = "../../modules/private.go"
			case "uppercase module source", "mixed-case module source":
				moduleDir := "MODULES"
				if kind == "mixed-case module source" {
					moduleDir = "MoDuLeS"
				}
				// These names alias modules/ on case-insensitive filesystems.
				// Materialize them on other filesystems to test the same policy.
				writeFiles(t, custom, map[string]string{moduleDir + "/private.go": secret})
				target = "../../" + moduleDir + "/private.go"
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			var uploaded []byte
			env.Exec = func(_ context.Context, c Cmd) error {
				env.host.calls = append(env.host.calls, c.String())
				if c.Stdin != nil {
					var err error
					uploaded, err = io.ReadAll(c.Stdin)
					return err
				}
				return nil
			}
			err := env.Deploy(t.Context(), DeployOpts{Host: "server", Sites: []string{"a"}, ThemesOnly: true, ThemesDir: themes})
			if err == nil || !strings.Contains(err.Error(), "packing") {
				t.Errorf("unsafe theme deployment = %v, want a packing error", err)
			}
			if len(env.host.calls) != 0 {
				t.Errorf("unsafe themes reached SSH: %v", env.host.calls)
			}
			if len(uploaded) != 0 {
				zr, err := gzip.NewReader(bytes.NewReader(uploaded))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = zr.Close() }()
				tr := tar.NewReader(zr)
				for {
					_, err := tr.Next()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						break // directory cycles may abort before an archive is complete
					}
					data, err := io.ReadAll(tr)
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Contains(data, []byte(secret)) {
						t.Error("developer-private bytes were sent to the site")
					}
				}
			}
		})
	}
}

func TestArchiveOpenRejectsLinksReplacedAfterResolution(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		t.Run(fmt.Sprint("ancestor=", ancestor), func(t *testing.T) {
			work := t.TempDir()
			custom := filepath.Join(work, "custom")
			writeFiles(t, work, map[string]string{"custom/shared/file": "safe", "private/file": "private"})
			root, err := os.OpenRoot(custom)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			resolved, err := filepath.EvalSymlinks(filepath.Join(custom, "shared", "file"))
			if err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(custom, resolved)
			if err != nil {
				t.Fatal(err)
			}
			link, target := resolved, filepath.Join(work, "private", "file")
			if ancestor {
				link, target = filepath.Dir(link), filepath.Dir(target)
			}
			if err := os.Rename(link, link+".saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			file, err := openArchiveFile(root, rel)
			if file != nil {
				_ = file.Close()
			}
			if err == nil {
				t.Error("archive open followed a link substituted after canonical resolution")
			}
		})
	}
}

func TestBackupArchivePreservesLinksWithoutReadingTargets(t *testing.T) {
	work := t.TempDir()
	writeFiles(t, work, map[string]string{"custom/theme.json": "{}", "private": "private-fixture"})
	if err := os.Symlink("../private", filepath.Join(work, "custom", "link")); err != nil {
		t.Fatal(err)
	}
	var packed bytes.Buffer
	if err := writeTarGz(&packed, filepath.Join(work, "custom"), "custom", false); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(&packed)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	tr := tar.NewReader(zr)
	found := false
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "custom/link" {
			found = true
			if header.Typeflag != tar.TypeSymlink || header.Linkname != "../private" {
				t.Errorf("backup changed the symlink: %+v", header)
			}
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("private-fixture")) {
			t.Error("backup dereferenced a stored symlink")
		}
	}
	if !found {
		t.Error("backup omitted its symlink")
	}
}
