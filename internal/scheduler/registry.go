// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/olegiv/ocms-go/internal/store"
)

// registeredJob holds metadata about a registered cron job.
type registeredJob struct {
	source          string
	name            string
	description     string
	defaultSchedule string
	schedule        string // effective schedule (override or default)
	cronInstance    *cron.Cron
	entryID         cron.EntryID
	jobFunc         func()
	triggerFunc     func() error // nil if manual trigger not allowed
}

// JobInfo is the public view of a registered job.
type JobInfo struct {
	Source          string
	Name            string
	Description     string
	DefaultSchedule string
	Schedule        string // effective schedule
	IsOverridden    bool
	LastRun         time.Time
	NextRun         time.Time
	CanTrigger      bool
}

// Registry manages all scheduled jobs across core and modules.
type Registry struct {
	db      *sql.DB
	queries *store.Queries
	logger  *slog.Logger
	mu      sync.RWMutex
	jobs    map[string]*registeredJob // key: "source:name"
}

// NewRegistry creates a new scheduler registry and ensures the overrides table exists.
func NewRegistry(db *sql.DB, logger *slog.Logger) *Registry {
	r := &Registry{
		db:      db,
		queries: store.New(db),
		logger:  logger,
		jobs:    make(map[string]*registeredJob),
	}
	r.ensureTable()
	return r
}

// ensureTable creates the scheduler_overrides table if it doesn't exist.
// SEC-005: DDL statement must remain as direct SQL — SQLC cannot generate DDL.
// This is a safety net for startup before goose migrations run.
func (r *Registry) ensureTable() {
	_, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS scheduler_overrides (
			source TEXT NOT NULL,
			name TEXT NOT NULL,
			override_schedule TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (source, name)
		)
	`)
	if err != nil {
		r.logger.Error("failed to create scheduler_overrides table", "error", err)
	}
}

// GetEffectiveSchedule returns the override schedule if one exists, otherwise the default.
// Call this BEFORE cron.AddFunc to use the correct schedule.
func (r *Registry) GetEffectiveSchedule(source, name, defaultSchedule string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	override, err := r.queries.GetSchedulerOverride(ctx, store.GetSchedulerOverrideParams{
		Source: source,
		Name:   name,
	})
	if err == nil && override != "" {
		return override
	}
	return defaultSchedule
}

// Register records a job in the registry after it has been added to a cron instance.
func (r *Registry) Register(source, name, description, defaultSchedule string, cronInst *cron.Cron, entryID cron.EntryID, jobFunc func(), triggerFunc func() error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := fmt.Sprintf("%s:%s", source, name)
	effectiveSchedule := r.GetEffectiveSchedule(source, name, defaultSchedule)

	r.jobs[key] = &registeredJob{
		source:          source,
		name:            name,
		description:     description,
		defaultSchedule: defaultSchedule,
		schedule:        effectiveSchedule,
		cronInstance:    cronInst,
		entryID:         entryID,
		jobFunc:         jobFunc,
		triggerFunc:     triggerFunc,
	}

	r.logger.Debug("registered scheduled job", "source", source, "name", name, "schedule", effectiveSchedule)
}

// List returns all registered jobs sorted by source then name.
func (r *Registry) List() []JobInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]JobInfo, 0, len(r.jobs))
	for _, job := range r.jobs {
		info := JobInfo{
			Source:          job.source,
			Name:            job.name,
			Description:     job.description,
			DefaultSchedule: job.defaultSchedule,
			Schedule:        job.schedule,
			IsOverridden:    job.schedule != job.defaultSchedule,
			CanTrigger:      job.triggerFunc != nil,
		}

		// Get timing info from cron entry
		if job.cronInstance != nil {
			entry := job.cronInstance.Entry(job.entryID)
			info.NextRun = entry.Next
			info.LastRun = entry.Prev
		}

		result = append(result, info)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].Name < result[j].Name
	})

	return result
}

// TriggerNow manually executes a job immediately.
func (r *Registry) TriggerNow(source, name string) error {
	r.mu.RLock()
	key := fmt.Sprintf("%s:%s", source, name)
	job, ok := r.jobs[key]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("job not found: %s:%s", source, name)
	}

	if job.triggerFunc == nil {
		return fmt.Errorf("manual trigger not available for: %s:%s", source, name)
	}

	r.logger.Info("manually triggering job", "source", source, "name", name)
	return job.triggerFunc()
}

// UpdateSchedule changes the schedule for a job. Removes the old cron entry,
// adds a new one with the updated schedule, and persists the override to DB.
func (r *Registry) UpdateSchedule(source, name, newSchedule string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := fmt.Sprintf("%s:%s", source, name)
	job, ok := r.jobs[key]
	if !ok {
		return fmt.Errorf("job not found: %s:%s", source, name)
	}

	if job.cronInstance == nil || job.jobFunc == nil {
		return fmt.Errorf("job cannot be rescheduled: %s:%s", source, name)
	}

	// Validate new schedule by parsing it
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := parser.Parse(newSchedule); err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", newSchedule, err)
	}

	// Remove old entry and add new one
	job.cronInstance.Remove(job.entryID)
	newEntryID, err := job.cronInstance.AddFunc(newSchedule, job.jobFunc)
	if err != nil {
		// Re-add with old schedule on failure
		fallbackID, fallbackErr := job.cronInstance.AddFunc(job.schedule, job.jobFunc)
		if fallbackErr != nil {
			return fmt.Errorf("critical: failed to restore schedule after update failure: %w (original: %w)", fallbackErr, err)
		}
		job.entryID = fallbackID
		return fmt.Errorf("failed to apply new schedule: %w", err)
	}

	// Update in-memory state
	job.entryID = newEntryID
	job.schedule = newSchedule

	// Persist override to DB
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dbErr := r.queries.UpsertSchedulerOverride(ctx, store.UpsertSchedulerOverrideParams{
		Source:           source,
		Name:             name,
		OverrideSchedule: newSchedule,
	})
	if dbErr != nil {
		r.logger.Error("failed to persist schedule override", "error", dbErr, "source", source, "name", name)
	}

	r.logger.Info("updated job schedule", "source", source, "name", name, "schedule", newSchedule)
	return nil
}

// Unregister removes a job from the registry, stops its cron entry,
// and deletes any schedule override from the database.
func (r *Registry) Unregister(source, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := fmt.Sprintf("%s:%s", source, name)
	job, ok := r.jobs[key]
	if !ok {
		return
	}

	if job.cronInstance != nil {
		job.cronInstance.Remove(job.entryID)
	}

	delete(r.jobs, key)

	// Remove any override from DB
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := r.queries.DeleteSchedulerOverride(ctx, store.DeleteSchedulerOverrideParams{
		Source: source,
		Name:   name,
	})
	if err != nil {
		r.logger.Error("failed to delete schedule override on unregister", "error", err, "source", source, "name", name)
	}

	r.logger.Debug("unregistered scheduled job", "source", source, "name", name)
}

// ResetSchedule removes the override and restores the default schedule.
func (r *Registry) ResetSchedule(source, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := fmt.Sprintf("%s:%s", source, name)
	job, ok := r.jobs[key]
	if !ok {
		return fmt.Errorf("job not found: %s:%s", source, name)
	}

	if job.schedule == job.defaultSchedule {
		return nil // Already at default
	}

	if job.cronInstance == nil || job.jobFunc == nil {
		return fmt.Errorf("job cannot be rescheduled: %s:%s", source, name)
	}

	// Remove old entry and add with default schedule
	job.cronInstance.Remove(job.entryID)
	newEntryID, err := job.cronInstance.AddFunc(job.defaultSchedule, job.jobFunc)
	if err != nil {
		return fmt.Errorf("failed to restore default schedule: %w", err)
	}

	job.entryID = newEntryID
	job.schedule = job.defaultSchedule

	// Remove override from DB
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dbErr := r.queries.DeleteSchedulerOverride(ctx, store.DeleteSchedulerOverrideParams{
		Source: source,
		Name:   name,
	})
	if dbErr != nil {
		r.logger.Error("failed to remove schedule override", "error", dbErr, "source", source, "name", name)
	}

	r.logger.Info("reset job schedule to default", "source", source, "name", name, "schedule", job.defaultSchedule)
	return nil
}
