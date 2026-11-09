// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

type pausedPageQueries struct {
	*store.Queries
	loaded, resume chan struct{}
	once           sync.Once
}

func (q *pausedPageQueries) pause() {
	q.once.Do(func() { close(q.loaded); <-q.resume })
}

func (q *pausedPageQueries) GetPublishedPageBySlugAndLanguage(ctx context.Context, args store.GetPublishedPageBySlugAndLanguageParams) (store.Page, error) {
	page, err := q.Queries.GetPublishedPageBySlugAndLanguage(ctx, args)
	q.pause()
	return page, err
}

func (q *pausedPageQueries) GetPublishedPageByID(ctx context.Context, id int64) (store.Page, error) {
	page, err := q.Queries.GetPublishedPageByID(ctx, id)
	q.pause()
	return page, err
}

func (q *pausedPageQueries) ListPublishedPages(ctx context.Context, args store.ListPublishedPagesParams) ([]store.Page, error) {
	pages, err := q.Queries.ListPublishedPages(ctx, args)
	q.pause()
	return pages, err
}

func TestPageCacheInvalidationRejectsInFlightPublishedSnapshot(t *testing.T) {
	for _, load := range []string{"slug", "id", "preload"} {
		for _, invalidate := range []string{"page", "slug", "all"} {
			t.Run(load+"/"+invalidate, func(t *testing.T) {
				q := newTestDB(t)
				page, _ := createLanguageScopedPages(t, q, "withdrawn")
				c := NewPageCache(q)
				paused := &pausedPageQueries{Queries: q, loaded: make(chan struct{}), resume: make(chan struct{})}
				c.queries = paused
				ctx := t.Context()
				cacheCtx := NewContext("en", "anonymous")
				done := make(chan error, 1)
				go func() {
					var err error
					switch load {
					case "slug":
						_, err = c.GetBySlug(ctx, cacheCtx, page.Slug)
					case "id":
						_, err = c.GetByID(ctx, cacheCtx, page.ID)
					case "preload":
						err = c.Preload(ctx, cacheCtx, 20)
					}
					done <- err
				}()
				defer func() {
					select {
					case <-paused.resume:
					default:
						close(paused.resume)
					}
				}()
				select {
				case <-paused.loaded:
				case <-time.After(5 * time.Second):
					t.Fatal("published read did not reach barrier")
				}
				if _, err := q.DBHandle().ExecContext(ctx, `UPDATE pages SET status = 'draft' WHERE id = ?`, page.ID); err != nil {
					t.Fatal(err)
				}
				switch invalidate {
				case "page":
					c.InvalidatePage(page.ID)
				case "slug":
					c.InvalidateBySlug(page.Slug)
				case "all":
					c.Invalidate()
				}
				close(paused.resume)
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if c.CacheEntryCount() != 0 {
					t.Fatal("withdrawn snapshot was restored after invalidation")
				}
				if _, err := c.GetBySlug(ctx, cacheCtx, page.Slug); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("withdrawn slug remained available: %v", err)
				}
				if _, err := c.GetByID(ctx, cacheCtx, page.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("withdrawn ID remained available: %v", err)
				}
				if _, err := q.DBHandle().ExecContext(ctx, `UPDATE pages SET status = 'published' WHERE id = ?`, page.ID); err != nil {
					t.Fatal(err)
				}
				c.InvalidatePage(page.ID)
				if _, err := c.GetBySlug(ctx, cacheCtx, page.Slug); err != nil {
					t.Fatal(err)
				}
				before := c.Stats().Hits
				if _, err := c.GetByID(ctx, cacheCtx, page.ID); err != nil || c.Stats().Hits != before+1 {
					t.Fatalf("normal published cache hit failed: %v", err)
				}
			})
		}
	}
}
