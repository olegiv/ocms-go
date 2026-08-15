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
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// MaxTablePrefixLength is the maximum allowed length for a source table prefix.
const MaxTablePrefixLength = 20

// MaxIdentifierLength is MySQL's limit for a column or table name.
const MaxIdentifierLength = 64

// MaxImportedAliasLength bounds a legacy path stored in page_aliases. Source
// CMS alias columns use the same 255-character limit.
const MaxImportedAliasLength = 255

// RoutableDefaultLanguage returns the single language that may receive
// language-neutral imported content. Legacy databases can contain multiple
// default rows because the schema historically had no unique constraint, so a
// single-row lookup is not sufficient or deterministic.
func RoutableDefaultLanguage(ctx context.Context, queries *store.Queries) (store.Language, error) {
	languages, err := queries.ListLanguages(ctx)
	if err != nil {
		return store.Language{}, fmt.Errorf("list destination languages: %w", err)
	}
	defaults := make([]store.Language, 0, 1)
	for _, language := range languages {
		if language.IsDefault {
			defaults = append(defaults, language)
		}
	}
	if len(defaults) != 1 {
		return store.Language{}, fmt.Errorf("destination must contain exactly one default language; found %d", len(defaults))
	}
	defaultLanguage := defaults[0]
	if !defaultLanguage.IsActive {
		return store.Language{}, fmt.Errorf("default language %q is inactive", defaultLanguage.Code)
	}
	if !util.IsValidLangCode(defaultLanguage.Code) {
		return store.Language{}, fmt.Errorf("default language %q has an invalid route code", defaultLanguage.Code)
	}
	if util.IsReservedLanguageCode(defaultLanguage.Code) {
		return store.Language{}, fmt.Errorf("default language %q uses a reserved route prefix", defaultLanguage.Code)
	}
	return defaultLanguage, nil
}

// IsSafeImportedAliasPath reports whether a source CMS path can be preserved
// verbatim as a local page alias. Imported URLs are intentionally allowed to
// be broader than administrator-created slugs (for example mixed case,
// underscores, and Unicode), while traversal, ambiguous separators, absolute
// URLs, control characters, query strings, and fragments remain forbidden.
func IsSafeImportedAliasPath(alias string) bool {
	// Counted in runes, not bytes: the source column's limit is 255 characters,
	// and this function exists to let Unicode aliases through. Measuring UTF-8
	// bytes would reject a 200-character Cyrillic path the source site serves
	// happily, and the established URL would 404 after the migration.
	if alias == "" || utf8.RuneCountInString(alias) > MaxImportedAliasLength {
		return false
	}
	if strings.ContainsAny(alias, "?#\\") || strings.HasPrefix(alias, "/") || strings.HasSuffix(alias, "/") {
		return false
	}
	if strings.Contains(alias, "//") || strings.Contains(alias, ":") {
		return false
	}
	for _, r := range alias {
		if r < 0x20 || r == 0x7f || unicode.IsSpace(r) {
			return false
		}
	}
	for _, segment := range strings.Split(alias, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

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

// SanitizeTablePrefix validates that a table prefix contains only safe SQL
// identifier characters (alphanumeric and underscore, max MaxTablePrefixLength)
// and returns the sanitized value.
//
// Source table names cannot be bound as query parameters, so the prefix is
// interpolated into SQL; this is what keeps that safe. Callers must use the
// returned value rather than the input — the string is rebuilt rune by rune so
// static analysis can see the taint is broken.
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
//
// "Taken" means taken by a page slug *or* by an existing page alias. Checking
// only slugs let an imported page claim a value that was already some other
// page's alias: the frontend resolves a slug first and only falls back to
// GetPublishedPageByAlias, so the old URL silently began serving the imported
// page instead of its real destination.
func MakeUniqueSlug(ctx context.Context, queries *store.Queries, baseSlug string) string {
	return MakeUniqueSlugWithGuard(ctx, queries, baseSlug, nil)
}

// MakeUniqueSlugWithGuard extends MakeUniqueSlug with a destination-route
// ownership predicate. The database protects page/alias ownership, while the
// caller can also reject core, module, or redirect paths that would make the
// stored page unreachable.
func MakeUniqueSlugWithGuard(
	ctx context.Context,
	queries *store.Queries,
	baseSlug string,
	available func(string) bool,
) string {
	isAvailable := func(slug string) bool {
		return slugIsFree(ctx, queries, slug) && (available == nil || available(slug))
	}
	if isAvailable(baseSlug) {
		return baseSlug
	}

	for i := 2; i <= maxSlugSuffix; i++ {
		slug := baseSlug + "-" + strconv.Itoa(i)
		if isAvailable(slug) {
			return slug
		}
	}

	// A route guard may reject the entire base family (for example an enabled
	// `/news*` redirect also owns `news-2`, `news-3`, and so on). Switch to an
	// unrelated deterministic-safe family and still apply both ownership
	// checks; never return an unchecked fallback.
	fallbackBase := "imported-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if isAvailable(fallbackBase) {
		return fallbackBase
	}
	for i := 2; i <= maxSlugSuffix; i++ {
		fallback := fallbackBase + "-" + strconv.Itoa(i)
		if isAvailable(fallback) {
			return fallback
		}
	}
	return ""
}

// slugIsFree reports whether a slug is claimed by neither a page nor an alias.
//
// A lookup failure counts as "taken" rather than "free". The previous form read
// any error — including a transient SQLite BUSY — as "no such page" and handed
// the caller a slug that was already in use, turning a momentary database blip
// into a permanently shadowed URL. Treating it as taken costs only a suffix.
func slugIsFree(ctx context.Context, queries *store.Queries, slug string) bool {
	exists, err := queries.SlugOrAliasExists(ctx, store.SlugOrAliasExistsParams{
		Slug:  slug,
		Alias: slug,
	})
	if err != nil {
		return false
	}
	return exists == 0
}

// ReplaceURLs rewrites every old media path in body to its new oCMS URL.
func ReplaceURLs(body string, mediaMap map[string]string) string {
	type replacement struct {
		old      string
		match    string
		new      string
		priority int
	}
	replacements := make([]replacement, 0, len(mediaMap)*2)
	for oldPath, newPath := range mediaMap {
		for priority, candidate := range []string{(&url.URL{Path: oldPath}).EscapedPath(), oldPath} {
			if candidate == "" {
				continue
			}
			replacements = append(replacements, replacement{
				old: candidate, match: normalizePercentEscapes(candidate), new: newPath, priority: priority,
			})
		}
	}
	sort.Slice(replacements, func(i, j int) bool {
		switch {
		case len(replacements[i].match) != len(replacements[j].match):
			return len(replacements[i].match) > len(replacements[j].match)
		case replacements[i].priority != replacements[j].priority:
			// A URL-escaped raw path wins over a different filename whose
			// literal percent bytes happen to spell the same URL.
			return replacements[i].priority < replacements[j].priority
		case replacements[i].match != replacements[j].match:
			return replacements[i].match < replacements[j].match
		default:
			return replacements[i].new < replacements[j].new
		}
	})

	byFirstByte := make(map[byte][]replacement)
	seen := make(map[string]struct{}, len(replacements))
	for _, item := range replacements {
		if _, exists := seen[item.match]; exists {
			continue
		}
		seen[item.match] = struct{}{}
		byFirstByte[item.match[0]] = append(byFirstByte[item.match[0]], item)
	}
	if len(byFirstByte) == 0 {
		return body
	}

	normalizedBody := normalizePercentEscapes(body)
	var rewritten strings.Builder
	rewritten.Grow(len(body))
	for offset := 0; offset < len(body); {
		matched := false
		for _, item := range byFirstByte[normalizedBody[offset]] {
			if strings.HasPrefix(normalizedBody[offset:], item.match) {
				rewritten.WriteString(item.new)
				offset += len(item.old)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		rewritten.WriteByte(body[offset])
		offset++
	}
	return rewritten.String()
}

func normalizePercentEscapes(value string) string {
	var normalized strings.Builder
	normalized.Grow(len(value))
	for i := 0; i < len(value); i++ {
		normalized.WriteByte(value[i])
		if value[i] != '%' || i+2 >= len(value) || !isHexByte(value[i+1]) || !isHexByte(value[i+2]) {
			continue
		}
		normalized.WriteByte(toUpperHex(value[i+1]))
		normalized.WriteByte(toUpperHex(value[i+2]))
		i += 2
	}
	return normalized.String()
}

func isHexByte(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func toUpperHex(value byte) byte {
	if value >= 'a' && value <= 'f' {
		return value - ('a' - 'A')
	}
	return value
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
	Path string // Relative path from the files root (e.g. "images/photo.jpg")
	// FullPath is retained for compatibility with older source implementations.
	// New importers must keep a MediaRoot open and call MediaRoot.Open(Path), so
	// filesystem access remains capability-scoped beneath the trusted root.
	FullPath string
	Filename string // Base filename
	Size     int64  // Size in bytes
	MimeType string // Detected MIME type
}

// MediaRoot is a capability-scoped view of one validated source-media tree.
// All subsequent scanning and opening uses relative paths through os.Root, so
// a path replacement or symlink race cannot escape the trusted root after the
// initial policy check.
type MediaRoot struct {
	root *os.Root
	path string
}

// OpenMediaRoot validates filesPath against the trusted-root policy and opens
// a root-relative filesystem handle. Callers must Close it.
func OpenMediaRoot(filesPath string) (*MediaRoot, error) {
	locations, err := trustedMediaLocations(filesPath)
	if err != nil {
		return nil, err
	}
	return openMediaRootLocations(locations)
}

func openMediaRootLocations(locations []trustedMediaLocation) (*MediaRoot, error) {
	var openErrors []error
	for _, location := range locations {
		// This absolute open receives only an operator-controlled allowlist
		// entry. The submitted path is used solely as a rebuilt relative name
		// for OpenRoot, whose capability boundary rejects symlink escapes.
		parentRoot, err := os.OpenRoot(filepath.Dir(location.canonicalRoot))
		if err != nil {
			openErrors = append(openErrors,
				fmt.Errorf("open trusted media parent for %q: %w", location.policyRoot, err))
			continue
		}
		trustedRoot, openErr := parentRoot.OpenRoot(filepath.ToSlash(filepath.Base(location.canonicalRoot)))
		parentCloseErr := parentRoot.Close()
		if openErr != nil {
			openErrors = append(openErrors,
				errors.Join(fmt.Errorf("open trusted media root %q: %w", location.policyRoot, openErr), parentCloseErr))
			continue
		}
		if parentCloseErr != nil {
			_ = trustedRoot.Close()
			openErrors = append(openErrors,
				fmt.Errorf("close trusted media parent for %q: %w", location.policyRoot, parentCloseErr))
			continue
		}
		openedInfo, err := trustedRoot.Stat(".")
		if err != nil || !os.SameFile(location.rootInfo, openedInfo) {
			closeErr := trustedRoot.Close()
			if err != nil {
				openErrors = append(openErrors,
					fmt.Errorf("inspect trusted media root %q: %w", location.policyRoot, err))
			} else {
				openErrors = append(openErrors,
					fmt.Errorf("trusted media root %q changed while it was being opened", location.policyRoot))
			}
			if closeErr != nil {
				openErrors = append(openErrors,
					fmt.Errorf("close trusted media root %q: %w", location.policyRoot, closeErr))
			}
			continue
		}

		mediaRoot := trustedRoot
		if location.relative != "." {
			mediaRoot, err = trustedRoot.OpenRoot(filepath.ToSlash(location.relative))
			closeErr := trustedRoot.Close()
			if err != nil {
				openErrors = append(openErrors,
					fmt.Errorf("media directory escapes trusted root %q or cannot be opened: %w", location.policyRoot, err))
				if closeErr != nil {
					openErrors = append(openErrors,
						fmt.Errorf("close trusted media root %q: %w", location.policyRoot, closeErr))
				}
				continue
			}
			if closeErr != nil {
				_ = mediaRoot.Close()
				openErrors = append(openErrors,
					fmt.Errorf("close trusted media root %q: %w", location.policyRoot, closeErr))
				continue
			}
		}

		return &MediaRoot{root: mediaRoot, path: mediaRoot.Name()}, nil
	}

	return nil, errors.Join(openErrors...)
}

// Close releases the source-media root handle.
func (r *MediaRoot) Close() error {
	if r == nil || r.root == nil {
		return nil
	}
	return r.root.Close()
}

// Open opens a relative media path beneath the trusted root. Absolute paths,
// traversal components, and symlinks escaping the root are rejected by
// os.Root. The returned file belongs to the caller.
func (r *MediaRoot) Open(relativePath string) (*os.File, error) {
	if r == nil || r.root == nil {
		return nil, errors.New("media root is not open")
	}
	safePath, err := safeMediaRelativePath(relativePath)
	if err != nil {
		return nil, err
	}
	file, err := r.root.Open(safePath)
	if err != nil {
		return nil, fmt.Errorf("open media file %q: %w", relativePath, err)
	}
	return file, nil
}

// Scan walks importable files through the root-relative filesystem handle.
func (r *MediaRoot) Scan() ([]MediaFile, error) {
	if r == nil || r.root == nil {
		return nil, errors.New("media root is not open")
	}

	var files []MediaFile
	err := fs.WalkDir(r.root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		safePath, err := safeMediaRelativePath(path)
		if err != nil {
			return nil
		}
		mimeType := MimeTypeFromExt(safePath)
		if mimeType == "" || !IsAllowedMediaMime(mimeType) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}

		files = append(files, MediaFile{
			Path:     filepath.FromSlash(safePath),
			FullPath: filepath.Join(r.path, filepath.FromSlash(safePath)),
			Filename: entry.Name(),
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

func safeMediaRelativePath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("media path %q must be relative", path)
	}
	// Paths from fs.WalkDir use slashes on every OS. Convert native separators
	// for callers too, then validate before cleaning so an interior traversal
	// such as "images/../secret.pdf" is rejected rather than silently folded.
	normalized := filepath.ToSlash(path)
	if normalized == "." || !fs.ValidPath(normalized) {
		return "", fmt.Errorf("media path %q contains an invalid or traversal component", path)
	}
	return normalized, nil
}

// ResolveMediaRoot validates an admin-supplied files directory and returns its
// absolute validated path.
//
// The files path comes from admin configuration, which limits the risk, but it
// is still attacker-influenced input pointing at the local filesystem — so it
// is checked against trusted roots before an os.Root capability is opened.
func ResolveMediaRoot(filesPath string) (cleanPath, realRoot string, err error) {
	mediaRoot, err := OpenMediaRoot(filesPath)
	if err != nil {
		return "", "", err
	}
	validatedPath := mediaRoot.path
	if err := mediaRoot.Close(); err != nil {
		return "", "", fmt.Errorf("close validated media root: %w", err)
	}
	return validatedPath, validatedPath, nil
}

// ResolveWithinRoot resolves path through symlinks and confirms it stays inside
// realRoot. It returns the resolved absolute path, or ok=false when the file
// escapes the root, cannot be resolved, or does not exist.
func ResolveWithinRoot(realRoot, path string) (string, bool) {
	resolvedRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		return "", false
	}
	resolvedRoot, err = filepath.Abs(resolvedRoot)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
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
	mediaRoot, err := OpenMediaRoot(filesPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = mediaRoot.Close() }()
	files, err := mediaRoot.Scan()
	if err != nil {
		return nil, err
	}
	return files, nil
}

// copyBufferSize is the chunk size used when copying media into the uploads dir.
const copyBufferSize = 32 * 1024

// SaveNonImageFile copies a non-image file into the uploads directory under its
// media UUID. The filename and UUID are validated before the destination is
// opened through os.Root, so neither value can escape the uploads directory.
func SaveNonImageFile(src io.ReadSeeker, uploadDir, fileUUID, filename string) error {
	_, err := SaveNonImageFileWithCanonicalRoot(src, uploadDir, fileUUID, filename)
	return err
}

// SaveNonImageFileWithCanonicalRoot returns the canonical uploads root used by
// the write. Importers retain it for compensation after a later source-close,
// database, or tracking failure instead of re-resolving the configured path.
func SaveNonImageFileWithCanonicalRoot(src io.ReadSeeker, uploadDir, fileUUID, filename string) (string, error) {
	if !imaging.IsCanonicalMediaUUID(fileUUID) {
		return "", fmt.Errorf("invalid media UUID %q", fileUUID)
	}
	safeFilename, err := util.SanitizeFilename(filename)
	if err != nil {
		return "", fmt.Errorf("invalid filename %q: %w", filename, err)
	}
	uploadRoot, err := openMediaWriteRoot(uploadDir)
	if err != nil {
		// Opening failed before any destination path was created.
		return "", fmt.Errorf("failed to open uploads directory: %w", err)
	}
	canonicalUploadRoot := uploadRoot.Name()
	fail := func(writeErr error) (string, error) {
		closeErr := uploadRoot.Close()
		if closeErr != nil {
			writeErr = errors.Join(writeErr, fmt.Errorf("failed to close uploads directory: %w", closeErr))
		}
		return canonicalUploadRoot, cleanupFailedMediaWrite(canonicalUploadRoot, fileUUID, writeErr)
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		// A stale UUID directory from an earlier failed attempt must still be
		// removed, but only through the root identity captured above.
		return fail(fmt.Errorf("failed to seek source file: %w", err))
	}

	destDir := pathpkg.Join(model.OriginalsDir, fileUUID)
	if !fs.ValidPath(destDir) {
		return fail(errors.New("invalid destination directory"))
	}
	if err := uploadRoot.MkdirAll(destDir, 0o750); err != nil {
		return fail(fmt.Errorf("failed to create directory: %w", err))
	}

	destPath := pathpkg.Join(destDir, safeFilename)
	if !fs.ValidPath(destPath) {
		return fail(errors.New("invalid destination path"))
	}
	dest, err := uploadRoot.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fail(fmt.Errorf("failed to create destination file: %w", err))
	}

	buf := make([]byte, copyBufferSize)
	_, copyErr := io.CopyBuffer(dest, src, buf)
	closeErr := dest.Close()
	rootCloseErr := uploadRoot.Close()
	if copyErr != nil || closeErr != nil || rootCloseErr != nil {
		var writeErrors []error
		if copyErr != nil {
			writeErrors = append(writeErrors, fmt.Errorf("failed to copy file: %w", copyErr))
		}
		if closeErr != nil {
			writeErrors = append(writeErrors, fmt.Errorf("failed to close destination file: %w", closeErr))
		}
		if rootCloseErr != nil {
			writeErrors = append(writeErrors, fmt.Errorf("failed to close uploads directory: %w", rootCloseErr))
		}
		return canonicalUploadRoot, cleanupFailedMediaWrite(canonicalUploadRoot, fileUUID, errors.Join(writeErrors...))
	}

	return canonicalUploadRoot, nil
}

func openMediaWriteRoot(uploadDir string) (*os.Root, error) {
	return openMediaWriteRootWith(uploadDir, nil)
}

// openMediaWriteRootWith opens the configured uploads directory through its
// canonical parent and verifies that it is still the directory inspected
// before symlink resolution. beforeOpen is a deterministic test seam.
func openMediaWriteRootWith(uploadDir string, beforeOpen func()) (*os.Root, error) {
	if strings.TrimSpace(uploadDir) == "" {
		return nil, errors.New("uploads directory is empty")
	}
	absRoot, err := filepath.Abs(filepath.Clean(uploadDir))
	if err != nil {
		return nil, fmt.Errorf("resolve uploads directory: %w", err)
	}
	if filepath.Dir(absRoot) == absRoot {
		return nil, fmt.Errorf("uploads directory %q is too broad", uploadDir)
	}
	if err := os.MkdirAll(absRoot, 0o750); err != nil {
		return nil, fmt.Errorf("create uploads directory: %w", err)
	}
	expected, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect uploads directory: %w", err)
	}
	if !expected.IsDir() {
		return nil, fmt.Errorf("uploads path %q is not a directory", uploadDir)
	}

	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve uploads directory symlinks: %w", err)
	}
	resolvedRoot, err = filepath.Abs(resolvedRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve uploads directory absolute path: %w", err)
	}
	resolvedRoot = filepath.Clean(resolvedRoot)
	if filepath.Dir(resolvedRoot) == resolvedRoot {
		return nil, fmt.Errorf("uploads directory %q resolves to filesystem root", uploadDir)
	}
	if beforeOpen != nil {
		beforeOpen()
	}

	parentRoot, err := os.OpenRoot(filepath.Dir(resolvedRoot))
	if err != nil {
		return nil, fmt.Errorf("open uploads parent directory: %w", err)
	}
	root, openErr := parentRoot.OpenRoot(filepath.ToSlash(filepath.Base(resolvedRoot)))
	parentCloseErr := parentRoot.Close()
	if openErr != nil {
		return nil, errors.Join(fmt.Errorf("open uploads directory: %w", openErr), parentCloseErr)
	}
	if parentCloseErr != nil {
		_ = root.Close()
		return nil, fmt.Errorf("close uploads parent directory: %w", parentCloseErr)
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(expected, opened) {
		closeErr := root.Close()
		if err != nil {
			return nil, errors.Join(fmt.Errorf("inspect opened uploads directory: %w", err), closeErr)
		}
		return nil, errors.Join(errors.New("uploads directory changed while it was being opened"), closeErr)
	}
	return root, nil
}

func cleanupFailedMediaWrite(uploadDir, fileUUID string, writeErr error) error {
	cleanupErr := imaging.DeleteMediaFilesFromCanonicalRoot(uploadDir, fileUUID)
	if cleanupErr == nil {
		return writeErr
	}
	return errors.Join(writeErr, fmt.Errorf("failed to remove partial media output: %w", cleanupErr))
}

// UnguessablePlaceholderHash returns a password hash that nobody can log in
// with.
//
// Imported accounts need *some* password hash: the source CMS stores phpass,
// bcrypt or SHA-512 digests that oCMS's Argon2id verifier cannot check, so the
// account always requires a reset before its owner can sign in.
//
// The obvious implementation — hash a fixed string like "imported-user-must-reset"
// — is an authentication bypass, because the plaintext is right there in the
// source code and the login handler applies no forced-reset or disabled-account
// gate. Anyone who knew an imported user's email address could sign in as them.
// Hashing a fresh random secret keeps the "hash once per run" performance
// property while making the credential unguessable, since the plaintext is
// discarded and never leaves this function.
func UnguessablePlaceholderHash() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("failed to generate placeholder secret: %w", err)
	}
	hash, err := auth.HashPassword(base64.RawURLEncoding.EncodeToString(secret))
	if err != nil {
		return "", fmt.Errorf("failed to hash placeholder secret: %w", err)
	}
	return hash, nil
}
