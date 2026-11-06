// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"sync"
	"time"
)

const (
	dcrRegistrationsPerHour = 20
	dcrRegistrationWindow   = time.Hour
	dcrRegistrationSources  = 1000
)

// dcrRegistrationBudget retains successful admissions for a rolling hour. It is
// process-local, like the public route limiter, but never evicts live counters.
type dcrRegistrationBudget struct {
	mu     sync.Mutex
	recent map[string][]time.Time
}

// registerDCR serializes admission and persistence so simultaneous requests
// cannot overspend a source's budget. Only committed registrations consume it;
// CIMD admission and existing connections do not take this lock or budget.
func (s *Service) registerDCR(ctx context.Context, c registeredClient, ip string) (time.Duration, error) {
	b := &s.dcrRegistrations
	b.mu.Lock()
	defer b.mu.Unlock()
	now := s.now()
	if b.recent == nil {
		b.recent = make(map[string][]time.Time)
	}
	var nextSourceExpiry time.Time
	for source, times := range b.recent {
		first := 0
		for first < len(times) && !times[first].Add(dcrRegistrationWindow).After(now) {
			first++
		}
		if first == len(times) {
			delete(b.recent, source)
			continue
		}
		times = times[:copy(times, times[first:])]
		b.recent[source] = times
		// A source key can be reclaimed only after its last success expires.
		expires := times[len(times)-1].Add(dcrRegistrationWindow)
		if nextSourceExpiry.IsZero() || expires.Before(nextSourceExpiry) {
			nextSourceExpiry = expires
		}
	}
	times := b.recent[ip]
	if len(times) >= dcrRegistrationsPerHour {
		return times[0].Add(dcrRegistrationWindow).Sub(now), nil
	}
	if len(times) == 0 && len(b.recent) >= dcrRegistrationSources {
		return nextSourceExpiry.Sub(now), nil
	}
	if err := s.saveClient(ctx, c, "dcr", now.Add(DCRUnusedClientLifetime).Unix()); err != nil {
		return 0, err
	}
	b.recent[ip] = append(times, s.now())
	return 0, nil
}
