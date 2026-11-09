// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"time"

	"github.com/olegiv/ocms-go/internal/service"
)

// AddEventCleanup registers a bounded cleanup of events older than 90 days.
func (s *Scheduler) AddEventCleanup() error {
	const source, name, defaultSchedule = "core", "event_cleanup", "*/15 * * * *"
	schedule := defaultSchedule
	if s.registry != nil {
		schedule = s.registry.GetEffectiveSchedule(source, name, defaultSchedule)
	}
	events := service.NewEventService(s.db)
	run := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return events.DeleteOldEvents(ctx, 90*24*time.Hour)
	}
	job := func() {
		if err := run(); err != nil {
			s.logger.Error("event retention batch failed", "error", err)
		}
	}
	entry, err := s.cron.AddFunc(schedule, job)
	if err != nil {
		return err
	}
	if s.registry != nil {
		s.registry.Register(source, name, "Remove events older than 90 days in bounded batches",
			defaultSchedule, s.cron, entry, job, run)
	}
	return nil
}
