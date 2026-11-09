// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"path/filepath"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

type writerLocation struct{ path, function string }

var pageMutationTables = []string{
	"pages", "page_tags", "page_categories", "page_aliases", "translations", "page_ai_access",
	"page_versions", "content_provenance", "idempotency_keys", "preview_links",
}

// These account and credential mutations invalidate shares via SQLite triggers
// or foreign keys. They are inventoried separately from direct page writers.
var indirectPreviewMutations = map[string]bool{
	"UpdateAPIKey": true, "DeactivateAPIKey": true, "DeleteAPIKey": true,
	"RevokeConnectedOAuthGrant": true, "RevokeConnectedAPIKey": true,
	"BumpUserSessionVersion": true, "RevokeOAuthGrant": true,
	"RevokeOAuthGrantByAccess": true, "RevokeOwnedOAuthGrant": true,
	"DeleteRetiredOAuthGrants": true, "DeleteUnredeemedOAuthGrants": true,
	"UpdateUser": true, "UpdateUserPassword": true, "DeleteUser": true,
}

var pageMutationBoundaries = map[string][]writerLocation{
	// Finish advances the revision after detecting a tracked change.
	"AdvancePageRevision": {{"internal/pagewrite/transaction.go", "Finish"}},
	// The deletion transaction rejects all references to this language,
	// including the translations that would otherwise cascade.
	"DeleteLanguage":    {{"internal/handler/languages.go", "deleteLanguageIfUnused"}},
	"CreatePageVersion": nil, // Legacy snapshots have no production writer.
	"CreateCompletePageVersion": {
		{"internal/pagewrite/transaction.go", "Finish"},
		{"internal/store/seed_demo.go", "seedDemoPages"},
	},
	"CreateContentProvenance": {
		{"internal/provenance/provenance.go", "Record"},
		{"internal/store/seed_demo.go", "seedDemoPages"},
	},
	"DeletePageVersions":     {{"internal/pagewrite/delete.go", "Delete"}},
	"CreateIdempotencyKey":   {{"internal/idempotency/idempotency.go", "Store"}},
	"CompactIdempotencyKeys": {{"internal/scheduler/scheduler.go", "processScheduledPages"}},
	"CompactPageIdempotencyKeys": {
		{"internal/pagewrite/delete.go", "Delete"},
		{"internal/pagewrite/queries.go", "DeleteFromQueries"},
		{"internal/pagewrite/transaction.go", "Finish"},
	},
	"CreatePreviewLink":             {{"internal/preview/service.go", "Issue"}},
	"RevokePagePreviewLink":         {{"internal/handler/pages_review.go", "RevokePreviewLink"}},
	"RevokeAgentPreviewLink":        {{"modules/mcpserver/handlers.go", "handleRevokeShareLink"}},
	"DeleteRetiredOAuthPreviews":    {{"internal/oauth/retention.go", "cleanupAt"}},
	"DeletePrunedPageVersion":       {{"internal/pagewrite/retention.go", "pruneAIDraftRevisionsAt"}},
	"DeletePrunedContentProvenance": {{"internal/pagewrite/retention.go", "pruneAIDraftRevisionsAt"}},
	"CompactPrunedRevisionReplays":  {{"internal/pagewrite/retention.go", "pruneAIDraftRevisionsAt"}},
}

// checkPageMutationQueryCatalog requires a caller rule for every writer in
// either catalog. A transaction alone cannot prove which rows were tracked.
func checkPageMutationQueryCatalog(t *testing.T) {
	mutations := testutil.SQLCMutations(t, filepath.Join("..", "store"), pageMutationTables...)
	found := make(map[string]bool)
	for _, mutation := range mutations {
		found[mutation.Name] = true
		if indirectPreviewMutations[mutation.Name] && !rawPageMutation.MatchString(mutation.SQL) {
			continue
		}
		if _, guarded := pageMutationBoundaries[mutation.Name]; !guarded && !pageMutations[mutation.Name] {
			t.Errorf("%s: %s writes page state without a tracking rule", mutation.Path, mutation.Name)
		}
	}

	for name := range pageMutations {
		if !found[name] {
			t.Errorf("%s has a tracking rule but no page mutation in the sqlc catalog", name)
		}
	}
	for name := range pageMutationBoundaries {
		if !found[name] {
			t.Errorf("%s has a dedicated guard but no page mutation in the sqlc catalog", name)
		}
	}
	for name := range indirectPreviewMutations {
		if !found[name] {
			t.Errorf("%s no longer has a cataloged preview invalidation", name)
		}
	}
}
