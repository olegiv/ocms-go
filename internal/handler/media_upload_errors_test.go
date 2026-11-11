// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/web"
)

func TestMediaUploadFailurePolicy(t *testing.T) {
	for _, transport := range []string{"json", "htmx", "form", "batch htmx", "batch form"} {
		for _, failure := range []string{"client", "server", "canceled", "unroutable"} {
			t.Run(transport+"/"+failure, func(t *testing.T) {
				h, user := pickerUploadFixture(t)
				data := pickerPNG(t, 30, 30)
				if failure == "unroutable" {
					if _, err := h.db.Exec("UPDATE languages SET is_active=0 WHERE is_default=1"); err != nil {
						t.Fatal(err)
					}
				}
				if failure == "client" {
					data = []byte("invalid image")
				}
				if failure == "server" {
					// A real filesystem fault includes this private path in the service error.
					blocked := filepath.Join(t.TempDir(), "private-upload-directory")
					if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
						t.Fatal(err)
					}
					h.mediaService = service.NewMediaService(h.db, blocked)
				}
				field := "file"
				if strings.HasPrefix(transport, "batch") {
					field = "files"
				}
				r := requestWithSession(h.sessionManager, withUser(pickerUploadRequest(t, field, []string{"test.png"}, data), user))
				if transport != "json" {
					r.Header.Set("Accept", "text/html")
				}
				if strings.HasSuffix(transport, "htmx") {
					r.Header.Set("HX-Request", "true")
				}
				if failure == "canceled" {
					ctx, cancel := context.WithCancel(r.Context())
					cancel()
					r = r.WithContext(ctx)
				}
				logs := captureLogs(t)
				w := httptest.NewRecorder()
				h.Upload(w, r)
				logged := logs.String()
				switch failure {
				case "canceled":
					if logged != "" {
						t.Errorf("canceled upload logged: %s", logged)
					}
				case "client":
					if strings.Contains(logged, "level=ERROR") || !strings.Contains(logged, "level=INFO") {
						t.Errorf("client refusal log levels: %s", logged)
					}
				case "unroutable":
					if !strings.Contains(logged, "level=ERROR") || !strings.Contains(logged, "default language is not routable") || strings.Contains(logged, "<nil>") {
						t.Errorf("unroutable language diagnostic: %s", logged)
					}
				case "server":
					if !strings.Contains(logged, "level=ERROR") || !strings.Contains(logged, "private-upload-directory") {
						t.Errorf("server failure missing diagnostic: %s", logged)
					}
				}
				if failure == "client" || failure == "server" {
					if !strings.Contains(logged, "filename=test.png") || !strings.Contains(logged, "failed to upload") {
						t.Errorf("missing upload context: %s", logged)
					}
				}
				body := w.Body.String()
				flash := h.sessionManager.GetString(r.Context(), "flash")
				if strings.Contains(body+flash, "private-upload-directory") || strings.Contains(body+flash, "failed to process image") {
					t.Fatalf("service internals escaped: body=%q flash=%q", body, flash)
				}
				if strings.HasPrefix(transport, "batch") && failure != "canceled" && failure != "unroutable" {
					if !strings.Contains(body+flash, "0 files") {
						t.Errorf("batch failure response: %d %q %q", w.Code, body, flash)
					}
					return
				}
				message := "Error uploading file"
				status := http.StatusInternalServerError
				if transport == "json" {
					message = "Error uploading image"
					var response struct{ Error string }
					if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					body = response.Error
				}
				if failure == "client" {
					message = "file type text/plain is not allowed"
					status = http.StatusBadRequest
				}
				if strings.HasSuffix(transport, "form") {
					status = http.StatusSeeOther
					if failure == "canceled" || failure == "unroutable" {
						message = "Error uploading file"
					} else {
						message = "Upload failed: " + message
					}
					body = flash
				}
				if w.Code != status || body != message {
					t.Errorf("response = %d %q, want %d %q", w.Code, body, status, message)
				}
			})
		}
	}
}

// Scan direct error formatting and writes in every media handler. This is a
// syntax tripwire (err/*Err identifiers and .Error()), not alias/dataflow analysis.
// Logs may contain errors; only the specific parseBulkActionIDs refusal is curated.
func TestMediaHandlersDoNotExposeRawErrorsOrFilenames(t *testing.T) {
	paths, err := filepath.Glob("media*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, violation := range mediaResponseViolations(t, path, string(source)) {
			t.Error(violation)
		}
	}
}

func mediaResponseViolations(t *testing.T, path, source string) []string {
	t.Helper()
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	curated := map[token.Pos]bool{}
	for node := range ast.Preorder(file) {
		fn, ok := node.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "BulkDelete" {
			continue
		}
		for i, statement := range fn.Body.List {
			branch, ok := statement.(*ast.IfStmt)
			if !ok || i == 0 {
				continue
			}
			assignment, ok := fn.Body.List[i-1].(*ast.AssignStmt)
			if !ok || len(assignment.Rhs) != 1 {
				continue
			}
			parse, ok := assignment.Rhs[0].(*ast.CallExpr)
			if !ok {
				continue
			}
			name, ok := parse.Fun.(*ast.Ident)
			if !ok || name.Name != "parseBulkActionIDs" {
				continue
			}
			// Exempt exactly writeJSONError(w, http.StatusBadRequest, err.Error()).
			for _, child := range branch.Body.List {
				expression, ok := child.(*ast.ExprStmt)
				if !ok {
					continue
				}
				call, ok := expression.X.(*ast.CallExpr)
				if !ok || len(call.Args) != 3 {
					continue
				}
				name, ok := call.Fun.(*ast.Ident)
				if !ok || name.Name != "writeJSONError" {
					continue
				}
				writer, wok := call.Args[0].(*ast.Ident)
				status, sok := call.Args[1].(*ast.SelectorExpr)
				raw, rok := call.Args[2].(*ast.CallExpr)
				if wok && writer.Name == "w" && sok && status.Sel.Name == "StatusBadRequest" && rok {
					if method, ok := raw.Fun.(*ast.SelectorExpr); ok && method.Sel.Name == "Error" && len(raw.Args) == 0 {
						if value, ok := method.X.(*ast.Ident); ok && value.Name == "err" {
							curated[raw.Pos()] = true
						}
					}
				}
			}
		}
	}
	hasError := func(node ast.Node) bool {
		for child := range ast.Preorder(node) {
			if name, ok := child.(*ast.Ident); ok && (name.Name == "err" || strings.HasSuffix(name.Name, "Err")) {
				return true
			}
		}
		return false
	}
	ast.PreorderStack(file, nil, func(node ast.Node, stack []ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if selected, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := selected.X.(*ast.Ident); ok && pkg.Name == "slog" {
					return false
				}
				if selected.Sel.Name == "Error" && len(call.Args) == 0 && !curated[call.Pos()] {
					violations = append(violations, fmt.Sprintf("%s: raw error text", set.Position(call.Pos())))
				}
				switch selected.Sel.Name {
				case "Sprint", "Sprintln", "Sprintf", "Fprint", "Fprintln", "Fprintf", "WriteString", "Write":
					for _, arg := range call.Args {
						if hasError(arg) {
							violations = append(violations, fmt.Sprintf("%s: formatted/written error", set.Position(call.Pos())))
							break
						}
					}
				}
			}
		}
		selected, ok := node.(*ast.SelectorExpr)
		if !ok || selected.Sel.Name != "Filename" {
			return true
		}
		for i := len(stack) - 1; i >= 0; i-- {
			call, ok := stack[i].(*ast.CallExpr)
			if !ok {
				continue
			}
			fun, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "html" && fun.Sel.Name == "EscapeString" {
				break
			}
			if fun.Sel.Name == "Fprintf" {
				violations = append(violations, fmt.Sprintf("%s: unescaped filename", set.Position(node.Pos())))
			}
		}
		return true
	})
	return violations
}

func TestMediaErrorDriftScanCoverage(t *testing.T) {
	for _, statement := range []string{
		`http.Error(w, err.Error(), 500)`, `flashError(w, r, renderer, "/", fmt.Sprint(err))`,
		`fmt.Fprintf(w, "%v", err)`, `fmt.Fprintf(w, "%s", writeErr)`,
		`fmt.Fprint(w, err)`, `fmt.Fprintln(w, err)`, `fmt.Sprintln(err)`,
		`io.WriteString(w, fmt.Sprint(err))`, `io.WriteString(w, "failed: " + err.Error())`,
		`w.Write([]byte("failed: " + err.Error()))`,
		`fmt.Fprintf(w, "%s", media.Filename)`,
	} {
		t.Run(statement, func(t *testing.T) {
			source := "package handler; func (h *MediaHandler) BulkDelete() { " + statement + " }"
			if len(mediaResponseViolations(t, "fixture.go", source)) == 0 {
				t.Fatal("unsafe response passed scan")
			}
		})
	}
	safe := `package handler; func (h *MediaHandler) BulkDelete() {
        ids, err := parseBulkActionIDs(w, r, limit)
        if err != nil { writeJSONError(w, http.StatusBadRequest, err.Error()); return }
        slog.Error("failed", "error", err)
        fmt.Fprintf(w, "%s", html.EscapeString(media.Filename))
        io.WriteString(w, clientError.Message)
    }`
	if got := mediaResponseViolations(t, "fixture.go", safe); len(got) != 0 {
		t.Fatalf("safe response rejected: %v", got)
	}
}

type disconnectedMediaWriter struct{ header http.Header }

func (w disconnectedMediaWriter) Header() http.Header     { return w.header }
func (disconnectedMediaWriter) WriteHeader(int)           {}
func (disconnectedMediaWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestMediaUploadDisconnectedResponse(t *testing.T) {
	for _, transport := range []string{"json", "htmx"} {
		for _, valid := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/valid=%v", transport, valid), func(t *testing.T) {
				h, user := pickerUploadFixture(t)
				data := pickerPNG(t, 30, 30)
				if !valid {
					data = []byte("invalid")
				}
				r := withUser(pickerUploadRequest(t, "file", []string{"disconnect.png"}, data), user)
				if transport == "htmx" {
					r.Header.Set("Accept", "text/html")
					r.Header.Set("HX-Request", "true")
				}
				logs := captureLogs(t)
				h.Upload(disconnectedMediaWriter{header: make(http.Header)}, r)
				if strings.Contains(logs.String(), "level=ERROR") {
					t.Fatalf("response disconnect logged at ERROR: %s", logs.String())
				}
				count, err := h.queries.CountMedia(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				want := int64(0)
				if valid {
					want = 1
				}
				if count != want {
					t.Fatalf("stored=%d, want %d", count, want)
				}
			})
		}
	}
}

func TestRegenerateVariantsFailureIsPrivate(t *testing.T) {
	for _, tc := range []struct {
		name, mimeType string
		hx             bool
	}{
		{"server/htmx", "image/png", true},
		{"server/form", "image/png", false},
		{"ICO/htmx", "image/x-icon", true},
		{"ICO/form", "image/x-icon", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, user := pickerUploadFixture(t)
			id, err := h.db.Exec(`INSERT INTO media (uuid,filename,mime_type,size,uploaded_by,language_code) VALUES ('regen','original.png',?,1,?,'en')`, tc.mimeType, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaID, err := id.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(h.uploadDir, "originals", "regen", "original.png")
			if tc.mimeType == "image/png" {
				// A symlink loop reliably produces an OS error containing the private path.
				if err := os.MkdirAll(filepath.Dir(original), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("original.png", original); err != nil {
					t.Fatal(err)
				}
			}
			router := chi.NewRouter()
			router.Post("/admin/media/{id}/regenerate", h.RegenerateVariants)
			r := requestWithSession(h.sessionManager, withUser(httptest.NewRequest("POST", fmt.Sprintf("/admin/media/%d/regenerate", mediaID), nil), user))
			if tc.hx {
				r.Header.Set("HX-Request", "true")
			}
			logs := captureLogs(t)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			message := w.Body.String()
			status := 500
			wantMessage := "Error regenerating variants"
			if tc.mimeType == "image/x-icon" {
				status = http.StatusBadRequest
				wantMessage = "This image type has no variants to regenerate"
			}
			if !tc.hx {
				message = h.sessionManager.GetString(r.Context(), "flash")
				status = 303
			}
			if w.Code != status || message != wantMessage {
				t.Fatalf("response=%d %q", w.Code, message)
			}
			if tc.mimeType == "image/x-icon" {
				if strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "level=INFO") {
					t.Fatalf("ICO refusal log levels: %s", logs.String())
				}
				return
			}
			if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "media_id="+strconv.FormatInt(mediaID, 10)) || !strings.Contains(logs.String(), "failed to regenerate variants") || !strings.Contains(logs.String(), original) {
				t.Fatalf("missing diagnostic: %s", logs.String())
			}
		})
	}
}

func TestMediaServiceUploadCancellationAfterLanguageLookup(t *testing.T) {
	// The raster branch and the stored-original branch (ICO, documents, video)
	// each wrap the record-creation error; both must keep context.Canceled.
	icon, err := web.Static.ReadFile("static/dist/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []struct {
		filename string
		data     func(*testing.T) []byte
	}{
		{"canceled.png", func(t *testing.T) []byte { return pickerPNG(t, 30, 30) }},
		{"canceled.ico", func(*testing.T) []byte { return icon }},
	} {
		for _, imagesOnly := range []bool{true, false} {
			t.Run(payload.filename+"/imagesOnly="+strconv.FormatBool(imagesOnly), func(t *testing.T) {
				testMediaServiceUploadCancellation(t, payload.filename, payload.data(t), imagesOnly)
			})
		}
	}
}

func testMediaServiceUploadCancellation(t *testing.T, filename string, data []byte, imagesOnly bool) {
	t.Helper()
	h, user := pickerUploadFixture(t)
	language, ok := h.mediaUploadLanguage(t.Context())
	if !ok {
		t.Fatal("language lookup failed before upload")
	}
	r := pickerUploadRequest(t, "file", []string{filename}, data)
	if err := r.ParseMultipartForm(service.MaxUploadSize); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.MultipartForm.RemoveAll(); err != nil {
			t.Error(err)
		}
	})
	header := r.MultipartForm.File["file"][0]
	file, err := header.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	logs := captureLogs(t)
	upload := h.mediaService.Upload
	if imagesOnly {
		upload = h.mediaService.UploadImage
	}
	result, err := upload(ctx, file, header, user.ID, nil, language.Code)
	failure := classifyMediaError(err, "Error uploading image")
	if result != nil || !failure.canceled || !strings.Contains(err.Error(), "failed to create media record") {
		t.Fatalf("upload result=%+v error=%v classification=%+v", result, err, failure)
	}
	failure.log(ctx, "failed to upload image", err)
	if logs.Len() != 0 {
		t.Errorf("canceled service upload logged: %s", logs.String())
	}
	count, err := h.queries.CountMedia(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("canceled upload left %d media rows", count)
	}
	if err := filepath.WalkDir(h.uploadDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			t.Errorf("canceled upload left file %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMediaErrorClassificationIsSilent(t *testing.T) {
	for _, tc := range []struct {
		name             string
		err              error
		status           int
		message          string
		client, canceled bool
	}{
		{"fault", errors.New("private path"), 500, "Safe message", false, false},
		{"client", fmt.Errorf("wrapped: %w", &service.ClientError{Message: "Invalid image"}), 400, "Invalid image", true, false},
		{"cancel", fmt.Errorf("wrapped: %w", context.Canceled), 500, "Safe message", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			got := classifyMediaError(tc.err, "Safe message")
			if got.status != tc.status || got.message != tc.message || got.client != tc.client || got.canceled != tc.canceled {
				t.Fatalf("classification=%+v", got)
			}
			if logs.Len() != 0 {
				t.Fatalf("classification logged: %s", logs.String())
			}
		})
	}
}
