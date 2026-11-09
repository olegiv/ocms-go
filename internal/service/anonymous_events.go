// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
)

const anonymousEventWindow = 30 * time.Second
const maxAnonymousEventSources = 4096

type anonymousEventLimiter struct {
	mu    sync.Mutex
	until time.Time
	seen  map[anonymousEventKey]struct{}
}

type anonymousEventKey struct {
	message string
	source  string
}

// AllowAnonymousEvent limits opt-in abuse diagnostics by static message and
// trusted client source. Never gate an account audit or an enforcement action.
func (s *EventService) AllowAnonymousEvent(message, source string) bool {
	return s.anonymousEvents.allow(message, source, time.Now())
}

// LogAnonymousSecurityEvent records a sampled public rejection.
func (s *EventService) LogAnonymousSecurityEvent(ctx context.Context, message, source, path string, metadata map[string]any) error {
	if !s.AllowAnonymousEvent(message, source) {
		return nil
	}
	return s.LogSecurityEvent(ctx, model.EventLevelWarning, message, nil, source, path, metadata)
}

func (l *anonymousEventLimiter) allow(message, source string, now time.Time) bool {
	key := anonymousEventKey{message: message}
	if ip, err := netip.ParseAddr(source); err == nil {
		ip = ip.Unmap().WithZone("")
		if ip.Is6() {
			key.source = netip.PrefixFrom(ip, 64).Masked().String()
		} else {
			key.source = ip.String()
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.until) {
		l.until = now.Add(anonymousEventWindow)
		clear(l.seen)
	}
	if _, exists := l.seen[key]; exists || len(l.seen) >= maxAnonymousEventSources {
		return false
	}
	if l.seen == nil {
		l.seen = make(map[anonymousEventKey]struct{})
	}
	l.seen[key] = struct{}{}
	return true
}
