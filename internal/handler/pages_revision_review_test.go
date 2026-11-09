// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestRevisionReviewBindsRecordedDiffAndEvidenceToPage(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	ctx, q := t.Context(), store.New(db)
	editor := createTestUser(t, db, testUser{Email: "revision-editor@example.test", Name: "Revision editor", Role: model.RoleEditor})
	principal := provenance.Principal{UserID: editor.ID, Source: "human", Transport: "admin"}
	create := func(slug, title string) store.Page {
		t.Helper()
		now := time.Now().UTC()
		page, err := pagewrite.Insert(ctx, q, principal, store.CreatePageParams{
			Title: title, Slug: slug, Body: "<p>Body before review</p>", Status: "draft", AuthorID: editor.ID,
			LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	other := create("another-review-page", "Another review page")
	page := create("recorded-review-page", "Title before review")
	save := func(current store.Page, title, body string, evidence provenance.Evidence) store.Page {
		t.Helper()
		work, err := pagewrite.InQueriesTx(ctx, q, principal, func(tx *pagewrite.Tx) error {
			if _, trackErr := tx.TrackSystem(ctx, current.ID); trackErr != nil {
				return trackErr
			}
			params := pagewrite.ParamsFor(current)
			params.Title, params.Body = title, body
			tx.SetEvidence(evidence)
			_, updateErr := tx.Q.UpdatePage(ctx, params)
			return updateErr
		})
		if err != nil {
			t.Fatal(err)
		}
		return work.Pages[current.ID]
	}
	recorded := save(page, "Recorded review title", "<p>Recorded review body</p>", provenance.Evidence{
		Validated: []byte(`{"prompt_contract_version":"recorded-review-contract"}`),
		Reported:  []byte(`{"agent":{"claims_to_verify":["Recorded claim to verify"]}}`),
	})
	save(recorded, "Latest review title", "<p>Latest review body</p>", provenance.Evidence{
		Validated: []byte(`{"prompt_contract_version":"latest-review-contract"}`),
	})
	version, err := q.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{
		PageID: recorded.ID, Revision: sql.NullInt64{Int64: recorded.Revision, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	sm := testSessionManager(t)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	h := NewPagesHandler(db, renderer, sm)
	router := chi.NewRouter()
	router.Use(sm.LoadAndSave, middleware.RequireEditor())
	router.Get("/admin/pages/{id}/versions/{versionId}", h.RevisionReview)
	get := func(pageID int64) *httptest.ResponseRecorder {
		target := fmt.Sprintf("/admin/pages/%d/versions/%d", pageID, version.ID)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, withUser(httptest.NewRequest(http.MethodGet, target, nil), editor))
		return response
	}
	t.Run("rejects a version from another page", func(t *testing.T) {
		response := get(other.ID)
		if response.Code != http.StatusNotFound {
			t.Errorf("cross-page review status = %d, want 404", response.Code)
		}
		if body := response.Body.String(); strings.Contains(body, recorded.Title) || strings.Contains(body, "Recorded claim to verify") {
			t.Error("cross-page review rendered the other page's title or evidence")
		}
	})
	t.Run("renders the selected revision", func(t *testing.T) {
		response := get(recorded.ID)
		if response.Code != http.StatusOK {
			t.Fatalf("revision review status = %d, want 200", response.Code)
		}
		body := response.Body.String()
		for _, want := range []string{
			"Title before review</pre>", "Recorded review title</pre>", "Body before review</pre>", "Recorded review body</pre>",
			`data-provenance-class="verified"`, `data-provenance-class="validated"`, `data-provenance-class="reported"`,
			"recorded-review-contract", "Recorded claim to verify",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("revision review omitted %q", want)
			}
		}
		if strings.Contains(body, "Latest review body") || strings.Contains(body, "latest-review-contract") {
			t.Error("revision review rendered the current body or validation evidence in the historical comparison")
		}
	})
}
