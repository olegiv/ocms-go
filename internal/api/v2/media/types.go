// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package media is the /api/v2/media domain. Service owns every media
// operation end-to-end; operations.go turns huma input into service calls.
package media

import (
	"bytes"
	"encoding/json"
	"time"
)

// NullableInt64 decodes a JSON int field that distinguishes three states:
//   - field absent from the JSON object → IsSet=false (leave field alone)
//   - field present with JSON `null`     → IsSet=true,  IsNull=true
//   - field present with a number        → IsSet=true,  IsNull=false, Value=N
//
// Plain `*int64` cannot represent "explicitly null" separately from "absent",
// so PATCH-style updates that advertise `null` as "clear the field" have no
// way to honor it. OpenAPI consumers see the same schema as int64, with the
// `nullable: true` hint via huma's SchemaProvider below.
type NullableInt64 struct {
	IsSet  bool
	IsNull bool
	Value  int64
}

// UnmarshalJSON captures the three-way present/null/value signal.
func (n *NullableInt64) UnmarshalJSON(b []byte) error {
	n.IsSet = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		n.IsNull = true
		return nil
	}
	return json.Unmarshal(b, &n.Value)
}

// MarshalJSON round-trips the type; not used on input but keeps symmetry if
// the struct is ever serialized.
func (n NullableInt64) MarshalJSON() ([]byte, error) {
	if !n.IsSet || n.IsNull {
		return []byte("null"), nil
	}
	return json.Marshal(n.Value)
}

// Media is the DTO returned by every media response.
type Media struct {
	ID           int64         `json:"id"`
	UUID         string        `json:"uuid" format:"uuid"`
	Filename     string        `json:"filename"`
	MimeType     string        `json:"mime_type"`
	Size         int64         `json:"size" doc:"File size in bytes."`
	Width        *int64        `json:"width,omitempty"`
	Height       *int64        `json:"height,omitempty"`
	Alt          string        `json:"alt"`
	Caption      string        `json:"caption"`
	FolderID     *int64        `json:"folder_id,omitempty"`
	UploadedBy   int64         `json:"uploaded_by"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	URLs         *URLs         `json:"urls,omitempty"`
	Variants     []Variant     `json:"variants,omitempty"`
	Folder       *Folder       `json:"folder,omitempty"`
	Translations []Translation `json:"translations,omitempty"`
}

// URLs collects the public URLs at which a media item is served.
type URLs struct {
	Original  string `json:"original"`
	Thumbnail string `json:"thumbnail,omitempty"`
	Medium    string `json:"medium,omitempty"`
	Large     string `json:"large,omitempty"`
}

// Variant is a generated variant (thumbnail / medium / large) for an image.
type Variant struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type" doc:"Variant kind (thumbnail / medium / large)."`
	Width     int64     `json:"width"`
	Height    int64     `json:"height"`
	Size      int64     `json:"size"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// Folder is a media folder reference.
type Folder struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	ParentID  *int64    `json:"parent_id,omitempty"`
	Position  int64     `json:"position"`
	CreatedAt time.Time `json:"created_at"`
}

// Translation is a per-language alt / caption override.
type Translation struct {
	LanguageID   int64  `json:"language_id"`
	LanguageCode string `json:"language_code"`
	LanguageName string `json:"language_name"`
	Alt          string `json:"alt"`
	Caption      string `json:"caption"`
}

// UpdateMediaBody is the patch-style input for updating media metadata.
// FolderID uses NullableInt64 so callers can distinguish "leave unchanged"
// (omit the field) from "move to root" (send `null` or `0`) — JSON `null` on
// a plain `*int64` would decode identically to an absent field and silently
// no-op.
type UpdateMediaBody struct {
	Filename *string       `json:"filename,omitempty" minLength:"1" maxLength:"255"`
	Alt      *string       `json:"alt,omitempty"`
	Caption  *string       `json:"caption,omitempty"`
	FolderID NullableInt64 `json:"folder_id,omitempty" doc:"Send 0 or null to move to root."`
}

// UploadMediaMetadata carries the common metadata for a single upload. File content
// comes in via the operation layer (huma.FormFile).
type UploadMediaMetadata struct {
	FolderID *int64
	Alt      string
	Caption  string
}

// ListFilter is the input for Service.List.
type ListFilter struct {
	Page                int
	PerPage             int
	Type                string // "", "image", "document", "video"
	FolderID            *int64
	Search              string
	IncludeVariants     bool
	IncludeFolder       bool
	IncludeTranslations bool
}

// ListResult is the paginated list return value.
type ListResult struct {
	Media   []Media
	Total   int64
	Page    int
	PerPage int
}
