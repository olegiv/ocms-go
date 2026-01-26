// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewMediaHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewMediaHandler(db, nil, sm, "./uploads")
	if h == nil {
		t.Fatal("NewMediaHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
	if h.uploadDir != "./uploads" {
		t.Errorf("uploadDir = %q, want %q", h.uploadDir, "./uploads")
	}
}

func TestNewMediaHandlerDefaultUploadDir(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewMediaHandler(db, nil, sm, "")
	if h.uploadDir != "./uploads" {
		t.Errorf("uploadDir = %q, want default %q", h.uploadDir, "./uploads")
	}
}

func TestMediaHandlerSetDispatcher(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewMediaHandler(db, nil, sm, "./uploads")
	if h.dispatcher != nil {
		t.Error("dispatcher should be nil initially")
	}

	h.SetDispatcher(nil)
	if h.dispatcher != nil {
		t.Error("dispatcher should still be nil")
	}
}

func TestMediaItemStruct(t *testing.T) {
	item := MediaItem{
		Medium: store.Medium{
			ID:       1,
			Uuid:     "test-uuid",
			Filename: "test.jpg",
			MimeType: "image/jpeg",
			Size:     1024,
		},
		ThumbnailURL: "/uploads/thumb/test.jpg",
		OriginalURL:  "/uploads/test.jpg",
		IsImage:      true,
		TypeIcon:     "image",
	}

	if item.ID != 1 {
		t.Errorf("ID = %d, want 1", item.ID)
	}
	if item.Uuid != "test-uuid" {
		t.Errorf("Uuid = %q, want %q", item.Uuid, "test-uuid")
	}
	if !item.IsImage {
		t.Error("IsImage should be true")
	}
}

func TestMediaLibraryData(t *testing.T) {
	data := MediaLibraryData{
		Media:      []MediaItem{{IsImage: true}},
		Folders:    []store.MediaFolder{{ID: 1}},
		TotalCount: 10,
		Filter:     "images",
		Search:     "test",
	}

	if len(data.Media) != 1 {
		t.Errorf("Media length = %d, want 1", len(data.Media))
	}
	if data.TotalCount != 10 {
		t.Errorf("TotalCount = %d, want 10", data.TotalCount)
	}
	if data.Filter != "images" {
		t.Errorf("Filter = %q, want %q", data.Filter, "images")
	}
}

func TestMediaDispatchEventNilDispatcher(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewMediaHandler(db, nil, sm, "./uploads")

	// Should not panic with nil dispatcher
	h.dispatchMediaEvent(context.Background(), "media.created", store.Medium{
		ID:       1,
		Uuid:     "test-uuid",
		Filename: "test.jpg",
	})
}

func TestMediaFolderOperations(t *testing.T) {
	db, _ := testHandlerSetup(t)

	queries := store.New(db)

	t.Run("create folder", func(t *testing.T) {
		folder, err := queries.CreateMediaFolder(context.Background(), store.CreateMediaFolderParams{
			Name:      "Test Folder",
			Position:  0,
			CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("CreateMediaFolder failed: %v", err)
		}
		if folder.Name != "Test Folder" {
			t.Errorf("Name = %q, want %q", folder.Name, "Test Folder")
		}
	})

	t.Run("list folders", func(t *testing.T) {
		folders, err := queries.ListMediaFolders(context.Background())
		if err != nil {
			t.Fatalf("ListMediaFolders failed: %v", err)
		}
		if len(folders) == 0 {
			t.Error("expected at least one folder")
		}
	})
}

func TestMediaOperations(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)

	t.Run("create media", func(t *testing.T) {
		media, err := queries.CreateMedia(context.Background(), store.CreateMediaParams{
			Uuid:       "test-uuid-123",
			Filename:   "test-image.jpg",
			MimeType:   "image/jpeg",
			Size:       1024,
			Width:      sql.NullInt64{Int64: 800, Valid: true},
			Height:     sql.NullInt64{Int64: 600, Valid: true},
			UploadedBy: user.ID,
		})
		if err != nil {
			t.Fatalf("CreateMedia failed: %v", err)
		}
		if media.Filename != "test-image.jpg" {
			t.Errorf("Filename = %q, want %q", media.Filename, "test-image.jpg")
		}
		if media.MimeType != "image/jpeg" {
			t.Errorf("MimeType = %q, want %q", media.MimeType, "image/jpeg")
		}
	})

	t.Run("get media by uuid", func(t *testing.T) {
		media, err := queries.GetMediaByUUID(context.Background(), "test-uuid-123")
		if err != nil {
			t.Fatalf("GetMediaByUUID failed: %v", err)
		}
		if media.Uuid != "test-uuid-123" {
			t.Errorf("Uuid = %q, want %q", media.Uuid, "test-uuid-123")
		}
	})

	t.Run("list media", func(t *testing.T) {
		mediaList, err := queries.ListMedia(context.Background(), store.ListMediaParams{
			Limit:  10,
			Offset: 0,
		})
		if err != nil {
			t.Fatalf("ListMedia failed: %v", err)
		}
		if len(mediaList) == 0 {
			t.Error("expected at least one media item")
		}
	})

	t.Run("count media", func(t *testing.T) {
		count, err := queries.CountMedia(context.Background())
		if err != nil {
			t.Fatalf("CountMedia failed: %v", err)
		}
		if count == 0 {
			t.Error("expected count > 0")
		}
	})
}

func TestMediaWithFolder(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)

	// Create a folder first
	folder, err := queries.CreateMediaFolder(context.Background(), store.CreateMediaFolderParams{
		Name:      "Images",
		Position:  0,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateMediaFolder failed: %v", err)
	}

	// Create media in folder
	media, err := queries.CreateMedia(context.Background(), store.CreateMediaParams{
		Uuid:       "folder-media-uuid",
		Filename:   "folder-image.jpg",
		MimeType:   "image/jpeg",
		Size:       2048,
		FolderID:   sql.NullInt64{Int64: folder.ID, Valid: true},
		UploadedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("CreateMedia failed: %v", err)
	}

	if !media.FolderID.Valid || media.FolderID.Int64 != folder.ID {
		t.Errorf("FolderID = %v, want %d", media.FolderID, folder.ID)
	}
}

func TestMediaUpdate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)

	// Create media
	media, err := queries.CreateMedia(context.Background(), store.CreateMediaParams{
		Uuid:       "update-test-uuid",
		Filename:   "original.jpg",
		MimeType:   "image/jpeg",
		Size:       1024,
		UploadedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("CreateMedia failed: %v", err)
	}

	// Update media
	_, err = queries.UpdateMedia(context.Background(), store.UpdateMediaParams{
		ID:      media.ID,
		Alt:     sql.NullString{String: "Updated alt text", Valid: true},
		Caption: sql.NullString{String: "Updated caption", Valid: true},
	})
	if err != nil {
		t.Fatalf("UpdateMedia failed: %v", err)
	}

	// Verify update
	updated, err := queries.GetMediaByID(context.Background(), media.ID)
	if err != nil {
		t.Fatalf("GetMediaByID failed: %v", err)
	}
	if updated.Alt.String != "Updated alt text" {
		t.Errorf("Alt = %q, want %q", updated.Alt.String, "Updated alt text")
	}
}

func TestMediaDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)

	// Create media
	media, err := queries.CreateMedia(context.Background(), store.CreateMediaParams{
		Uuid:       "delete-test-uuid",
		Filename:   "to-delete.jpg",
		MimeType:   "image/jpeg",
		Size:       1024,
		UploadedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("CreateMedia failed: %v", err)
	}

	// Delete media
	if err := queries.DeleteMedia(context.Background(), media.ID); err != nil {
		t.Fatalf("DeleteMedia failed: %v", err)
	}

	// Verify deletion
	_, err = queries.GetMediaByID(context.Background(), media.ID)
	if err == nil {
		t.Error("expected error when getting deleted media")
	}
}
