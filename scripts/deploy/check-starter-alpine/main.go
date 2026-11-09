// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var (
	alpineAssets   = [...]string{"alpine.min.js", "alpine-collapse.min.js"}
	layoutComments = regexp.MustCompile(`(?s)<!--.*?-->|{{-?\s*/\*.*?\*/\s*-?}}`)
	scriptTags     = regexp.MustCompile(`(?i)<script\s[^>]*>`)
	// Starter uses scriptURL inside its src attribute. Also accept literal URLs.
	alpineSource = regexp.MustCompile(`(?i)\ssrc\s*=\s*["'](?:{{-?\s*scriptURL\s+")?/static/dist/js/(alpine(?:-collapse)?\.min\.js)["'?]`)
	integrity    = regexp.MustCompile(`(?i)\sintegrity\s*=\s*["']([^"']*)["']`)
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: check-starter-alpine <bundle-dist-js-dir> <starter-layout> [starter-layout ...]")
		os.Exit(2)
	}
	if err := checkLayouts(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Alpine integrity matches the bundle in %d Starter layouts\n", len(os.Args)-2)
}

func checkLayouts(assetDir string, layouts []string) error {
	expected := make(map[string]string, len(alpineAssets))
	for _, asset := range alpineAssets {
		data, err := os.ReadFile(filepath.Join(assetDir, asset)) // #nosec G304 -- operator-supplied bundle path
		if err != nil {
			return err
		}
		sum := sha512.Sum384(data)
		expected[asset] = "sha384-" + base64.StdEncoding.EncodeToString(sum[:])
	}

	var problems []error
	for _, path := range layouts {
		data, err := os.ReadFile(path) // #nosec G304 -- operator-supplied layout path
		if err != nil {
			problems = append(problems, err)
			continue
		}
		seen := make(map[string]bool, len(alpineAssets))
		for _, tag := range scriptTags.FindAllString(layoutComments.ReplaceAllString(string(data), ""), -1) {
			source := alpineSource.FindStringSubmatch(tag)
			if source == nil {
				continue
			}
			asset := source[1]
			seen[asset] = true
			declared := integrity.FindStringSubmatch(tag)
			if declared == nil {
				problems = append(problems, fmt.Errorf("%s: %s is missing an integrity attribute (expected %s)", path, asset, expected[asset]))
			} else if declared[1] != expected[asset] {
				problems = append(problems, fmt.Errorf("%s: %s integrity is stale (declared %s, expected %s)", path, asset, declared[1], expected[asset]))
			}
		}
		for _, asset := range alpineAssets {
			if !seen[asset] {
				problems = append(problems, fmt.Errorf("%s: missing script tag for %s", path, asset))
			}
		}
	}
	return errors.Join(problems...)
}
