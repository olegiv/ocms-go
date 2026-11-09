// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package drupal

import (
	"context"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// TestNodeAliasWithPreviewSegmentIsSkippedWithANotice keeps a legacy Drupal
// path such as /news/preview/x from becoming a hard import error. oCMS pages
// cannot have a "preview" segment (pagewrite.RouteConflict), because the
// preview middleware cannot tell "news/preview/x" from "ru/preview/x" before
// the active languages are known; the alias is reported like any other path a
// reserved route owns (security audit 2026-11-03, C-3).
//
// Bug state: drop util.ShadowsPreviewRoute from reservedPublicPath and this
// reports a "failed to create alias" error instead of the notice.
func TestNodeAliasWithPreviewSegmentIsSkippedWithANotice(t *testing.T) {
	reader := &fakeReader{
		nodes: []Node{{NID: 1, Type: "page", Title: "Guide", Status: 1, Langcode: "en"}},
		aliases: []PathAlias{
			{ID: 1, Path: "/node/1", Alias: "/news/preview/guide", Langcode: "en"},
			{ID: 2, Path: "/node/1", Alias: "/news/previews/guide", Langcode: "en"},
		},
	}
	st, _, queries := newTestState(t, reader, types.ImportOptions{ImportPages: true})
	ctx := context.Background()
	if err := NewSource().importNodes(ctx, st); err != nil {
		t.Fatal(err)
	}
	page, err := queries.GetPageByID(ctx, st.nodes[1])
	if err != nil {
		t.Fatal(err)
	}
	aliases, err := queries.GetAliasesForPage(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]bool{}
	for _, alias := range aliases {
		stored[alias.Alias] = true
	}
	if stored["news/preview/guide"] || !stored["news/previews/guide"] {
		t.Errorf("stored aliases = %v, want news/previews/guide and not news/preview/guide", stored)
	}
	if len(st.result.Errors) != 0 {
		t.Errorf("a path owned by the preview namespace was reported as an error: %v", st.result.Errors)
	}
	noticed := false
	for _, notice := range st.result.Notices {
		noticed = noticed || (strings.Contains(notice, "news/preview/guide") && strings.Contains(notice, "reserved"))
	}
	if !noticed {
		t.Errorf("no notice names the skipped alias; notices = %v", st.result.Notices)
	}
}
