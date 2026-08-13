// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package shared

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestResolveMediaRoot(t *testing.T) {
	dir := t.TempDir()

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

	if err := SaveNonImageFile(src, uploadDir, "uuid-1", "report.pdf"); err != nil {
		t.Fatalf("SaveNonImageFile() error = %v", err)
	}

	written, err := os.ReadFile(filepath.Join(uploadDir, "originals", "uuid-1", "report.pdf"))
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

	if err := SaveNonImageFile(src, uploadDir, "uuid-2", "report..pdf"); err != nil {
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
			if err := SaveNonImageFile(src, uploadDir, "uuid-3", name); err == nil {
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
