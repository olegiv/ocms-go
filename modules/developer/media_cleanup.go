// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package developer

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
)

const developerCleanupDrainTimeout = 30 * time.Second

type developerMediaCleanupWork struct {
	id         int64
	uploadRoot string
	mediaUUID  string
	generation int64
}

type developerCleanupExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// canonicalDeveloperUploadRoot returns the stable absolute root stored with a
// cleanup intent. A missing root is retained lexically so idempotent cleanup
// can complete; an existing symlink is resolved once and every later drain
// rejects a retargeted canonical path before removing anything.
func canonicalDeveloperUploadRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("developer media cleanup upload root is empty")
	}
	abs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve developer media cleanup root: %w", err)
	}
	if filepath.Dir(abs) == abs {
		return "", fmt.Errorf("developer media cleanup root %q is too broad", root)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("resolve developer media cleanup root absolute path: %w", err)
		}
		resolved = filepath.Clean(resolved)
		if filepath.Dir(resolved) == resolved {
			return "", fmt.Errorf("developer media cleanup root %q resolves to filesystem root", root)
		}
		return resolved, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return abs, nil
	}
	return "", fmt.Errorf("resolve developer media cleanup root symlinks: %w", err)
}

func validateDeveloperMediaCleanupWork(work developerMediaCleanupWork) error {
	if !imaging.IsCanonicalMediaUUID(work.mediaUUID) {
		return fmt.Errorf("invalid developer media cleanup UUID %q", work.mediaUUID)
	}
	cleanRoot := filepath.Clean(work.uploadRoot)
	if !filepath.IsAbs(work.uploadRoot) || cleanRoot != work.uploadRoot || filepath.Dir(cleanRoot) == cleanRoot {
		return fmt.Errorf("developer media cleanup root %q is not a canonical absolute directory", work.uploadRoot)
	}
	return nil
}

// enqueueMediaCleanup persists filesystem work in the same transaction as the
// media-row deletion. Refreshing an existing intent increments generation so
// an older concurrent drain cannot remove newly queued work.
func enqueueDeveloperMediaCleanup(
	ctx context.Context,
	execer developerCleanupExecer,
	uploadRoot, mediaUUID string,
) error {
	work := developerMediaCleanupWork{uploadRoot: uploadRoot, mediaUUID: mediaUUID}
	if err := validateDeveloperMediaCleanupWork(work); err != nil {
		return err
	}
	now := time.Now()
	_, err := execer.ExecContext(ctx, `
		INSERT INTO developer_media_cleanup_queue
			(upload_root, media_uuid, generation, attempts, last_error, created_at, updated_at)
		VALUES (?, ?, 1, 0, '', ?, ?)
		ON CONFLICT(upload_root, media_uuid) DO UPDATE SET
			generation = developer_media_cleanup_queue.generation + 1,
			attempts = 0,
			last_error = '',
			updated_at = excluded.updated_at
	`, uploadRoot, mediaUUID, now, now)
	if err != nil {
		return fmt.Errorf("queue developer media cleanup for %s: %w", mediaUUID, err)
	}
	return nil
}

// drainMediaCleanup attempts every durable developer cleanup intent. Failed or
// concurrently refreshed rows stay queued for initialization or a later delete
// attempt; completed work is removed with a generation compare-and-swap.
func (m *Module) drainMediaCleanup(ctx context.Context) error {
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT id, upload_root, media_uuid, generation
		FROM developer_media_cleanup_queue
		ORDER BY created_at, id
	`)
	if err != nil {
		return fmt.Errorf("read developer media cleanup queue: %w", err)
	}
	var work []developerMediaCleanupWork
	for rows.Next() {
		var item developerMediaCleanupWork
		if err := rows.Scan(&item.id, &item.uploadRoot, &item.mediaUUID, &item.generation); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan developer media cleanup queue: %w", err)
		}
		work = append(work, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate developer media cleanup queue: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close developer media cleanup queue: %w", err)
	}

	remove := m.removeMediaFiles
	if remove == nil {
		remove = imaging.DeleteMediaFilesFromCanonicalRoot
	}
	var failures []error
	for index, item := range work {
		if err := ctx.Err(); err != nil {
			failures = append(failures,
				fmt.Errorf("stop developer media cleanup with %d item(s) pending: %w", len(work)-index, err))
			break
		}

		removeErr := validateDeveloperMediaCleanupWork(item)
		if removeErr == nil {
			currentRoot, err := canonicalDeveloperUploadRoot(item.uploadRoot)
			switch {
			case err != nil:
				removeErr = err
			case currentRoot != item.uploadRoot:
				removeErr = fmt.Errorf("queued developer media root %q now resolves to %q", item.uploadRoot, currentRoot)
			}
		}
		if removeErr == nil {
			removeErr = remove(item.uploadRoot, item.mediaUUID)
		}
		if removeErr != nil {
			failures = append(failures, fmt.Errorf("cleanup developer media %s: %w", item.mediaUUID, removeErr))
			if _, updateErr := m.ctx.DB.ExecContext(ctx, `
				UPDATE developer_media_cleanup_queue
				SET attempts = attempts + 1, last_error = ?, updated_at = ?
				WHERE id = ? AND generation = ?
			`, removeErr.Error(), time.Now(), item.id, item.generation); updateErr != nil {
				failures = append(failures,
					fmt.Errorf("record developer cleanup failure for %s: %w", item.mediaUUID, updateErr))
			}
			continue
		}

		result, err := m.ctx.DB.ExecContext(ctx, `
			DELETE FROM developer_media_cleanup_queue
			WHERE id = ? AND generation = ?
		`, item.id, item.generation)
		if err != nil {
			failures = append(failures,
				fmt.Errorf("remove completed developer cleanup row for %s: %w", item.mediaUUID, err))
			continue
		}
		removed, err := result.RowsAffected()
		if err != nil {
			failures = append(failures,
				fmt.Errorf("confirm developer cleanup row removal for %s: %w", item.mediaUUID, err))
			continue
		}
		if removed != 1 {
			failures = append(failures,
				fmt.Errorf("developer cleanup row for %s was refreshed while draining", item.mediaUUID))
		}
	}
	return errors.Join(failures...)
}
