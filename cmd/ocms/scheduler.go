// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"

	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/scheduler"
)

func startCoreScheduler(s *scheduler.Scheduler, cfg *config.Config) error {
	if err := s.AddOAuthCleanup(); err != nil {
		return fmt.Errorf("registering OAuth retention: %w", err)
	}
	if err := s.AddRevisionRetention(); err != nil {
		return fmt.Errorf("registering AI draft revision retention: %w", err)
	}
	if err := s.AddEventCleanup(cfg.EventCleanupEnabled, cfg.EventDiagnosticDays, cfg.EventAuditDays); err != nil {
		return fmt.Errorf("registering event retention: %w", err)
	}
	return s.Start()
}
