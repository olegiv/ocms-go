// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package pagewrite owns atomic page writes, revision checks and complete snapshots.
package pagewrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/olegiv/ocms-go/internal/provenance"
	"github.com/olegiv/ocms-go/internal/store"
)

// Conflict is safe to expose only after checking the caller's access to the page.
// ChangedFields contains names, never the intervening content.
type Conflict struct {
	CurrentRevision int64
	ChangedFields   []string
}

func (e *Conflict) Error() string { return "revision_conflict" }

// ErrRevisionRequired rejects unconditional changes to an AI-editable page.
var ErrRevisionRequired = errors.New("revision_required")

type trackedPage struct {
	before   string
	revision int64
	created  bool
}

// Tx is a unit of work. Track existing pages before their first mutation, and
// register inserts with Created. All related writes use Q from this transaction.
type Tx struct {
	Q         *store.Queries
	principal provenance.Principal
	evidence  provenance.Evidence
	tracked   map[int64]trackedPage
	Pages     map[int64]store.Page
	Changed   map[int64]bool
}

// NewTx joins an existing transaction (imports and compound system operations).
// The caller must call Finish before committing and must roll back on any error.
func NewTx(q *store.Queries, principal provenance.Principal) (*Tx, error) {
	if q == nil {
		return nil, errors.New("page writes require transaction-bound queries")
	}
	if _, ok := q.DBHandle().(*sql.Tx); !ok {
		return nil, errors.New("page writes require transaction-bound queries")
	}
	return &Tx{Q: q, principal: principal, tracked: make(map[int64]trackedPage), Pages: make(map[int64]store.Page), Changed: make(map[int64]bool)}, nil
}

// InTx commits only after every changed page has a revision, snapshot and provenance.
// Side effects such as webhooks and cache invalidation belong after this returns.
func InTx(ctx context.Context, db *sql.DB, principal provenance.Principal, fn func(*Tx) error) (*Tx, error) {
	if db == nil {
		return nil, errors.New("page write requires a database handle")
	}
	dbtx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin page write: %w", err)
	}
	defer func() { _ = dbtx.Rollback() }()
	work, err := NewTx(store.New(dbtx), principal)
	if err != nil {
		return nil, err
	}
	if err = fn(work); err != nil {
		return nil, err
	}
	if err = work.Finish(ctx); err != nil {
		return nil, err
	}
	if err = dbtx.Commit(); err != nil {
		return nil, fmt.Errorf("commit page write: %w", err)
	}
	return work, nil
}

// SetEvidence associates new validation and report data with this operation.
func (t *Tx) SetEvidence(evidence provenance.Evidence) { t.evidence = evidence }

// Track captures fresh state and enforces CAS before any mutation. Set required
// for an editor/agent action; legacy REST updates may omit it on ordinary pages.
func (t *Tx) Track(ctx context.Context, id int64, base *int64, required bool) (store.Page, error) {
	page, err := t.Q.GetPageByID(ctx, id)
	if err != nil {
		return store.Page{}, err
	}
	access, err := t.Q.GetPageAIAccess(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return store.Page{}, err
	}
	if base == nil && (required || access.AiEditable) {
		return store.Page{}, ErrRevisionRequired
	}
	if base != nil && *base != page.Revision {
		changed, changedErr := t.changedSince(ctx, page, *base)
		if changedErr != nil {
			return store.Page{}, changedErr
		}
		return store.Page{}, &Conflict{CurrentRevision: page.Revision, ChangedFields: changed}
	}
	if _, ok := t.tracked[id]; !ok {
		snapshot, snapshotErr := t.Q.GetPageSnapshot(ctx, id)
		if snapshotErr != nil {
			return store.Page{}, snapshotErr
		}
		t.tracked[id] = trackedPage{before: snapshot, revision: page.Revision}
	}
	return page, nil
}

// TrackSystem captures current transactional state for a narrow system action.
// Unlike client updates it does not require an externally supplied revision.
func (t *Tx) TrackSystem(ctx context.Context, id int64) (store.Page, error) {
	p, err := t.Q.GetPageByID(ctx, id)
	if err != nil {
		return store.Page{}, err
	}
	return t.Track(ctx, id, &p.Revision, false)
}

// TrackComponent tracks every sibling whose translation membership may change.
func (t *Tx) TrackComponent(ctx context.Context, id int64) error {
	if _, err := t.TrackSystem(ctx, id); err != nil {
		return err
	}
	members, err := t.Q.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{EntityType: "page", SourceEntityID: id})
	if err != nil {
		return err
	}
	for _, member := range members {
		if _, err := t.TrackSystem(ctx, member.EntityID); err != nil {
			return err
		}
	}
	return nil
}

// Created registers an inserted page; its first committed revision is one.
func (t *Tx) Created(page store.Page) { t.tracked[page.ID] = trackedPage{created: true, revision: 1} }

// Finish records complete snapshots, including relation-only changes, once per
// affected page. Repeated calls are harmless. A no-op does not add a revision.
func (t *Tx) Finish(ctx context.Context) error {
	ids := make([]int64, 0, len(t.tracked))
	for id := range t.tracked {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		tracked := t.tracked[id]
		page, err := t.Q.GetPageByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			if err = t.Q.CompactPageIdempotencyKeys(ctx, id); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		snapshot, err := t.Q.GetPageSnapshot(ctx, id)
		if err != nil {
			return err
		}
		if !tracked.created {
			fields, fieldsErr := changedFields(tracked.before, snapshot)
			if fieldsErr != nil {
				return fieldsErr
			}
			if len(fields) == 0 {
				t.Pages[id] = page
				continue
			}
			page, err = t.Q.AdvancePageRevision(ctx, store.AdvancePageRevisionParams{ID: id, Revision: tracked.revision})
			if errors.Is(err, sql.ErrNoRows) {
				current, loadErr := t.Q.GetPageByID(ctx, id)
				if loadErr != nil {
					return loadErr
				}
				return &Conflict{CurrentRevision: current.Revision}
			}
			if err != nil {
				return err
			}
			snapshot, err = t.Q.GetPageSnapshot(ctx, id)
			if err != nil {
				return err
			}
		}
		changedBy := t.principal.UserID
		// The legacy FK requires a user; provenance explicitly records system
		// identity rather than pretending that this fallback user made the edit.
		if changedBy == 0 {
			changedBy = page.AuthorID
		}
		_, err = t.Q.CreateCompletePageVersion(ctx, store.CreateCompletePageVersionParams{PageID: id, Title: page.Title, Body: page.Body, ChangedBy: changedBy, CreatedAt: time.Now().UTC(), Revision: sql.NullInt64{Int64: page.Revision, Valid: true}, SnapshotJson: sql.NullString{String: snapshot, Valid: true}})
		if err != nil {
			return fmt.Errorf("record page snapshot: %w", err)
		}
		if err = provenance.Record(ctx, t.Q, page, t.principal, t.evidence); err != nil {
			return fmt.Errorf("record page provenance: %w", err)
		}
		t.Pages[id] = page
		t.Changed[id] = true
	}
	clear(t.tracked)
	return nil
}

func (t *Tx) changedSince(ctx context.Context, page store.Page, base int64) ([]string, error) {
	previous, err := t.Q.GetPageVersionByRevision(ctx, store.GetPageVersionByRevisionParams{PageID: page.ID, Revision: sql.NullInt64{Int64: base, Valid: true}})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	current, err := t.Q.GetPageSnapshot(ctx, page.ID)
	if err != nil {
		return nil, err
	}
	return changedFields(previous.SnapshotJson.String, current)
}

func changedFields(before, after string) ([]string, error) {
	var left, right map[string]json.RawMessage
	if err := json.Unmarshal([]byte(before), &left); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(after), &right); err != nil {
		return nil, err
	}
	var fields []string
	for key, value := range right {
		if key == "revision" || key == "updated_at" {
			continue
		}
		if !reflect.DeepEqual(left[key], value) {
			fields = append(fields, key)
		}
	}
	sort.Strings(fields)
	return fields, nil
}

// IsTracked reports whether state was captured before a compound mutation.
func (t *Tx) IsTracked(id int64) bool { _, ok := t.tracked[id]; return ok }
