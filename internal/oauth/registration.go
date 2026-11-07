// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

const (
	dcrRegistrationsPerSource = 20
	dcrRegistrationsTotal     = 2000
	dcrRegistrationWindow     = time.Hour
	dcrGlobalWindow           = 10 * time.Minute
	dcrSingleClientGrace      = 10 * time.Minute
)

type dcrRefusalKind uint8

const (
	dcrSourceLimit dcrRefusalKind = iota
	dcrGlobalLimit
	dcrRegistryFull
	dcrRefusalKinds
)

type dcrRefusal struct {
	kind       dcrRefusalKind
	retryAfter time.Duration
}

// dcrSourceKey groups privacy addresses in an IPv6 /64. IPv4 and IPv4-mapped
// IPv6 share the same per-address key. Unparseable peers use one shared key.
func dcrSourceKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "unknown"
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is6() {
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
}

func checkDCRBudget(ctx context.Context, q *store.Queries, source string, now time.Time) (dcrRefusal, error) {
	budget, err := q.GetOAuthDCRRegistrationBudget(ctx, store.GetOAuthDCRRegistrationBudgetParams{
		SourceKey:    source,
		SourceCutoff: now.Add(-dcrRegistrationWindow).UnixNano(),
		GlobalCutoff: now.Add(-dcrGlobalWindow).UnixNano(),
	})
	if err != nil {
		return dcrRefusal{}, err
	}
	if budget.SourceTotal >= dcrRegistrationsPerSource {
		return dcrRefusal{dcrSourceLimit, time.Unix(0, budget.SourceOldest).Add(dcrRegistrationWindow).Sub(now)}, nil
	}
	if budget.Total >= dcrRegistrationsTotal {
		return dcrRefusal{dcrGlobalLimit, time.Unix(0, budget.Oldest).Add(dcrGlobalWindow).Sub(now)}, nil
	}
	return dcrRefusal{}, nil
}

// registerDCR rechecks admission in the same SQLite write transaction as
// client creation and any eviction. Failed writes consume no hourly budget.
func (s *Service) registerDCR(ctx context.Context, c registeredClient, ip string) (dcrRefusal, error) {
	source := dcrSourceKey(ip)
	// An already exhausted source can be refused even while SQLite has a writer.
	refusal, err := checkDCRBudget(ctx, s.q, source, s.now())
	if err != nil || refusal.retryAfter > 0 {
		return refusal, err
	}
	// Queue writes before borrowing a database connection. A stalled
	// SQLite writer must not fill the pool with DCR waiters.
	select {
	case s.dcrWrites <- struct{}{}:
		defer func() { <-s.dcrWrites }()
	case <-ctx.Done():
		return dcrRefusal{}, ctx.Err()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dcrRefusal{}, err
	}
	defer rollback(tx)
	q := s.q.WithTx(tx)
	now := s.now()
	refusal, err = checkDCRBudget(ctx, q, source, now)
	if err != nil || refusal.retryAfter > 0 {
		return refusal, err
	}
	save := func() error {
		return s.saveClientTx(ctx, q, c, "dcr", now.Add(DCRUnusedClientLifetime).Unix())
	}
	err = save()
	if errors.Is(err, errOAuthClientCapacity) {
		evicted, evictErr := q.EvictOAuthDCRClient(ctx, store.EvictOAuthDCRClientParams{
			SourceKey: source, GraceCutoff: now.Add(-dcrSingleClientGrace).UnixNano(),
		})
		if evictErr != nil {
			return dcrRefusal{}, evictErr
		}
		if evicted == 0 {
			return dcrRefusal{}, errOAuthClientCapacity
		}
		err = save()
	}
	if err != nil {
		return dcrRefusal{}, err
	}
	if err = q.DeleteOldOAuthDCRRegistrations(ctx, now.Add(-dcrRegistrationWindow).UnixNano()); err != nil {
		return dcrRefusal{}, err
	}
	if err = q.RecordOAuthDCRRegistration(ctx, store.RecordOAuthDCRRegistrationParams{
		ClientID: c.ID, SourceKey: source, RegisteredAt: now.UnixNano(),
	}); err != nil {
		return dcrRefusal{}, err
	}
	return dcrRefusal{}, tx.Commit()
}

// Public callers can generate unlimited refusals. Log each reason at most
// once per minute per service, without recording an address or client metadata.
func (s *Service) logDCRRefusal(ctx context.Context, kind dcrRefusalKind) {
	now := s.now().Unix()
	last := s.dcrRefusalLogs[kind].Load()
	if (last != 0 && now-last < 60) || !s.dcrRefusalLogs[kind].CompareAndSwap(last, now) {
		return
	}
	reasons := [dcrRefusalKinds]string{"source_limit", "global_limit", "registry_full"}
	slog.WarnContext(ctx, "OAuth client registration refused", "reason", reasons[kind])
}
