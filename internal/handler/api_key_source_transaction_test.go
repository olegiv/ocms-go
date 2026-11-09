// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestAPIKeyUpdateRollsBackMetadataWhenSourcePolicyFails(t *testing.T) {
	db, sm := testHandlerSetup(t)
	if _, err := db.Exec(`CREATE TABLE api_key_source_cidrs (id INTEGER PRIMARY KEY, api_key_id INTEGER NOT NULL REFERENCES api_keys(id), cidr TEXT NOT NULL, UNIQUE(api_key_id, cidr))`); err != nil {
		t.Fatal(err)
	}
	user := createTestAdminUser(t, db)
	q := store.New(db)
	now := time.Now()
	key, err := q.CreateAPIKey(t.Context(), store.CreateAPIKeyParams{Name: "Original", KeyHash: "test-hash", KeyPrefix: "test", Permissions: `["pages:read"]`, IsActive: true, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err = q.ReplaceAPIKeySourceCIDRs(t.Context(), key.ID, []string{"198.51.100.1/32"}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TRIGGER reject_source_update BEFORE INSERT ON api_key_source_cidrs BEGIN SELECT RAISE(ABORT, 'test source failure'); END`); err != nil {
		t.Fatal(err)
	}
	h := NewAPIKeysHandler(db, newStatusTestRenderer(t, db, sm), sm)
	form := url.Values{"name": {"Changed"}, "permissions": {"pages:read", "mcp:access"}, "source_cidrs": {"203.0.113.1/32"}, "is_active": {"on"}}
	sendUpdate := func() *http.Request {
		req := httptest.NewRequest(http.MethodPut, "/admin/api-keys/"+strconv.FormatInt(key.ID, 10), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = addUserToContext(requestWithSession(sm, req), &user)
		route := chi.NewRouteContext()
		route.URLParams.Add("id", strconv.FormatInt(key.ID, 10))
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
		h.Update(httptest.NewRecorder(), req)
		return req
	}
	req := sendUpdate()
	got, err := q.GetAPIKeyByID(t.Context(), key.ID)
	if err != nil || got.Name != key.Name || got.Permissions != key.Permissions || got.IsActive != key.IsActive {
		t.Fatalf("failed update committed metadata: %+v, %v", got, err)
	}
	policy, err := q.ListAPIKeySourceCIDRs(t.Context(), key.ID)
	if err != nil || len(policy) != 1 || policy[0] != "198.51.100.1/32" {
		t.Fatalf("failed update changed source policy: %v, %v", policy, err)
	}
	if sm.GetString(req.Context(), "flash_type") != "error" || sm.GetString(req.Context(), "flash") == "" {
		t.Fatal("failed update did not report an error")
	}
	if _, err = db.Exec(`DROP TRIGGER reject_source_update`); err != nil {
		t.Fatal(err)
	}
	req = sendUpdate()
	got, err = q.GetAPIKeyByID(t.Context(), key.ID)
	if err != nil || got.Name != "Changed" || !strings.Contains(got.Permissions, "mcp:access") || sm.GetString(req.Context(), "flash_type") != "success" {
		t.Fatalf("normal metadata update failed: %+v, %v", got, err)
	}
	policy, err = q.ListAPIKeySourceCIDRs(t.Context(), key.ID)
	if err != nil || len(policy) != 1 || policy[0] != "203.0.113.1/32" {
		t.Fatalf("normal source update failed: %v, %v", policy, err)
	}
	if _, err = db.Exec(`DROP TABLE api_key_source_cidrs`); err != nil {
		t.Fatal(err)
	}
	form.Set("name", "Legacy")
	req = sendUpdate()
	got, err = q.GetAPIKeyByID(t.Context(), key.ID)
	if err != nil || got.Name != "Legacy" || sm.GetString(req.Context(), "flash_type") != "success" {
		t.Fatalf("legacy missing-table update changed compatibility: %+v, %v", got, err)
	}
}
