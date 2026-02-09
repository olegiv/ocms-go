// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	// hourlyLookback is how far back hourly aggregation looks for raw data.
	hourlyLookback = 48 * time.Hour

	// dailyLookbackDays is how far back daily aggregation looks.
	dailyLookbackDays = 7

	// startupDelay is the time to wait before running startup catch-up,
	// allowing the server to finish initialization.
	startupDelay = 10 * time.Second

	// datetimeFormat is the SQLite-compatible datetime format (no timezone).
	datetimeFormat = "2006-01-02 15:04:05"
)

// addCronJob registers a cron job with timeout, error logging, and registry tracking.
func (m *Module) addCronJob(defaultSchedule string, timeout time.Duration, jobFunc func(context.Context) error, jobName, description, errMsg string) {
	// Check for schedule override in the registry
	schedule := defaultSchedule
	if m.ctx.SchedulerRegistry != nil {
		schedule = m.ctx.SchedulerRegistry.GetEffectiveSchedule("analytics_int", jobName, defaultSchedule)
	}

	cronFunc := func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := jobFunc(ctx); err != nil {
			m.ctx.Logger.Error(errMsg, "error", err)
		}
	}

	entryID, err := m.cron.AddFunc(schedule, cronFunc)
	if err != nil {
		m.ctx.Logger.Error("failed to add cron job", "job", jobName, "schedule", schedule, "error", err)
		return
	}

	// Register with scheduler registry for admin UI visibility
	if m.ctx.SchedulerRegistry != nil {
		triggerFunc := func() error {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			return jobFunc(ctx)
		}
		m.ctx.SchedulerRegistry.Register(
			"analytics_int", jobName, description, defaultSchedule,
			m.cron, entryID, cronFunc, triggerFunc,
		)
	}

	m.ctx.Logger.Info("cron job scheduled", "job", jobName, "schedule", schedule)
}

// StartAggregator starts background aggregation jobs.
//
// Uses interval-based scheduling (@every) instead of fixed cron times
// (e.g., "5 * * * *") to ensure jobs run on auto-stop platforms like
// Fly.io where the machine may not be running at specific clock times.
// With fixed schedules, a machine that starts at :08 and stops at :55
// would always miss the :05 hourly job.
func (m *Module) StartAggregator() {
	m.cron = cron.New()

	// Interval-based scheduling: fires N time after Start(), then every N
	m.addCronJob("@every 1h", 5*time.Minute, m.aggregateHourly,
		"hourly_aggregation", "Aggregate raw views into hourly stats", "hourly aggregation failed")
	m.addCronJob("@every 6h", 10*time.Minute, m.aggregateDaily,
		"daily_aggregation", "Aggregate hourly stats into daily summaries", "daily aggregation failed")
	m.addCronJob("@every 24h", 5*time.Minute, m.cleanupOldRawData,
		"raw_data_cleanup", "Delete raw page view data older than 7 days", "raw data cleanup failed")
	m.addCronJob("@every 168h", 30*time.Minute, m.cleanupExpiredData,
		"expired_data_cleanup", "Delete aggregate data older than retention period", "expired data cleanup failed")

	m.cron.Start()
	m.ctx.Logger.Info("Page Analytics aggregator started")

	// Run catch-up aggregation on startup in background
	go m.runStartupCatchUp()
}

// runStartupCatchUp runs aggregation immediately after startup to process
// any data accumulated while the machine was stopped (e.g., Fly.io auto-stop).
func (m *Module) runStartupCatchUp() {
	// Short delay to let the system finish initialization
	time.Sleep(startupDelay)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	m.ctx.Logger.Info("running startup aggregation catch-up")

	// Normalize any old hourly data that has timezone suffixes from
	// the previous Go time.Time binding format (e.g., "2024-01-15 14:00:00+00:00").
	// These will be re-created in clean "YYYY-MM-DD HH:MM:SS" format.
	if result, err := m.ctx.DB.ExecContext(ctx,
		"DELETE FROM page_analytics_hourly WHERE LENGTH(hour_start) > 19"); err != nil {
		m.ctx.Logger.Warn("failed to normalize hourly data", "error", err)
	} else if affected, _ := result.RowsAffected(); affected > 0 {
		m.ctx.Logger.Info("normalized hourly data format", "removed_old_format", affected)
	}

	if err := m.aggregateHourly(ctx); err != nil {
		m.ctx.Logger.Error("startup hourly aggregation failed", "error", err)
	}

	if err := m.aggregateDaily(ctx); err != nil {
		m.ctx.Logger.Error("startup daily aggregation failed", "error", err)
	}

	m.ctx.Logger.Info("startup aggregation catch-up complete")
}

// aggregateHourly processes raw views into hourly stats.
// Aggregates ALL hours within the lookback window (48h) to catch up
// on any periods missed while the machine was stopped.
func (m *Module) aggregateHourly(ctx context.Context) error {
	now := time.Now()
	windowStart := now.Add(-hourlyLookback)
	windowStartStr := windowStart.Format(datetimeFormat)
	// Exclude the current (incomplete) hour
	currentHourStart := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
	currentHourStr := currentHourStart.Format(datetimeFormat)

	m.ctx.Logger.Info("aggregating hourly stats", "from", windowStartStr, "to", currentHourStr)

	// Single query aggregates all hours in the window using STRFTIME
	// to bucket raw views by hour. INSERT OR REPLACE is safe for re-runs.
	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT OR REPLACE INTO page_analytics_hourly (hour_start, path, views, unique_visitors)
		SELECT
			STRFTIME('%Y-%m-%d %H:00:00', created_at) as hour_start,
			path,
			COUNT(*) as views,
			COUNT(DISTINCT visitor_hash) as unique_visitors
		FROM page_analytics_views
		WHERE created_at >= ? AND created_at < ?
		GROUP BY STRFTIME('%Y-%m-%d %H:00:00', created_at), path
	`, windowStartStr, currentHourStr)
	if err != nil {
		return err
	}

	m.ctx.Logger.Info("hourly aggregation complete")
	return nil
}

// aggregateDaily processes hourly data into daily summaries.
// Aggregates ALL days within the lookback window (7d) to catch up
// on any periods missed while the machine was stopped.
func (m *Module) aggregateDaily(ctx context.Context) error {
	now := time.Now()
	windowStartStr := now.AddDate(0, 0, -dailyLookbackDays).Format(dateFormat)
	todayStr := now.Format(dateFormat)

	m.ctx.Logger.Info("aggregating daily stats", "from", windowStartStr, "to", todayStr)

	// Aggregate page views from hourly into daily (single query for all days)
	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT OR REPLACE INTO page_analytics_daily (date, path, views, unique_visitors, bounces)
		SELECT
			DATE(hour_start) as date,
			path,
			SUM(views),
			SUM(unique_visitors),
			0
		FROM page_analytics_hourly
		WHERE DATE(hour_start) >= ? AND DATE(hour_start) < ?
		GROUP BY DATE(hour_start), path
	`, windowStartStr, todayStr)
	if err != nil {
		return err
	}

	// Get distinct dates from raw views to process referrers/tech/geo/bounce
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT DISTINCT DATE(created_at) as date
		FROM page_analytics_views
		WHERE DATE(created_at) >= ? AND DATE(created_at) < ?
		ORDER BY date
	`, windowStartStr, todayStr)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	var dates []string
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			return err
		}
		dates = append(dates, date)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Process referrers, tech, geo, and bounce rates for each date
	for _, dateStr := range dates {
		if err := m.aggregateReferrers(ctx, dateStr); err != nil {
			m.ctx.Logger.Warn("referrer aggregation failed", "date", dateStr, "error", err)
		}
		if err := m.aggregateTechStats(ctx, dateStr); err != nil {
			m.ctx.Logger.Warn("tech stats aggregation failed", "date", dateStr, "error", err)
		}
		if err := m.aggregateGeoStats(ctx, dateStr); err != nil {
			m.ctx.Logger.Warn("geo stats aggregation failed", "date", dateStr, "error", err)
		}
		if err := m.calculateBounceRates(ctx, dateStr); err != nil {
			m.ctx.Logger.Warn("bounce rate calculation failed", "date", dateStr, "error", err)
		}
	}

	m.ctx.Logger.Info("daily aggregation complete", "dates_processed", len(dates))
	return nil
}

// aggregateReferrers aggregates referrer data for a date.
func (m *Module) aggregateReferrers(ctx context.Context, dateStr string) error {
	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT OR REPLACE INTO page_analytics_referrers (date, referrer_domain, views, unique_visitors)
		SELECT
			?,
			referrer_domain,
			COUNT(*) as views,
			COUNT(DISTINCT visitor_hash) as unique_visitors
		FROM page_analytics_views
		WHERE DATE(created_at) = ? AND referrer_domain != ''
		GROUP BY referrer_domain
	`, dateStr, dateStr)
	return err
}

// aggregateTechStats aggregates browser/OS/device data for a date.
func (m *Module) aggregateTechStats(ctx context.Context, dateStr string) error {
	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT OR REPLACE INTO page_analytics_tech (date, browser, os, device_type, views)
		SELECT
			?,
			browser,
			os,
			device_type,
			COUNT(*) as views
		FROM page_analytics_views
		WHERE DATE(created_at) = ?
		GROUP BY browser, os, device_type
	`, dateStr, dateStr)
	return err
}

// aggregateGeoStats aggregates geographic data for a date.
func (m *Module) aggregateGeoStats(ctx context.Context, dateStr string) error {
	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT OR REPLACE INTO page_analytics_geo (date, country_code, views, unique_visitors)
		SELECT
			?,
			CASE WHEN country_code = '' THEN 'Unknown' ELSE country_code END,
			COUNT(*) as views,
			COUNT(DISTINCT visitor_hash) as unique_visitors
		FROM page_analytics_views
		WHERE DATE(created_at) = ?
		GROUP BY country_code
	`, dateStr, dateStr)
	return err
}

// calculateBounceRates calculates bounce rates for pages.
// A bounce is when a visitor views only one page in their session.
func (m *Module) calculateBounceRates(ctx context.Context, dateStr string) error {
	// Count sessions with only one page view
	_, err := m.ctx.DB.ExecContext(ctx, `
		UPDATE page_analytics_daily
		SET bounces = (
			SELECT COUNT(*)
			FROM (
				SELECT session_hash
				FROM page_analytics_views
				WHERE DATE(created_at) = ? AND path = page_analytics_daily.path
				GROUP BY session_hash
				HAVING COUNT(*) = 1
			)
		)
		WHERE date = ?
	`, dateStr, dateStr)
	return err
}

// cleanupOldRawData deletes raw page view data older than 7 days.
// We keep hourly/daily aggregates for historical analysis.
func (m *Module) cleanupOldRawData(ctx context.Context) error {
	// Use string-formatted time to avoid timezone suffix mismatch
	cutoffStr := time.Now().AddDate(0, 0, -7).Format(datetimeFormat)

	result, err := m.ctx.DB.ExecContext(ctx,
		"DELETE FROM page_analytics_views WHERE created_at < ?", cutoffStr)
	if err != nil {
		return err
	}

	if affected, _ := result.RowsAffected(); affected > 0 {
		m.ctx.Logger.Info("cleaned up old raw analytics data", "deleted", affected)
	}
	return nil
}

// cleanupExpiredData deletes all aggregate data older than retention period.
func (m *Module) cleanupExpiredData(ctx context.Context) error {
	cutoff := time.Now().AddDate(0, 0, -m.settings.RetentionDays)
	cutoffDateStr := cutoff.Format(dateFormat)
	cutoffTimeStr := cutoff.Format(datetimeFormat)

	m.ctx.Logger.Info("cleaning up expired analytics data", "older_than", cutoffDateStr)

	// Tables with date-based cleanup (use string values to avoid timezone issues)
	tables := []struct {
		name   string
		column string
		value  string
	}{
		{"page_analytics_hourly", "hour_start", cutoffTimeStr},
		{"page_analytics_daily", "date", cutoffDateStr},
		{"page_analytics_referrers", "date", cutoffDateStr},
		{"page_analytics_tech", "date", cutoffDateStr},
		{"page_analytics_geo", "date", cutoffDateStr},
	}

	for _, t := range tables {
		if _, err := m.ctx.DB.ExecContext(ctx,
			"DELETE FROM "+t.name+" WHERE "+t.column+" < ?", t.value); err != nil {
			return err
		}
	}

	m.ctx.Logger.Info("expired analytics data cleanup complete")
	return nil
}

// RunAggregationNow runs all aggregation jobs immediately (for testing).
func (m *Module) RunAggregationNow() error {
	ctx := context.Background()

	if err := m.aggregateHourly(ctx); err != nil {
		return err
	}

	if err := m.aggregateDaily(ctx); err != nil {
		return err
	}

	return nil
}

// RunFullAggregation aggregates all historical raw data into daily stats.
// This backfills page_analytics_daily from page_analytics_views for all past dates.
func (m *Module) RunFullAggregation(ctx context.Context) (int, error) {
	m.ctx.Logger.Info("starting full aggregation of all historical data")

	// Use local date to match created_at storage (which uses local time)
	todayStr := time.Now().Format(dateFormat)

	// Get all distinct dates from raw views (excluding today)
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT DISTINCT DATE(created_at) as date
		FROM page_analytics_views
		WHERE DATE(created_at) < ?
		ORDER BY date
	`, todayStr)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	var dates []string
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			return 0, err
		}
		dates = append(dates, date)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	if len(dates) == 0 {
		m.ctx.Logger.Info("no historical data to aggregate")
		return 0, nil
	}

	m.ctx.Logger.Info("found dates to aggregate", "count", len(dates))

	// Aggregate daily stats directly from raw views (bypass hourly)
	_, err = m.ctx.DB.ExecContext(ctx, `
		INSERT OR REPLACE INTO page_analytics_daily (date, path, views, unique_visitors, bounces)
		SELECT
			DATE(created_at) as date,
			path,
			COUNT(*) as views,
			COUNT(DISTINCT visitor_hash) as unique_visitors,
			0 as bounces
		FROM page_analytics_views
		WHERE DATE(created_at) < ?
		GROUP BY DATE(created_at), path
	`, todayStr)
	if err != nil {
		return 0, err
	}

	// Aggregate other stats for each date
	for _, dateStr := range dates {
		if err := m.aggregateReferrers(ctx, dateStr); err != nil {
			m.ctx.Logger.Warn("referrer aggregation failed", "date", dateStr, "error", err)
		}
		if err := m.aggregateTechStats(ctx, dateStr); err != nil {
			m.ctx.Logger.Warn("tech stats aggregation failed", "date", dateStr, "error", err)
		}
		if err := m.aggregateGeoStats(ctx, dateStr); err != nil {
			m.ctx.Logger.Warn("geo stats aggregation failed", "date", dateStr, "error", err)
		}
		if err := m.calculateBounceRates(ctx, dateStr); err != nil {
			m.ctx.Logger.Warn("bounce rate calculation failed", "date", dateStr, "error", err)
		}
	}

	m.ctx.Logger.Info("full aggregation complete", "dates_processed", len(dates))
	return len(dates), nil
}
