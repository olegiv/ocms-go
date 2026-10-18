// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package media_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/api/v2/media"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/webhook"
)

// memoryFile is an uploaded file held in memory.
type memoryFile struct{ *bytes.Reader }

func (memoryFile) Close() error { return nil }

// TestMediaWritesSendWebhookEvents checks that uploading and deleting media
// through the REST API sends the media.uploaded and media.deleted webhook
// events the admin UI sends. Only the admin UI sent them (review of the
// 2026-10-17 audit fixes).
//
// Bug state: replace a dispatchMediaEvent call in the media service with a
// no-op (for Delete, `_ = media`) and this fails.
func TestMediaWritesSendWebhookEvents(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := media.NewService(db, queries, nil, t.TempDir())
	recorder := &testutil.WebhookRecorder{}
	svc.SetWebhookDispatcher(recorder)
	ctx := context.Background()
	now := time.Now()
	uploader, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "media@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "Media", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := v2.Actor{APIKey: &store.ApiKey{ID: 1, CreatedBy: uploader.ID}, Permissions: []string{model.PermissionMediaWrite}}

	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	uploaded, err := svc.Upload(ctx, writer, media.UploadMediaMetadata{}, media.UploadedFile{
		Reader: memoryFile{bytes.NewReader(buf.Bytes())}, Filename: "dot.png", Size: int64(buf.Len()), ContentType: "image/png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, writer, uploaded.ID); err != nil {
		t.Fatal(err)
	}

	want := []string{model.EventMediaUploaded, model.EventMediaDeleted}
	if len(recorder.Events()) != len(want) {
		t.Fatalf("sent %d events, want %d: %+v", len(recorder.Events()), len(want), recorder.Events())
	}
	for i, eventType := range want {
		got := recorder.Events()[i]
		data, ok := got.Data.(webhook.MediaEventData)
		if got.Type != eventType || !ok || data.ID != uploaded.ID || data.Filename != uploaded.Filename || data.UploaderID != uploader.ID {
			t.Errorf("event %d = %s %+v, want %s for media %d", i, got.Type, got.Data, eventType, uploaded.ID)
		}
	}
}

// TestFailedMediaDeleteSendsNoWebhookEvent checks that a REST media delete
// the database refuses, here through a trigger, sends no media.deleted
// event.
//
// Bug state: dispatch media.deleted before the delete in the media service
// and this fails.
func TestFailedMediaDeleteSendsNoWebhookEvent(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	queries := store.New(db)
	svc := media.NewService(db, queries, nil, t.TempDir())
	recorder := &testutil.WebhookRecorder{}
	svc.SetWebhookDispatcher(recorder)
	ctx := context.Background()
	now := time.Now()
	uploader, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "media@example.com", PasswordHash: "x", Role: model.RoleAdmin, Name: "Media", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err := queries.CreateMedia(ctx, store.CreateMediaParams{
		Uuid: "u-1", Filename: "a.png", MimeType: "image/png", Size: 1, UploadedBy: uploader.ID, LanguageCode: "en",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_media_delete BEFORE DELETE ON media BEGIN SELECT RAISE(ABORT, 'probe'); END`); err != nil {
		t.Fatal(err)
	}
	writer := v2.Actor{APIKey: &store.ApiKey{ID: 1, CreatedBy: uploader.ID}, Permissions: []string{model.PermissionMediaWrite}}
	if err := svc.Delete(ctx, writer, row.ID); err == nil {
		t.Fatal("the delete succeeded; the trigger did not refuse it and this test is vacuous")
	}
	if events := recorder.Events(); len(events) != 0 {
		t.Errorf("events = %+v, want none for a delete that failed", events)
	}
}
