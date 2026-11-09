// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import "github.com/olegiv/ocms-go/internal/security"

// SetSanitizePageHTML configures the same save-time sanitizer as the editor.
func (i *Importer) SetSanitizePageHTML(sanitize bool) {
	i.sanitizePageHTML = sanitize
}

// SetBlockSuspiciousMarkup configures rejection before sanitization or writes.
func (i *Importer) SetBlockSuspiciousMarkup(block bool) {
	i.blockSuspiciousMarkup = block
}

// Check selected page bodies before dry-run counting, the transaction, and ZIP
// extraction. Upload validation remains option-neutral. Like the admin policy,
// this checks all supplied pages when pages are selected, even skipped conflicts.
func (i *Importer) validatePageHTMLPolicy(data *ExportData, opts ImportOptions) []ImportError {
	if !opts.ImportPages || !i.blockSuspiciousMarkup {
		return nil
	}
	var failures []ImportError
	for _, page := range data.Pages {
		if len(security.DetectSuspiciousHTMLTokens(page.Body)) > 0 {
			failures = append(failures, ImportError{Entity: "page", ID: page.Slug,
				Message: "Body contains suspicious HTML markup that is blocked by policy"})
		}
	}
	return failures
}

func noteImportedPageHTML(result *ImportResult, slug string, removed []string) {
	if len(removed) > 0 {
		result.AddNotice("page", slug, "Unsupported HTML was removed from the page body")
	}
}
