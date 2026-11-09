// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package webhook provides webhook event dispatching and delivery.
package webhook

import (
	"context"
	"log/slog"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// Event represents a webhook event to be dispatched.
type Event struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// NewEvent creates a new webhook event.
func NewEvent(eventType string, data any) *Event {
	return &Event{
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Data:      data,
	}
}

// entityID identifies the entity for both debouncing and error diagnostics.
// A form event identifies the submission, so different submissions stay distinct.
func (e *Event) entityID() (int64, bool) {
	switch data := e.Data.(type) {
	case PageEventData:
		return data.ID, true
	case *PageEventData:
		if data != nil {
			return data.ID, true
		}
	case MediaEventData:
		return data.ID, true
	case *MediaEventData:
		if data != nil {
			return data.ID, true
		}
	case UserEventData:
		return data.ID, true
	case *UserEventData:
		if data != nil {
			return data.ID, true
		}
	case FormEventData:
		return data.SubmissionID, true
	case *FormEventData:
		if data != nil {
			return data.SubmissionID, true
		}
	case map[string]any:
		switch id := data["id"].(type) {
		case int64:
			return id, true
		case float64:
			return int64(id), true
		}
		// Keep the existing debounce key for maps without a numeric ID.
		return 0, true
	}
	return 0, false
}

// PageEventData contains data for page-related events.
type PageEventData struct {
	ID           int64   `json:"id"`
	Title        string  `json:"title"`
	Slug         string  `json:"slug"`
	Status       string  `json:"status"`
	AuthorID     int64   `json:"author_id"`
	AuthorEmail  string  `json:"author_email,omitempty"`
	LanguageCode *string `json:"language_code,omitempty"`
	PublishedAt  *string `json:"published_at,omitempty"`
}

// NewPageEventData builds the data of a page event. actorEmail is the email
// of the user who made the change, empty for API keys and the scheduler; it
// is sent as author_email, so it can name another user than AuthorID.
func NewPageEventData(page store.Page, actorEmail string) PageEventData {
	data := PageEventData{
		ID:          page.ID,
		Title:       page.Title,
		Slug:        page.Slug,
		Status:      page.Status,
		AuthorID:    page.AuthorID,
		AuthorEmail: actorEmail,
	}
	if page.LanguageCode != "" {
		data.LanguageCode = new(page.LanguageCode)
	}
	if page.PublishedAt.Valid {
		data.PublishedAt = new(page.PublishedAt.Time.Format(time.RFC3339))
	}
	return data
}

// NewMediaEventData builds the data of a media event.
func NewMediaEventData(media store.Medium) MediaEventData {
	return MediaEventData{
		ID:         media.ID,
		UUID:       media.Uuid,
		Filename:   media.Filename,
		MimeType:   media.MimeType,
		Size:       media.Size,
		UploaderID: media.UploadedBy,
	}
}

// EventDispatcher sends webhook events; *Dispatcher is one.
type EventDispatcher interface {
	DispatchEvent(ctx context.Context, eventType string, data any) error
}

// DispatchEvent sends an event for a saved change and logs any error that the
// dispatcher has not already recorded.
func DispatchEvent(ctx context.Context, d EventDispatcher, eventType string, data any) {
	if err := d.DispatchEvent(ctx, eventType, data); err != nil {
		logUnreportedDispatchError(slog.Default(), &Event{Type: eventType, Data: data}, err)
	}
}

// DispatchPageEvent sends the same page data from the admin UI, REST API and scheduler.
func DispatchPageEvent(ctx context.Context, d EventDispatcher, eventType string, page store.Page, actorEmail string) {
	DispatchEvent(ctx, d, eventType, NewPageEventData(page, actorEmail))
}

// DispatchMediaEvent sends a media event for a saved change.
func DispatchMediaEvent(ctx context.Context, d EventDispatcher, eventType string, media store.Medium) {
	DispatchEvent(ctx, d, eventType, NewMediaEventData(media))
}

// MediaEventData contains data for media-related events.
type MediaEventData struct {
	ID         int64  `json:"id"`
	UUID       string `json:"uuid"`
	Filename   string `json:"filename"`
	MimeType   string `json:"mime_type"`
	Size       int64  `json:"size"`
	UploaderID int64  `json:"uploader_id"`
}

// FormEventData contains data for form submission events.
type FormEventData struct {
	FormID       int64             `json:"form_id"`
	FormName     string            `json:"form_name"`
	FormSlug     string            `json:"form_slug"`
	SubmissionID int64             `json:"submission_id"`
	Data         map[string]string `json:"data,omitempty"`
	DataMode     string            `json:"data_mode,omitempty"`
	SubmittedAt  time.Time         `json:"submitted_at"`
}

// UserEventData contains data for user-related events.
type UserEventData struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

// TestEventData contains data for test webhook events.
type TestEventData struct {
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}
