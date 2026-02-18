// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"golang.org/x/time/rate"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

const (
	taskSource         = "task"
	maxResponseBodyLen = 4096
	defaultHTTPTimeout = 30 * time.Second
)

// TaskExecutor manages user-created scheduled tasks that make HTTP GET requests.
type TaskExecutor struct {
	db              *sql.DB
	logger          *slog.Logger
	registry        *Registry
	cronInst        *cron.Cron
	httpClient      *http.Client
	mu              sync.Mutex
	taskEntries     map[int64]cron.EntryID
	triggerLimiters map[int64]*rate.Limiter
}

// NewTaskExecutor creates a new TaskExecutor.
func NewTaskExecutor(db *sql.DB, logger *slog.Logger, registry *Registry, cronInst *cron.Cron) *TaskExecutor {
	return &TaskExecutor{
		db:       db,
		logger:   logger,
		registry: registry,
		cronInst: cronInst,
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
			Transport: &http.Transport{
				DialContext: util.SSRFSafeDialContext(&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}),
				MaxIdleConns:       10,
				IdleConnTimeout:    90 * time.Second,
				DisableCompression: false,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if err := ValidateTaskURL(req.URL.String()); err != nil {
					return fmt.Errorf("redirect blocked (SSRF protection): %w", err)
				}
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		taskEntries:     make(map[int64]cron.EntryID),
		triggerLimiters: make(map[int64]*rate.Limiter),
	}
}

// LoadAndScheduleAll loads all active tasks from the database and schedules them.
func (te *TaskExecutor) LoadAndScheduleAll() error {
	ctx := context.Background()
	queries := store.New(te.db)

	tasks, err := queries.ListActiveScheduledTasks(ctx)
	if err != nil {
		return fmt.Errorf("failed to load active scheduled tasks: %w", err)
	}

	for _, task := range tasks {
		if err := te.scheduleTask(task); err != nil {
			te.logger.Error("failed to schedule task", "error", err, "task_id", task.ID, "task_name", task.Name)
		}
	}

	if len(tasks) > 0 {
		te.logger.Info("loaded scheduled tasks", "count", len(tasks))
	}

	return nil
}

// AddTask schedules a new or re-enabled task.
func (te *TaskExecutor) AddTask(task store.ScheduledTask) error {
	return te.scheduleTask(task)
}

// RemoveTask stops and unregisters a task.
func (te *TaskExecutor) RemoveTask(taskID int64) {
	te.mu.Lock()
	entryID, ok := te.taskEntries[taskID]
	if ok {
		te.cronInst.Remove(entryID)
		delete(te.taskEntries, taskID)
	}
	te.mu.Unlock()

	if te.registry != nil {
		name := taskName(taskID)
		te.registry.Unregister(taskSource, name)
	}
}

// TriggerTask manually executes a task immediately with rate limiting.
func (te *TaskExecutor) TriggerTask(taskID int64) error {
	// Rate limit manual triggers to 1 per 10 seconds per task
	te.mu.Lock()
	limiter := te.triggerLimiters[taskID]
	if limiter == nil {
		limiter = rate.NewLimiter(rate.Every(10*time.Second), 1)
		te.triggerLimiters[taskID] = limiter
	}
	te.mu.Unlock()

	if !limiter.Allow() {
		return fmt.Errorf("rate limit exceeded, try again in a few seconds")
	}

	ctx := context.Background()
	queries := store.New(te.db)

	task, err := queries.GetScheduledTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("task not found: %w", err)
	}

	go te.executeTask(task)
	return nil
}

// RescheduleTask updates the cron entry for a task with a new schedule.
func (te *TaskExecutor) RescheduleTask(task store.ScheduledTask) error {
	te.RemoveTask(task.ID)
	return te.scheduleTask(task)
}

// scheduleTask creates a cron entry for a task and registers it in the registry.
func (te *TaskExecutor) scheduleTask(task store.ScheduledTask) error {
	name := taskName(task.ID)

	// Check for schedule override
	schedule := task.Schedule
	if te.registry != nil {
		schedule = te.registry.GetEffectiveSchedule(taskSource, name, task.Schedule)
	}

	// Capture task for closure
	taskCopy := task
	jobFunc := func() {
		te.executeTask(taskCopy)
	}

	entryID, err := te.cronInst.AddFunc(schedule, jobFunc)
	if err != nil {
		return fmt.Errorf("invalid schedule %q for task %q: %w", schedule, task.Name, err)
	}

	te.mu.Lock()
	te.taskEntries[task.ID] = entryID
	te.mu.Unlock()

	if te.registry != nil {
		triggerFunc := func() error {
			return te.TriggerTask(taskCopy.ID)
		}
		te.registry.Register(
			taskSource, name,
			task.Name+": GET "+task.Url,
			task.Schedule,
			te.cronInst, entryID, jobFunc, triggerFunc,
		)
	}

	return nil
}

// executeTask performs the HTTP GET request and records the result.
func (te *TaskExecutor) executeTask(task store.ScheduledTask) {
	ctx := context.Background()
	queries := store.New(te.db)
	startedAt := time.Now()

	// Create a run record
	run, err := queries.CreateScheduledTaskRun(ctx, store.CreateScheduledTaskRunParams{
		TaskID:    task.ID,
		StartedAt: startedAt,
	})
	if err != nil {
		te.logger.Error("failed to create task run record", "error", err, "task_id", task.ID)
		return
	}

	// Revalidate URL before each execution to prevent DNS rebinding attacks
	if err := ValidateTaskURL(task.Url); err != nil {
		te.recordFailure(queries, run.ID, startedAt, fmt.Sprintf("SSRF protection: %v", err))
		return
	}

	// Make HTTP GET with per-task timeout
	timeout := time.Duration(task.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, task.Url, nil)
	if err != nil {
		te.recordFailure(queries, run.ID, startedAt, fmt.Sprintf("invalid URL: %v", err))
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible)")

	resp, err := te.httpClient.Do(req)
	if err != nil {
		te.recordFailure(queries, run.ID, startedAt, fmt.Sprintf("request failed: %v", err))
		return
	}
	if resp == nil {
		te.recordFailure(queries, run.ID, startedAt, "nil response from server")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body to measure size (discard content for security)
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyLen))
	if err != nil {
		te.recordFailure(queries, run.ID, startedAt, fmt.Sprintf("failed to read response: %v", err))
		return
	}

	// Store content-type summary instead of raw body to prevent XSS/data leaks
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "unknown"
	}
	summary := fmt.Sprintf("%s (%d bytes)", contentType, len(body))

	completedAt := time.Now()
	durationMs := completedAt.Sub(startedAt).Milliseconds()

	updateErr := queries.UpdateScheduledTaskRunSuccess(ctx, store.UpdateScheduledTaskRunSuccessParams{
		StatusCode:   sql.NullInt64{Int64: int64(resp.StatusCode), Valid: true},
		ResponseBody: sql.NullString{String: summary, Valid: true},
		DurationMs:   sql.NullInt64{Int64: durationMs, Valid: true},
		CompletedAt:  sql.NullTime{Time: completedAt, Valid: true},
		ID:           run.ID,
	})
	if updateErr != nil {
		te.logger.Error("failed to update task run success", "error", updateErr, "run_id", run.ID)
	}

	te.logger.Debug("task executed", "task_id", task.ID, "task_name", task.Name, "status_code", resp.StatusCode, "duration_ms", durationMs)
}

// recordFailure updates a run record with an error.
func (te *TaskExecutor) recordFailure(queries *store.Queries, runID int64, startedAt time.Time, errMsg string) {
	completedAt := time.Now()
	durationMs := completedAt.Sub(startedAt).Milliseconds()

	updateErr := queries.UpdateScheduledTaskRunFailed(context.Background(), store.UpdateScheduledTaskRunFailedParams{
		ErrorMessage: sql.NullString{String: errMsg, Valid: true},
		DurationMs:   sql.NullInt64{Int64: durationMs, Valid: true},
		CompletedAt:  sql.NullTime{Time: completedAt, Valid: true},
		ID:           runID,
	})
	if updateErr != nil {
		te.logger.Error("failed to update task run failure", "error", updateErr, "run_id", runID)
	}

	te.logger.Warn("task execution failed", "run_id", runID, "error", errMsg)
}

// RegisterCleanupJob adds a daily cron job that deletes task runs older than 30 days.
func (te *TaskExecutor) RegisterCleanupJob() {
	const cleanupSchedule = "0 3 * * *" // daily at 03:00
	const retentionDays = 30

	jobFunc := func() {
		ctx := context.Background()
		queries := store.New(te.db)
		cutoff := time.Now().AddDate(0, 0, -retentionDays)
		if err := queries.DeleteAllOldTaskRuns(ctx, cutoff); err != nil {
			te.logger.Error("failed to clean up old task runs", "error", err)
			return
		}
		te.logger.Info("cleaned up old task runs", "older_than", cutoff.Format("2006-01-02"))
	}

	// Apply persisted schedule override if one exists
	effectiveSchedule := cleanupSchedule
	if te.registry != nil {
		effectiveSchedule = te.registry.GetEffectiveSchedule(taskSource, "cleanup", cleanupSchedule)
	}

	entryID, err := te.cronInst.AddFunc(effectiveSchedule, jobFunc)
	if err != nil {
		te.logger.Error("failed to register task run cleanup job", "error", err)
		return
	}

	if te.registry != nil {
		te.registry.Register(
			taskSource, "cleanup",
			"Delete task runs older than 30 days",
			cleanupSchedule,
			te.cronInst, entryID, jobFunc, func() error {
				go jobFunc()
				return nil
			},
		)
	}

	te.logger.Info("task run cleanup job registered", "schedule", effectiveSchedule, "retention_days", retentionDays)
}

// taskName generates the registry name for a task.
func taskName(taskID int64) string {
	return fmt.Sprintf("task_%d", taskID)
}
