// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestMediaDeleteWithFilesLeftCountsAsDone deletes media whose files cannot
// be removed through the admin Delete handler (HTMX and form) and
// BulkDelete. The database records are gone, so each must count the delete
// as done and log it, and warn that the files stayed on disk: the HTMX
// delete by reloading the library, which shows the warning flash, and the
// bulk delete by a flash the library shows after its own reload. The
// handlers answered 500, or counted a failure, and logged nothing, though
// the media no longer existed.
//
// Bug state: treat an ErrMediaFilesNotRemoved error from
// MediaService.Delete as a failure in Delete or BulkDelete and this fails.
func TestMediaDeleteWithFilesLeftCountsAsDone(t *testing.T) {
	db, sm := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	uploadDir := t.TempDir()
	h := NewMediaHandler(db, renderer, sm, uploadDir)
	var flash string
	router := chi.NewRouter()
	router.Use(sm.LoadAndSave)
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, withUser(r, user))
			flash = sm.GetString(r.Context(), "flash")
		})
	})
	router.Post("/admin/media/{id}/delete", h.Delete)
	router.Post("/admin/media/bulk-delete", h.BulkDelete)

	ctx := context.Background()
	queries := store.New(db)
	now := time.Now()
	uuids := []string{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333",
	}
	ids := make([]int64, len(uuids))
	for i, uuid := range uuids {
		media, err := queries.CreateMedia(ctx, store.CreateMediaParams{
			Uuid: uuid, Filename: "file.pdf", MimeType: model.MimeTypePDF, Size: 3, UploadedBy: user.ID,
			LanguageCode: "en", CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = media.ID
	}
	dirs := testutil.LockMediaFiles(t, uploadDir, uuids...)
	post := func(target, body string, header http.Header) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		for name, values := range header {
			req.Header[name] = values
		}
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

	rr := post("/admin/media/"+strconv.FormatInt(ids[0], 10)+"/delete", "", http.Header{"Hx-Request": {"true"}})
	if rr.Code != http.StatusOK || rr.Header().Get("HX-Refresh") != "true" || flash != mediaFilesLeftMessage {
		t.Errorf("HTMX delete: status %d, HX-Refresh %q, flash %q; want 200, a reload and the leftover warning",
			rr.Code, rr.Header().Get("HX-Refresh"), flash)
	}
	rr = post("/admin/media/"+strconv.FormatInt(ids[1], 10)+"/delete", "", nil)
	if rr.Code != http.StatusSeeOther || flash != mediaFilesLeftMessage {
		t.Errorf("form delete: status %d, flash %q; want a redirect with the leftover warning", rr.Code, flash)
	}
	rr = post("/admin/media/bulk-delete", fmt.Sprintf(`{"ids":[%d]}`, ids[2]), http.Header{"Content-Type": {"application/json"}})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"deleted":1`) {
		t.Errorf("bulk delete: status %d, body %s; want one deleted", rr.Code, rr.Body)
	}
	if want := "1 media deleted, but their files could not be removed from disk"; flash != want {
		t.Errorf("bulk delete flash = %q, want %q", flash, want)
	}

	for i, id := range ids {
		if _, err := queries.GetMediaByID(ctx, id); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("media %d lookup = %v, want it deleted", id, err)
		}
		if _, err := os.Stat(dirs[i]); err != nil {
			t.Fatalf("%s is gone (%v): the removal did not fail and this test is vacuous", dirs[i], err)
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT message FROM events WHERE category = 'media'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	logged := 0
	for rows.Next() {
		var message string
		if err := rows.Scan(&message); err != nil {
			t.Fatal(err)
		}
		if message != "Media deleted" {
			t.Errorf("logged %q, want \"Media deleted\"", message)
		}
		logged++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if logged != len(ids) {
		t.Errorf("logged %d media events, want %d", logged, len(ids))
	}
}
