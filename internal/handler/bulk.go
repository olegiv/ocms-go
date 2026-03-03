// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

const (
	defaultBulkActionMaxBatch = 200

	bulkScopePages    = "pages-list"
	bulkScopeTags     = "tags-list"
	bulkScopeUsers    = "users-list"
	bulkScopeAPIKeys  = "api-keys-list"
	bulkScopeMedia    = "media-library"
	bulkScopeFormsSub = "form-submissions-"
)

type bulkIDsPayload struct {
	IDs []int64 `json:"ids"`
}

type bulkActionFailedItem struct {
	ID     int64  `json:"id"`
	Reason string `json:"reason"`
}

func parseBulkActionIDs(w http.ResponseWriter, r *http.Request, maxBatch int) ([]int64, error) {
	if maxBatch <= 0 {
		maxBatch = defaultBulkActionMaxBatch
	}

	var payload bulkIDsPayload
	if err := decodeJSONWithLimit(w, r, &payload, MaxJSONBodyBytes); err != nil {
		return nil, errors.New("Invalid request body")
	}
	if len(payload.IDs) == 0 {
		return nil, errors.New("At least one ID is required")
	}

	seen := make(map[int64]struct{}, len(payload.IDs))
	normalized := make([]int64, 0, len(payload.IDs))
	for _, id := range payload.IDs {
		if id <= 0 {
			return nil, errors.New("IDs must be positive integers")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}

	if len(normalized) == 0 {
		return nil, errors.New("At least one ID is required")
	}
	if len(normalized) > maxBatch {
		return nil, fmt.Errorf("Maximum %d IDs allowed", maxBatch)
	}

	return normalized, nil
}

func writeBulkActionSuccess(w http.ResponseWriter, deleted int, failed []bulkActionFailedItem) {
	if failed == nil {
		failed = make([]bulkActionFailedItem, 0)
	}
	writeJSONSuccess(w, map[string]any{
		"deleted": deleted,
		"failed":  failed,
	})
}

func bulkPaginationAction(scope string, deleteURL string) *adminviews.PaginationBulkAction {
	return &adminviews.PaginationBulkAction{
		Enabled:   true,
		Scope:     scope,
		DeleteURL: deleteURL,
	}
}

func formSubmissionsBulkScope(formID int64) string {
	return bulkScopeFormsSub + strconv.FormatInt(formID, 10)
}

func formSubmissionsBulkDeleteURL(formID int64) string {
	return fmt.Sprintf(redirectAdminFormsIDSubmissions, formID) + RouteSuffixBulkDelete
}
