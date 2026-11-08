// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
)

// TestDocumentedEnvironmentDefaults compares the raw tag defaults. The
// parenthetical production overrides are a separate policy in config.Load.
func TestDocumentedEnvironmentDefaults(t *testing.T) {
	defaults := make(map[string]string)
	for field := range reflect.TypeFor[config.Config]().Fields() {
		if value, ok := field.Tag.Lookup("envDefault"); ok {
			name, _, _ := strings.Cut(field.Tag.Get("env"), ",")
			defaults[name] = value
		}
	}

	for _, name := range []string{"CLAUDE.md", "README.md"} {
		t.Run(name, func(t *testing.T) {
			body := readWorkflowFile(t, filepath.Join("..", "..", name))
			seen := make(map[string]bool)
			for line := range strings.SplitSeq(string(body), "\n") {
				if !strings.HasPrefix(line, "|") {
					continue
				}
				cells := strings.SplitN(line, "|", 5)
				if len(cells) < 5 {
					continue
				}
				key := strings.Trim(strings.TrimSpace(cells[1]), "`")
				want, ok := defaults[key]
				if !ok {
					continue
				}
				value, quoted := strings.CutPrefix(strings.TrimSpace(cells[3]), "`")
				got, _, closed := strings.Cut(value, "`")
				if !quoted || !closed || got != want {
					t.Errorf("%s documented default %q, want envDefault %q", key, strings.TrimSpace(cells[3]), want)
				}
				seen[key] = true
			}
			for _, key := range []string{"OCMS_ENV", "OCMS_PREVIEW_LINK_TTL_HOURS"} {
				if !seen[key] {
					t.Errorf("missing %s row", key)
				}
			}
		})
	}
}
