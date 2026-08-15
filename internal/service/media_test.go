// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestClientError(t *testing.T) {
	t.Run("implements error interface", func(t *testing.T) {
		var err error = &ClientError{Message: "file too large"}
		if err.Error() != "file too large" {
			t.Errorf("Error() = %q, want %q", err.Error(), "file too large")
		}
	})

	t.Run("detectable with errors.As", func(t *testing.T) {
		err := &ClientError{Message: "invalid file"}
		var target *ClientError
		if !errors.As(err, &target) {
			t.Fatal("errors.As should match ClientError")
		}
		if target.Message != "invalid file" {
			t.Errorf("Message = %q, want %q", target.Message, "invalid file")
		}
	})

	t.Run("validation errors are ClientError", func(t *testing.T) {
		// Test that validation functions return ClientError
		validationCases := []struct {
			name string
			fn   func() error
		}{
			{"empty file", func() error {
				f := tempMultipartFile(t, []byte{})
				defer f.Close()
				_, err := detectAndValidateUploadMime(f, "test.png")
				return err
			}},
			{"unsupported mime", func() error {
				_, err := canonicalizeUploadFilename("file.txt", "text/plain")
				return err
			}},
		}

		for _, tc := range validationCases {
			t.Run(tc.name, func(t *testing.T) {
				err := tc.fn()
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var clientErr *ClientError
				if !errors.As(err, &clientErr) {
					t.Errorf("expected ClientError, got %T: %v", err, err)
				}
			})
		}
	})
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal.jpg", "normal.jpg"},
		{"file name.jpg", "file-name.jpg"},
		{"file'name.jpg", "filename.jpg"},
		{"file\"name.jpg", "filename.jpg"},
		{"<script>.jpg", "script.jpg"},
		{"file&name.jpg", "filename.jpg"},
		{"path/to/file.jpg", "file.jpg"},
		{"../../../etc/passwd", "passwd.bin"},
		{"noextension", "noextension.bin"},
		{"file#name?.jpg", "filename.jpg"},
		{"file%20name.jpg", "file20name.jpg"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := sanitizeFilename(tt.input); got != tt.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateMediaStoredFilename(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "valid filename", input: "image.jpg", want: "image.jpg"},
		{name: "valid with spaces", input: "my image.jpg", want: "my image.jpg"},
		{name: "empty", input: "", wantErr: true},
		{name: "absolute path", input: "/etc/passwd", wantErr: true},
		{name: "traversal", input: "../../secret.png", wantErr: true},
		{name: "nested path", input: "foo/bar.png", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateMediaStoredFilename(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateMediaStoredFilename(%q) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateMediaStoredFilename(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("validateMediaStoredFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetMimeTypeFromExtension(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"image.jpg", model.MimeTypeJPEG},
		{"image.jpeg", model.MimeTypeJPEG},
		{"IMAGE.JPG", model.MimeTypeJPEG},
		{"photo.png", model.MimeTypePNG},
		{"animation.gif", model.MimeTypeGIF},
		{"modern.webp", model.MimeTypeWebP},
		{"document.pdf", model.MimeTypePDF},
		{"video.mp4", model.MimeTypeMP4},
		{"video.webm", model.MimeTypeWebM},
		{"unknown.xyz", "application/octet-stream"},
		{"noextension", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			if got := getMimeTypeFromExtension(tt.filename); got != tt.want {
				t.Errorf("getMimeTypeFromExtension(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestIsImageMimeType(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{model.MimeTypeJPEG, true},
		{model.MimeTypePNG, true},
		{model.MimeTypeGIF, true},
		{model.MimeTypeWebP, true},
		{model.MimeTypePDF, false},
		{model.MimeTypeMP4, false},
		{model.MimeTypeWebM, false},
		{"text/plain", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			if got := isImageMimeType(tt.mimeType); got != tt.want {
				t.Errorf("isImageMimeType(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestAllowedMimeTypes(t *testing.T) {
	// Verify all expected types are allowed
	expected := []string{
		model.MimeTypeJPEG,
		model.MimeTypePNG,
		model.MimeTypeGIF,
		model.MimeTypeWebP,
		model.MimeTypePDF,
		model.MimeTypeMP4,
		model.MimeTypeWebM,
	}

	for _, mt := range expected {
		if !AllowedMimeTypes[mt] {
			t.Errorf("Expected %q to be in AllowedMimeTypes", mt)
		}
	}

	// Verify unknown types are not allowed
	disallowed := []string{
		"text/plain",
		"application/javascript",
		"text/html",
	}

	for _, mt := range disallowed {
		if AllowedMimeTypes[mt] {
			t.Errorf("Expected %q to NOT be in AllowedMimeTypes", mt)
		}
	}
}

func TestDetectAndValidateUploadMime(t *testing.T) {
	t.Run("accepts valid png with png extension", func(t *testing.T) {
		file := tempMultipartFile(t, []byte{
			0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
			0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		})
		defer func() { _ = file.Close() }()

		mimeType, err := detectAndValidateUploadMime(file, "image.png")
		if err != nil {
			t.Fatalf("expected valid upload, got error: %v", err)
		}
		if mimeType != model.MimeTypePNG {
			t.Fatalf("expected mime %q, got %q", model.MimeTypePNG, mimeType)
		}
	})

	t.Run("rejects extension mismatch", func(t *testing.T) {
		file := tempMultipartFile(t, []byte{
			0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
			0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		})
		defer func() { _ = file.Close() }()

		_, err := detectAndValidateUploadMime(file, "image.html")
		if err == nil {
			t.Fatal("expected extension mismatch error, got nil")
		}
	})

	t.Run("rejects disallowed content type even with allowed extension", func(t *testing.T) {
		file := tempMultipartFile(t, []byte("<!doctype html><html><body>x</body></html>"))
		defer func() { _ = file.Close() }()

		_, err := detectAndValidateUploadMime(file, "image.png")
		if err == nil {
			t.Fatal("expected disallowed mime type error, got nil")
		}
	})
}

func TestCanonicalizeUploadFilename(t *testing.T) {
	tests := []struct {
		name            string
		filename        string
		mimeType        string
		want            string
		wantErr         bool
		wantClientError bool
	}{
		{
			name:     "normalizes jpeg extension",
			filename: "photo.jpeg",
			mimeType: model.MimeTypeJPEG,
			want:     "photo.jpg",
		},
		{
			name:     "preserves sanitized basename",
			filename: "my file.png",
			mimeType: model.MimeTypePNG,
			want:     "my-file.png",
		},
		{
			name:     "empty basename falls back",
			filename: ".jpeg",
			mimeType: model.MimeTypeJPEG,
			want:     "file.jpg",
		},
		{
			name:            "unsupported mime",
			filename:        "note.txt",
			mimeType:        "text/plain",
			wantErr:         true,
			wantClientError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canonicalizeUploadFilename(tt.filename, tt.mimeType)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantClientError {
					var clientErr *ClientError
					if !errors.As(err, &clientErr) {
						t.Errorf("expected ClientError, got %T: %v", err, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("canonicalizeUploadFilename() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("canonicalizeUploadFilename() = %q, want %q", got, tt.want)
			}
		})
	}
}

func tempMultipartFile(t *testing.T, content []byte) *os.File {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "upload.bin")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}

	return file
}

func TestGetAdminGridPreviewURL(t *testing.T) {
	makeVariantFile := func(t *testing.T, rootDir, variantDir, uuid, filename string) {
		t.Helper()
		dirPath := filepath.Join(rootDir, variantDir, uuid)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dirPath, err)
		}
		filePath := filepath.Join(dirPath, filename)
		if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile(%q): %v", filePath, err)
		}
	}

	testMedia := store.Medium{
		Uuid:     "media-uuid",
		Filename: "image.jpg",
		MimeType: model.MimeTypeJPEG,
	}

	t.Run("uses grid when available", func(t *testing.T) {
		uploadsDir := t.TempDir()
		makeVariantFile(t, uploadsDir, model.VariantGrid, testMedia.Uuid, testMedia.Filename)
		makeVariantFile(t, uploadsDir, model.VariantThumbnail, testMedia.Uuid, testMedia.Filename)
		makeVariantFile(t, uploadsDir, "originals", testMedia.Uuid, testMedia.Filename)

		svc := NewMediaService(nil, uploadsDir)
		got := svc.GetAdminGridPreviewURL(testMedia)
		want := "/uploads/grid/media-uuid/image.jpg"
		if got != want {
			t.Errorf("GetAdminGridPreviewURL() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to thumbnail when grid missing", func(t *testing.T) {
		uploadsDir := t.TempDir()
		makeVariantFile(t, uploadsDir, model.VariantThumbnail, testMedia.Uuid, testMedia.Filename)
		makeVariantFile(t, uploadsDir, "originals", testMedia.Uuid, testMedia.Filename)

		svc := NewMediaService(nil, uploadsDir)
		got := svc.GetAdminGridPreviewURL(testMedia)
		want := "/uploads/thumbnail/media-uuid/image.jpg"
		if got != want {
			t.Errorf("GetAdminGridPreviewURL() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to original when variants missing", func(t *testing.T) {
		uploadsDir := t.TempDir()
		makeVariantFile(t, uploadsDir, "originals", testMedia.Uuid, testMedia.Filename)

		svc := NewMediaService(nil, uploadsDir)
		got := svc.GetAdminGridPreviewURL(testMedia)
		want := "/uploads/originals/media-uuid/image.jpg"
		if got != want {
			t.Errorf("GetAdminGridPreviewURL() = %q, want %q", got, want)
		}
	})

	t.Run("returns stable original URL even when files missing", func(t *testing.T) {
		uploadsDir := t.TempDir()
		svc := NewMediaService(nil, uploadsDir)

		got := svc.GetAdminGridPreviewURL(testMedia)
		want := "/uploads/originals/media-uuid/image.jpg"
		if got != want {
			t.Errorf("GetAdminGridPreviewURL() = %q, want %q", got, want)
		}
	})

	t.Run("returns empty for non-image media", func(t *testing.T) {
		uploadsDir := t.TempDir()
		svc := NewMediaService(nil, uploadsDir)

		nonImage := testMedia
		nonImage.MimeType = model.MimeTypePDF
		got := svc.GetAdminGridPreviewURL(nonImage)
		if got != "" {
			t.Errorf("GetAdminGridPreviewURL() = %q, want empty string", got)
		}
	})
}

func TestMediaServiceDeleteRejectsMalformedUUIDBeforeDatabaseMutation(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	now := time.Now()
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "cleanup@example.com", PasswordHash: "hash", Role: "admin", Name: "Cleanup",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	language, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("GetDefaultLanguage() error = %v", err)
	}
	media, err := queries.CreateMedia(ctx, store.CreateMediaParams{
		Uuid: "legacy-media-1", Filename: "legacy.pdf", MimeType: model.MimeTypePDF, Size: 6,
		Width: sql.NullInt64{}, Height: sql.NullInt64{}, UploadedBy: user.ID,
		LanguageCode: language.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateMedia() error = %v", err)
	}
	if _, err := queries.CreateMediaVariant(ctx, store.CreateMediaVariantParams{
		MediaID: media.ID, Type: model.VariantThumbnail, Width: 150, Height: 150, Size: 1, CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateMediaVariant() error = %v", err)
	}
	uploadRoot := t.TempDir()
	sentinel := filepath.Join(uploadRoot, model.OriginalsDir, media.Uuid, media.Filename)
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err = NewMediaService(db, uploadRoot).Delete(ctx, media.ID)
	if err == nil || !strings.Contains(err.Error(), "invalid media UUID") {
		t.Fatalf("Delete() error = %v, want malformed UUID rejection", err)
	}
	if _, err := queries.GetMediaByID(ctx, media.ID); err != nil {
		t.Fatalf("media row was deleted after failed cleanup validation: %v", err)
	}
	variants, err := queries.GetMediaVariants(ctx, media.ID)
	if err != nil || len(variants) != 1 {
		t.Fatalf("media variants = %+v, error = %v; want preserved variant", variants, err)
	}
	if content, err := os.ReadFile(sentinel); err != nil || string(content) != "legacy" {
		t.Fatalf("legacy file = %q, error = %v; want preserved", content, err)
	}
}

func TestMediaServiceDeleteKeepsCapturedRootAcrossReplacement(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ctx := context.Background()
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	now := time.Now()
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "replace-root@example.com", PasswordHash: "hash", Role: "admin", Name: "Cleanup",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	language, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	media, err := queries.CreateMedia(ctx, store.CreateMediaParams{
		Uuid: mediaUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		UploadedBy: user.ID, LanguageCode: language.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	uploadRoot := filepath.Join(parent, "uploads")
	originalFile := filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "report.pdf")
	if err := os.MkdirAll(filepath.Dir(originalFile), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originalFile, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	renamedRoot := filepath.Join(parent, "uploads-renamed")
	replacementSentinel := filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "outside.pdf")
	svc := NewMediaService(db, uploadRoot)
	svc.beforeMediaCleanup = func() {
		if err := os.Rename(uploadRoot, renamedRoot); err != nil {
			t.Fatalf("Rename(upload root): %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(replacementSentinel), 0o750); err != nil {
			t.Fatalf("MkdirAll(replacement): %v", err)
		}
		if err := os.WriteFile(replacementSentinel, []byte("replacement"), 0o600); err != nil {
			t.Fatalf("WriteFile(replacement): %v", err)
		}
	}
	if err := svc.Delete(ctx, media.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := queries.GetMediaByID(ctx, media.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted media lookup = %v, want sql.ErrNoRows", err)
	}
	if _, err := os.Stat(filepath.Join(renamedRoot, model.OriginalsDir, mediaUUID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("captured original storage remains: %v", err)
	}
	if content, err := os.ReadFile(replacementSentinel); err != nil || string(content) != "replacement" {
		t.Fatalf("replacement sentinel = %q, error = %v", content, err)
	}
}

func TestMediaServiceDeleteCleanupFailureIsReturnedAfterCommit(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ctx := context.Background()
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	now := time.Now()
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "cleanup-failure@example.com", PasswordHash: "hash", Role: "admin", Name: "Cleanup",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	language, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	media, err := queries.CreateMedia(ctx, store.CreateMediaParams{
		Uuid: mediaUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		UploadedBy: user.ID, LanguageCode: language.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateMediaVariant(ctx, store.CreateMediaVariantParams{
		MediaID: media.ID, Type: model.VariantThumbnail, Width: 10, Height: 10, Size: 1, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	uploadRoot := t.TempDir()
	svc := NewMediaService(db, uploadRoot)
	svc.deleteMediaFiles = func(*os.Root, string) error { return errors.New("injected cleanup failure") }
	err = svc.Delete(ctx, media.ID)
	if err == nil || !strings.Contains(err.Error(), "injected cleanup failure") {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := queries.GetMediaByID(ctx, media.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("media row lookup = %v, want committed deletion", err)
	}
	variants, err := queries.GetMediaVariants(ctx, media.ID)
	if err != nil || len(variants) != 0 {
		t.Fatalf("variant rows = %+v, error = %v; want committed deletion", variants, err)
	}
}

func TestMediaServiceDeleteCancellationBeforeCommitPreservesRowsAndFiles(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ctx, cancel := context.WithCancel(context.Background())
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	now := time.Now()
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "cleanup-cancel@example.com", PasswordHash: "hash", Role: "admin", Name: "Cleanup",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	language, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	media, err := queries.CreateMedia(ctx, store.CreateMediaParams{
		Uuid: mediaUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		UploadedBy: user.ID, LanguageCode: language.Code, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateMediaVariant(ctx, store.CreateMediaVariantParams{
		MediaID: media.ID, Type: model.VariantThumbnail, Width: 10, Height: 10, Size: 1, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	uploadRoot := t.TempDir()
	original := filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "report.pdf")
	if err := os.MkdirAll(filepath.Dir(original), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewMediaService(db, uploadRoot)
	svc.beforeMediaCommit = cancel
	err = svc.Delete(ctx, media.ID)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete() error = %v, want context.Canceled", err)
	}
	if _, err := queries.GetMediaByID(context.Background(), media.ID); err != nil {
		t.Fatalf("media row was not rolled back: %v", err)
	}
	variants, err := queries.GetMediaVariants(context.Background(), media.ID)
	if err != nil || len(variants) != 1 {
		t.Fatalf("variant rows = %+v, error = %v; want rollback", variants, err)
	}
	if content, err := os.ReadFile(original); err != nil || string(content) != "report" {
		t.Fatalf("original = %q, error = %v; want preserved", content, err)
	}
}
