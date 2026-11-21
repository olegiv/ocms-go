// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"time"

	"github.com/disintegration/imaging"
	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/model"
)

//go:embed demo_media/*.jpg
var demoPhotos embed.FS

type demoPhoto struct {
	filename, alt, caption    string
	legacyFilename, legacyAlt string
}

// Source links and licenses are in demo_media/CREDITS.md.
var demoPhotoLibrary = []demoPhoto{
	{"studio-table.jpg", "Two small plants on a sunlit wooden studio table", "The studio table before a planning session. Photo by Alvin Engler.", "hero-banner.png", "Hero banner image"},
	{"reader-research.jpg", "A workshop group kneels around a large sheet of paper to compare ideas", "A design research exercise with 18F.", "about-image.png", "About page image"},
	{"planning-workshop.jpg", "Four workshop participants at a table review a sheet of colored notes", "A planning workshop documented by 18F.", "blog-post-1.png", "Blog post featured image"},
	{"service-architecture.jpg", "Hand-drawn diagram showing servers, application code, an API and a database", "A service boundary drawn on paper. Illustration by 18F.", "blog-post-2.png", "Blog tutorial image"},
	{"geometric-facade.jpg", "Repeating white geometric panels cast triangular shadows", "Light and shadow form a repeating design motif.", "blog-post-3.png", "Blog news image"},
	{"book-catalog.jpg", "A monochrome book cover photographed against a pale background", "A sample catalog product photographed against a neutral background. Book mockup by Beatriz Fialho.", "portfolio-1.png", "E-commerce project screenshot"},
	{"gallery-staircase.jpg", "A visitor in an orange jacket descends a curved wooden staircase", "Architectural detail for a corporate design study.", "portfolio-2.png", "Corporate website screenshot"},
	{"studio-interior.jpg", "A high skylight illuminates a narrow passage in a modern building", "Natural light in a contemporary interior.", "services-web.png", "Web development services"},
	{"mobile-wireframe.jpg", "An early interface sketch displayed on a phone on a wooden table", "A mobile prototype photographed by 18F.", "team-photo.png", "Team photo placeholder"},
	{"writers-typewriter.jpg", "A writer seen from behind at a typewriter", "Archival photograph of an editorial workspace.", "logo-sample.png", "Sample logo image"},
}

func seedDemoMedia(ctx context.Context, q *Queries, userID int64, langCode, uploadsDir string) (map[string]int64, error) {
	ids := make(map[string]int64, len(demoPhotoLibrary))
	for _, photo := range demoPhotoLibrary {
		id, err := seedDemoPhoto(ctx, q, userID, langCode, uploadsDir, photo)
		if err != nil {
			return nil, fmt.Errorf("seeding %s: %w", photo.filename, err)
		}
		ids[photo.filename] = id
	}
	return ids, nil
}

func seedDemoPhoto(ctx context.Context, q *Queries, userID int64, langCode, uploadsDir string, photo demoPhoto) (int64, error) {
	// An upgraded placeholder keeps its ID and UUID so pages still reference it.
	// Match the old seed metadata to leave unrelated uploads with the same name alone.
	stableUUID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://ocms.tech/demo/media/"+photo.filename)).String()
	var id int64
	var fileUUID, filename string
	err := q.db.QueryRowContext(ctx, `
		SELECT id, uuid, filename FROM media WHERE uuid = ? OR (uploaded_by = ? AND language_code = ? AND (
			(filename = ? AND mime_type = 'image/jpeg' AND caption = ?) OR
			(filename = ? AND mime_type = 'image/png' AND alt = ? AND width = 2400 AND height = 1600 AND COALESCE(caption,'') = '')
		)) ORDER BY uuid = ? DESC, filename = ? DESC, id LIMIT 1`,
		stableUUID, userID, langCode, photo.filename, photo.caption, photo.legacyFilename, photo.legacyAlt, stableUUID, photo.filename,
	).Scan(&id, &fileUUID, &filename)
	switch {
	case err == nil && (fileUUID == stableUUID || filename == photo.filename):
		return id, nil
	case errors.Is(err, sql.ErrNoRows):
		// A failed transaction can be retried against the same upload paths.
		fileUUID = stableUUID
	case err != nil:
		return 0, err
	case uuid.Validate(fileUUID) != nil:
		return 0, fmt.Errorf("invalid stored media UUID for %s", photo.legacyFilename)
	}

	data, err := demoPhotos.ReadFile("demo_media/" + photo.filename)
	if err != nil {
		return 0, err
	}
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	writeFile := func(variant string, content []byte) error {
		dir := filepath.Join(uploadsDir, variant, fileUUID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, photo.filename), content, 0644)
	}
	if err := writeFile(model.OriginalsDir, data); err != nil {
		return 0, err
	}

	now := time.Now()
	width := sql.NullInt64{Int64: int64(src.Bounds().Dx()), Valid: true}
	height := sql.NullInt64{Int64: int64(src.Bounds().Dy()), Valid: true}
	alt := sql.NullString{String: photo.alt, Valid: true}
	caption := sql.NullString{String: photo.caption, Valid: true}
	if id == 0 {
		media, err := q.CreateMedia(ctx, CreateMediaParams{
			Uuid: fileUUID, Filename: photo.filename, MimeType: model.MimeTypeJPEG, Size: int64(len(data)),
			Width: width, Height: height, Alt: alt, Caption: caption, UploadedBy: userID,
			LanguageCode: langCode, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return 0, err
		}
		id = media.ID
	} else {
		_, err := q.UpdateMediaForImport(ctx, UpdateMediaForImportParams{
			ID: id, Filename: photo.filename, MimeType: model.MimeTypeJPEG, Size: int64(len(data)),
			Width: width, Height: height, Alt: alt, Caption: caption, UploadedBy: userID,
			LanguageCode: langCode, UpdatedAt: now,
		})
		if err != nil {
			return 0, err
		}
		if err := q.DeleteMediaVariants(ctx, id); err != nil {
			return 0, err
		}
	}

	for name, variant := range model.ImageVariants {
		var resized image.Image = src
		switch {
		case variant.Crop:
			resized = imaging.Fill(src, variant.Width, variant.Height, imaging.Center, imaging.Lanczos)
		case src.Bounds().Dx() > variant.Width || src.Bounds().Dy() > variant.Height:
			resized = imaging.Fit(src, variant.Width, variant.Height, imaging.Lanczos)
		}
		variantData := data
		if resized != src {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: variant.Quality}); err != nil {
				return 0, err
			}
			variantData = buf.Bytes()
		}
		if err := writeFile(name, variantData); err != nil {
			return 0, err
		}
		_, err := q.CreateMediaVariant(ctx, CreateMediaVariantParams{
			MediaID: id, Type: name, Width: int64(resized.Bounds().Dx()), Height: int64(resized.Bounds().Dy()),
			Size: int64(len(variantData)), CreatedAt: now,
		})
		if err != nil {
			return 0, err
		}
	}
	return id, nil
}
