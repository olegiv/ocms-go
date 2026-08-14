// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
)

type mediaCleanupWork struct {
	source     string
	uploadRoot string
	mediaUUID  string
	updatedAt  string
}

// mediaCleanupPendingError means database deletion succeeded, but one or more
// filesystem removals remain in the durable retry queue.
type mediaCleanupPendingError struct {
	count int
	err   error
}

func (e *mediaCleanupPendingError) Error() string {
	if e.count > 0 {
		return fmt.Sprintf("media file cleanup remains pending for %d item(s): %v", e.count, e.err)
	}
	return fmt.Sprintf("media file cleanup status could not be determined: %v", e.err)
}

func (e *mediaCleanupPendingError) Unwrap() error { return e.err }

// QueueMediaCleanup implements types.MediaCleanupQueuer. Sources call it only
// after immediate compensation failed, so the work survives a restart.
func (m *Module) QueueMediaCleanup(ctx context.Context, source, uploadRoot, mediaUUID string) error {
	root, err := canonicalUploadRoot(uploadRoot)
	if err != nil {
		return err
	}
	if strings.TrimSpace(source) == "" {
		return errors.New("media cleanup source is empty")
	}
	if !imaging.IsCanonicalMediaUUID(mediaUUID) {
		return fmt.Errorf("invalid media cleanup UUID %q", mediaUUID)
	}
	return enqueueMediaCleanup(ctx, m.ctx.DB, mediaCleanupWork{
		source: source, uploadRoot: root, mediaUUID: mediaUUID,
	})
}

func canonicalUploadRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("media cleanup upload root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve media cleanup upload root: %w", err)
	}
	abs = filepath.Clean(abs)
	if filepath.Dir(abs) == abs {
		return "", fmt.Errorf("media cleanup upload root %q is too broad", root)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("resolve media cleanup upload root absolute path: %w", err)
		}
		resolved = filepath.Clean(resolved)
		if filepath.Dir(resolved) == resolved {
			return "", fmt.Errorf("media cleanup upload root %q resolves to filesystem root", root)
		}
		return resolved, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		// A missing uploads directory has no files to escape through. Retaining
		// the absolute lexical root lets a later idempotent drain clear the row.
		return abs, nil
	}
	return "", fmt.Errorf("resolve media cleanup upload root symlinks: %w", err)
}

type cleanupExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func enqueueMediaCleanup(ctx context.Context, db cleanupExecer, work mediaCleanupWork) error {
	if err := validateMediaCleanupWork(work); err != nil {
		return err
	}

	now := time.Now()
	_, err := db.ExecContext(ctx, `
		INSERT INTO migrator_media_cleanup_queue
			(source, upload_root, media_uuid, attempts, last_error, created_at, updated_at)
		VALUES (?, ?, ?, 0, '', ?, ?)
		ON CONFLICT(source, upload_root, media_uuid) DO UPDATE SET
			updated_at = excluded.updated_at
	`, work.source, work.uploadRoot, work.mediaUUID, now, now)
	if err != nil {
		return fmt.Errorf("queue media cleanup for %s: %w", work.mediaUUID, err)
	}
	return nil
}

func validateMediaCleanupWork(work mediaCleanupWork) error {
	if strings.TrimSpace(work.source) == "" {
		return errors.New("media cleanup source is empty")
	}
	if !imaging.IsCanonicalMediaUUID(work.mediaUUID) {
		return fmt.Errorf("invalid media cleanup UUID %q", work.mediaUUID)
	}
	cleanRoot := filepath.Clean(work.uploadRoot)
	if !filepath.IsAbs(work.uploadRoot) || cleanRoot != work.uploadRoot || filepath.Dir(cleanRoot) == cleanRoot {
		return fmt.Errorf("media cleanup upload root %q is not a canonical absolute directory", work.uploadRoot)
	}
	return nil
}

// drainMediaCleanup attempts every queued item for source. An empty source
// drains all sources and is used during module initialization.
func (m *Module) drainMediaCleanup(ctx context.Context, source string) error {
	query := `
		SELECT source, upload_root, media_uuid, CAST(updated_at AS TEXT)
		FROM migrator_media_cleanup_queue`
	var args []any
	if source != "" {
		query += ` WHERE source = ?`
		args = append(args, source)
	}
	query += ` ORDER BY created_at, source, media_uuid`

	rows, err := m.ctx.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return &mediaCleanupPendingError{err: fmt.Errorf("read media cleanup queue: %w", err)}
	}
	var work []mediaCleanupWork
	for rows.Next() {
		var item mediaCleanupWork
		if err := rows.Scan(&item.source, &item.uploadRoot, &item.mediaUUID, &item.updatedAt); err != nil {
			_ = rows.Close()
			return &mediaCleanupPendingError{err: fmt.Errorf("scan media cleanup queue: %w", err)}
		}
		work = append(work, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return &mediaCleanupPendingError{err: fmt.Errorf("iterate media cleanup queue: %w", err)}
	}
	if err := rows.Close(); err != nil {
		return &mediaCleanupPendingError{err: fmt.Errorf("close media cleanup queue: %w", err)}
	}

	var failures []error
	pending := 0
	for index, item := range work {
		if err := ctx.Err(); err != nil {
			pending += len(work) - index
			failures = append(failures, fmt.Errorf("stop media cleanup drain: %w", err))
			break
		}
		remove := m.removeMediaFiles
		if remove == nil {
			remove = imaging.DeleteMediaFilesFromCanonicalRoot
		}
		removeErr := validateMediaCleanupWork(item)
		if removeErr == nil {
			currentRoot, err := canonicalUploadRoot(item.uploadRoot)
			switch {
			case err != nil:
				removeErr = err
			case currentRoot != item.uploadRoot:
				removeErr = fmt.Errorf("queued media cleanup root %q now resolves to %q", item.uploadRoot, currentRoot)
			}
		}
		if removeErr == nil {
			removeErr = remove(item.uploadRoot, item.mediaUUID)
		}
		if removeErr != nil {
			pending++
			failures = append(failures, removeErr)
			if _, updateErr := m.ctx.DB.ExecContext(ctx, `
				UPDATE migrator_media_cleanup_queue
					SET attempts = attempts + 1, last_error = ?, updated_at = ?
					WHERE source = ? AND upload_root = ? AND media_uuid = ?
			`, removeErr.Error(), time.Now(), item.source, item.uploadRoot, item.mediaUUID); updateErr != nil {
				failures = append(failures,
					fmt.Errorf("record cleanup failure for %s: %w", item.mediaUUID, updateErr))
			}
			continue
		}
		result, err := m.ctx.DB.ExecContext(ctx, `
			DELETE FROM migrator_media_cleanup_queue
			WHERE source = ? AND upload_root = ? AND media_uuid = ?
				AND CAST(updated_at AS TEXT) = ?
		`, item.source, item.uploadRoot, item.mediaUUID, item.updatedAt)
		if err != nil {
			pending++
			failures = append(failures,
				fmt.Errorf("remove completed cleanup row for %s: %w", item.mediaUUID, err))
			continue
		}
		removed, err := result.RowsAffected()
		if err != nil {
			pending++
			failures = append(failures,
				fmt.Errorf("confirm completed cleanup row for %s: %w", item.mediaUUID, err))
			continue
		}
		if removed == 1 {
			continue
		}

		// Another drain may have completed the same idempotent work first. Only
		// report pending cleanup when the row still exists, which means it was
		// refreshed while this attempt was on the filesystem.
		var stillQueued int
		if err := m.ctx.DB.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM migrator_media_cleanup_queue
				WHERE source = ? AND upload_root = ? AND media_uuid = ?
			)
		`, item.source, item.uploadRoot, item.mediaUUID).Scan(&stillQueued); err != nil {
			pending++
			failures = append(failures,
				fmt.Errorf("check refreshed cleanup row for %s: %w", item.mediaUUID, err))
		} else if stillQueued != 0 {
			pending++
			failures = append(failures,
				fmt.Errorf("cleanup row for %s was refreshed while draining; retry retained", item.mediaUUID))
		}
	}
	if len(failures) > 0 {
		return &mediaCleanupPendingError{count: pending, err: errors.Join(failures...)}
	}
	return nil
}

func (m *Module) configuredUploadRoot() (string, error) {
	return canonicalUploadRoot(shared.UploadDir())
}
