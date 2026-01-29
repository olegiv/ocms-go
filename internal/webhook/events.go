// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package webhook provides webhook event dispatching and delivery.
package webhook

import (
	"time"
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
	Data         map[string]string `json:"data"`
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
