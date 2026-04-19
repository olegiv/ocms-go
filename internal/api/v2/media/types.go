// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package media is the /api/v2/media domain. Service owns every media
// operation end-to-end; operations.go turns huma input into service calls.
package media

import "time"

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
type UpdateMediaBody struct {
	Filename *string `json:"filename,omitempty" minLength:"1" maxLength:"255"`
	Alt      *string `json:"alt,omitempty"`
	Caption  *string `json:"caption,omitempty"`
	FolderID *int64  `json:"folder_id,omitempty" doc:"Send 0 or null to move to root."`
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
