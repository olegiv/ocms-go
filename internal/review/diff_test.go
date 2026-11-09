// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package review_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/review"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestLegacyDiffUsesOnlyRecordedFieldsAndEscapesRemainText(t *testing.T) {
	before := store.PageVersion{ID: 1, Legacy: true, Title: "Old title", Body: "<p>Old text</p>"}
	after := store.PageVersion{ID: 2, Title: "New title", Body: "<p>New text &lt;script&gt;literal&lt;/script&gt;</p>", SnapshotJson: sql.NullString{Valid: true, String: `{"title":"New title","body":"<p>New text &lt;script&gt;literal&lt;/script&gt;</p>","summary":"New summary","meta_title":"SEO title"}`}}
	diff, err := review.Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]review.Difference{}
	for _, field := range diff {
		seen[field.Field] = field
	}
	if !seen["title"].BeforeRecorded || !seen["title"].AfterRecorded || seen["summary"].BeforeRecorded || !seen["summary"].AfterRecorded {
		t.Fatal("legacy values were invented")
	}
	if strings.Contains(seen["body"].Before, "<p>") || !strings.Contains(seen["body"].After, "<script>literal</script>") {
		t.Fatal("body comparison is not plain recorded text")
	}
	if _, err = review.Compare(before, store.PageVersion{ID: 3, SnapshotJson: sql.NullString{Valid: true, String: "malformed"}}); err == nil {
		t.Fatal("corrupt snapshot silently fell back")
	}
}

func TestDiffBoundsLargeFieldsAndOmitsIdenticalText(t *testing.T) {
	before := store.PageVersion{ID: 1, Legacy: true, Title: "Same", Body: "Same body"}
	identical, err := review.Compare(before, before)
	if err != nil || len(identical) != 0 {
		t.Fatal("identical revision produced changes")
	}
	after := store.PageVersion{ID: 2, Legacy: true, Title: "Same", Body: strings.Repeat("Ж", 40000)}
	diff, err := review.Compare(before, after)
	if err != nil || len(diff) != 1 || !diff[0].Truncated || len([]rune(diff[0].After)) != 32768 {
		t.Fatal("large revision comparison was not bounded")
	}
}
