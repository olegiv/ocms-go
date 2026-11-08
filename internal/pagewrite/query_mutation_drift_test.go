// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"path/filepath"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

var pageMutationBoundaries = map[string]struct{ path, function string }{
	// Finish advances the revision after detecting a tracked change.
	"AdvancePageRevision": {"internal/pagewrite/transaction.go", "Finish"},
	// The deletion transaction rejects all references to this language,
	// including the translations that would otherwise cascade.
	"DeleteLanguage": {"internal/handler/languages.go", "deleteLanguageIfUnused"},
}

// checkPageMutationQueryCatalog requires a caller rule for every writer in
// either catalog. A transaction alone cannot prove which rows were tracked.
func checkPageMutationQueryCatalog(t *testing.T) {
	mutations := testutil.SQLCMutations(t, filepath.Join("..", "store"),
		"pages", "page_tags", "page_categories", "page_aliases", "translations", "page_ai_access")
	found := make(map[string]bool)
	for _, mutation := range mutations {
		found[mutation.Name] = true
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
}
