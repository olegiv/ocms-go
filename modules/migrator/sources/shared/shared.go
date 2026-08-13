// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package shared holds helpers common to every migrator source: source-database
// identifier sanitizing, upload-directory resolution, slug de-duplication and
// the hardened filesystem paths used when copying media out of a foreign CMS
// install.
//
// These live here rather than in each source package so that adding a source
// does not mean copy-pasting the security-sensitive parts.
package shared

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// MaxTablePrefixLength is the maximum allowed length for a source table prefix.
const MaxTablePrefixLength = 20

// SanitizeTablePrefix validates that a table prefix contains only safe SQL
// identifier characters (alphanumeric and underscore, max MaxTablePrefixLength)
// and returns the sanitized value.
//
// Source table names cannot be bound as query parameters, so the prefix is
// interpolated into SQL; this is what keeps that safe. Callers must use the
// returned value rather than the input — the string is rebuilt rune by rune so
// static analysis can see the taint is broken.
// MaxIdentifierLength is MySQL's limit for a column or table name.
const MaxIdentifierLength = 64

// SanitizeIdentifier validates a SQL identifier — a column or table name — and
// returns it unchanged, or an error.
//
// Identifiers cannot be bound as parameters, so anything interpolated into a
// query must be proven safe first. Today every caller passes a Go literal or a
// value from a package-level table, which is why this was not exploitable; the
// point is that the guarantee now lives in the function rather than in the
// discipline of each call site.
func SanitizeIdentifier(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("invalid identifier: empty")
	}
	if len(name) > MaxIdentifierLength {
		return "", fmt.Errorf("invalid identifier: exceeds maximum length of %d characters", MaxIdentifierLength)
	}
	// Rebuilt rune by rune rather than returned as-is, matching
	// SanitizeTablePrefix. Returning the argument makes the function a no-op to
	// static taint analysis, so a scanner still sees the caller's untrusted
	// string flowing into the query.
	var builder strings.Builder
	builder.Grow(len(name))
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' {
			builder.WriteRune(c)
			continue
		}
		return "", fmt.Errorf("invalid identifier %q: contains invalid character %q", name, c)
	}
	return builder.String(), nil
}

func SanitizeTablePrefix(prefix string) (string, error) {
	if prefix == "" {
		return "", nil
	}
	if len(prefix) > MaxTablePrefixLength {
		return "", fmt.Errorf("invalid table prefix: exceeds maximum length of %d characters", MaxTablePrefixLength)
	}
	var builder strings.Builder
	for _, c := range prefix {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' {
			builder.WriteRune(c)
		} else {
			return "", fmt.Errorf("invalid table prefix: contains invalid character %q", c)
		}
	}
	return builder.String(), nil
}

// EnvOrDefault returns the environment variable value or the default if unset.
func EnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// UploadDir returns the oCMS uploads directory from the environment, or the
// default when unset.
func UploadDir() string {
	if dir := os.Getenv("OCMS_UPLOADS_DIR"); dir != "" {
		return dir
	}
	return "./uploads"
}

// NullString converts a nullable string column to a plain string.
func NullString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// NullInt64 converts a nullable integer column to a plain int64.
func NullInt64(ni sql.NullInt64) int64 {
	if ni.Valid {
		return ni.Int64
	}
	return 0
}

// maxSlugSuffix bounds the linear probe for a free slug before falling back to
// a timestamp suffix.
const maxSlugSuffix = 100

// MakeUniqueSlug returns baseSlug, or baseSlug with a -2, -3, … suffix when the
// slug is already taken. After maxSlugSuffix attempts it falls back to a base36
// nanosecond suffix, which always terminates.
func MakeUniqueSlug(ctx context.Context, queries *store.Queries, baseSlug string) string {
	if _, err := queries.GetPageBySlug(ctx, baseSlug); err != nil {
		return baseSlug
	}

	for i := 2; i <= maxSlugSuffix; i++ {
		slug := baseSlug + "-" + strconv.Itoa(i)
		if _, err := queries.GetPageBySlug(ctx, slug); err != nil {
			return slug
		}
	}

	return baseSlug + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// ReplaceURLs rewrites every old media path in body to its new oCMS URL.
func ReplaceURLs(body string, mediaMap map[string]string) string {
	for oldPath, newPath := range mediaMap {
		body = strings.ReplaceAll(body, oldPath, newPath)
	}
	return body
}

// AllowedMediaMimeTypes defines the MIME types a source may import.
var AllowedMediaMimeTypes = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/gif":       true,
	"image/webp":      true,
	"application/pdf": true,
	"video/mp4":       true,
	"video/webm":      true,
}

// IsAllowedMediaMime reports whether a MIME type may be imported.
func IsAllowedMediaMime(mimeType string) bool {
	return AllowedMediaMimeTypes[mimeType]
}

// MimeTypeFromExt returns the MIME type for a file based on its extension.
//
// Known types are resolved first for consistent cross-platform results —
// mime.TypeByExtension(".webm") returns "audio/webm" on some systems.
func MimeTypeFromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return ""
	}

	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	}

	mimeType := mime.TypeByExtension(ext)
	if mimeType != "" {
		// Strip charset suffix if present (e.g. "text/plain; charset=utf-8").
		if idx := strings.Index(mimeType, ";"); idx != -1 {
			mimeType = strings.TrimSpace(mimeType[:idx])
		}
		return mimeType
	}

	return ""
}

// MediaFile describes one file discovered in a source CMS install.
type MediaFile struct {
	Path     string // Relative path from the files root (e.g. "images/photo.jpg")
	FullPath string // Absolute, symlink-resolved path
	Filename string // Base filename
	Size     int64  // Size in bytes
	MimeType string // Detected MIME type
}

// ResolveMediaRoot validates an admin-supplied files directory and returns its
// absolute, symlink-resolved path.
//
// The files path comes from admin configuration, which limits the risk, but it
// is still attacker-influenced input pointing at the local filesystem — so it
// is cleaned, checked for traversal, confirmed to be a directory, and resolved
// through symlinks before anything reads from it.
func ResolveMediaRoot(filesPath string) (cleanPath, realRoot string, err error) {
	if filesPath == "" {
		return "", "", fmt.Errorf("files path is empty")
	}

	cleanPath = filepath.Clean(filesPath)
	if strings.Contains(cleanPath, "..") {
		return "", "", fmt.Errorf("invalid files path: path traversal detected")
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to access files directory: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("files path is not a directory: %s", cleanPath)
	}

	realRoot, err = filepath.EvalSymlinks(cleanPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve files directory: %w", err)
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve files directory absolute path: %w", err)
	}

	return cleanPath, realRoot, nil
}

// ResolveWithinRoot resolves path through symlinks and confirms it stays inside
// realRoot. It returns the resolved absolute path, or ok=false when the file
// escapes the root, cannot be resolved, or does not exist.
func ResolveWithinRoot(realRoot, path string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(realRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return resolved, true
}

// ScanMediaFiles walks a source CMS files directory and returns every file with
// an importable MIME type. Symlinks are skipped and every entry is confirmed to
// resolve inside the root, so a malicious link cannot pull in files from
// elsewhere on the host.
func ScanMediaFiles(filesPath string) ([]MediaFile, error) {
	cleanPath, realRoot, err := ResolveMediaRoot(filesPath)
	if err != nil {
		return nil, err
	}

	var files []MediaFile

	err = filepath.Walk(cleanPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		// Skip symlinks to prevent importing files outside filesPath.
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		resolvedPath, ok := ResolveWithinRoot(realRoot, path)
		if !ok {
			return nil
		}

		mimeType := MimeTypeFromExt(path)
		if mimeType == "" || !IsAllowedMediaMime(mimeType) {
			return nil
		}

		relPath, err := filepath.Rel(cleanPath, path)
		if err != nil {
			relPath = filepath.Base(path)
		}

		files = append(files, MediaFile{
			Path:     relPath,
			FullPath: resolvedPath,
			Filename: info.Name(),
			Size:     info.Size(),
			MimeType: mimeType,
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan files directory: %w", err)
	}

	return files, nil
}

// copyBufferSize is the chunk size used when copying media into the uploads dir.
const copyBufferSize = 32 * 1024

// SaveNonImageFile copies a non-image file into the uploads directory under its
// media UUID. The filename is sanitized and the destination is validated twice
// — once for the directory and once for the final path — so neither the UUID
// nor the filename can escape the uploads root.
func SaveNonImageFile(src io.ReadSeeker, uploadDir, fileUUID, filename string) error {
	safeFilename, err := util.SanitizeFilename(filename)
	if err != nil {
		return fmt.Errorf("invalid filename %q: %w", filename, err)
	}

	destDir := filepath.Join(uploadDir, "originals", fileUUID)
	if err := util.ValidatePathWithinBase(uploadDir, destDir); err != nil {
		return fmt.Errorf("invalid destination directory: %w", err)
	}

	// Additional inline validation for CodeQL.
	cleanDestDir := filepath.Clean(destDir)
	if strings.Contains(cleanDestDir, "..") || filepath.IsAbs(fileUUID) {
		return fmt.Errorf("invalid destination directory: path traversal detected")
	}

	if err := os.MkdirAll(cleanDestDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	destPath := filepath.Join(cleanDestDir, safeFilename)
	if err := util.ValidatePathWithinBase(uploadDir, destPath); err != nil {
		return fmt.Errorf("invalid destination path: %w", err)
	}

	dest, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() {
		if err := dest.Close(); err != nil {
			slog.Error("failed to close destination file", "path", destPath, "error", err)
		}
	}()

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek source file: %w", err)
	}

	buf := make([]byte, copyBufferSize)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, writeErr := dest.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("failed to write file: %w", writeErr)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("failed to read file: %w", readErr)
		}
	}

	return nil
}
