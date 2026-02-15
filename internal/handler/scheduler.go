// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/scheduler"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

const (
	redirectAdminScheduler = "/admin/scheduler"
	taskRunsPerPage        = 20
	taskNameMinLen         = 3
	taskNameMaxLen         = 100
	taskURLMaxLen          = 2048
	taskTimeoutMin         = 1
	taskTimeoutMax         = 300
	taskTimeoutDefault     = 30
)

// parseTaskTimeout parses and clamps a timeout string to the allowed range.
func parseTaskTimeout(s string) int64 {
	timeout := int64(taskTimeoutDefault)
	if s != "" {
		if t, err := strconv.ParseInt(s, 10, 64); err == nil && t > 0 {
			timeout = t
		}
	}
	if timeout < taskTimeoutMin {
		timeout = taskTimeoutMin
	}
	if timeout > taskTimeoutMax {
		timeout = taskTimeoutMax
	}
	return timeout
}

// validateCronSchedule validates a cron schedule expression.
func validateCronSchedule(schedule string) error {
	cronParser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	_, err := cronParser.Parse(schedule)
	return err
}

// SchedulerHandler handles scheduler admin routes.
type SchedulerHandler struct {
	db             *sql.DB
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	registry       *scheduler.Registry
	taskExecutor   *scheduler.TaskExecutor
	eventService   *service.EventService
}

// NewSchedulerHandler creates a new SchedulerHandler.
func NewSchedulerHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, registry *scheduler.Registry, taskExec *scheduler.TaskExecutor, es *service.EventService) *SchedulerHandler {
	return &SchedulerHandler{
		db:             db,
		renderer:       renderer,
		sessionManager: sm,
		registry:       registry,
		taskExecutor:   taskExec,
		eventService:   es,
	}
}

// SchedulerJobView represents a job for the template.
type SchedulerJobView struct {
	Source          string
	Name            string
	Description     string
	DefaultSchedule string
	Schedule        string
	IsOverridden    bool
	LastRun         string
	NextRun         string
	CanTrigger      bool
}

// SchedulerTaskView represents a custom task for the template.
type SchedulerTaskView struct {
	ID       int64
	Name     string
	URL      string
	Schedule string
	IsActive bool
	Timeout  int64
	LastRun  string
}

// SchedulerListData holds all data for the scheduler list page.
type SchedulerListData struct {
	Jobs  []SchedulerJobView
	Tasks []SchedulerTaskView
}

// List handles GET /admin/scheduler - displays all scheduled jobs and custom tasks.
func (h *SchedulerHandler) List(w http.ResponseWriter, r *http.Request) {
	lang := middleware.GetAdminLang(r)

	jobs := h.registry.List()

	views := make([]SchedulerJobView, 0, len(jobs))
	for _, job := range jobs {
		lastRun := "-"
		if !job.LastRun.IsZero() {
			lastRun = job.LastRun.Format("2006-01-02 15:04:05")
		}
		nextRun := "-"
		if !job.NextRun.IsZero() {
			nextRun = job.NextRun.Format("2006-01-02 15:04:05")
		}

		views = append(views, SchedulerJobView{
			Source:          job.Source,
			Name:            job.Name,
			Description:     job.Description,
			DefaultSchedule: job.DefaultSchedule,
			Schedule:        job.Schedule,
			IsOverridden:    job.IsOverridden,
			LastRun:         lastRun,
			NextRun:         nextRun,
			CanTrigger:      job.CanTrigger,
		})
	}

	// Load custom tasks
	queries := store.New(h.db)
	tasks, err := queries.ListScheduledTasks(r.Context())
	if err != nil {
		slog.Error("failed to list scheduled tasks", "error", err)
	}

	taskViews := make([]SchedulerTaskView, 0, len(tasks))
	for _, task := range tasks {
		lastRun := "-"
		// Get last run from the registry if active
		if task.IsActive == 1 {
			regJobs := h.registry.List()
			taskRegName := fmt.Sprintf("task_%d", task.ID)
			for _, rj := range regJobs {
				if rj.Source == "task" && rj.Name == taskRegName && !rj.LastRun.IsZero() {
					lastRun = rj.LastRun.Format("2006-01-02 15:04:05")
					break
				}
			}
		}

		taskViews = append(taskViews, SchedulerTaskView{
			ID:       task.ID,
			Name:     task.Name,
			URL:      task.Url,
			Schedule: task.Schedule,
			IsActive: task.IsActive == 1,
			Timeout:  task.TimeoutSeconds,
			LastRun:  lastRun,
		})
	}

	data := SchedulerListData{
		Jobs:  views,
		Tasks: taskViews,
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "scheduler.title"), schedulerBreadcrumbs(lang))
	viewData := convertSchedulerListViewData(data)
	renderTempl(w, r, adminviews.SchedulerListPage(pc, viewData))
}

// UpdateSchedule handles POST /admin/scheduler/update - updates job schedule.
func (h *SchedulerHandler) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionScheduler, redirectAdminScheduler) {
		return
	}

	if err := r.ParseForm(); err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, "Invalid form data")
		return
	}

	lang := middleware.GetAdminLang(r)
	source := strings.TrimSpace(r.FormValue("source"))
	name := strings.TrimSpace(r.FormValue("name"))
	newSchedule := strings.TrimSpace(r.FormValue("schedule"))

	if source == "" || name == "" || newSchedule == "" {
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.error_update"))
		return
	}

	if err := h.registry.UpdateSchedule(source, name, newSchedule); err != nil {
		slog.Error("failed to update schedule", "error", err, "source", source, "name", name)
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.error_update")+": "+err.Error())
		return
	}

	if h.eventService != nil {
		clientIP := middleware.GetClientIP(r)
		_ = h.eventService.LogSchedulerEvent(r.Context(), model.EventLevelInfo,
			"Schedule updated: "+source+":"+name+" -> "+newSchedule,
			middleware.GetUserIDPtr(r), clientIP, middleware.GetRequestURL(r), map[string]any{
				"source": source, "name": name, "schedule": newSchedule,
			})
	}

	slog.Info("scheduler job updated", "source", source, "name", name, "schedule", newSchedule, "updated_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.success_update"))
}

// ResetSchedule handles POST /admin/scheduler/reset - resets job to default schedule.
func (h *SchedulerHandler) ResetSchedule(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionScheduler, redirectAdminScheduler) {
		return
	}

	if err := r.ParseForm(); err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, "Invalid form data")
		return
	}

	lang := middleware.GetAdminLang(r)
	source := strings.TrimSpace(r.FormValue("source"))
	name := strings.TrimSpace(r.FormValue("name"))

	if source == "" || name == "" {
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.error_reset"))
		return
	}

	if err := h.registry.ResetSchedule(source, name); err != nil {
		slog.Error("failed to reset schedule", "error", err, "source", source, "name", name)
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.error_reset")+": "+err.Error())
		return
	}

	if h.eventService != nil {
		clientIP := middleware.GetClientIP(r)
		_ = h.eventService.LogSchedulerEvent(r.Context(), model.EventLevelInfo,
			"Schedule reset to default: "+source+":"+name,
			middleware.GetUserIDPtr(r), clientIP, middleware.GetRequestURL(r), map[string]any{
				"source": source, "name": name,
			})
	}

	slog.Info("scheduler job reset", "source", source, "name", name, "reset_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.success_reset"))
}

// TriggerNow handles POST /admin/scheduler/trigger/{source}/{name} - manually triggers a job.
func (h *SchedulerHandler) TriggerNow(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionScheduler, redirectAdminScheduler) {
		return
	}

	lang := middleware.GetAdminLang(r)
	source := chi.URLParam(r, "source")
	name := chi.URLParam(r, "name")

	if source == "" || name == "" {
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.error_trigger"))
		return
	}

	if err := h.registry.TriggerNow(source, name); err != nil {
		slog.Error("failed to trigger job", "error", err, "source", source, "name", name)
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.error_trigger")+": "+err.Error())
		return
	}

	if h.eventService != nil {
		clientIP := middleware.GetClientIP(r)
		_ = h.eventService.LogSchedulerEvent(r.Context(), model.EventLevelInfo,
			"Job manually triggered: "+source+":"+name,
			middleware.GetUserIDPtr(r), clientIP, middleware.GetRequestURL(r), map[string]any{
				"source": source, "name": name,
			})
	}

	slog.Info("scheduler job triggered", "source", source, "name", name, "triggered_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.success_trigger"))
}

// TaskForm handles GET /admin/scheduler/tasks/new and /admin/scheduler/tasks/{id}/edit.
func (h *SchedulerHandler) TaskForm(w http.ResponseWriter, r *http.Request) {
	lang := middleware.GetAdminLang(r)

	var task store.ScheduledTask
	isEdit := false

	idStr := chi.URLParam(r, "id")
	if idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			flashError(w, r, h.renderer, redirectAdminScheduler, "Invalid task ID")
			return
		}

		queries := store.New(h.db)
		task, err = queries.GetScheduledTask(r.Context(), id)
		if err != nil {
			flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.task_not_found"))
			return
		}
		isEdit = true
	}

	title := i18n.T(lang, "scheduler.new_task")
	if isEdit {
		title = i18n.T(lang, "scheduler.edit_task")
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, title, schedulerTaskFormBreadcrumbs(lang, title))
	viewData := convertSchedulerTaskFormViewData(task, isEdit)
	renderTempl(w, r, adminviews.SchedulerTaskFormPage(pc, viewData))
}

// TaskCreate handles POST /admin/scheduler/tasks - creates a new scheduled task.
func (h *SchedulerHandler) TaskCreate(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionScheduler, redirectAdminScheduler) {
		return
	}

	if err := r.ParseForm(); err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, "Invalid form data")
		return
	}

	lang := middleware.GetAdminLang(r)
	name := strings.TrimSpace(r.FormValue("name"))
	url := strings.TrimSpace(r.FormValue("url"))
	schedule := strings.TrimSpace(r.FormValue("schedule"))
	timeoutStr := strings.TrimSpace(r.FormValue("timeout"))

	if name == "" || url == "" || schedule == "" {
		flashError(w, r, h.renderer, redirectAdminScheduler+"/tasks/new", i18n.T(lang, "scheduler.error_task_required"))
		return
	}

	// Validate name length
	if len(name) < taskNameMinLen || len(name) > taskNameMaxLen {
		flashError(w, r, h.renderer, redirectAdminScheduler+"/tasks/new", i18n.T(lang, "scheduler.error_name_length"))
		return
	}

	// Validate URL length
	if len(url) > taskURLMaxLen {
		flashError(w, r, h.renderer, redirectAdminScheduler+"/tasks/new", i18n.T(lang, "scheduler.error_url_length"))
		return
	}

	// SSRF protection: validate URL before storing
	if err := scheduler.ValidateTaskURL(url); err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler+"/tasks/new",
			i18n.T(lang, "scheduler.error_invalid_url")+": "+err.Error())
		return
	}

	// Validate cron schedule syntax
	if cronErr := validateCronSchedule(schedule); cronErr != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler+"/tasks/new",
			i18n.T(lang, "scheduler.error_invalid_schedule")+": "+cronErr.Error())
		return
	}

	timeout := parseTaskTimeout(timeoutStr)

	now := time.Now()
	userID := middleware.GetUserID(r)

	queries := store.New(h.db)
	task, err := queries.CreateScheduledTask(r.Context(), store.CreateScheduledTaskParams{
		Name:           name,
		Url:            url,
		Schedule:       schedule,
		IsActive:       1,
		TimeoutSeconds: timeout,
		CreatedBy:      sql.NullInt64{Int64: userID, Valid: userID > 0},
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		slog.Error("failed to create scheduled task", "error", err)
		flashError(w, r, h.renderer, redirectAdminScheduler+"/tasks/new", i18n.T(lang, "scheduler.error_task_create"))
		return
	}

	// Schedule the task
	if h.taskExecutor != nil {
		if err := h.taskExecutor.AddTask(task); err != nil {
			slog.Error("failed to schedule new task", "error", err, "task_id", task.ID)
			flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.error_task_schedule")+": "+err.Error())
			return
		}
	}

	if h.eventService != nil {
		clientIP := middleware.GetClientIP(r)
		_ = h.eventService.LogSchedulerEvent(r.Context(), model.EventLevelInfo,
			"Scheduled task created: "+name,
			middleware.GetUserIDPtr(r), clientIP, middleware.GetRequestURL(r), map[string]any{
				"task_id": task.ID, "name": name, "url": url, "schedule": schedule,
			})
	}

	slog.Info("scheduled task created", "task_id", task.ID, "name", name, "url", url, "schedule", schedule)
	flashSuccess(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.task_created"))
}

// TaskUpdate handles POST /admin/scheduler/tasks/{id} - updates a scheduled task.
func (h *SchedulerHandler) TaskUpdate(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionScheduler, redirectAdminScheduler) {
		return
	}

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, "Invalid task ID")
		return
	}

	if parseErr := r.ParseForm(); parseErr != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, "Invalid form data")
		return
	}

	lang := middleware.GetAdminLang(r)
	name := strings.TrimSpace(r.FormValue("name"))
	taskURL := strings.TrimSpace(r.FormValue("url"))
	schedule := strings.TrimSpace(r.FormValue("schedule"))
	timeoutStr := strings.TrimSpace(r.FormValue("timeout"))

	if name == "" || taskURL == "" || schedule == "" {
		redirectURL := fmt.Sprintf("/admin/scheduler/tasks/%d/edit", id)
		flashError(w, r, h.renderer, redirectURL, i18n.T(lang, "scheduler.error_task_required"))
		return
	}

	editRedirect := fmt.Sprintf("/admin/scheduler/tasks/%d/edit", id)

	// Validate name length
	if len(name) < taskNameMinLen || len(name) > taskNameMaxLen {
		flashError(w, r, h.renderer, editRedirect, i18n.T(lang, "scheduler.error_name_length"))
		return
	}

	// Validate URL length
	if len(taskURL) > taskURLMaxLen {
		flashError(w, r, h.renderer, editRedirect, i18n.T(lang, "scheduler.error_url_length"))
		return
	}

	// SSRF protection: validate URL before storing
	if urlErr := scheduler.ValidateTaskURL(taskURL); urlErr != nil {
		flashError(w, r, h.renderer, editRedirect,
			i18n.T(lang, "scheduler.error_invalid_url")+": "+urlErr.Error())
		return
	}

	// Validate cron schedule syntax
	if cronErr := validateCronSchedule(schedule); cronErr != nil {
		flashError(w, r, h.renderer, editRedirect,
			i18n.T(lang, "scheduler.error_invalid_schedule")+": "+cronErr.Error())
		return
	}

	timeout := parseTaskTimeout(timeoutStr)

	queries := store.New(h.db)
	task, err := queries.UpdateScheduledTask(r.Context(), store.UpdateScheduledTaskParams{
		Name:           name,
		Url:            taskURL,
		Schedule:       schedule,
		TimeoutSeconds: timeout,
		UpdatedAt:      time.Now(),
		ID:             id,
	})
	if err != nil {
		slog.Error("failed to update scheduled task", "error", err, "task_id", id)
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.error_task_update"))
		return
	}

	// Reschedule if active
	if h.taskExecutor != nil && task.IsActive == 1 {
		if err := h.taskExecutor.RescheduleTask(task); err != nil {
			slog.Error("failed to reschedule task", "error", err, "task_id", task.ID)
		}
	}

	if h.eventService != nil {
		clientIP := middleware.GetClientIP(r)
		_ = h.eventService.LogSchedulerEvent(r.Context(), model.EventLevelInfo,
			"Scheduled task updated: "+name,
			middleware.GetUserIDPtr(r), clientIP, middleware.GetRequestURL(r), map[string]any{
				"task_id": id, "name": name, "url": taskURL, "schedule": schedule,
			})
	}

	slog.Info("scheduled task updated", "task_id", id, "name", name, "updated_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.task_updated"))
}

// TaskToggle handles POST /admin/scheduler/tasks/{id}/toggle - toggles task active state.
func (h *SchedulerHandler) TaskToggle(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionScheduler, redirectAdminScheduler) {
		return
	}

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, "Invalid task ID")
		return
	}

	lang := middleware.GetAdminLang(r)
	queries := store.New(h.db)

	// Get current state
	existing, err := queries.GetScheduledTask(r.Context(), id)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.task_not_found"))
		return
	}

	newActive := int64(1)
	if existing.IsActive == 1 {
		newActive = 0
	}

	task, err := queries.ToggleScheduledTask(r.Context(), store.ToggleScheduledTaskParams{
		IsActive:  newActive,
		UpdatedAt: time.Now(),
		ID:        id,
	})
	if err != nil {
		slog.Error("failed to toggle scheduled task", "error", err, "task_id", id)
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.error_task_toggle"))
		return
	}

	// Add or remove from cron
	if h.taskExecutor != nil {
		if task.IsActive == 1 {
			if addErr := h.taskExecutor.AddTask(task); addErr != nil {
				slog.Error("failed to schedule toggled task", "error", addErr, "task_id", task.ID)
			}
		} else {
			h.taskExecutor.RemoveTask(task.ID)
		}
	}

	action := "disabled"
	if task.IsActive == 1 {
		action = "enabled"
	}

	if h.eventService != nil {
		clientIP := middleware.GetClientIP(r)
		_ = h.eventService.LogSchedulerEvent(r.Context(), model.EventLevelInfo,
			"Scheduled task "+action+": "+task.Name,
			middleware.GetUserIDPtr(r), clientIP, middleware.GetRequestURL(r), map[string]any{
				"task_id": id, "name": task.Name, "action": action,
			})
	}

	slog.Info("scheduled task toggled", "task_id", id, "active", task.IsActive, "toggled_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.task_toggled"))
}

// TaskDelete handles POST /admin/scheduler/tasks/{id}/delete - deletes a scheduled task.
func (h *SchedulerHandler) TaskDelete(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionScheduler, redirectAdminScheduler) {
		return
	}

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, "Invalid task ID")
		return
	}

	lang := middleware.GetAdminLang(r)
	queries := store.New(h.db)

	// Get task name for logging
	task, err := queries.GetScheduledTask(r.Context(), id)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.task_not_found"))
		return
	}

	// Remove from cron first
	if h.taskExecutor != nil {
		h.taskExecutor.RemoveTask(id)
	}

	// Delete from DB (cascade deletes runs)
	if err := queries.DeleteScheduledTask(r.Context(), id); err != nil {
		slog.Error("failed to delete scheduled task", "error", err, "task_id", id)
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.error_task_delete"))
		return
	}

	if h.eventService != nil {
		clientIP := middleware.GetClientIP(r)
		_ = h.eventService.LogSchedulerEvent(r.Context(), model.EventLevelInfo,
			"Scheduled task deleted: "+task.Name,
			middleware.GetUserIDPtr(r), clientIP, middleware.GetRequestURL(r), map[string]any{
				"task_id": id, "name": task.Name,
			})
	}

	slog.Info("scheduled task deleted", "task_id", id, "name", task.Name, "deleted_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.task_deleted"))
}

// TaskRuns handles GET /admin/scheduler/tasks/{id}/runs - shows run history.
func (h *SchedulerHandler) TaskRuns(w http.ResponseWriter, r *http.Request) {
	lang := middleware.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, "Invalid task ID")
		return
	}

	queries := store.New(h.db)
	task, err := queries.GetScheduledTask(r.Context(), id)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.task_not_found"))
		return
	}

	page := ParsePageParam(r)
	totalCount, err := queries.CountScheduledTaskRuns(r.Context(), id)
	if err != nil {
		logAndInternalError(w, "failed to count task runs", "error", err)
		return
	}

	page, _ = NormalizePagination(page, int(totalCount), taskRunsPerPage)
	offset := int64((page - 1) * taskRunsPerPage)

	runs, err := queries.ListScheduledTaskRuns(r.Context(), store.ListScheduledTaskRunsParams{
		TaskID: id,
		Limit:  taskRunsPerPage,
		Offset: offset,
	})
	if err != nil {
		logAndInternalError(w, "failed to list task runs", "error", err)
		return
	}

	pagination := BuildAdminPagination(page, int(totalCount), taskRunsPerPage, fmt.Sprintf("/admin/scheduler/tasks/%d/runs", id), r.URL.Query())

	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "scheduler.task_runs"), schedulerTaskRunsBreadcrumbs(lang, task.Name))
	viewData := convertSchedulerTaskRunsViewData(task, runs, totalCount, pagination)
	renderTempl(w, r, adminviews.SchedulerTaskRunsPage(pc, viewData))
}

// TaskTrigger handles POST /admin/scheduler/tasks/{id}/trigger - manually triggers a task.
func (h *SchedulerHandler) TaskTrigger(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionScheduler, redirectAdminScheduler) {
		return
	}

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, "Invalid task ID")
		return
	}

	lang := middleware.GetAdminLang(r)

	if h.taskExecutor == nil {
		flashError(w, r, h.renderer, redirectAdminScheduler, "Task executor not available")
		return
	}

	if err := h.taskExecutor.TriggerTask(id); err != nil {
		slog.Error("failed to trigger task", "error", err, "task_id", id)
		flashError(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.error_task_trigger")+": "+err.Error())
		return
	}

	if h.eventService != nil {
		clientIP := middleware.GetClientIP(r)
		_ = h.eventService.LogSchedulerEvent(r.Context(), model.EventLevelInfo,
			fmt.Sprintf("Scheduled task manually triggered: task_%d", id),
			middleware.GetUserIDPtr(r), clientIP, middleware.GetRequestURL(r), map[string]any{
				"task_id": id,
			})
	}

	slog.Info("scheduled task triggered", "task_id", id, "triggered_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, redirectAdminScheduler, i18n.T(lang, "scheduler.task_triggered"))
}
