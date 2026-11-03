// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	apiv2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/media"
	"github.com/olegiv/ocms-go/internal/api/v2/pages"
	"github.com/olegiv/ocms-go/internal/api/v2/taxonomy"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestDemoModeRefusesEveryWriteOperation registers every REST v2 operation
// and checks that, in demo mode, each one with a method other than GET or
// HEAD answers 403 even for a key holding every write permission. Demo mode
// guarded only page writes, so a key created before demo mode was turned on
// could still change tags, categories and media, and a tag delete rewrites
// the revisions of every page using it (2026-11-03 audit B-2). New write
// operations are covered without a list. IsDemoMode is read once per
// process, so the check runs in a child test process.
//
// Bug state: drop api.UseMiddleware(refuseWritesInDemoMode(api)) from
// apiv2.Register and this names every taxonomy and media write.
func TestDemoModeRefusesEveryWriteOperation(t *testing.T) {
	if os.Getenv("OCMS_V2_DEMO_CHILD") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestDemoModeRefusesEveryWriteOperation$", "-test.count=1")
		command.Env = []string{"OCMS_DEMO_MODE=true", "OCMS_V2_DEMO_CHILD=1", "OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!!",
			"HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.TempDir()}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("demo-mode child: %v\n%s", err, output)
		}
		return
	}
	if !middleware.IsDemoMode() {
		t.Fatal("child process is not in demo mode")
	}
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	permissions, err := json.Marshal(model.AllPermissions())
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			key := store.ApiKey{ID: 1, CreatedBy: 1, IsActive: true, Permissions: string(permissions)}
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAPIKey, key)))
		})
	})
	h := apiv2.Register(r, apiv2.Deps{DB: db, Queries: queries})
	pages.Register(h.API, pages.NewService(db, queries, nil, nil, pages.Policy{}))
	media.Register(h.API, media.NewService(db, queries, nil, t.TempDir()))
	taxonomy.Register(h.API, taxonomy.NewService(db, queries, nil))

	writes := 0
	for path, item := range h.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{
			http.MethodPost: item.Post, http.MethodPut: item.Put, http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
		} {
			if op == nil {
				continue
			}
			writes++
			live := strings.NewReplacer("{id}", "1", "{slug}", "probe").Replace(path)
			req := httptest.NewRequest(method, live, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), apiv2.DemoReadOnlyMessage) {
				t.Errorf("%s %s (%s) in demo mode: %d %s, want 403 %q", method, live, op.OperationID, rr.Code, rr.Body.String(), apiv2.DemoReadOnlyMessage)
			}
		}
	}
	if writes < 13 {
		t.Fatalf("exercised only %d write operations", writes)
	}
}
