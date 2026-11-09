// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"

	"github.com/olegiv/ocms-go/internal/pagewrite"
)

// AddRevisionRetention registers bounded pruning of old revisions written
// through API keys or OAuth on unpublished AI drafts. Editor and system
// revisions are always kept.
// It is independent of the MCP module: drafts and their revisions exist, and
// grow, whether or not the module is currently active.
func (s *Scheduler) AddRevisionRetention() error {
	const source, name, defaultSchedule = "core", "ai_revision_cleanup", "*/15 * * * *"
	schedule := defaultSchedule
	if s.registry != nil {
		schedule = s.registry.GetEffectiveSchedule(source, name, defaultSchedule)
	}
	run := func() error {
		result, err := pagewrite.PruneAIDraftRevisions(context.Background(), s.db)
		if err != nil {
			return err
		}
		s.logger.Info("AI draft revision retention batch completed", "versions", result.Versions,
			"provenance", result.Provenance, "replays", result.Replays)
		return nil
	}
	job := func() {
		if err := run(); err != nil {
			s.logger.Error("AI draft revision retention batch failed", "error", err)
		}
	}
	entry, err := s.cron.AddFunc(schedule, job)
	if err != nil {
		return err
	}
	if s.registry != nil {
		s.registry.Register(source, name, "Remove old revisions written through API keys or OAuth from unpublished AI drafts in bounded batches",
			defaultSchedule, s.cron, entry, job, run)
	}
	return nil
}
