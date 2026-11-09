// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

// AddImportedAlias preserves the wider imported-alias grammar while protecting
// the route/alias namespace and recording the route change atomically.
func AddImportedAlias(ctx context.Context, q *store.Queries, pageID int64, alias string, at time.Time) (store.PageAlias, error) {
	var created store.PageAlias
	_, err := InQueriesTx(ctx, q, provenance.Principal{Source: "system", Transport: "migrator"}, func(work *Tx) error {
		if _, err := work.TrackSystem(ctx, pageID); err != nil {
			return err
		}
		conflict, err := RouteConflict(ctx, work.Q, alias)
		if err != nil {
			return err
		}
		if conflict != "" {
			return refuse(conflict)
		}
		exists, err := work.Q.SlugOrAliasExistsExcluding(ctx, store.SlugOrAliasExistsExcludingParams{Slug: alias, ID: pageID, Alias: alias, PageID: pageID})
		if err != nil {
			return err
		}
		if exists != 0 {
			return refuse("alias is occupied")
		}
		created, err = work.Q.CreatePageAlias(ctx, store.CreatePageAliasParams{PageID: pageID, Alias: alias, CreatedAt: at})
		return err
	})
	return created, err
}

// DeleteAliasFromQueries handles rollback after an alias or its parent is gone.
func DeleteAliasFromQueries(ctx context.Context, q *store.Queries, id int64) error {
	_, err := InQueriesTx(ctx, q, provenance.Principal{Source: "system", Transport: "rollback"}, func(work *Tx) error {
		alias, err := work.Q.GetPageAliasByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = work.TrackSystem(ctx, alias.PageID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return work.Q.DeletePageAlias(ctx, id)
	})
	return err
}

// DeleteTranslationFromQueries preserves revisions of surviving components.
func DeleteTranslationFromQueries(ctx context.Context, q *store.Queries, id int64) error {
	_, err := InQueriesTx(ctx, q, provenance.Principal{Source: "system", Transport: "rollback"}, func(work *Tx) error {
		link, err := work.Q.GetTranslationByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if link.EntityType == "page" {
			for _, pageID := range []int64{link.EntityID, link.TranslationID} {
				if err = work.TrackComponent(ctx, pageID); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
		}
		return work.Q.DeleteTranslation(ctx, id)
	})
	return err
}

// TrackMedia captures references before a media FK clears either image field.
func (t *Tx) TrackMedia(ctx context.Context, id int64) error {
	ids, err := t.Q.ListPageIDsForMedia(ctx, store.ListPageIDsForMediaParams{FeaturedImageID: sql.NullInt64{Int64: id, Valid: true}, OgImageID: sql.NullInt64{Int64: id, Valid: true}})
	if err != nil {
		return err
	}
	for _, pageID := range ids {
		if _, err = t.TrackSystem(ctx, pageID); err != nil {
			return err
		}
	}
	return nil
}

// DeleteMediaFromQueries joins a rollback transaction; its owner removes files
// only after that transaction commits.
func DeleteMediaFromQueries(ctx context.Context, q *store.Queries, id int64) error {
	_, err := InQueriesTx(ctx, q, provenance.Principal{Source: "system", Transport: "rollback"}, func(work *Tx) error {
		if err := work.TrackMedia(ctx, id); err != nil {
			return err
		}
		if err := work.Q.DeleteMediaVariants(ctx, id); err != nil {
			return err
		}
		return work.Q.DeleteMedia(ctx, id)
	})
	return err
}

var savepointSequence atomic.Uint64

// InQueriesTx adapts existing query-based system writers. Joined writes use a
// savepoint and never commit the enclosing transaction.
func InQueriesTx(ctx context.Context, q *store.Queries, principal provenance.Principal, fn func(*Tx) error) (work *Tx, writeErr error) {
	switch handle := q.DBHandle().(type) {
	case *sql.DB:
		return InTx(ctx, handle, principal, fn)
	case *sql.Tx:
		// Roll back a failed entity even when a bulk owner continues with the next
		// entity. Releasing a savepoint never commits its enclosing transaction.
		name := fmt.Sprintf("ocms_pagewrite_%x", savepointSequence.Add(1))
		if _, err := handle.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
			return nil, err
		}
		released := false
		defer func() {
			if released {
				return
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, rollbackErr := handle.ExecContext(cleanup, "ROLLBACK TO SAVEPOINT "+name)
			_, releaseErr := handle.ExecContext(cleanup, "RELEASE SAVEPOINT "+name)
			if !errors.Is(rollbackErr, sql.ErrTxDone) {
				writeErr = errors.Join(writeErr, rollbackErr)
			}
			if !errors.Is(releaseErr, sql.ErrTxDone) {
				writeErr = errors.Join(writeErr, releaseErr)
			}
		}()
		var err error
		work, err = NewTx(q, principal)
		if err != nil {
			return nil, err
		}
		if err := fn(work); err != nil {
			return nil, err
		}
		if err := work.Finish(ctx); err != nil {
			return nil, err
		}
		if _, err := handle.ExecContext(ctx, "RELEASE SAVEPOINT "+name); err != nil {
			return nil, err
		}
		released = true
		return work, nil
	default:
		return nil, errors.New("page writes require a database or transaction handle")
	}
}

// Insert saves a system-created page and its initial assignments as one revision.
// Importers pass their taxonomy/alias work in related, before any tracker records
// the committed entity. Agent writes use the v2 service and ForceDraft instead.
func Insert(ctx context.Context, q *store.Queries, principal provenance.Principal, params store.CreatePageParams, related func(*Tx, store.Page) error) (store.Page, error) {
	var page store.Page
	work, err := InQueriesTx(ctx, q, principal, func(work *Tx) error {
		if err := SlugAvailableInLanguage(ctx, work.Q, params.Slug, params.LanguageCode, 0); err != nil {
			return err
		}
		var err error
		page, err = work.Q.CreatePage(ctx, params)
		if err != nil {
			return err
		}
		work.Created(page)
		if related != nil {
			return related(work, page)
		}
		return nil
	})
	if err != nil {
		return store.Page{}, err
	}
	return work.Pages[page.ID], nil
}

// DeleteFromQueries is the same boundary for migration/generator rollback.
func DeleteFromQueries(ctx context.Context, q *store.Queries, id int64) error {
	_, err := InQueriesTx(ctx, q, provenance.Principal{Source: "system", Transport: "rollback"}, func(work *Tx) error {
		if _, err := work.Q.GetPageByID(ctx, id); errors.Is(err, sql.ErrNoRows) {
			return work.Q.CompactPageIdempotencyKeys(ctx, id)
		} else if err != nil {
			return err
		}
		return work.Delete(ctx, id)
	})
	return err
}

// DeleteTermFromQueries revisions surviving page assignments during rollback.
func DeleteTermFromQueries(ctx context.Context, q *store.Queries, kind string, id int64) error {
	_, err := InQueriesTx(ctx, q, provenance.Principal{Source: "system", Transport: "rollback"}, func(work *Tx) error { return work.DeleteTerm(ctx, kind, id) })
	return err
}
