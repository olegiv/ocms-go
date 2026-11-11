// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/pagewrite"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/web"
)

func pickerUploadFixture(t *testing.T) (*MediaHandler, store.User) {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	user := createTestUser(t, db, testUser{Email: "picker@example.test", Name: "Picker editor", Role: model.RoleEditor})
	sm := testSessionManager(t)
	renderer, err := render.New(render.Config{TemplatesFS: web.Templates, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	return NewMediaHandler(db, renderer, sm, t.TempDir()), user
}

func pickerPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var data bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(picture, picture.Bounds(), &image.Uniform{C: color.RGBA{R: 39, G: 95, B: 160, A: 255}}, image.Point{}, draw.Src)
	draw.Draw(picture, image.Rect(width/8, height/3, width*7/8, height*7/8), &image.Uniform{C: color.RGBA{R: 130, G: 195, B: 180, A: 255}}, image.Point{}, draw.Src)
	if err := png.Encode(&data, picture); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func pickerUploadRequest(t *testing.T, field string, names []string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, name := range names {
		part, err := writer.CreateFormFile(field, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/media/upload", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Accept", "application/json")
	return r
}

func TestMediaPickerUploadJSON(t *testing.T) {
	h, user := pickerUploadFixture(t)
	recorder := &testutil.WebhookRecorder{}
	h.dispatcher = recorder
	r := withUser(pickerUploadRequest(t, "file", []string{"new image.png"}, pickerPNG(t, 1200, 800)), user)
	w := httptest.NewRecorder()
	h.Upload(w, r)
	var response struct{ Item mediaAPIItem }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	item := response.Item
	stored, err := h.queries.GetMediaByID(t.Context(), item.ID)
	if err != nil || stored.UploadedBy != user.ID || stored.FolderID.Valid || stored.Width.Int64 != 1200 || stored.Height.Int64 != 800 {
		t.Fatalf("stored image: %+v %v", stored, err)
	}
	if item.Mimetype != model.MimeTypePNG || item.Thumbnail == "" || item.Filepath == "" || w.Header().Get("Location") != "" {
		t.Fatalf("picker response: %+v, headers: %v", item, w.Header())
	}
	for _, variant := range model.MediaStorageDirs() {
		if _, err := os.Stat(filepath.Join(h.uploadDir, variant, stored.Uuid, stored.Filename)); err != nil {
			t.Fatal(err)
		}
	}
	if events := recorder.Events(); len(events) != 1 || events[0].Type != model.EventMediaUploaded {
		t.Fatalf("upload webhooks: %+v", events)
	}
	listed := httptest.NewRecorder()
	h.API(listed, httptest.NewRequest(http.MethodGet, "/admin/media/api", nil))
	var library struct{ Items []mediaAPIItem }
	if err := json.Unmarshal(listed.Body.Bytes(), &library); err != nil || len(library.Items) != 1 || library.Items[0] != item {
		t.Fatalf("upload/list representation differs: %s %v", listed.Body.String(), err)
	}
	// Upload a real icon: it has an original but no generated thumbnails.
	iconBytes, err := web.Static.ReadFile("static/dist/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	iconResponse := httptest.NewRecorder()
	h.Upload(iconResponse, withUser(pickerUploadRequest(t, "file", []string{"favicon.ico"}, iconBytes), user))
	response.Item = mediaAPIItem{}
	if err := json.Unmarshal(iconResponse.Body.Bytes(), &response); err != nil || iconResponse.Code != http.StatusCreated || response.Item.Thumbnail != "" || response.Item.Filepath == "" {
		t.Fatalf("icon fallback: %d %s %v", iconResponse.Code, iconResponse.Body.String(), err)
	}
}

func TestMediaPickerUploadFailures(t *testing.T) {
	for _, tt := range []struct {
		name, field string
		names       []string
		data        []byte
		status      int
	}{
		{"missing", "file", nil, nil, 400},
		{"multiple", "file", []string{"one.png", "two.png"}, nil, 400},
		{"batch field", "files", []string{"one.png"}, nil, 400},
		{"not an image", "file", []string{"one.pdf"}, []byte("%PDF-1.4"), 400},
		{"invalid image", "file", []string{"one.png"}, []byte("invalid"), 400},
		{"corrupt PNG", "file", []string{"broken.png"}, []byte("\x89PNG\r\n\x1a\ncorrupted"), 400},
		{"over dimension", "file", []string{"wide.png"}, pickerPNG(t, 10001, 1), 400},
		{"per file limit", "file", []string{"large.png"}, make([]byte, service.MaxUploadSize+1), 413},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, user := pickerUploadFixture(t)
			w := httptest.NewRecorder()
			h.Upload(w, withUser(pickerUploadRequest(t, tt.field, tt.names, tt.data), user))
			var response struct{ Error string }
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != tt.status || response.Error == "" || w.Header().Get("Location") != "" {
				t.Fatalf("error response: %d %s %v", w.Code, w.Body.String(), err)
			}
			if tt.status == 500 && response.Error != "Error uploading image" {
				t.Fatalf("internal detail escaped: %q", response.Error)
			}
			if count, err := h.queries.CountMedia(t.Context()); err != nil || count != 0 {
				t.Fatalf("failed upload stored media: %d %v", count, err)
			}
		})
	}
	t.Run("invalid multipart", func(t *testing.T) {
		h, _ := pickerUploadFixture(t)
		r := httptest.NewRequest(http.MethodPost, "/admin/media/upload", strings.NewReader("invalid"))
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		h.Upload(w, r)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatalf("invalid multipart: %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("request limit", func(t *testing.T) {
		h, _ := pickerUploadFixture(t)
		// Stream the oversized body instead of allocating a second 100 MB fixture.
		prefix := "--picker\r\nContent-Disposition: form-data; name=\"file\"; filename=\"large.png\"\r\n\r\n"
		body := io.MultiReader(strings.NewReader(prefix), io.LimitReader(zeroUploadReader{}, maxMediaUploadRequestBytes+1))
		r := httptest.NewRequest(http.MethodPost, "/admin/media/upload", body)
		r.Header.Set("Accept", "application/json")
		r.Header.Set("Content-Type", "multipart/form-data; boundary=picker")
		w := httptest.NewRecorder()
		h.Upload(w, r)
		if w.Code != 413 {
			t.Fatalf("request limit: %d %s", w.Code, w.Body.String())
		}
	})
}

type zeroUploadReader struct{}

func (zeroUploadReader) Read(data []byte) (int, error) {
	clear(data)
	return len(data), nil
}

func TestMediaPickerUploadLegacyAndAccess(t *testing.T) {
	h, user := pickerUploadFixture(t)
	imageData := pickerPNG(t, 30, 30)
	for _, tt := range []struct {
		name, field, accept, hx string
		status                  int
	}{
		{"form", "file", "text/html", "", 303},
		{"absent accept", "file", "", "", 303},
		{"zero quality", "file", "application/json;q=0.0", "", 303},
		{"htmx", "file", "text/html", "true", 200},
		{"htmx zero quality", "file", "application/json;q=0.000", "true", 200},
		{"batch form", "files", "text/html", "", 303},
		{"batch htmx", "files", "text/html", "true", 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := requestWithSession(h.sessionManager, withUser(pickerUploadRequest(t, tt.field, []string{"normal.png"}, imageData), user))
			r.Header.Set("Accept", tt.accept)
			r.Header.Set("HX-Request", tt.hx)
			w := httptest.NewRecorder()
			h.Upload(w, r)
			if w.Code != tt.status {
				t.Fatalf("legacy response: %d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Header().Get("Content-Type"), "application/json") {
				t.Fatal("legacy/HTMX response became JSON")
			}
		})
	}
	router := chi.NewRouter()
	router.Use(middleware.CSRF(middleware.CSRFConfig{}), middleware.RequireEditor())
	router.Post("/admin/media/upload", h.Upload)
	for _, tt := range []struct {
		name, role, site string
		status           int
	}{
		{"anonymous", "", "same-origin", 303},
		{"public user", "user", "same-origin", 403},
		{"cross site editor", "editor", "cross-site", 403},
		{"editor", "editor", "same-origin", 201},
		{"admin", "admin", "same-origin", 201},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := pickerUploadRequest(t, "file", []string{"access.png"}, imageData)
			if tt.role != "" {
				u := user
				u.Role = tt.role
				r = withUser(r, u)
			}
			r.Header.Set("Sec-Fetch-Site", tt.site)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("access: %d %s", w.Code, w.Body.String())
			}
			if tt.role == "" && w.Header().Get("Location") != "/login" {
				t.Fatalf("missing session did not redirect to login: %v", w.Header())
			}
			if tt.site == "cross-site" && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
				t.Fatalf("CSRF refusal must retain its plain-text response: %v", w.Header())
			}
		})
	}
}

func TestAcceptsMediaUploadJSON(t *testing.T) {
	for _, tt := range []struct {
		accept string
		want   bool
	}{
		{"application/json;q=0", false},
		{"application/json;q=0.0", false},
		{"application/json;q=0.000", false},
		{"text/html, application/json", true},
		{"", false},
		{"text/html", false},
		{"application/json", true},
		{"application/json;q=0.5", true},
		{"application/json;q=bad", false},
	} {
		t.Run(tt.accept, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/admin/media/upload", nil)
			r.Header.Set("Accept", tt.accept)
			if got := acceptsMediaUploadJSON(r); got != tt.want {
				t.Fatalf("JSON = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPickerThumbnailLookupErrors(t *testing.T) {
	h, _ := pickerUploadFixture(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	media := store.Medium{ID: 99, Uuid: "icon", Filename: "icon.ico", MimeType: model.MimeTypeICO}
	if item := h.pickerItem(t.Context(), media); item.Thumbnail != "" || item.Filepath == "" || logs.Len() != 0 {
		t.Fatalf("no rows must silently retain original: %+v %s", item, logs.String())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if item := h.pickerItem(ctx, media); item.Thumbnail != "" || item.Filepath == "" {
		t.Fatalf("lookup fault must retain original: %+v", item)
	}
	if logs.Len() != 0 {
		t.Fatalf("canceled lookup logged: %s", logs.String())
	}
	if _, err := h.db.Exec("ALTER TABLE media_variants RENAME TO unavailable_variants"); err != nil {
		t.Fatal(err)
	}
	if item := h.pickerItem(t.Context(), media); item.Thumbnail != "" || item.Filepath == "" {
		t.Fatalf("fault lost original: %+v", item)
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "failed to get picker thumbnail") || !strings.Contains(logs.String(), "media_id=99") {
		t.Fatalf("server fault missing diagnostic: %s", logs.String())
	}
}

func TestPickerUploadEventFailure(t *testing.T) {
	h, user := pickerUploadFixture(t)
	if _, err := h.db.Exec(`CREATE TRIGGER reject_picker_event BEFORE INSERT ON events BEGIN SELECT RAISE(FAIL, 'event unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	w := httptest.NewRecorder()
	h.Upload(w, withUser(pickerUploadRequest(t, "file", []string{"event.png"}, pickerPNG(t, 30, 30)), user))
	if w.Code != http.StatusCreated {
		t.Fatalf("event failure broke upload: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(logs.String(), "failed to log picker upload event") || !strings.Contains(logs.String(), "event unavailable") {
		t.Fatalf("upload event error not handled: %s", logs.String())
	}
}

// Sniffing and rewind belong to the service, regardless of upload transport.
func TestPickerUploadUsesServiceValidation(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "media_upload.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	usesImageUpload := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch selector.Sel.Name {
		case "DetectContentType", "ReadFull", "Seek":
			t.Errorf("transport duplicates service validation: %s", selector.Sel.Name)
		case "UploadImage":
			usesImageUpload = true
		}
		return true
	})
	if !usesImageUpload {
		t.Fatal("picker must use images-only service upload")
	}
}

func TestFeaturedICOEditorMessageAndAssignments(t *testing.T) {
	h, user := pickerUploadFixture(t)
	pages := NewPagesHandler(h.db, h.renderer, h.sessionManager)
	if _, err := h.db.Exec(`INSERT INTO media (uuid, filename, mime_type, size, uploaded_by, language_code) VALUES ('ico-message', 'favicon.ico', 'image/x-icon', 1, ?, 'en')`, user.ID); err != nil {
		t.Fatal(err)
	}
	media, err := h.queries.GetMediaByUUID(t.Context(), "ico-message")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ru"} {
		got := pages.validateFeaturedImageSize(t.Context(), sql.NullInt64{Int64: media.ID, Valid: true}, lang)
		want := i18n.T(lang, "pages.error.featured_image_ico")
		if got == "" || got != want || got == "pages.error.featured_image_ico" {
			t.Fatalf("%s ICO refusal = %q, want %q", lang, got, want)
		}
		// RestoreVersion uses pageWriteMessage for the same typed refusal.
		if message, ok := pageWriteMessage(lang, pagewrite.CheckFeaturedImage(media.MimeType, 0, 0, false)); !ok || message != want {
			t.Fatalf("%s restore ICO message = %q (%v), want %q", lang, message, ok, want)
		}
	}
	ctx, now := t.Context(), time.Now()
	id := sql.NullInt64{Int64: media.ID, Valid: true}
	params := store.CreatePageParams{Title: "ICO", Slug: "ico-featured", Status: model.PageStatusDraft, AuthorID: user.ID, LanguageCode: "en", PageType: "post", CreatedAt: now, UpdatedAt: now, FeaturedImageID: id}
	var refusal *pagewrite.Refusal
	if _, err := pages.createPageGuarded(ctx, params, pageFormWrite{UserID: user.ID}); !errors.As(err, &refusal) {
		t.Fatalf("admin create accepted ICO: %v", err)
	}
	params.FeaturedImageID, params.OgImageID = sql.NullInt64{}, id
	page, err := pages.createPageGuarded(ctx, params, pageFormWrite{UserID: user.ID})
	if err != nil {
		t.Fatalf("normal create with OG ICO: %v", err)
	}
	update := pagewrite.ParamsFor(page)
	update.FeaturedImageID = id
	if _, err := pages.updatePageGuarded(ctx, update, page.Slug, pageFormWrite{UserID: user.ID, BaseRevision: &page.Revision}); !errors.As(err, &refusal) {
		t.Fatalf("admin change accepted ICO: %v", err)
	}
	if _, err := h.db.ExecContext(ctx, `UPDATE pages SET featured_image_id = ? WHERE id = ?`, id.Int64, page.ID); err != nil {
		t.Fatal(err)
	}
	update.Title = "Historical ICO"
	updated, err := pages.updatePageGuarded(ctx, update, page.Slug, pageFormWrite{UserID: user.ID, BaseRevision: &page.Revision})
	if err != nil || updated.FeaturedImageID != id {
		t.Fatalf("historical admin ICO lost: %+v %v", updated, err)
	}
}

func TestMediaPickerUploadDemo(t *testing.T) {
	if os.Getenv("OCMS_PICKER_DEMO_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMediaPickerUploadDemo$", "-test.v")
		cmd.Env = append(os.Environ(), "OCMS_DEMO_MODE=true", "OCMS_PICKER_DEMO_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "picker demo verified") {
			t.Fatalf("demo child: %v %s", err, out)
		}
		return
	}
	h, user := pickerUploadFixture(t)
	w := httptest.NewRecorder()
	h.Upload(w, withUser(pickerUploadRequest(t, "file", []string{"demo.png"}, pickerPNG(t, 30, 30)), user))
	if w.Code != 403 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("demo response: %d %s", w.Code, w.Body.String())
	}
	if count, err := h.queries.CountMedia(t.Context()); err != nil || count != 0 {
		t.Fatalf("demo stored media: %d %v", count, err)
	}
	t.Log("picker demo verified")
}

func TestMediaAPICancellationIsSilent(t *testing.T) {
	for _, query := range []string{"", "?type=image", "?q=image"} {
		t.Run(query, func(t *testing.T) {
			h, _ := pickerUploadFixture(t)
			logs := captureLogs(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			h.API(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/admin/media/api"+query, nil).WithContext(ctx))
			if logs.Len() != 0 {
				t.Fatalf("canceled listing logged: %s", logs.String())
			}
			logs.Reset()
			if _, err := h.db.Exec("ALTER TABLE media RENAME TO unavailable_media"); err != nil {
				t.Fatal(err)
			}
			h.API(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/admin/media/api"+query, nil))
			if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "failed to fetch media for API") {
				t.Fatalf("missing server diagnostic: %s", logs.String())
			}
		})
	}
}
