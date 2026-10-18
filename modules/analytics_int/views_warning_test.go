// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"context"
	"strings"
	"testing"

	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

// TestSettingsPageWarnsWhileOnFallback checks that the settings page warns
// while it shows fallback values, marks its form with from_fallback and
// disables saving, and does none of that otherwise. A refused save used to be
// the first sign, and a fallback form could overwrite recovered settings.
func TestSettingsPageWarnsWhileOnFallback(t *testing.T) {
	for _, unavailable := range []bool{true, false} {
		var out strings.Builder
		// The settings form is shown to admins only.
		pc := &adminviews.PageContext{AdminLang: "en", User: adminviews.UserInfo{Role: "admin"}}
		if err := AnalyticsIntPage(pc, AnalyticsIntViewData{SettingsUnavailable: unavailable}).Render(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{`role="alert"`, `name="from_fallback"`, " disabled"} {
			if got := strings.Contains(out.String(), marker); got != unavailable {
				t.Errorf("SettingsUnavailable=%v: page contains %s = %v", unavailable, marker, got)
			}
		}
	}
}
