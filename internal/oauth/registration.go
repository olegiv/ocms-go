// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/store"
)

const (
	dcrRegistrationsPerSource  = 20
	dcrRegistrationsPerNetwork = 100
	dcrRegistrationsTotal      = 2000
	dcrRegistrationWindow      = time.Hour
	dcrGlobalWindow            = 10 * time.Minute
	dcrSingleClientGrace       = 10 * time.Minute
	dcrRegistryPressureGrace   = time.Minute
)

type dcrRefusalKind uint8

const (
	dcrAdmitted dcrRefusalKind = iota
	dcrSourceLimit
	dcrNetworkLimit
	dcrGlobalLimit
	dcrRegistryFull
	dcrRefusalKinds
)

type dcrRefusal struct {
	kind       dcrRefusalKind
	retryAfter time.Duration
}

type dcrSource struct {
	key        string
	networkMin string
	networkEnd string
	zeroSubnet string
}

// authorizeDCR checks protected registration before metadata parsing or admission.
func (s *Service) authorizeDCR(w http.ResponseWriter, r *http.Request) bool {
	if s.dcrInitialAccessTokenHash == "" {
		return true
	}
	const challenge = `Bearer realm="client-registration"`
	headers := r.Header.Values("Authorization")
	if len(headers) > 1 {
		w.Header().Set("WWW-Authenticate", challenge+`, error="invalid_request"`)
		WriteProblem(w, problem("invalid_request"))
		return false
	}
	if len(headers) == 1 {
		scheme, token, _ := strings.Cut(headers[0], " ")
		if strings.EqualFold(scheme, "Bearer") {
			token = strings.TrimLeft(token, " ")
			if strings.ContainsAny(token, " \t,") {
				w.Header().Set("WWW-Authenticate", challenge+`, error="invalid_request"`)
				WriteProblem(w, problem("invalid_request"))
				return false
			}
			if len(token) == config.DCRInitialAccessTokenLength &&
				subtle.ConstantTimeCompare([]byte(tokenHash(token)), []byte(s.dcrInitialAccessTokenHash)) == 1 {
				return true
			}
			w.Header().Set("WWW-Authenticate", challenge+`, error="invalid_token"`)
			p := problem("invalid_token")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": p.Code, "error_description": p.Description})
			return false
		}
	}
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	return false
}

// parseDCRSource keeps the /64 source key for admission and eviction, while
// locating all /64 keys from the same /48 in the existing ledger.
func parseDCRSource(ip string) dcrSource {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return dcrSource{key: "unknown"}
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return dcrSource{key: addr.String()}
	}
	network := netip.PrefixFrom(addr, 48).Masked().Addr()
	first := network.As16()
	first[7] = 1
	// With a nonzero fourth hextet, netip always compresses the four
	// trailing zeros, so the first three hextets form an indexable range.
	// A zero fourth hextet can compress farther left and needs an exact key.
	firstKey := netip.PrefixFrom(netip.AddrFrom16(first), 64).String()
	prefix := strings.TrimSuffix(firstKey, "1::/64")
	return dcrSource{
		key:        netip.PrefixFrom(addr, 64).Masked().String(),
		networkMin: prefix,
		networkEnd: strings.TrimSuffix(prefix, ":") + ";",
		zeroSubnet: netip.PrefixFrom(network, 64).String(),
	}
}

// checkDCRBudget uses the same admission evidence for preflight and the write transaction.
func checkDCRBudget(ctx context.Context, q *store.Queries, source dcrSource, now time.Time) (dcrRefusal, error) {
	budget, err := q.GetOAuthDCRRegistrationBudget(ctx, store.GetOAuthDCRRegistrationBudgetParams{
		SourceKey:     source.key,
		SourceCutoff:  now.Add(-dcrRegistrationWindow).UnixNano(),
		GlobalCutoff:  now.Add(-dcrGlobalWindow).UnixNano(),
		NetworkMin:    source.networkMin,
		NetworkEnd:    source.networkEnd,
		ZeroSubnet:    source.zeroSubnet,
		NetworkOffset: dcrRegistrationsPerNetwork - 1,
	})
	if err != nil {
		return dcrRefusal{}, err
	}
	var refusal dcrRefusal
	if budget.SourceTotal >= dcrRegistrationsPerSource {
		refusal = dcrRefusal{dcrSourceLimit, time.Unix(0, budget.SourceOldest).Add(dcrRegistrationWindow).Sub(now)}
	}
	if delay := time.Duration(budget.NetworkRetryNs); delay > refusal.retryAfter {
		refusal = dcrRefusal{dcrNetworkLimit, delay}
	}
	if budget.Total >= dcrRegistrationsTotal {
		// Keep the issuer-wide alert visible even when a source limit
		// contributes a longer delay.
		delay := time.Unix(0, budget.Oldest).Add(dcrGlobalWindow).Sub(now)
		return dcrRefusal{dcrGlobalLimit, max(delay, refusal.retryAfter)}, nil
	}
	return refusal, nil
}

// registerDCR rechecks admission in the same SQLite write transaction as
// client creation and any eviction. Failed writes consume no hourly budget.
func (s *Service) registerDCR(ctx context.Context, c registeredClient, ip string) (dcrRefusal, error) {
	source := parseDCRSource(ip)
	// An already exhausted source can be refused even while SQLite has a writer.
	refusal, err := checkDCRBudget(ctx, s.q, source, s.now())
	if err != nil || refusal.kind != dcrAdmitted {
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
	if err != nil || refusal.kind != dcrAdmitted {
		return refusal, err
	}
	save := func() error {
		return s.saveClientTx(ctx, q, c, "dcr", now.Add(DCRUnusedClientLifetime).Unix())
	}
	err = save()
	if errors.Is(err, errOAuthClientCapacity) {
		evicted, evictErr := q.EvictOAuthDCRClient(ctx, store.EvictOAuthDCRClientParams{
			SourceKey:      source.key,
			GraceCutoff:    now.Add(-dcrSingleClientGrace).UnixNano(),
			PressureCutoff: now.Add(-dcrRegistryPressureGrace).UnixNano(),
			SourceCutoff:   now.Add(-dcrRegistrationWindow).UnixNano(),
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
		ClientID: c.ID, SourceKey: source.key, RegisteredAt: now.UnixNano(),
	}); err != nil {
		return dcrRefusal{}, err
	}
	return dcrRefusal{}, tx.Commit()
}

func (s *Service) logDCRRefusal(ctx context.Context, kind dcrRefusalKind) {
	reasons := [dcrRefusalKinds]string{dcrSourceLimit: "source_limit", dcrNetworkLimit: "network_limit", dcrGlobalLimit: "global_limit", dcrRegistryFull: "registry_full"}
	s.logClientRefusal(ctx, &s.dcrRefusalLogs[kind], "dcr", reasons[kind])
}

// logClientRefusal reports each refusal reason at most once per minute per
// service, without recording an address or client metadata.
func (s *Service) logClientRefusal(ctx context.Context, previous *atomic.Int64, source, reason string) {
	now := s.now().Unix()
	last := previous.Load()
	if (last != 0 && now-last < 60) || !previous.CompareAndSwap(last, now) {
		return
	}
	slog.WarnContext(ctx, "OAuth client registration refused", "source", source, "reason", reason)
}
