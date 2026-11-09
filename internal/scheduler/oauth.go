// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"

	"github.com/olegiv/ocms-go/internal/oauth"
)

// AddOAuthCleanup registers bounded retention independently of issuer enablement.
func (s *Scheduler) AddOAuthCleanup() error {
	const source, name, defaultSchedule = "core", "oauth_cleanup", "*/15 * * * *"
	schedule := defaultSchedule
	if s.registry != nil {
		schedule = s.registry.GetEffectiveSchedule(source, name, defaultSchedule)
	}
	run := func() error {
		result, err := oauth.Cleanup(context.Background(), s.db)
		if err != nil {
			return err
		}
		s.logger.Info("OAuth retention batch completed", "codes", result.Codes,
			"tokens", result.Tokens, "previews", result.Previews, "grants", result.Grants,
			"unredeemed_grants", result.Unredeemed, "clients", result.Clients)
		return nil
	}
	job := func() {
		if err := run(); err != nil {
			s.logger.Error("OAuth retention batch failed", "error", err)
		}
	}
	entry, err := s.cron.AddFunc(schedule, job)
	if err != nil {
		return err
	}
	if s.registry != nil {
		s.registry.Register(source, name, "Remove expired OAuth evidence in bounded batches",
			defaultSchedule, s.cron, entry, job, run)
	}
	return nil
}
