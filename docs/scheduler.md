# Scheduler

oCMS includes a built-in cron scheduler that manages periodic tasks across the core system and modules. The scheduler admin UI provides a centralized interface to view, manage, and manually trigger scheduled jobs.

## Overview

The scheduler manages three types of jobs:

- **Core jobs** — Built-in system tasks (e.g., published page scheduling)
- **Module jobs** — Tasks registered by installed modules (e.g., analytics reporting, data cleanup)
- **Custom tasks** — Scheduled HTTP GET requests that administrators create in the admin UI (see [Custom Tasks](#custom-tasks))

Each job has:
- A default schedule (cron expression)
- An optional override schedule (persisted to `scheduler_overrides` table)
- Timing information (last run, next run)
- Optional manual trigger capability

## How Scheduled Jobs Work

### Core Scheduler

The core scheduler manages a single `*cron.Cron` instance that runs continuously in the background (the custom-task executor shares it). Jobs are registered at application startup:

```go
// Core job: runs on schedule
entryID, _ := cronInst.AddFunc(effectiveSchedule, jobFunc)
registry.Register("core", "scheduled_pages", "Publish pages scheduled for publishing", "* * * * *", cronInst, entryID, jobFunc, triggerFunc)
```

Built-in jobs:

| Source | Name | Default schedule | Description |
|--------|------|------------------|-------------|
| `core` | `scheduled_pages` | `* * * * *` (every minute) | Publish pages scheduled for publishing; can be triggered manually |
| `core` | `demo_reset` | `0 1 * * *` (daily at 01:00 server time, UTC on Fly.io) | Only in demo mode: reset the demo data and restart; no manual trigger |
| `task` | `cleanup` | `0 3 * * *` (daily at 03:00 server time) | Delete custom-task runs older than 30 days; can be triggered manually |

### Module Jobs

Modules register jobs through `module.Context.SchedulerRegistry`. A module keeps its own `*cron.Cron`, reads any saved override, adds the job, and registers it so it shows up in the admin UI (see `modules/analytics_int` for a working example):

```go
schedule := defaultSchedule
if m.ctx.SchedulerRegistry != nil {
    schedule = m.ctx.SchedulerRegistry.GetEffectiveSchedule("analytics_int", jobName, defaultSchedule)
}
entryID, err := m.cron.AddFunc(schedule, cronFunc)
// ...
m.ctx.SchedulerRegistry.Register("analytics_int", jobName, description, defaultSchedule,
    m.cron, entryID, cronFunc, triggerFunc) // triggerFunc may be nil
```

Jobs are registered from the module's `Init`, which runs only for active modules. A module that passes a nil `triggerFunc` gets no **Run Now** button. The registry applies schedule edits by replacing the job's entry in the module's cron instance.

## Viewing Jobs

Navigate to `/admin/scheduler` (admin only) to view all scheduled jobs in a table:

| Column | Description |
|--------|-------------|
| **Job** | Job description and internal name |
| **Source** | Origin (`core` or module name) |
| **Schedule** | Current cron expression (or override) |
| **Last Run** | When the job last executed |
| **Next Run** | When the job will execute next |
| **Actions** | Edit, reset, or trigger buttons |

The **Source** column uses color-coded badges:
- **Indigo** — Core system job
- **Green** — Custom task (`task`)
- **Cyan** — Module job

## Editing Job Schedules

Click the **Edit** button to change a job's cron schedule:

1. A modal dialog appears with the job name and current schedule
2. Enter a new cron expression (standard 5-field or `@every` interval format)
3. Click **Save** — The new schedule takes effect immediately

The system validates the cron expression before applying it. An invalid expression is rejected and the current schedule stays in place.

### Cron Expression Format

oCMS uses standard cron syntax with support for shorthand intervals:

| Expression | Meaning |
|------------|---------|
| `* * * * *` | Every minute |
| `0 * * * *` | Every hour at :00 |
| `0 1 * * *` | Daily at 01:00 |
| `0 1 * * 0` | Weekly (Sundays at 01:00) |
| `0 0 1 * *` | Monthly (1st of each month) |
| `@every 1h` | Every 1 hour (interval) |
| `@every 6h` | Every 6 hours (interval) |
| `@every 24h` | Every 24 hours (interval) |
| `@every 30m` | Every 30 minutes (interval) |

**5-Field Cron Fields:**
1. Minute (0-59)
2. Hour (0-23)
3. Day of Month (1-31)
4. Month (1-12)
5. Day of Week (0-6, 0=Sunday)

Use `*` to match any value in a field.

### Examples

- Publish scheduled pages every 5 minutes: `*/5 * * * *`
- Run analytics export daily at 2:00 AM: `0 2 * * *`
- Sync data every Sunday at noon: `0 12 * * 0`
- Cleanup old logs every 6 hours: `@every 6h`

## Manually Triggering Jobs

If a job supports manual triggers, a **Run Now** button appears in the actions column. Click it to execute the job immediately (outside its normal schedule).

The system confirms your action before executing.

Manual triggers:
- Execute immediately, bypassing the cron schedule
- Log the trigger event with the user who initiated it
- Do not reset the job's schedule or timing counters

Not all jobs support manual triggers. Jobs that require specific timing conditions (e.g., "publish at exact moment") may not allow manual execution.

## Resetting to Default Schedule

If a job's schedule is overridden, a **Reset** button appears. Click it to:

1. Remove the override from the database
2. Restore the default schedule immediately
3. Update the cron job with the default timing

After reset, the job follows its original schedule and the **Reset** button disappears.

## Schedule Overrides

When you edit a job's schedule, the override is persisted to the `scheduler_overrides` table:

```sql
CREATE TABLE scheduler_overrides (
    source TEXT NOT NULL,
    name TEXT NOT NULL,
    override_schedule TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (source, name)
)
```

**Key points:**

- Overrides survive application restarts
- Each job has at most one override (insert/replace behavior)
- `updated_at` tracks when the override was last changed
- Overrides are deleted when the schedule is reset

On startup, the scheduler loads all overrides and applies them to jobs automatically, so your custom schedules persist across deployments.

### Database Management

The `scheduler_overrides` table is created by goose migration `20260213100000_create_scheduler_overrides.sql`. All override queries use SQLC-generated type-safe code. A runtime `CREATE TABLE IF NOT EXISTS` remains as a safety net for backwards compatibility with databases that predate the migration.

## Custom Tasks

Below the jobs table, the **Custom Tasks** section lets administrators schedule an HTTP GET request to a URL (**New Task**):

| Field | Rules |
|-------|-------|
| Name | 3-100 characters |
| URL | At most 2048 characters; `http` or `https` (only `https` when `OCMS_REQUIRE_HTTPS_OUTBOUND` is on, which is the default in production) |
| Schedule | Cron expression or `@every` interval |
| Timeout | 1-300 seconds (default 30) |

Each task can be edited, enabled or disabled, run immediately (rate limited to one manual run per 10 seconds per task), deleted together with its run history, and its runs inspected (status, HTTP status code, duration, error). Active tasks also appear in the jobs table with source `task` and name `task_<id>`.

Safety rules:

- URLs that resolve to private, loopback, link-local or other reserved addresses, `localhost` and cloud metadata hosts are rejected. The check runs when the task is saved, again before every run, and on every redirect (at most 10).
- The response body is not stored: a run records only the content type and the number of bytes read (the read is capped at 4096 bytes).
- Runs older than 30 days are deleted daily by the `task:cleanup` job.

## Demo Mode Restrictions

When `OCMS_DEMO_MODE=true`, scheduler modifications are disabled to prevent demo users from breaking the demo experience:

- ✓ View all jobs and schedules
- ✗ Edit job schedules
- ✗ Reset schedules
- ✗ Trigger jobs manually
- ✗ Create, edit, enable/disable, delete or run custom tasks

Attempting to modify schedules in demo mode shows an error and redirects to the scheduler page. This prevents accidental or intentional changes to demo job timing.

## Audit Logging

Scheduler events are logged for audit trails:

- **Schedule updated** — Recorded when a job's cron expression is changed
- **Schedule reset** — Recorded when an override is removed
- **Job manually triggered** — Recorded when a job is executed manually
- **Scheduled task created / updated / enabled / disabled / deleted / manually triggered** — Recorded for custom tasks

Each event includes:
- The user who made the change
- The job source and name
- The new schedule (if applicable)
- Client IP address
- Request URL

View audit logs at `/admin/events` to review scheduler changes.

## Common Use Cases

### Check for Scheduled Pages Less Often

By default, pages scheduled for publishing are picked up every minute (`core:scheduled_pages`). If a minute-level check is more than you need, change the schedule:

**Current:** `* * * * *` (every minute)
**New:** `*/5 * * * *` (every 5 minutes)

### Disable a Job

To prevent a job from running, set its schedule to an impossible expression:

```
0 0 31 2 *  (never - Feb 31 doesn't exist)
```

For a custom task, use **Disable** instead.

### Shift Job Timing

If a job conflicts with your backup window, shift it:

**Current:** `0 2 * * *` (daily at 2:00 AM)
**New:** `0 4 * * *` (daily at 4:00 AM)

## Troubleshooting

### Job Never Runs

1. Check the cron expression is valid
2. Verify the job's source module is active (for module jobs)
3. Ensure the application is running (jobs run in-process)
4. Check application logs for execution errors

### Manual Trigger Returns Error

- Job may not support manual triggers
- Check application logs for the specific error
- Verify the job's source module is loaded

### Override Not Persisting

Overrides require the `scheduler_overrides` table to exist. On startup, the scheduler automatically creates this table if missing. If overrides still don't persist:

1. Verify database write permissions
2. Check application logs for database errors
3. Restart the application to re-apply overrides from the database
