// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
)

// TestHTTPErrorEnvelopeSurvivesSchemaLinks exercises the production Huma
// transformer. Marshaling ToHuma alone misses its reflection-based wrapper.
func TestHTTPErrorEnvelopeSurvivesSchemaLinks(t *testing.T) {
	previous, previousContext := huma.NewError, huma.NewErrorWithContext
	t.Cleanup(func() { huma.NewError = previous; huma.NewErrorWithContext = previousContext })
	router := chi.NewRouter()
	h := Register(router, Deps{})
	huma.Register(h.API, huma.Operation{OperationID: "fixtureRevisionConflict", Method: http.MethodGet, Path: "/fixture-conflict", Errors: []int{http.StatusConflict}}, func(ctx context.Context, _ *struct{}) (*struct{ Body struct{} }, error) {
		return nil, ToHuma(ctx, &Error{Kind: ErrConflict, MachineCode: "revision_conflict", Msg: "Page changed", Fields: map[string]string{"current_revision": "3", "changed_fields": "summary"}})
	})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/fixture-conflict", nil))
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d", rr.Code)
	}
	var body ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "revision_conflict" || body.Error.Message != "Page changed" || body.Error.Details["current_revision"] != "3" || body.Error.Details["changed_fields"] != "summary" {
		t.Fatalf("schema transformer lost error envelope: %s", rr.Body.String())
	}
}
