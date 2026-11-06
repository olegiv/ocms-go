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
	"slices"
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
	// relationOnly marks a sibling tracked only because a translation link
	// changes its component: its revision is recorded as a system change.
	relationOnly bool
}

// Tx is a unit of work. Track existing pages before their first mutation, and
// register inserts with Created. All related writes use Q from this transaction.
type Tx struct {
	Q               *store.Queries
	principal       provenance.Principal
	evidence        provenance.Evidence
	pageEvidence    map[int64]provenance.Evidence
	evidenceChanges map[int64]bool
	tracked         map[int64]trackedPage
	Pages           map[int64]store.Page
	Changed         map[int64]bool
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

// SetPageEvidence keeps an authored page's claims/validation separate from
// relation-only revisions of other pages in the same transaction.
func (t *Tx) SetPageEvidence(id int64, evidence provenance.Evidence) {
	if t.pageEvidence == nil {
		t.pageEvidence = make(map[int64]provenance.Evidence)
	}
	t.pageEvidence[id] = evidence
}

// TrackEvidenceChanges allows an explicit references/report edit to create a
// revision even when the article fields did not change. Identical evidence is a no-op.
func (t *Tx) TrackEvidenceChanges(id int64) {
	if t.evidenceChanges == nil {
		t.evidenceChanges = make(map[int64]bool)
	}
	t.evidenceChanges[id] = true
}

// Track captures fresh state and enforces CAS before any mutation. Set required
// for an editor/agent action; legacy REST updates may omit it on ordinary pages.
func (t *Tx) Track(ctx context.Context, id int64, base *int64, required bool) (store.Page, error) {
	page, err := t.track(ctx, id, base, required)
	if tracked, ok := t.tracked[id]; err == nil && ok && tracked.relationOnly {
		// The operation changes this page itself, not only its component.
		tracked.relationOnly = false
		t.tracked[id] = tracked
	}
	return page, err
}

func (t *Tx) track(ctx context.Context, id int64, base *int64, required bool) (store.Page, error) {
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
// A page this transaction does not otherwise change gets its new revision
// recorded as a system change (RelationOnlyPrincipal), so a translation link
// never credits the acting credential with an edit to a page it only read
// (2026-11-03 audit B-4).
func (t *Tx) TrackComponent(ctx context.Context, id int64) error {
	if err := t.trackRelation(ctx, id); err != nil {
		return err
	}
	members, err := t.Q.ListTranslationComponentMembers(ctx, store.ListTranslationComponentMembersParams{EntityType: "page", SourceEntityID: id})
	if err != nil {
		return err
	}
	for _, member := range members {
		if err := t.trackRelation(ctx, member.EntityID); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tx) trackRelation(ctx context.Context, id int64) error {
	_, already := t.tracked[id]
	p, err := t.Q.GetPageByID(ctx, id)
	if err != nil {
		return err
	}
	if _, err = t.track(ctx, id, &p.Revision, false); err != nil {
		return err
	}
	if !already {
		tracked := t.tracked[id]
		tracked.relationOnly = true
		t.tracked[id] = tracked
	}
	return nil
}

// RelationOnlyPrincipal is the provenance of a revision that only records a
// changed translation component. UserID names the account whose action
// linked it; no credential is credited with the page's text.
func RelationOnlyPrincipal(userID int64) provenance.Principal {
	return provenance.Principal{UserID: userID, Source: "system", Transport: "translation_link"}
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
				changed := false
				if t.evidenceChanges[id] {
					previous, provenanceErr := t.Q.GetContentProvenance(ctx, store.GetContentProvenanceParams{PageID: id, Revision: page.Revision})
					if provenanceErr != nil && !errors.Is(provenanceErr, sql.ErrNoRows) {
						return provenanceErr
					}
					evidence := t.pageEvidence[id]
					changed = !equalJSON(previous.ValidatedJson, evidence.Validated) || !equalJSON(previous.ReportedJson, evidence.Reported)
				}
				if !changed {
					t.Pages[id] = page
					continue
				}
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
		principal, evidence := t.principal, t.evidence
		if selected, ok := t.pageEvidence[id]; ok {
			evidence = selected
		}
		if tracked.relationOnly {
			principal, evidence = RelationOnlyPrincipal(t.principal.UserID), provenance.Evidence{}
		}
		changedBy := principal.UserID
		// The legacy FK requires a user; provenance explicitly records system
		// identity rather than pretending that this fallback user made the edit.
		if changedBy == 0 {
			changedBy = page.AuthorID
		}
		_, err = t.Q.CreateCompletePageVersion(ctx, store.CreateCompletePageVersionParams{PageID: id, Title: page.Title, Body: page.Body, ChangedBy: changedBy, CreatedAt: time.Now().UTC(), Revision: sql.NullInt64{Int64: page.Revision, Valid: true}, SnapshotJson: sql.NullString{String: snapshot, Valid: true}})
		if err != nil {
			return fmt.Errorf("record page snapshot: %w", err)
		}
		if err = provenance.Record(ctx, t.Q, page, principal, evidence); err != nil {
			return fmt.Errorf("record page provenance: %w", err)
		}
		t.Pages[id] = page
		t.Changed[id] = true
	}
	clear(t.tracked)
	return nil
}

func equalJSON(previous string, next json.RawMessage) bool {
	var a, b any
	if previous == "" {
		previous = "{}"
	}
	if len(next) == 0 {
		next = json.RawMessage(`{}`)
	}
	return json.Unmarshal([]byte(previous), &a) == nil && json.Unmarshal(next, &b) == nil && reflect.DeepEqual(a, b)
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

// ChangedPages returns, in ID order, the committed state of every page this
// transaction gave a new revision. Callers run the after-commit side effects
// (page cache invalidation, the page.updated webhook) for exactly these pages:
// a tracked page whose content did not change is not in it. Call it after
// Finish, or after InTx returns.
func (t *Tx) ChangedPages() []store.Page {
	ids := make([]int64, 0, len(t.Changed))
	for id := range t.Changed {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	pages := make([]store.Page, 0, len(ids))
	for _, id := range ids {
		pages = append(pages, t.Pages[id])
	}
	return pages
}

// IsTracked reports whether state was captured before a compound mutation.
func (t *Tx) IsTracked(id int64) bool { _, ok := t.tracked[id]; return ok }

// ErrPageNotTracked rejects a helper called before its page's initial snapshot.
var ErrPageNotTracked = errors.New("page must be tracked before mutation")

// RequireTracked checks a compound writer's precondition without taking a late
// snapshot, which would hide an earlier mutation from Finish.
func (t *Tx) RequireTracked(id int64) error {
	if !t.IsTracked(id) {
		return fmt.Errorf("%w: %d", ErrPageNotTracked, id)
	}
	return nil
}

// RequireTrackedEntity checks page edges in a mixed-entity translation writer.
func (t *Tx) RequireTrackedEntity(kind string, ids ...int64) error {
	if kind == "page" {
		for _, id := range ids {
			if err := t.RequireTracked(id); err != nil {
				return err
			}
		}
	}
	return nil
}
