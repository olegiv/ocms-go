// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package dbmanager

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	"github.com/olegiv/ocms-go/internal/i18n"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

// TestDashboardShowsTruncation fails when a capped result or a truncated
// history row stops being marked on the page.
func TestDashboardShowsTruncation(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	pc := &adminviews.PageContext{AdminLang: "en", User: adminviews.UserInfo{Role: "admin"}}
	render := func(result *QueryResult, history []QueryHistoryItem) string {
		var buffer bytes.Buffer
		if err := DBManagerPage(pc, DBManagerViewData{Query: "SELECT 1", Result: result, History: history}).Render(templ.WithNonce(t.Context(), "n"), &buffer); err != nil {
			t.Fatal(err)
		}
		return buffer.String()
	}
	capped := &QueryResult{IsSelect: true, Truncated: true, RowsAffected: maxResultRows, Columns: []string{"n"}, Rows: [][]string{{"1"}}}
	history := []QueryHistoryItem{{ID: 1, Query: "SELECT n FROM big", ExecutedAt: time.Now(), RowsAffected: maxResultRows, Truncated: true, Error: sql.NullString{}}}
	html := render(capped, history)
	if strings.Count(html, "badge-warning") != 2 {
		t.Fatalf("expected a truncation badge on the result and on the history row, got %d", strings.Count(html, "badge-warning"))
	}
	plain := &QueryResult{IsSelect: true, RowsAffected: 1, Columns: []string{"n"}, Rows: [][]string{{"1"}}}
	history[0].Truncated = false
	if html := render(plain, history); strings.Contains(html, "badge-warning") {
		t.Fatal("untruncated results must not carry the badge")
	}
}
