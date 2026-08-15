// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package shared

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

const testMediaUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func TestRoutableDefaultLanguageRequiresOneSafeActiveDefault(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	queries := store.New(db)
	defaultLanguage, err := RoutableDefaultLanguage(ctx, queries)
	if err != nil || defaultLanguage.Code != "en" {
		t.Fatalf("RoutableDefaultLanguage() = %+v, %v", defaultLanguage, err)
	}

	if _, err := db.ExecContext(ctx, `UPDATE languages SET code = 'admin' WHERE is_default = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := RoutableDefaultLanguage(ctx, queries); err == nil {
		t.Fatal("reserved default language was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE languages SET code = 'en' WHERE is_default = 1`); err != nil {
		t.Fatal(err)
	}
	_, err = queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsDefault: true, IsActive: true,
		Direction: "ltr", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RoutableDefaultLanguage(ctx, queries); err == nil {
		t.Fatal("multiple default languages were accepted")
	}
}

func trustMediaRoot(t *testing.T, root string) {
	t.Helper()
	t.Setenv(EnvAllowedFileRoots, root)
	t.Setenv("DRUPAL_FILES", "")
	t.Setenv("ELEFANT_FILES", "")
}

func TestSanitizeTablePrefix(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		want    string
		wantErr bool
	}{
		{"empty is allowed", "", "", false},
		{"simple prefix", "drupal_", "drupal_", false},
		{"alphanumeric", "d8site2", "d8site2", false},
		{"mixed case", "Drupal_", "Drupal_", false},
		{"sql injection attempt", "drop;--", "", true},
		{"backtick escape", "a`b", "", true},
		{"space", "a b", "", true},
		{"hyphen", "a-b", "", true},
		{"quote", `a"b`, "", true},
		{"exactly at the limit", strings.Repeat("a", MaxTablePrefixLength), strings.Repeat("a", MaxTablePrefixLength), false},
		{"one over the limit", strings.Repeat("a", MaxTablePrefixLength+1), "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizeTablePrefix(tt.prefix)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SanitizeTablePrefix(%q) error = %v, wantErr %v", tt.prefix, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("SanitizeTablePrefix(%q) = %q, want %q", tt.prefix, got, tt.want)
			}
		})
	}
}

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("OCMS_TEST_SHARED_VALUE", "set")
	if got := EnvOrDefault("OCMS_TEST_SHARED_VALUE", "fallback"); got != "set" {
		t.Errorf("EnvOrDefault() = %q, want %q", got, "set")
	}
	if got := EnvOrDefault("OCMS_TEST_SHARED_UNSET", "fallback"); got != "fallback" {
		t.Errorf("EnvOrDefault() = %q, want %q", got, "fallback")
	}
}

func TestIsSafeImportedAliasPath(t *testing.T) {
	for _, alias := range []string{"About_Us", "News/Archive", "о-нас/Команда"} {
		if !IsSafeImportedAliasPath(alias) {
			t.Errorf("IsSafeImportedAliasPath(%q) = false, want true", alias)
		}
	}
	for _, alias := range []string{"", "/about", "about/", "a//b", "a/../b", "https://example.com", "a?x=1", "a#x", "a b"} {
		if IsSafeImportedAliasPath(alias) {
			t.Errorf("IsSafeImportedAliasPath(%q) = true, want false", alias)
		}
	}
}

// TestIsSafeImportedAliasPathCountsCharactersNotBytes fails if the length
// limit goes back to counting UTF-8 bytes: a Cyrillic alias occupies two bytes
// per character, so a byte-counted limit rejects paths at 128 characters that
// the source site stores and serves within its own 255-character column.
func TestIsSafeImportedAliasPathCountsCharactersNotBytes(t *testing.T) {
	atLimit := strings.Repeat("о", MaxImportedAliasLength)
	if len(atLimit) <= MaxImportedAliasLength {
		t.Fatalf("alias is %d bytes; it must exceed the limit in bytes to test rune counting", len(atLimit))
	}
	if !IsSafeImportedAliasPath(atLimit) {
		t.Errorf("IsSafeImportedAliasPath() rejected a %d-character Unicode alias at the limit",
			MaxImportedAliasLength)
	}
	if IsSafeImportedAliasPath(atLimit + "о") {
		t.Errorf("IsSafeImportedAliasPath() accepted a %d-character alias", MaxImportedAliasLength+1)
	}
	if IsSafeImportedAliasPath(strings.Repeat("x", MaxImportedAliasLength+1)) {
		t.Errorf("IsSafeImportedAliasPath() accepted a %d-character ASCII alias", MaxImportedAliasLength+1)
	}
}

func TestUploadDir(t *testing.T) {
	t.Setenv("OCMS_UPLOADS_DIR", "/custom/uploads")
	if got := UploadDir(); got != "/custom/uploads" {
		t.Errorf("UploadDir() = %q, want %q", got, "/custom/uploads")
	}
	t.Setenv("OCMS_UPLOADS_DIR", "")
	if got := UploadDir(); got != "./uploads" {
		t.Errorf("UploadDir() = %q, want %q", got, "./uploads")
	}
}

func TestNullHelpers(t *testing.T) {
	if got := NullString(sql.NullString{String: "x", Valid: true}); got != "x" {
		t.Errorf("NullString() = %q, want %q", got, "x")
	}
	if got := NullString(sql.NullString{String: "x"}); got != "" {
		t.Errorf("NullString() on an invalid value = %q, want empty", got)
	}
	if got := NullInt64(sql.NullInt64{Int64: 7, Valid: true}); got != 7 {
		t.Errorf("NullInt64() = %d, want 7", got)
	}
	if got := NullInt64(sql.NullInt64{Int64: 7}); got != 0 {
		t.Errorf("NullInt64() on an invalid value = %d, want 0", got)
	}
}

func TestMimeTypeFromExt(t *testing.T) {
	tests := []struct{ path, want string }{
		{"a.jpg", "image/jpeg"},
		{"a.JPEG", "image/jpeg"},
		{"a.png", "image/png"},
		{"a.gif", "image/gif"},
		{"a.webp", "image/webp"},
		{"a.pdf", "application/pdf"},
		{"a.mp4", "video/mp4"},
		// Explicitly pinned: mime.TypeByExtension returns audio/webm on some systems.
		{"a.webm", "video/webm"},
		{"noext", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := MimeTypeFromExt(tt.path); got != tt.want {
				t.Errorf("MimeTypeFromExt(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsAllowedMediaMime(t *testing.T) {
	if !IsAllowedMediaMime("image/jpeg") {
		t.Error("image/jpeg should be importable")
	}
	if IsAllowedMediaMime("application/x-msdownload") {
		t.Error("an executable MIME type must never be importable")
	}
	if IsAllowedMediaMime("text/html") {
		t.Error("text/html must not be importable")
	}
}

func TestReplaceURLs(t *testing.T) {
	got := ReplaceURLs("see /files/a.jpg and /files/b.png",
		map[string]string{"/files/a.jpg": "/uploads/a", "/files/b.png": "/uploads/b"})
	if !strings.Contains(got, "/uploads/a") || !strings.Contains(got, "/uploads/b") {
		t.Errorf("ReplaceURLs() = %q, want both paths rewritten", got)
	}
	if unchanged := ReplaceURLs("nothing here", nil); unchanged != "nothing here" {
		t.Errorf("ReplaceURLs() with no map = %q, want it unchanged", unchanged)
	}
}

func TestReplaceURLsRewritesPercentEncodedPaths(t *testing.T) {
	got := ReplaceURLs(
		`<img src="/files/My%20Photo.jpg?size=large#hero"><a href="/files/%d0%a4%d0%b0%d0%b9%d0%bb.pdf">PDF</a>`,
		map[string]string{
			"/files/My Photo.jpg": "/uploads/photo.jpg",
			"/files/Файл.pdf":     "/uploads/file.pdf",
		},
	)
	want := `<img src="/uploads/photo.jpg?size=large#hero"><a href="/uploads/file.pdf">PDF</a>`
	if got != want {
		t.Fatalf("ReplaceURLs() = %q, want %q", got, want)
	}
}

func TestReplaceURLsPrefersEscapedPathOnPercentLiteralCollision(t *testing.T) {
	got := ReplaceURLs(
		`<img src="/files/My%20Photo.jpg"><a href="/files/My%2520Photo.jpg">literal</a>`,
		map[string]string{
			"/files/My Photo.jpg":   "/uploads/space",
			"/files/My%20Photo.jpg": "/uploads/percent",
		},
	)
	want := `<img src="/uploads/space"><a href="/uploads/percent">literal</a>`
	if got != want {
		t.Fatalf("ReplaceURLs() = %q, want %q", got, want)
	}
}

func TestReplaceURLsUsesLongestPathFirst(t *testing.T) {
	const body = `<img src="/files/a.jpg.webp"><img src="/files/a.jpg">`
	want := `<img src="/uploads/a-webp"><img src="/uploads/a">`
	for range 100 {
		got := ReplaceURLs(body, map[string]string{
			"/files/a.jpg":      "/uploads/a",
			"/files/a.jpg.webp": "/uploads/a-webp",
		})
		if got != want {
			t.Fatalf("ReplaceURLs() = %q, want %q", got, want)
		}
	}
}

func TestResolveMediaRoot(t *testing.T) {
	dir := t.TempDir()
	trustMediaRoot(t, dir)

	cleanPath, realRoot, err := ResolveMediaRoot(dir)
	if err != nil {
		t.Fatalf("ResolveMediaRoot() error = %v", err)
	}
	if cleanPath == "" || realRoot == "" {
		t.Error("ResolveMediaRoot() returned empty paths")
	}

	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	for _, tt := range []struct{ name, path string }{
		{"empty path", ""},
		{"traversal", filepath.Join(dir, "..", "..", "etc")},
		{"missing directory", filepath.Join(dir, "nope")},
		{"file rather than directory", file},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := ResolveMediaRoot(tt.path); err == nil {
				t.Errorf("ResolveMediaRoot(%q) should have returned an error", tt.path)
			}
		})
	}
}

// TestResolveWithinRootRejectsEscape covers the containment check that stops a
// symlinked file inside the source install from pulling in host files.
func TestResolveWithinRootRejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	trustMediaRoot(t, root)

	inside := filepath.Join(root, "ok.txt")
	if err := os.WriteFile(inside, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	link := filepath.Join(root, "escape.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, realRoot, err := ResolveMediaRoot(root)
	if err != nil {
		t.Fatalf("ResolveMediaRoot() error = %v", err)
	}

	if _, ok := ResolveWithinRoot(realRoot, inside); !ok {
		t.Error("a file inside the root should resolve")
	}
	if _, ok := ResolveWithinRoot(realRoot, link); ok {
		t.Error("a symlink pointing outside the root must be rejected")
	}
	if _, ok := ResolveWithinRoot(realRoot, filepath.Join(root, "missing.txt")); ok {
		t.Error("a missing file must be rejected")
	}
}

func TestScanMediaFilesFiltersAndContains(t *testing.T) {
	root := t.TempDir()
	trustMediaRoot(t, root)
	nested := filepath.Join(root, "2026-01")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("failed to create fixture dir: %v", err)
	}

	for _, name := range []string{
		filepath.Join(root, "a.jpg"),
		filepath.Join(nested, "b.png"),
		filepath.Join(root, "script.php"),
		filepath.Join(root, "notes.txt"),
	} {
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatalf("failed to write fixture: %v", err)
		}
	}

	files, err := ScanMediaFiles(root)
	if err != nil {
		t.Fatalf("ScanMediaFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("ScanMediaFiles() found %d files, want 2 (only importable MIME types)", len(files))
	}

	byName := make(map[string]MediaFile, len(files))
	for _, f := range files {
		byName[f.Filename] = f
	}
	if _, ok := byName["script.php"]; ok {
		t.Error("a PHP file must never be importable")
	}
	if got := byName["b.png"].Path; got != filepath.Join("2026-01", "b.png") {
		t.Errorf("nested file Path = %q, want it relative to the root", got)
	}
	if byName["a.jpg"].MimeType != "image/jpeg" {
		t.Errorf("MimeType = %q, want image/jpeg", byName["a.jpg"].MimeType)
	}
}

func TestMediaRootScansAndOpensRelativeFiles(t *testing.T) {
	root := t.TempDir()
	trustMediaRoot(t, root)
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "report.pdf"), []byte("pdf"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mediaRoot, err := OpenMediaRoot(root)
	if err != nil {
		t.Fatalf("OpenMediaRoot() error = %v", err)
	}
	defer func() { _ = mediaRoot.Close() }()

	files, err := mediaRoot.Scan()
	if err != nil {
		t.Fatalf("MediaRoot.Scan() error = %v", err)
	}
	if len(files) != 1 || files[0].Path != filepath.Join("nested", "report.pdf") {
		t.Fatalf("MediaRoot.Scan() = %+v, want one relative report path", files)
	}

	file, err := mediaRoot.Open(files[0].Path)
	if err != nil {
		t.Fatalf("MediaRoot.Open() error = %v", err)
	}
	data, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || string(data) != "pdf" {
		t.Fatalf("MediaRoot.Open() read = (%q, %v), want pdf", data, err)
	}
	for _, path := range []string{
		"../outside.pdf",
		"nested/../outside.pdf",
		"./nested/report.pdf",
		"/etc/passwd",
	} {
		if _, err := mediaRoot.Open(path); err == nil {
			t.Errorf("MediaRoot.Open(%q) accepted an escaping path", path)
		}
	}
}

func TestMediaRootOpenRejectsSymlinkEscapeAfterOpen(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	trustMediaRoot(t, root)

	mediaRoot, err := OpenMediaRoot(root)
	if err != nil {
		t.Fatalf("OpenMediaRoot() error = %v", err)
	}
	defer func() { _ = mediaRoot.Close() }()

	target := filepath.Join(outside, "secret.pdf")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(root, "late-link.pdf")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := mediaRoot.Open("late-link.pdf"); err == nil {
		t.Fatal("MediaRoot.Open() followed a post-validation symlink outside its root")
	}
}

func TestMediaRootHandleSurvivesCandidatePathReplacement(t *testing.T) {
	trustedRoot := t.TempDir()
	originalDir := filepath.Join(trustedRoot, "files")
	outsideDir := t.TempDir()
	trustMediaRoot(t, trustedRoot)

	if err := os.Mkdir(originalDir, 0o755); err != nil {
		t.Fatalf("Mkdir original: %v", err)
	}
	if err := os.WriteFile(filepath.Join(originalDir, "safe.pdf"), []byte("safe"), 0o600); err != nil {
		t.Fatalf("WriteFile safe: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "outside.pdf"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}

	mediaRoot, err := OpenMediaRoot(originalDir)
	if err != nil {
		t.Fatalf("OpenMediaRoot() error = %v", err)
	}
	defer func() { _ = mediaRoot.Close() }()

	movedDir := filepath.Join(trustedRoot, "files-moved")
	if err := os.Rename(originalDir, movedDir); err != nil {
		t.Fatalf("Rename candidate directory: %v", err)
	}
	if err := os.Symlink(outsideDir, originalDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	files, err := mediaRoot.Scan()
	if err != nil {
		t.Fatalf("MediaRoot.Scan() after path replacement error = %v", err)
	}
	if len(files) != 1 || files[0].Filename != "safe.pdf" {
		t.Fatalf("MediaRoot.Scan() after replacement = %+v, want original safe file only", files)
	}
	if _, err := mediaRoot.Open("outside.pdf"); err == nil {
		t.Fatal("MediaRoot.Open() escaped through the replacement symlink")
	}
}

func TestOpenMediaRootRejectsTrustedRootReplacementDuringPolicyResolution(t *testing.T) {
	parent := t.TempDir()
	trustedDir := filepath.Join(parent, "trusted")
	if err := os.Mkdir(trustedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	movedDir := filepath.Join(parent, "trusted-original")
	outsideDir := t.TempDir()

	policy, err := normalizeTrustedFileRootWith(trustedDir, func() {
		if renameErr := os.Rename(trustedDir, movedDir); renameErr != nil {
			t.Fatalf("rename trusted root: %v", renameErr)
		}
		if symlinkErr := os.Symlink(outsideDir, trustedDir); symlinkErr != nil {
			t.Fatalf("replace trusted root with symlink: %v", symlinkErr)
		}
	})
	if err != nil {
		t.Fatalf("normalize trusted root: %v", err)
	}
	root, err := openMediaRootLocations([]trustedMediaLocation{{
		policyRoot: policy.policyRoot, canonicalRoot: policy.canonicalRoot,
		rootInfo: policy.info, relative: ".",
	}})
	if root != nil {
		_ = root.Close()
		t.Fatal("openMediaRootLocations() returned a root after trusted-root replacement")
	}
	if err == nil {
		t.Fatal("openMediaRootLocations() accepted a replacement trusted-root symlink")
	}
}

func TestSaveNonImageFile(t *testing.T) {
	uploadDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "report.pdf")
	if err := os.WriteFile(srcPath, []byte("pdf-bytes"), 0o600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("failed to open fixture: %v", err)
	}
	defer func() { _ = src.Close() }()

	if err := SaveNonImageFile(src, uploadDir, testMediaUUID, "report.pdf"); err != nil {
		t.Fatalf("SaveNonImageFile() error = %v", err)
	}

	written, err := os.ReadFile(filepath.Join(uploadDir, "originals", testMediaUUID, "report.pdf"))
	if err != nil {
		t.Fatalf("saved file not found: %v", err)
	}
	if string(written) != "pdf-bytes" {
		t.Errorf("saved content = %q, want %q", written, "pdf-bytes")
	}
}

// TestSaveNonImageFileAllowsDoubleDotInFilename guards a real regression: the
// traversal check applies to the destination directory, so a legitimate
// filename containing ".." must still be accepted.
func TestSaveNonImageFileAllowsDoubleDotInFilename(t *testing.T) {
	uploadDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "weird.pdf")
	if err := os.WriteFile(srcPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("failed to open fixture: %v", err)
	}
	defer func() { _ = src.Close() }()

	if err := SaveNonImageFile(src, uploadDir, testMediaUUID, "report..pdf"); err != nil {
		t.Errorf("SaveNonImageFile() rejected a legitimate double-dot filename: %v", err)
	}
}

func TestSaveNonImageFileRejectsTraversal(t *testing.T) {
	uploadDir := t.TempDir()
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "x.bin")
	if err := os.WriteFile(srcPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("failed to open fixture: %v", err)
	}
	defer func() { _ = src.Close() }()

	for _, name := range []string{"../escape.bin", "/etc/passwd", ".."} {
		t.Run(name, func(t *testing.T) {
			if err := SaveNonImageFile(src, uploadDir, testMediaUUID, name); err == nil {
				// filepath.Base reduces "../escape.bin" to "escape.bin", which is
				// safe; what must never happen is a write outside uploadDir.
				escaped := filepath.Join(uploadDir, "..", "escape.bin")
				if _, statErr := os.Stat(escaped); statErr == nil {
					t.Errorf("filename %q escaped the uploads directory", name)
				}
			}
		})
	}
}

type partialReadFailure struct {
	done bool
}

type seekFailure struct{}

func (*seekFailure) Seek(int64, int) (int64, error) { return 0, errors.New("seek failed") }
func (*seekFailure) Read([]byte) (int, error)       { return 0, io.EOF }

func TestSaveNonImageFileRemovesStaleOutputOnSeekFailure(t *testing.T) {
	uploadDir := t.TempDir()
	staleDir := filepath.Join(uploadDir, "originals", testMediaUUID)
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "partial.pdf"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := SaveNonImageFile(&seekFailure{}, uploadDir, testMediaUUID, "report.pdf")
	if err == nil || !strings.Contains(err.Error(), "seek failed") {
		t.Fatalf("SaveNonImageFile() error = %v, want seek failure", err)
	}
	if _, statErr := os.Stat(staleDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stale output survived seek failure: %v", statErr)
	}
}

func (r *partialReadFailure) Seek(int64, int) (int64, error) {
	r.done = false
	return 0, nil
}

func (r *partialReadFailure) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	n := copy(p, "partial-pdf")
	return n, errors.New("source read failed")
}

func TestSaveNonImageFileRemovesPartialOutputOnCopyFailure(t *testing.T) {
	uploadDir := t.TempDir()
	const mediaUUID = testMediaUUID

	err := SaveNonImageFile(&partialReadFailure{}, uploadDir, mediaUUID, "report.pdf")
	if err == nil || !strings.Contains(err.Error(), "source read failed") {
		t.Fatalf("SaveNonImageFile() error = %v, want source read failure", err)
	}
	for _, storageDir := range model.MediaStorageDirs() {
		path := filepath.Join(uploadDir, storageDir, mediaUUID)
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("partial media path %q remains after copy failure: %v", path, statErr)
		}
	}
}

type retargetingReadFailure struct {
	onFailure func()
	done      bool
}

func (r *retargetingReadFailure) Seek(int64, int) (int64, error) {
	r.done = false
	return 0, nil
}

func (r *retargetingReadFailure) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	n := copy(p, "partial-pdf")
	r.onFailure()
	return n, errors.New("source read failed after retarget")
}

func TestSaveNonImageFileCleanupUsesCapturedCanonicalRoot(t *testing.T) {
	parent := t.TempDir()
	originalRoot := filepath.Join(parent, "uploads-original")
	outsideRoot := filepath.Join(parent, "uploads-outside")
	configuredRoot := filepath.Join(parent, "uploads")
	for _, root := range []string{originalRoot, outsideRoot} {
		if err := os.MkdirAll(filepath.Join(root, model.OriginalsDir, testMediaUUID), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	outsideSentinel := filepath.Join(outsideRoot, model.OriginalsDir, testMediaUUID, "must-remain.pdf")
	if err := os.WriteFile(outsideSentinel, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(originalRoot, configuredRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	reader := &retargetingReadFailure{onFailure: func() {
		if err := os.Remove(configuredRoot); err != nil {
			t.Fatalf("remove configured symlink: %v", err)
		}
		if err := os.Symlink(outsideRoot, configuredRoot); err != nil {
			t.Fatalf("retarget configured symlink: %v", err)
		}
	}}
	canonicalRoot, err := SaveNonImageFileWithCanonicalRoot(
		reader, configuredRoot, testMediaUUID, "report.pdf")
	if err == nil || !strings.Contains(err.Error(), "source read failed after retarget") {
		t.Fatalf("SaveNonImageFileWithCanonicalRoot() error = %v", err)
	}
	wantCanonicalRoot, evalErr := filepath.EvalSymlinks(originalRoot)
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if canonicalRoot != wantCanonicalRoot {
		t.Fatalf("captured root = %q, want %q", canonicalRoot, wantCanonicalRoot)
	}
	if _, statErr := os.Stat(filepath.Join(originalRoot, model.OriginalsDir, testMediaUUID)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial output survived in original root: %v", statErr)
	}
	if data, readErr := os.ReadFile(outsideSentinel); readErr != nil || string(data) != "outside" {
		t.Fatalf("outside sentinel changed: data=%q error=%v", data, readErr)
	}
}

func TestSaveNonImageFileRejectsSymlinkedStorageDirectory(t *testing.T) {
	uploadDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(uploadDir, model.OriginalsDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := SaveNonImageFile(strings.NewReader("private"), uploadDir, testMediaUUID, "report.pdf")
	if err == nil {
		t.Fatal("SaveNonImageFile() followed a storage symlink outside uploads")
	}
	outsidePath := filepath.Join(outsideDir, testMediaUUID, "report.pdf")
	if _, statErr := os.Stat(outsidePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside destination was created: %v", statErr)
	}
}

func TestOpenMediaWriteRootRejectsReplacementSymlink(t *testing.T) {
	parent := t.TempDir()
	uploadDir := filepath.Join(parent, "uploads")
	if err := os.Mkdir(uploadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	movedDir := filepath.Join(parent, "uploads-original")

	root, err := openMediaWriteRootWith(uploadDir, func() {
		if renameErr := os.Rename(uploadDir, movedDir); renameErr != nil {
			t.Fatalf("rename upload root: %v", renameErr)
		}
		if symlinkErr := os.Symlink(outsideDir, uploadDir); symlinkErr != nil {
			t.Fatalf("replace upload root with symlink: %v", symlinkErr)
		}
	})
	if root != nil {
		_ = root.Close()
		t.Fatal("openMediaWriteRootWith() returned a root after path replacement")
	}
	if err == nil {
		t.Fatal("openMediaWriteRootWith() accepted a replacement symlink")
	}
}

func TestParseAllowedFileRoots(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	roots, err := ParseAllowedFileRoots(root + ", " + nested + "," + root)
	if err != nil {
		t.Fatalf("ParseAllowedFileRoots() error = %v", err)
	}
	canonicalNested, err := filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0] != canonicalNested || roots[1] != canonicalRoot {
		t.Errorf("ParseAllowedFileRoots() = %v, want narrowest-first de-duplicated roots", roots)
	}
	fileRoot := filepath.Join(root, "file.pdf")
	if err := os.WriteFile(fileRoot, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		"relative/path",
		string(filepath.Separator),
		filepath.Join(root, "missing"),
		fileRoot,
	} {
		if _, err := ParseAllowedFileRoots(invalid); err == nil {
			t.Errorf("ParseAllowedFileRoots(%q) accepted an unsafe root", invalid)
		}
	}
}

func TestAllowedFileRootSymlinkToFilesystemRootIsRejected(t *testing.T) {
	link := filepath.Join(t.TempDir(), "media")
	filesystemRoot := filepath.VolumeName(link) + string(filepath.Separator)
	if err := os.Symlink(filesystemRoot, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	trustMediaRoot(t, link)

	if _, err := OpenMediaRoot(link); err == nil || !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("OpenMediaRoot(symlink to root) error = %v, want broad-root rejection", err)
	}
}

func TestAllowedFileRootSymlinkToSafeDirectorySupportsNestedCandidate(t *testing.T) {
	realRoot := t.TempDir()
	nested := filepath.Join(realRoot, "site", "files")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "report.pdf"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "configured-media")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	trustMediaRoot(t, link)

	mediaRoot, err := OpenMediaRoot(filepath.Join(link, "site", "files"))
	if err != nil {
		t.Fatalf("OpenMediaRoot(nested under safe symlink root) error = %v", err)
	}
	defer func() { _ = mediaRoot.Close() }()
	files, err := mediaRoot.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Filename != "report.pdf" {
		t.Fatalf("MediaRoot.Scan() = %+v, want nested report", files)
	}
	file, err := mediaRoot.Open(files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || string(data) != "safe" {
		t.Fatalf("nested symlink-root read = (%q, %v, %v), want safe", data, readErr, closeErr)
	}

	roots, err := AllowedFileRoots()
	if err != nil {
		t.Fatal(err)
	}
	canonicalRealRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0] != canonicalRealRoot {
		t.Fatalf("AllowedFileRoots() = %v, want canonical root %q", roots, canonicalRealRoot)
	}
}

func TestResolveMediaRootFailsClosedAndEnforcesContainment(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "site", "files")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	t.Run("no trusted roots", func(t *testing.T) {
		t.Setenv(EnvAllowedFileRoots, "")
		t.Setenv("DRUPAL_FILES", "")
		t.Setenv("ELEFANT_FILES", "")
		if _, _, err := ResolveMediaRoot(nested); err == nil ||
			!strings.Contains(err.Error(), "no trusted media roots") {
			t.Fatalf("ResolveMediaRoot() error = %v, want fail-closed root policy", err)
		}
	})

	t.Run("nested path", func(t *testing.T) {
		trustMediaRoot(t, root)
		clean, real, err := ResolveMediaRoot(nested)
		if err != nil {
			t.Fatalf("ResolveMediaRoot() error = %v", err)
		}
		if clean != real || !filepath.IsAbs(real) {
			t.Errorf("ResolveMediaRoot() = (%q, %q), want one absolute resolved root", clean, real)
		}
	})

	t.Run("sibling prefix", func(t *testing.T) {
		trustMediaRoot(t, root)
		sibling := root + "-other"
		if err := os.MkdirAll(sibling, 0o755); err != nil {
			t.Fatalf("MkdirAll sibling: %v", err)
		}
		if _, _, err := ResolveMediaRoot(sibling); err == nil {
			t.Fatal("ResolveMediaRoot() accepted a sibling sharing the trusted root prefix")
		}
	})
}

func TestResolveMediaRootRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	trustMediaRoot(t, root)

	link := filepath.Join(root, "files")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := ResolveMediaRoot(link); err == nil || !strings.Contains(err.Error(), "escapes trusted root") {
		t.Fatalf("ResolveMediaRoot() error = %v, want symlink escape rejection", err)
	}
}

func TestAllowedFileRootsIncludesLegacySourceRoots(t *testing.T) {
	drupalRoot := t.TempDir()
	elefantRoot := t.TempDir()
	t.Setenv(EnvAllowedFileRoots, "")
	t.Setenv("DRUPAL_FILES", drupalRoot)
	t.Setenv("ELEFANT_FILES", elefantRoot)

	roots, err := AllowedFileRoots()
	if err != nil {
		t.Fatalf("AllowedFileRoots() error = %v", err)
	}
	canonicalDrupalRoot, err := filepath.EvalSymlinks(drupalRoot)
	if err != nil {
		t.Fatal(err)
	}
	canonicalElefantRoot, err := filepath.EvalSymlinks(elefantRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || !containsString(roots, canonicalDrupalRoot) || !containsString(roots, canonicalElefantRoot) {
		t.Errorf("AllowedFileRoots() = %v, want DRUPAL_FILES and ELEFANT_FILES", roots)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// --- Host allowlist policy ---

func TestCheckDBHostAllowedWithEmptyAllowlist(t *testing.T) {
	t.Setenv(EnvAllowedDBHosts, "")
	// A CMS database being migrated from usually lives on a private address, so
	// an unset allowlist deliberately means "no restriction" rather than
	// applying the private-IP denylist used for webhooks.
	for _, host := range []string{"localhost", "10.0.0.5", "db.example.com"} {
		if err := CheckDBHostAllowed(host); err != nil {
			t.Errorf("CheckDBHostAllowed(%q) with no allowlist = %v, want nil", host, err)
		}
	}
}

func TestCheckDBHostAllowedEnforcesList(t *testing.T) {
	t.Setenv(EnvAllowedDBHosts, "db.internal, 10.0.0.5 ,DB.EXAMPLE.COM.")

	for _, host := range []string{"db.internal", "10.0.0.5", "db.example.com", "DB.Example.com"} {
		if err := CheckDBHostAllowed(host); err != nil {
			t.Errorf("CheckDBHostAllowed(%q) = %v, want nil", host, err)
		}
	}
	for _, host := range []string{"evil.example.com", "10.0.0.6", "localhost"} {
		if err := CheckDBHostAllowed(host); err == nil {
			t.Errorf("CheckDBHostAllowed(%q) = nil, want an error", host)
		}
	}
}

func TestCheckDBHostAllowedNormalizesIPv6(t *testing.T) {
	t.Setenv(EnvAllowedDBHosts, "::1")
	for _, host := range []string{"::1", "[::1]", "0:0:0:0:0:0:0:1"} {
		if err := CheckDBHostAllowed(host); err != nil {
			t.Errorf("CheckDBHostAllowed(%q) = %v, want nil after IPv6 normalization", host, err)
		}
	}
}

// TestParseAllowedHostsRejectsMalformedEntries pins the rule that an entry
// carrying a scheme, path or port is an error rather than being silently
// reinterpreted — "example.com:3306" would otherwise read as a wildcard over
// every port on that host.
func TestParseAllowedHostsRejectsMalformedEntries(t *testing.T) {
	for _, entry := range []string{
		"https://db.example.com",
		"db.example.com/path",
		"db.example.com?x=1",
		"user@db.example.com",
		"db example.com",
	} {
		t.Run(entry, func(t *testing.T) {
			if _, err := ParseAllowedHosts(entry); err == nil {
				t.Errorf("ParseAllowedHosts(%q) = nil, want an error", entry)
			}
		})
	}

	allowed, err := ParseAllowedHosts(" , db.example.com , ")
	if err != nil {
		t.Fatalf("ParseAllowedHosts() error = %v", err)
	}
	if len(allowed) != 1 {
		t.Errorf("ParseAllowedHosts() = %v, want a single entry with blanks dropped", allowed)
	}
}

// TestHostShapeRulesAgreeOnBothSides drives one table through both places a
// host is validated: the submitted host and an allowlist entry.
//
// The two are only useful if they agree on what a host is. They did not: the
// submitted side rejected "db.example.com:3306" while ParseAllowedHosts stored
// it verbatim as a key no normalized host could ever equal, so an operator who
// wrote the port into the allowlist got every import refused with an error
// naming the host rather than the bad entry — fail-closed, but invisible.
//
// The old TestParseAllowedHostsRejectsPorts promised this rule in its name and
// never exercised it: all five of its cases were caught by the "/?#@\\ " check,
// and not one contained a colon.
func TestHostShapeRulesAgreeOnBothSides(t *testing.T) {
	cases := []struct {
		host string
		want bool // true = accepted by both sides
	}{
		{"db.example.com", true},
		{"localhost", true},
		{"10.0.0.5", true},
		{"::1", true},
		{"[::1]", true},
		{"2001:db8::1", true},
		{"[2001:db8::1]", true},

		{"db.example.com:3306", false},
		{"[db.example.com]:3306", false},
		{"10.0.0.5:3306", false},
		{"localhost:3306", false},
		{"[::1]:3306", false},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			// Submitted-host side. The allowlist is set to the same value so
			// an accepted host also has to match, isolating shape from policy.
			t.Setenv(EnvAllowedDBHosts, tc.host)
			gotHost := CheckDBHostAllowed(tc.host) == nil

			// Allowlist-entry side.
			_, entryErr := ParseAllowedHosts(tc.host)
			gotEntry := entryErr == nil

			if gotHost != tc.want {
				t.Errorf("CheckDBHostAllowed(%q) accepted = %v, want %v", tc.host, gotHost, tc.want)
			}
			if gotEntry != tc.want {
				t.Errorf("ParseAllowedHosts(%q) accepted = %v, want %v", tc.host, gotEntry, tc.want)
			}
			if gotHost != gotEntry {
				t.Errorf("host %q: submitted-host side accepted = %v but allowlist-entry side accepted = %v; "+
					"the two validators must agree on what a host is", tc.host, gotHost, gotEntry)
			}
		})
	}
}

// TestMakeUniqueSlugAvoidsActiveLanguagePrefixes keeps imported pages
// reachable. The language middleware strips a first path segment matching an
// active language code before the frontend router runs, so a page imported at
// "eng" is answered by the language homepage and never by itself. A suffix
// costs one character; the alternative is importing a dead URL.
func TestMakeUniqueSlugAvoidsActiveLanguagePrefixes(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "eng", Name: "English (legacy)", NativeName: "English", IsActive: true,
		Direction: "ltr", Position: 3, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateLanguage: %v", err)
	}
	if _, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code: "fra", Name: "French (inactive)", NativeName: "Français", IsActive: false,
		Direction: "ltr", Position: 4, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateLanguage: %v", err)
	}

	if got := MakeUniqueSlug(ctx, queries, "eng"); got != "eng-2" {
		t.Errorf("MakeUniqueSlug(%q) = %q, want a suffixed slug the language prefix cannot swallow", "eng", got)
	}
	if got := MakeUniqueSlug(ctx, queries, "fra"); got != "fra" {
		t.Errorf("MakeUniqueSlug(%q) = %q, want an inactive language to leave the slug free", "fra", got)
	}
	if got := MakeUniqueSlug(ctx, queries, "engineering"); got != "engineering" {
		t.Errorf("MakeUniqueSlug(%q) = %q, want an unrelated slug untouched", "engineering", got)
	}
}
