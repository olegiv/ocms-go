// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
)

func TestProductionUpgradeDocsCoverBreakingDefaults(t *testing.T) {
	for _, doc := range []struct{ file, start, end string }{
		{"CHANGELOG.md", "**Production is the default (I-10).**", "- **Stricter script CSP"},
		{"README.md", "## Upgrading sites with no `OCMS_ENV`", "## Environment Variables"},
	} {
		t.Run(doc.file, func(t *testing.T) {
			body := string(readWorkflowFile(t, filepath.Join("..", "..", doc.file)))
			_, tail, foundStart := strings.Cut(body, doc.start)
			section, _, foundEnd := strings.Cut(tail, doc.end)
			if !foundStart || !foundEnd {
				t.Fatal("production upgrade guidance not found")
			}
			section = strings.Join(strings.Fields(section), " ")
			for _, detail := range []string{
				"per-key source CIDR", "sessions end", "MFA", "enroll", "Secure `__Host-session`", "HSTS",
				"non-HTTPS URLs", "webhooks", "scheduled HTTP tasks", "embed providers", "OCMS_WEBHOOK_FORM_DATA_MODE", "full",
			} {
				if !strings.Contains(section, detail) {
					t.Errorf("production upgrade guidance is missing %q", detail)
				}
			}
		})
	}
}

// TestDocumentedEnvironmentDefaults checks both the raw tag defaults and
// the effective production overrides applied by config.Load.
func TestDocumentedEnvironmentDefaults(t *testing.T) {
	type documentedDefault struct {
		raw   string
		field int
	}
	defaults := make(map[string]documentedDefault)
	for field := range reflect.TypeFor[config.Config]().Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("env"), ",")
		if name == "" {
			continue
		}
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		if value, ok := field.Tag.Lookup("envDefault"); ok {
			defaults[name] = documentedDefault{raw: value, field: field.Index[0]}
		}
	}
	t.Setenv("OCMS_ENV", "production")
	t.Setenv("OCMS_DEMO_MODE", "")
	t.Setenv("OCMS_SESSION_SECRET", "Hq4Wz9Lx2Vb7Np3Rt6Yc1Mk8Df5Gs0Ja4Ue7Xi2Zo=")
	t.Setenv("OCMS_MFA_KEY", "k9Qw2xZt7Lp4Vn8sRb3Yh6Mc1Ud5Ef0Gj2Hx4Nz8Tq=")
	t.Setenv("OCMS_TRUSTED_PROXIES", "127.0.0.1/32")
	t.Setenv("OCMS_API_ALLOWED_CIDRS", "192.0.2.1/32")
	prod, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	production := reflect.ValueOf(prod).Elem()

	for _, doc := range []struct{ name, qualifier string }{
		{"CLAUDE.md", " (effective `%s` in prod)"},
		{"README.md", " (`%s` in production when unset)"},
	} {
		t.Run(doc.name, func(t *testing.T) {
			body := readWorkflowFile(t, filepath.Join("..", "..", doc.name))
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
				expected := "`" + want.raw + "`"
				if effective := fmt.Sprint(production.Field(want.field).Interface()); effective != want.raw {
					expected += fmt.Sprintf(doc.qualifier, effective)
				}
				if got := strings.TrimSpace(cells[3]); got != expected {
					t.Errorf("%s documented default %q, want %q", key, got, expected)
				}
				if seen[key] {
					t.Errorf("duplicate %s row", key)
				}
				seen[key] = true
			}
			for key := range defaults {
				if !seen[key] {
					t.Errorf("missing %s row", key)
				}
			}
		})
	}
}
