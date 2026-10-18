// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

// LockMediaFiles writes an original file for each media UUID under uploadDir
// and makes the originals directory read-only, so removing the media's
// directories fails while its database rows can still be deleted. It returns
// those directories, which a failed removal leaves behind. It skips the test
// where permissions do not stop the removal (root, Windows) and restores them
// in cleanup so the directory can be removed.
func LockMediaFiles(t *testing.T, uploadDir string, uuids ...string) []string {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop file removal here")
	}
	originals := filepath.Join(uploadDir, model.OriginalsDir)
	dirs := make([]string, 0, len(uuids))
	for _, uuid := range uuids {
		dir := filepath.Join(originals, uuid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "file.pdf"), []byte("pdf"), 0o644); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	if err := os.Chmod(originals, 0o555); err != nil { // #nosec G302 -- test directory made read-only on purpose
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(originals, 0o755); err != nil { // #nosec G302 -- restores the test directory
			t.Error(err)
		}
	})
	return dirs
}
