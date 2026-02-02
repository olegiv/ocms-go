// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"
)

// addCronJob registers a cron job with timeout and error logging.
func (m *Module) addCronJob(schedule string, timeout time.Duration, jobFunc func(context.Context) error, errMsg string) {
	_, _ = m.cron.AddFunc(schedule, func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := jobFunc(ctx); err != nil {
			m.ctx.Logger.Error(errMsg, "error", err)
		}
	})
}

// StartAggregator starts background aggregation jobs.
func (m *Module) StartAggregator() {
	m.cron = cron.New()

	// Every hour at minute 5: aggregate raw views into hourly stats
	m.addCronJob("5 * * * *", 5*time.Minute, m.aggregateHourly, "hourly aggregation failed")

	// Daily at 00:15: aggregate hourly into daily stats
	m.addCronJob("15 0 * * *", 10*time.Minute, m.aggregateDaily, "daily aggregation failed")

	// Daily at 00:30: cleanup old raw data (keep 7 days)
	m.addCronJob("30 0 * * *", 5*time.Minute, m.cleanupOldRawData, "raw data cleanup failed")

	// Monthly on 1st at 01:00: cleanup expired aggregate data
	m.addCronJob("0 1 1 * *", 30*time.Minute, m.cleanupExpiredData, "expired data cleanup failed")

	m.cron.Start()
	m.ctx.Logger.Debug("Page Analytics aggregator started")
}

// aggregateHourly processes raw views into hourly stats.
func (m *Module) aggregateHourly(ctx context.Context) error {
	// Get the hour to aggregate (previous hour)
	now := time.Now()
	hourStart := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()-1, 0, 0, 0, now.Location())
	hourEnd := hourStart.Add(time.Hour)

	m.ctx.Logger.Debug("aggregating hourly stats", "hour", hourStart.Format("2006-01-02 15:00"))

	// Aggregate by path
	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT OR REPLACE INTO page_analytics_hourly (hour_start, path, views, unique_visitors)
		SELECT
			?,
			path,
			COUNT(*) as views,
			COUNT(DISTINCT visitor_hash) as unique_visitors
		FROM page_analytics_views
		WHERE created_at >= ? AND created_at < ?
		GROUP BY path
	`, hourStart, hourStart, hourEnd)

	if err != nil {
		return err
	}

	m.ctx.Logger.Debug("hourly aggregation complete", "hour", hourStart.Format("2006-01-02 15:00"))
	return nil
}

// aggregateDaily processes data into daily summaries.
func (m *Module) aggregateDaily(ctx context.Context) error {
	yesterday := time.Now().AddDate(0, 0, -1)
	dateStr := yesterday.Format("2006-01-02")

	m.ctx.Logger.Debug("aggregating daily stats", "date", dateStr)

	// Aggregate page views into daily stats
	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT OR REPLACE INTO page_analytics_daily (date, path, views, unique_visitors, bounces)
		SELECT
			?,
			path,
			SUM(views),
			SUM(unique_visitors),
			0
		FROM page_analytics_hourly
		WHERE DATE(hour_start) = ?
		GROUP BY path
	`, dateStr, dateStr)
	if err != nil {
		return err
	}

	// Aggregate referrers
	if err := m.aggregateReferrers(ctx, dateStr); err != nil {
		m.ctx.Logger.Warn("referrer aggregation failed", "error", err)
	}

	// Aggregate tech stats (browser/OS/device)
	if err := m.aggregateTechStats(ctx, dateStr); err != nil {
		m.ctx.Logger.Warn("tech stats aggregation failed", "error", err)
	}

	// Aggregate geo stats
	if err := m.aggregateGeoStats(ctx, dateStr); err != nil {
		m.ctx.Logger.Warn("geo stats aggregation failed", "error", err)
	}

	// Calculate bounce rates
	if err := m.calculateBounceRates(ctx, dateStr); err != nil {
		m.ctx.Logger.Warn("bounce rate calculation failed", "error", err)
	}

	m.ctx.Logger.Debug("daily aggregation complete", "date", dateStr)
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
	cutoff := time.Now().AddDate(0, 0, -7)

	result, err := m.ctx.DB.ExecContext(ctx,
		"DELETE FROM page_analytics_views WHERE created_at < ?", cutoff)
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
	cutoffStr := cutoff.Format("2006-01-02")

	m.ctx.Logger.Info("cleaning up expired analytics data", "older_than", cutoffStr)

	// Tables with date-based cleanup
	tables := []struct {
		name   string
		column string
		value  any
	}{
		{"page_analytics_hourly", "hour_start", cutoff},
		{"page_analytics_daily", "date", cutoffStr},
		{"page_analytics_referrers", "date", cutoffStr},
		{"page_analytics_tech", "date", cutoffStr},
		{"page_analytics_geo", "date", cutoffStr},
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

	dateStr := time.Now().Format("2006-01-02")
	if err := m.aggregateReferrers(ctx, dateStr); err != nil {
		return err
	}
	if err := m.aggregateTechStats(ctx, dateStr); err != nil {
		return err
	}
	if err := m.aggregateGeoStats(ctx, dateStr); err != nil {
		return err
	}

	return nil
}

// RunFullAggregation aggregates all historical raw data into daily stats.
// This backfills page_analytics_daily from page_analytics_views for all past dates.
func (m *Module) RunFullAggregation(ctx context.Context) (int, error) {
	m.ctx.Logger.Info("starting full aggregation of all historical data")

	// Get all distinct dates from raw views (excluding today)
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT DISTINCT DATE(created_at) as date
		FROM page_analytics_views
		WHERE DATE(created_at) < DATE('now')
		ORDER BY date
	`)
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
		WHERE DATE(created_at) < DATE('now')
		GROUP BY DATE(created_at), path
	`)
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
