package page_analytics

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"
)

// StartAggregator starts background aggregation jobs.
func (m *Module) StartAggregator() {
	m.cron = cron.New()

	// Every hour at minute 5: aggregate raw views into hourly stats
	_, _ = m.cron.AddFunc("5 * * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := m.aggregateHourly(ctx); err != nil {
			m.ctx.Logger.Error("hourly aggregation failed", "error", err)
		}
	})

	// Daily at 00:15: aggregate hourly into daily stats
	_, _ = m.cron.AddFunc("15 0 * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := m.aggregateDaily(ctx); err != nil {
			m.ctx.Logger.Error("daily aggregation failed", "error", err)
		}
	})

	// Daily at 00:30: cleanup old raw data (keep 7 days)
	_, _ = m.cron.AddFunc("30 0 * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := m.cleanupOldRawData(ctx); err != nil {
			m.ctx.Logger.Error("raw data cleanup failed", "error", err)
		}
	})

	// Monthly on 1st at 01:00: cleanup expired aggregate data
	_, _ = m.cron.AddFunc("0 1 1 * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := m.cleanupExpiredData(ctx); err != nil {
			m.ctx.Logger.Error("expired data cleanup failed", "error", err)
		}
	})

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

	// Delete from hourly table
	if _, err := m.ctx.DB.ExecContext(ctx,
		"DELETE FROM page_analytics_hourly WHERE hour_start < ?", cutoff); err != nil {
		return err
	}

	// Delete from daily table
	if _, err := m.ctx.DB.ExecContext(ctx,
		"DELETE FROM page_analytics_daily WHERE date < ?", cutoffStr); err != nil {
		return err
	}

	// Delete from referrers table
	if _, err := m.ctx.DB.ExecContext(ctx,
		"DELETE FROM page_analytics_referrers WHERE date < ?", cutoffStr); err != nil {
		return err
	}

	// Delete from tech table
	if _, err := m.ctx.DB.ExecContext(ctx,
		"DELETE FROM page_analytics_tech WHERE date < ?", cutoffStr); err != nil {
		return err
	}

	// Delete from geo table
	if _, err := m.ctx.DB.ExecContext(ctx,
		"DELETE FROM page_analytics_geo WHERE date < ?", cutoffStr); err != nil {
		return err
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
