// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package theme

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// Archive limits apply to imports through both the manager and the admin UI.
const (
	MaxArchiveBytes   = 20 << 20
	maxExtractedBytes = 100 << 20
	maxArchiveEntries = 2000
	importPrefix      = ".theme-import-"
	deletePrefix      = ".theme-delete-"
)

var themeNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidationError describes a problem with an uploaded archive or a requested
// theme that the administrator can correct, so its text is safe to show in the
// admin UI. Every other error is internal: callers log it and show a generic
// message.
type ValidationError string

// Error returns the message to show to the administrator.
func (e ValidationError) Error() string { return string(e) }

func invalidf(format string, args ...any) error {
	return ValidationError(fmt.Sprintf(format, args...))
}

// ErrThemeProtected means a theme is active or has an embedded counterpart.
var ErrThemeProtected = ValidationError("active and core themes cannot be deleted")

// ImportArchive validates and installs a new HTML theme without activating it.
// All files are staged under custom/themes before an atomic rename. Templates
// are parsed under the read lock so page rendering continues; the write lock
// only covers the existence checks, the rename and registration.
func (m *Manager) ImportArchive(archive io.Reader) (string, error) {
	root, err := m.openCustomThemes(true)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()

	stage := importPrefix + rand.Text()
	if err := root.Mkdir(stage, 0700); err != nil {
		return "", fmt.Errorf("creating theme staging directory: %w", err)
	}
	defer func() {
		if err := root.RemoveAll(stage); err != nil {
			m.logger.Warn("failed to clean theme staging directory", "error", err)
		}
	}()
	stageRoot, err := root.OpenRoot(stage)
	if err != nil {
		return "", fmt.Errorf("opening theme staging directory: %w", err)
	}
	defer func() { _ = stageRoot.Close() }()

	name, err := extractThemeArchive(stageRoot, archive)
	if err != nil {
		return "", err
	}
	info, err := stageRoot.Stat(path.Join(name, "theme.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ValidationError("theme archive is missing theme.json")
		}
		return "", fmt.Errorf("checking theme.json: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", ValidationError("theme.json must be a regular file")
	}
	stagedPath := filepath.Join(root.Name(), stage, name)
	m.mu.RLock()
	thm, err := m.loadFilesystemTheme(name, stagedPath)
	m.mu.RUnlock()
	if err != nil {
		// Filesystem errors name server paths and are not the archive's
		// fault. JSON and template parse errors use relative template names.
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			return "", fmt.Errorf("loading theme: %w", err)
		}
		return "", invalidf("invalid theme: %v", err)
	}
	if err := validateImportedTheme(thm); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.themes[name]; exists || m.hasEmbeddedTheme(name) {
		return "", invalidf("theme %q already exists", name)
	}
	if _, err := root.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return "", fmt.Errorf("checking theme destination: %w", err)
		}
		return "", invalidf("theme directory %q already exists", name)
	}
	if err := root.Rename(filepath.Join(stage, name), name); err != nil {
		return "", fmt.Errorf("installing theme: %w", err)
	}
	thm.Path = filepath.Join(root.Name(), name)
	thm.StaticPath = filepath.Join(thm.Path, "static")
	m.themes[name] = thm
	m.logger.Info("theme imported", "theme", name)
	return name, nil
}

// RemoveTheme removes an inactive custom theme. Saved settings and widgets are
// retained so a subsequent installation can reuse them. The directory is
// removed after the theme is unregistered, outside the manager lock.
func (m *Manager) RemoveTheme(name string) error {
	root, tombstone, err := m.unregisterTheme(name)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.RemoveAll(tombstone); err != nil {
		// Logical deletion succeeded. Report cleanup trouble in the server log,
		// rather than claim failure after the theme is already unavailable.
		m.logger.Warn("deleted theme needs filesystem cleanup", "directory", tombstone, "error", err)
	}
	m.logger.Info("theme deleted", "theme", name)
	return nil
}

// unregisterTheme hides the theme directory and unregisters the theme under
// the write lock. It returns the open custom themes root and the hidden
// directory name for cleanup by the caller.
func (m *Manager) unregisterTheme(name string) (root *os.Root, tombstone string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !themeNamePattern.MatchString(name) {
		return nil, "", ValidationError("invalid theme name")
	}
	thm, exists := m.themes[name]
	if !exists {
		return nil, "", invalidf("theme not found: %s", name)
	}
	if thm.IsEmbedded || m.hasEmbeddedTheme(name) || (m.activeTheme != nil && m.activeTheme.Name == name) {
		return nil, "", ErrThemeProtected
	}
	root, err = m.openCustomThemes(false)
	if err != nil {
		return nil, "", err
	}
	opened := root // the named result is nil on error returns
	defer func() {
		if err != nil {
			_ = opened.Close()
		}
	}()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, "", fmt.Errorf("checking theme directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return nil, "", errors.New("theme directory must not be a symbolic link")
	}
	// Hide the directory before unregistering it. A failed rename changes no
	// registry state; a crash after rename cannot reload a deleted theme.
	tombstone = deletePrefix + rand.Text()
	if err = root.Rename(name, tombstone); err != nil {
		return nil, "", fmt.Errorf("removing theme directory: %w", err)
	}
	delete(m.themes, name)
	return root, tombstone, nil
}

func (m *Manager) hasEmbeddedTheme(name string) bool {
	_, err := fs.Stat(m.embeddedFS, name+"/theme.json")
	return err == nil
}

func (m *Manager) openCustomThemes(create bool) (*os.Root, error) {
	if m.customDir == "" {
		return nil, errors.New("custom theme directory is not configured")
	}
	customRoot, err := os.OpenRoot(m.customDir)
	if err != nil {
		return nil, fmt.Errorf("opening custom directory: %w", err)
	}
	defer func() { _ = customRoot.Close() }()
	if create {
		if err := customRoot.Mkdir("themes", 0755); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("creating custom themes directory: %w", err)
		}
	}
	root, err := customRoot.OpenRoot("themes")
	if err != nil {
		return nil, fmt.Errorf("opening custom themes directory: %w", err)
	}
	return root, nil
}

func extractThemeArchive(root *os.Root, archive io.Reader) (string, error) {
	compressed := &io.LimitedReader{R: archive, N: MaxArchiveBytes + 1}
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return "", ValidationError("invalid gzip archive")
	}
	defer func() { _ = gz.Close() }()
	// Include tar headers and padding in the decompression budget so metadata
	// and trailing data cannot bypass the uncompressed size limit.
	extracted := &io.LimitedReader{R: gz, N: maxExtractedBytes + 1}
	reader := tar.NewReader(extracted)
	seen := make(map[string]bool)
	name := ""
	var fileBytes int64
	for entries := 0; ; entries++ {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", invalidf("invalid tar archive: %v", err)
		}
		if entries >= maxArchiveEntries {
			return "", ValidationError("theme archive contains more than 2000 entries")
		}
		// macOS metadata entries are skipped, but still count toward the limits.
		skip := isAppleDoubleEntry(header)
		var entry string
		if !skip {
			if entry, err = validateArchiveEntry(header, name, seen); err != nil {
				return "", err
			}
		}
		if header.Size < 0 || header.Size > maxExtractedBytes-fileBytes || extracted.N <= header.Size {
			return "", ValidationError("theme archive exceeds the 100 MiB extracted limit")
		}
		fileBytes += header.Size
		if skip {
			continue
		}
		if name == "" {
			name = strings.SplitN(entry, "/", 2)[0]
		}
		if err := extractArchiveEntry(root, reader, header, entry); err != nil {
			return "", err
		}
	}
	// Read through the gzip trailer to verify its checksum, truncated streams,
	// and trailing data, including concatenated gzip members.
	if _, err := io.Copy(io.Discard, extracted); err != nil {
		return "", invalidf("invalid gzip archive: %v", err)
	}
	if extracted.N <= 0 || compressed.N <= 0 {
		return "", ValidationError("theme archive exceeds its size limit")
	}
	if name == "" {
		return "", ValidationError("theme archive is empty")
	}
	return name, nil
}

// isAppleDoubleEntry reports whether an entry is the "._name" metadata file
// that macOS tar adds for files and directories with extended attributes.
func isAppleDoubleEntry(header *tar.Header) bool {
	return header.Typeflag == tar.TypeReg && strings.HasPrefix(path.Base(header.Name), "._")
}

func validateArchiveEntry(header *tar.Header, themeName string, seen map[string]bool) (string, error) {
	entry := strings.TrimSuffix(header.Name, "/")
	if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg {
		return "", ValidationError("theme archives may contain only regular files and directories")
	}
	for key := range header.PAXRecords {
		if strings.HasPrefix(key, "GNU.sparse.") {
			return "", ValidationError("theme archives must not contain sparse files")
		}
	}
	if !fs.ValidPath(entry) || path.IsAbs(entry) || strings.ContainsAny(entry, "\\:\x00") {
		return "", ValidationError("theme archive contains an unsafe path")
	}
	name := strings.SplitN(entry, "/", 2)[0]
	if !themeNamePattern.MatchString(name) || (themeName != "" && name != themeName) {
		return "", ValidationError("theme archive must contain one named theme directory (lowercase letters, digits, hyphens and underscores; at most 64 characters)")
	}
	if entry == name && header.Typeflag != tar.TypeDir {
		return "", ValidationError("theme archive must contain a theme directory")
	}
	if seen[entry] {
		return "", ValidationError("theme archive contains a duplicate path")
	}
	seen[entry] = true
	return entry, nil
}

func extractArchiveEntry(root *os.Root, reader *tar.Reader, header *tar.Header, entry string) error {
	if header.Typeflag == tar.TypeDir {
		if err := root.MkdirAll(entry, 0755); err != nil {
			return extractionError("creating theme directory", err)
		}
		return nil
	}
	if err := root.MkdirAll(path.Dir(entry), 0755); err != nil {
		return extractionError("creating theme directory", err)
	}
	file, err := root.OpenFile(entry, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return extractionError("creating theme file", err)
	}
	_, copyErr := io.CopyN(file, reader, header.Size)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("extracting theme file: %w", err)
	}
	return nil
}

// extractionError classifies filesystem errors while writing an entry: a path
// that already exists, or whose parent is a file, comes from the archive
// itself (including names that differ only by case on case-insensitive
// filesystems); anything else is a server problem.
func extractionError(action string, err error) error {
	if errors.Is(err, fs.ErrExist) || errors.Is(err, syscall.ENOTDIR) {
		return ValidationError("theme archive contains conflicting file and directory paths")
	}
	return fmt.Errorf("%s: %w", action, err)
}

func validateImportedTheme(thm *Theme) error {
	if strings.TrimSpace(thm.Config.Name) == "" || strings.TrimSpace(thm.Config.Version) == "" {
		return ValidationError("theme.json must contain a name and version")
	}
	if thm.Config.Engine != "" && !strings.EqualFold(thm.Config.Engine, EngineHTML) {
		return ValidationError("imported themes must use the html rendering engine")
	}
	if thm.Templates.Lookup("layouts/base.html") == nil {
		return ValidationError("theme is missing templates/layouts/base.html")
	}
	for _, page := range []string{"home", "page", "list", "404", "category", "tag", "search"} {
		if thm.Templates.Lookup("content_"+page) == nil {
			return invalidf("theme is missing the content definition in templates/pages/%s.html", page)
		}
	}
	return nil
}
