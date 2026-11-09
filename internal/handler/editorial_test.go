// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestEditorialAdminFormSaveValidationAndRoleBoundary(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	sm := testSessionManager(t)
	q := store.New(db)
	ctx := context.Background()
	now := time.Now()
	admin, err := q.CreateUser(ctx, store.CreateUserParams{Email: "editorial-ui@example.test", Role: "admin", Name: "Editorial admin", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	h := NewEditorialHandler(db, renderer, sm)
	request := func(method, path string, values url.Values, role string) *http.Request {
		req := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = requestWithSession(sm, req)
		user := admin
		user.Role = role
		return req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, user))
	}
	rr := httptest.NewRecorder()
	h.Get(rr, request("GET", editorialPath, nil, "admin"))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `name="guide"`) || !strings.Contains(rr.Body.String(), "Style guide") {
		t.Fatalf("missing editorial form: %d", rr.Code)
	}
	values := url.Values{"kind": {"guide"}, "guide": {"A concise local editorial policy."}, "preset": {"blog-post"}}
	rr = httptest.NewRecorder()
	h.Update(rr, request("POST", editorialPath, values, "admin"))
	if rr.Code != 303 {
		t.Fatalf("save status %d: %s", rr.Code, rr.Body.String())
	}
	guide, err := q.GetEditorialGuide(ctx, "")
	if err != nil || guide.Body != values.Get("guide") || guide.UpdatedBy.Int64 != admin.ID {
		t.Fatalf("guide not saved by authenticated admin %+v %v", guide, err)
	}

	protected := middleware.CSRF(middleware.DefaultCSRFConfig([]byte("test-secret-key-32-bytes-long!!!"), true))(http.HandlerFunc(h.Update))
	blocked := request("POST", editorialPath, values, "admin")
	blocked.Header.Set("Sec-Fetch-Site", "cross-site")
	blocked.Header.Set("Origin", "https://outside.test")
	rr = httptest.NewRecorder()
	protected.ServeHTTP(rr, blocked)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-site editorial save accepted: %d", rr.Code)
	}
	same := request("POST", editorialPath, values, "admin")
	same.Header.Set("Sec-Fetch-Site", "same-origin")
	rr = httptest.NewRecorder()
	protected.ServeHTTP(rr, same)
	if rr.Code != 303 {
		t.Fatalf("same-origin editorial save rejected: %d", rr.Code)
	}
	values.Set("guide", strings.Repeat("x", 8001))
	rr = httptest.NewRecorder()
	h.Update(rr, request("POST", editorialPath, values, "admin"))
	if rr.Code != 422 || !strings.Contains(rr.Body.String(), values.Get("guide")) {
		t.Fatal("invalid guidance did not preserve submitted text")
	}
	guide, err = q.GetEditorialGuide(ctx, "")
	if err != nil || guide.Body != "A concise local editorial policy." {
		t.Fatalf("invalid guidance changed storage %+v %v", guide, err)
	}
	for _, role := range []string{"editor", "public"} {
		rr = httptest.NewRecorder()
		h.Get(rr, request("GET", editorialPath, nil, role))
		if rr.Code != 403 {
			t.Fatalf("%s read settings: %d", role, rr.Code)
		}
		rr = httptest.NewRecorder()
		h.Update(rr, request("POST", editorialPath, values, role))
		if rr.Code != 403 {
			t.Fatalf("%s wrote settings: %d", role, rr.Code)
		}
	}
	values = url.Values{"kind": {"preset"}, "preset": {"bot-blog"}, "name": {"Bot notes"}, "instructions": {"Separate observations and guesses."}}
	rr = httptest.NewRecorder()
	h.Update(rr, request("POST", editorialPath, values, "admin"))
	if rr.Code != 303 {
		t.Fatalf("preset save %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.Get(rr, request("GET", editorialPath+"?preset=bot-blog", nil, "admin"))
	if !strings.Contains(rr.Body.String(), "Separate observations and guesses.") {
		t.Fatal("preset changes not displayed")
	}
}

func TestEditorialDemoModeRefusesGuidanceChanges(t *testing.T) {
	if os.Getenv("OCMS_R14_DEMO_CHILD") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestEditorialDemoModeRefusesGuidanceChanges$")
		command.Env = []string{"OCMS_DEMO_MODE=true", "OCMS_R14_DEMO_CHILD=1", "OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!!"}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("demo guard fixture: %v %s", err, output)
		}
		return
	}
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	sm := testSessionManager(t)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm})
	if err != nil {
		t.Fatal(err)
	}
	h := &EditorialHandler{renderer: renderer, sessions: sm}
	for _, kind := range []string{"guide", "preset"} {
		req := httptest.NewRequest("POST", editorialPath, strings.NewReader(url.Values{"kind": {kind}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = requestWithSession(sm, req)
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, store.User{ID: 1, Role: "admin"}))
		rr := httptest.NewRecorder()
		h.Update(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("demo %s save reached storage: %d", kind, rr.Code)
		}
	}
}
