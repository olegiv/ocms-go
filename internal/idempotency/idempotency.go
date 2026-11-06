// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package idempotency stores successful mutations and permanent replay tombstones.
// Authorization belongs to the caller and must run again for every replay.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/store"
)

const ReplayWindow = 7 * 24 * time.Hour
const maxResultBytes = 256 * 1024

var (
	ErrInvalidRequest = errors.New("invalid_request_id")
	ErrKeyReused      = errors.New("idempotency_key_reused")
	ErrExpired        = errors.New("request_expired")
)

// Request identifies one logical operation. Construct it before applying mutable
// defaults. Input must retain any meaningful omitted/null distinction in its type.
type Request struct {
	UserID         int64
	Tool, ID, Hash string
}

// New hashes typed input canonically. The top-level request_id is excluded.
// JSON object order is irrelevant; absent properties, nulls and arrays remain distinct.
func New(userID int64, tool, requestID string, input any) (Request, error) {
	id, err := uuid.Parse(requestID)
	if err != nil || len(requestID) != 36 || id == uuid.Nil || userID <= 0 || tool == "" || len(tool) > 128 {
		return Request{}, ErrInvalidRequest
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return Request{}, fmt.Errorf("encode idempotent input: %w", err)
	}
	var fields map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err = decoder.Decode(&fields); err != nil || fields == nil {
		return Request{}, errors.New("idempotent input must be an object")
	}
	delete(fields, "request_id")
	encoded, err = json.Marshal(fields)
	if err != nil {
		return Request{}, err
	}
	digest := sha256.Sum256(encoded)
	return Request{UserID: userID, Tool: tool, ID: id.String(), Hash: hex.EncodeToString(digest[:])}, nil
}

// Lookup checks a committed request before content validation or CAS. authorize
// must recheck the entity's current visibility; missing entities stay tombstones.
func Lookup(ctx context.Context, q *store.Queries, request Request, now time.Time, authorize func(string, int64) error) (*store.IdempotencyKey, error) {
	record, err := q.GetIdempotencyKey(ctx, store.GetIdempotencyKeyParams{UserID: request.UserID, Tool: request.Tool, RequestID: request.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if record.RequestHash != request.Hash {
		return nil, ErrKeyReused
	}
	if authorize == nil {
		return nil, errors.New("replay requires fresh entity authorization")
	}
	if err = authorize(record.EntityType, record.EntityID); err != nil {
		return nil, err
	}
	if !record.ResultJson.Valid || !now.Before(record.CreatedAt.Add(ReplayWindow)) {
		return nil, ErrExpired
	}
	return &record, nil
}

// Store writes an approved page receipt in the same transaction as the mutation.
// InTx's immediate SQLite write lock serializes duplicates before lookup. A busy
// or canceled transaction is rolled back; the caller retries the same request ID.
func Store(ctx context.Context, q *store.Queries, request Request, entityType string, entityID, revision int64, result Receipt, now time.Time) error {
	switch value := result.(type) {
	case Page:
	case *Page:
		if value == nil {
			return errors.New("idempotent page receipt must not be nil")
		}
	default:
		return errors.New("idempotent result must be an approved page receipt")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(encoded) > maxResultBytes {
		return errors.New("idempotent result exceeds replay limit")
	}
	return q.CreateIdempotencyKey(ctx, store.CreateIdempotencyKeyParams{UserID: request.UserID, Tool: request.Tool, RequestID: request.ID, RequestHash: request.Hash, ResultJson: sql.NullString{String: string(encoded), Valid: true}, EntityType: entityType, EntityID: entityID, EntityRevision: revision, CreatedAt: now.UTC()})
}
