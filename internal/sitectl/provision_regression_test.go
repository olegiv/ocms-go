// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sitectl

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAddSiteRejectsSymlinkAncestorOutsideVhost(t *testing.T) {
	env := newTestEnv(t)
	vhost := filepath.Join(env.SitesBase, "example.com")
	if err := os.MkdirAll(vhost, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(vhost, "nested")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	err := env.AddSite(context.Background(), SiteSpec{
		Domain: "example.com",
		User:   currentUser(t),
		Dir:    filepath.Join(link, "newsite"),
	})
	if err == nil {
		t.Error("AddSite accepted an existing symlink ancestor escaping the vhost")
	}
	if _, err := os.Stat(filepath.Join(outside, "newsite")); !os.IsNotExist(err) {
		t.Errorf("provisioning changed the outside directory: Stat error = %v", err)
	}
}
