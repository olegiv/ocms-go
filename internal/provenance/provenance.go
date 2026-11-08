// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package provenance separates authenticated identity from validation and agent reports.
package provenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// Principal is supplied by a trusted adapter, never decoded from draft input.
// A registered public client ID identifies a registration, not a vendor.
type Principal struct {
	UserID        int64
	Source        string // human, api, oauth, system, migration
	APIKeyID      int64
	OAuthGrantID  string
	OAuthClientID string
	Transport     string
}

// ErrInvalidPrincipal reports inconsistent actor or credential fields.
var ErrInvalidPrincipal = errors.New("invalid provenance principal")

// Validate checks that the actor and credential fields match the source.
func (p Principal) Validate() error {
	if p.UserID < 0 || p.APIKeyID < 0 {
		return ErrInvalidPrincipal
	}
	noOAuth := p.OAuthGrantID == "" && p.OAuthClientID == ""
	var valid bool
	switch p.Source {
	case "human":
		valid = p.UserID > 0 && p.APIKeyID == 0 && noOAuth
	case "api":
		valid = p.UserID > 0 && p.APIKeyID > 0 && noOAuth
	case "oauth":
		valid = p.UserID > 0 && p.APIKeyID == 0 && p.OAuthGrantID != "" && p.OAuthClientID != ""
	case "system", "migration":
		valid = p.APIKeyID == 0 && noOAuth
	}
	if !valid {
		return ErrInvalidPrincipal
	}
	return nil
}

// Evidence contains only validation results and explicitly untrusted reports.
// Human and system writes supply the zero value; they inherit no agent claims.
type Evidence struct {
	Validated json.RawMessage
	Reported  json.RawMessage
}

// Record appends provenance in the transaction that saves its revision.
func Record(ctx context.Context, q *store.Queries, page store.Page, principal Principal, evidence Evidence) error {
	if err := principal.Validate(); err != nil {
		return err
	}
	validated, reported := evidence.Validated, evidence.Reported
	if len(validated) == 0 {
		validated = json.RawMessage(`{}`)
	}
	if len(reported) == 0 {
		reported = json.RawMessage(`{}`)
	}
	if !json.Valid(validated) || !json.Valid(reported) {
		return errors.New("provenance evidence must be valid JSON")
	}
	return q.CreateContentProvenance(ctx, store.CreateContentProvenanceParams{
		PageID: page.ID, Revision: page.Revision,
		UserID: sql.NullInt64{Int64: principal.UserID, Valid: principal.UserID > 0},
		Source: principal.Source, ApiKeyID: sql.NullInt64{Int64: principal.APIKeyID, Valid: principal.APIKeyID > 0},
		OauthGrantID: principal.OAuthGrantID, OauthClientID: principal.OAuthClientID,
		Transport: principal.Transport, ValidatedJson: string(validated), ReportedJson: string(reported), CreatedAt: time.Now().UTC(),
	})
}
