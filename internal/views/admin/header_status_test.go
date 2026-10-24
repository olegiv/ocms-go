// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package admin

import (
	"strings"
	"testing"
)

func TestHeaderStatusPageLink(t *testing.T) {
	const statusURL = "https://stats.uptimerobot.com/g50rYsPMxR"

	pc := testPageContext()
	if body := renderAdminTemplate(t, Header(pc)); strings.Contains(body, "stats.uptimerobot.com") {
		t.Error("header rendered a status page link with no URL configured")
	}

	pc.StatusPageURL = statusURL
	body := renderAdminTemplate(t, Header(pc))
	for _, want := range []string{`href="` + statusURL + `"`, `rel="noopener noreferrer"`, `target="_blank"`} {
		if !strings.Contains(body, want) {
			t.Errorf("header missing %q", want)
		}
	}
}
