// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package util

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SanitizeFilename extracts only the base filename, removing any directory
// components. This prevents path traversal attacks via filenames like
// "../../../etc/passwd". Returns an error if the filename is invalid.
func SanitizeFilename(filename string) (string, error) {
	safe := filepath.Base(filename)
	if safe == "." || safe == ".." || safe == "" || safe == string(filepath.Separator) {
		return "", fmt.Errorf("invalid filename: %q", filename)
	}
	return safe, nil
}

// ValidatePathWithinBase ensures that a resolved path is within the expected
// base directory. It cleans both paths and checks that the resolved path
// starts with the base path. Returns an error if path traversal is detected.
func ValidatePathWithinBase(basePath, targetPath string) error {
	// Clean and make absolute
	absBase, err := filepath.Abs(filepath.Clean(basePath))
	if err != nil {
		return fmt.Errorf("invalid base path: %w", err)
	}

	absTarget, err := filepath.Abs(filepath.Clean(targetPath))
	if err != nil {
		return fmt.Errorf("invalid target path: %w", err)
	}

	// Ensure target is within base (with trailing separator to prevent
	// matching /uploads-malicious when base is /uploads)
	if absTarget != absBase && !strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) {
		return fmt.Errorf("path traversal detected: path escapes base directory")
	}

	return nil
}

// SafeJoinPath joins path components and validates the result is within
// the base directory. Returns the cleaned path or an error if traversal
// is detected.
func SafeJoinPath(basePath string, components ...string) (string, error) {
	// Join all components
	fullPath := filepath.Join(append([]string{basePath}, components...)...)

	// Validate the result
	if err := ValidatePathWithinBase(basePath, fullPath); err != nil {
		return "", err
	}

	return fullPath, nil
}

// ContainsPathTraversal checks if a path contains traversal sequences.
// Returns true if the path contains ".." after cleaning.
func ContainsPathTraversal(path string) bool {
	cleaned := filepath.Clean(path)
	return strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, string(filepath.Separator)+"..")
}
