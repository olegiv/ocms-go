// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
	"github.com/olegiv/ocms-go/internal/model"
)

type scratchEntry struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	ExpiresAt time.Time `json:"expires_at"`
}

type scratchStore struct {
	mu      sync.Mutex
	entries map[int64]scratchEntry
	now     func() time.Time
}

func newScratchStore() *scratchStore {
	return &scratchStore{entries: make(map[int64]scratchEntry), now: time.Now}
}

func (m *Module) spikeEnabled() bool {
	return m.ctx != nil && m.ctx.Config != nil && m.ctx.Config.MCPSpikeTools && !m.ctx.Config.AIDisabled
}

type scratchInput struct {
	Title string `json:"title" minLength:"1" maxLength:"255"`
	Body  string `json:"body" maxLength:"8192"`
}

type scratchID struct {
	ID string `json:"id" minLength:"1" maxLength:"128"`
}

func (m *Module) saveScratch(_ context.Context, call *toolCall, in scratchInput) (scratchEntry, error) {
	if !m.spikeEnabled() || !slices.Contains(call.identity.scopes, model.PermissionMCPDrafts) {
		return scratchEntry{}, v2.NewError(v2.ErrForbidden, "mcp:drafts permission required")
	}
	if len(in.Title)+len(in.Body) > 8192 {
		return scratchEntry{}, v2.NewValidationError(map[string]string{"body": "Scratch entry exceeds 8 KiB"}, "Validation failed")
	}
	s := m.scratch
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, entry := range s.entries {
		if !now.Before(entry.ExpiresAt) {
			delete(s.entries, id)
		}
	}
	if _, exists := s.entries[call.identity.userID]; !exists && len(s.entries) >= 100 {
		return scratchEntry{}, v2.NewError(v2.ErrConflict, "Scratch storage is full; retry after entries expire")
	}
	entry := scratchEntry{ID: uuid.NewString(), Title: in.Title, Body: in.Body, ExpiresAt: now.Add(time.Hour)}
	s.entries[call.identity.userID] = entry
	return entry, nil
}

func (m *Module) getScratch(_ context.Context, call *toolCall, in scratchID) (scratchEntry, error) {
	if !m.spikeEnabled() || !slices.Contains(call.identity.scopes, model.PermissionMCPDrafts) {
		return scratchEntry{}, v2.NewError(v2.ErrForbidden, "mcp:drafts permission required")
	}
	s := m.scratch
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[call.identity.userID]
	if !exists || entry.ID != in.ID || !s.now().Before(entry.ExpiresAt) {
		return scratchEntry{}, v2.NewError(v2.ErrNotFound, "Scratch entry not found")
	}
	return entry, nil
}
