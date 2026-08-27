// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EnvAllowedFileRoots names the operator-controlled allowlist of filesystem
// roots from which migrator sources may read media. Values are comma-separated
// absolute paths. DRUPAL_FILES, ELEFANT_FILES and PHPNUKE_FILES are also
// treated as trusted roots for backwards-compatible deployments that already
// configure them.
const EnvAllowedFileRoots = "OCMS_MIGRATOR_ALLOWED_FILE_ROOTS"

var legacyFileRootEnvs = []string{"DRUPAL_FILES", "ELEFANT_FILES", "PHPNUKE_FILES"}

// TrustedFileRootEnvNames returns every environment variable that can supply a
// trusted media root, the allowlist first.
//
// Error messages derive their list from this rather than spelling one out.
// A hardcoded list silently goes stale the moment a source is added: the
// message kept naming DRUPAL_FILES and ELEFANT_FILES after PHPNUKE_FILES began
// working, so a PHP-Nuke operator was told to configure the wrong variables.
func TrustedFileRootEnvNames() []string {
	names := make([]string, 0, len(legacyFileRootEnvs)+1)
	names = append(names, EnvAllowedFileRoots)
	return append(names, legacyFileRootEnvs...)
}

// trustedFileRoot keeps both sides of the root policy. policyRoot is the
// operator-configured lexical path used only to decide whether a submitted
// path is in scope. canonicalRoot and info identify the directory capability
// that may actually be opened. Keeping both is what permits a configured
// symlink to a safe media tree without ever opening or statting the submitted
// form value.
type trustedFileRoot struct {
	policyRoot    string
	canonicalRoot string
	info          os.FileInfo
}

// ParseAllowedFileRoots parses a comma-separated trusted-root allowlist.
// Roots must be absolute and may not be the filesystem root: allowing "/"
// would turn the allowlist into unrestricted local-file access.
func ParseAllowedFileRoots(raw string) ([]string, error) {
	policies, err := parseTrustedFileRootPolicies(raw)
	if err != nil {
		return nil, err
	}
	return canonicalRootPaths(policies), nil
}

// AllowedFileRoots returns every configured trusted source-media root.
func AllowedFileRoots() ([]string, error) {
	policies, err := allowedFileRootPolicies()
	if err != nil {
		return nil, err
	}
	return canonicalRootPaths(policies), nil
}

func allowedFileRootPolicies() ([]trustedFileRoot, error) {
	policies, err := parseTrustedFileRootPolicies(os.Getenv(EnvAllowedFileRoots))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", EnvAllowedFileRoots, err)
	}

	seen := make(map[string]struct{}, len(policies)+len(legacyFileRootEnvs))
	for _, policy := range policies {
		seen[policy.policyRoot] = struct{}{}
	}
	for _, envName := range legacyFileRootEnvs {
		value := strings.TrimSpace(os.Getenv(envName))
		if value == "" {
			continue
		}
		root, err := normalizeTrustedFileRoot(value)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", envName, err)
		}
		if _, ok := seen[root.policyRoot]; ok {
			continue
		}
		seen[root.policyRoot] = struct{}{}
		policies = append(policies, root)
	}
	sortTrustedFileRoots(policies)
	return policies, nil
}

func parseTrustedFileRootPolicies(raw string) ([]trustedFileRoot, error) {
	var roots []trustedFileRoot
	seen := make(map[string]struct{})
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		root, err := normalizeTrustedFileRoot(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[root.policyRoot]; ok {
			continue
		}
		seen[root.policyRoot] = struct{}{}
		roots = append(roots, root)
	}
	sortTrustedFileRoots(roots)
	return roots, nil
}

func normalizeTrustedFileRoot(value string) (trustedFileRoot, error) {
	return normalizeTrustedFileRootWith(value, nil)
}

// normalizeTrustedFileRootWith captures the configured root identity before
// symlink resolution. afterResolve is a deterministic test seam for replacing
// the configured path between policy resolution and capability opening.
func normalizeTrustedFileRootWith(value string, afterResolve func()) (trustedFileRoot, error) {
	if !filepath.IsAbs(value) {
		return trustedFileRoot{}, fmt.Errorf("trusted file root %q must be absolute", value)
	}
	policyRoot := filepath.Clean(value)
	if filepath.Dir(policyRoot) == policyRoot {
		return trustedFileRoot{}, fmt.Errorf("trusted file root %q is too broad", value)
	}
	info, err := os.Stat(policyRoot)
	if err != nil {
		return trustedFileRoot{}, fmt.Errorf("inspect trusted file root %q: %w", value, err)
	}
	if !info.IsDir() {
		return trustedFileRoot{}, fmt.Errorf("trusted file root %q is not a directory", value)
	}

	canonicalRoot, err := filepath.EvalSymlinks(policyRoot)
	if err != nil {
		return trustedFileRoot{}, fmt.Errorf("resolve trusted file root %q: %w", value, err)
	}
	canonicalRoot, err = filepath.Abs(canonicalRoot)
	if err != nil {
		return trustedFileRoot{}, fmt.Errorf("resolve trusted file root %q absolute path: %w", value, err)
	}
	canonicalRoot = filepath.Clean(canonicalRoot)
	if filepath.Dir(canonicalRoot) == canonicalRoot {
		return trustedFileRoot{}, fmt.Errorf("trusted file root %q resolves to filesystem root", value)
	}
	if afterResolve != nil {
		afterResolve()
	}
	return trustedFileRoot{policyRoot: policyRoot, canonicalRoot: canonicalRoot, info: info}, nil
}

func sortTrustedFileRoots(roots []trustedFileRoot) {
	// Prefer the narrowest matching root when roots overlap.
	sort.Slice(roots, func(i, j int) bool {
		if len(roots[i].policyRoot) == len(roots[j].policyRoot) {
			return roots[i].policyRoot < roots[j].policyRoot
		}
		return len(roots[i].policyRoot) > len(roots[j].policyRoot)
	})
}

func canonicalRootPaths(policies []trustedFileRoot) []string {
	seen := make(map[string]struct{}, len(policies))
	roots := make([]string, 0, len(policies))
	for _, policy := range policies {
		if _, ok := seen[policy.canonicalRoot]; ok {
			continue
		}
		seen[policy.canonicalRoot] = struct{}{}
		roots = append(roots, policy.canonicalRoot)
	}
	sort.Slice(roots, func(i, j int) bool {
		if len(roots[i]) == len(roots[j]) {
			return roots[i] < roots[j]
		}
		return len(roots[i]) > len(roots[j])
	})
	return roots
}

type trustedMediaLocation struct {
	policyRoot    string
	canonicalRoot string
	rootInfo      os.FileInfo
	relative      string
}

// trustedMediaLocations validates an administrator-submitted media path using
// string operations only. It deliberately does not resolve, stat, walk or open
// the submitted absolute path. OpenMediaRoot first opens one of the
// operator-controlled roots below, then descends through os.Root using only the
// rebuilt relative name.
func trustedMediaLocations(candidate string) ([]trustedMediaLocation, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return nil, fmt.Errorf("files path is empty")
	}
	if !filepath.IsAbs(candidate) {
		return nil, fmt.Errorf("files path %q must be absolute", candidate)
	}

	roots, err := allowedFileRootPolicies()
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("no trusted media roots configured; set one of %s",
			strings.Join(TrustedFileRootEnvNames(), ", "))
	}

	cleanCandidate := filepath.Clean(candidate)
	var locations []trustedMediaLocation
	for _, root := range roots {
		rel, ok := relativePathWithin(root.policyRoot, cleanCandidate)
		if !ok {
			continue
		}

		// Rebuild the relative portion component by component. Returning a path
		// composed only of these fresh components prevents the raw request string
		// from reaching root-relative filesystem operations.
		safeRel, err := rebuildRelativePath(rel)
		if err != nil {
			return nil, err
		}
		locations = append(locations, trustedMediaLocation{
			policyRoot:    root.policyRoot,
			canonicalRoot: root.canonicalRoot,
			rootInfo:      root.info,
			relative:      safeRel,
		})
	}

	if len(locations) == 0 {
		return nil, fmt.Errorf("files path %q is outside configured trusted media roots", candidate)
	}
	return locations, nil
}

func relativePathWithin(root, candidate string) (string, bool) {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(rel) || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func rebuildRelativePath(rel string) (string, error) {
	if rel == "." {
		return ".", nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	rebuilt := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid relative files path component %q", part)
		}
		var component strings.Builder
		component.Grow(len(part))
		for _, r := range part {
			if r == 0 || r == '/' || r == '\\' {
				return "", fmt.Errorf("invalid character in files path component %q", part)
			}
			component.WriteRune(r)
		}
		rebuilt = append(rebuilt, component.String())
	}
	return filepath.Join(rebuilt...), nil
}
