// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"context"
	"time"
)

// getOverviewStats returns summary statistics for the dashboard.
func (m *Module) getOverviewStats(ctx context.Context, startDate, endDate time.Time) OverviewStats {
	stats := OverviewStats{}
	today := time.Now().Format("2006-01-02")
	startDateStr := startDate.Format("2006-01-02")

	// Total views: combine aggregated daily data + today's raw views
	var aggregatedViews int64
	_ = m.ctx.DB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(views), 0)
		FROM page_analytics_daily
		WHERE date >= ? AND date < ?
	`, startDateStr, today).Scan(&aggregatedViews)

	// Add today's views from raw table
	var todayViews int64
	_ = m.ctx.DB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM page_analytics_views
		WHERE DATE(created_at) = ?
	`, today).Scan(&todayViews)

	stats.TotalViews = aggregatedViews + todayViews

	// Unique visitors: combine aggregated + today's raw
	var aggregatedUnique int64
	_ = m.ctx.DB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(unique_visitors), 0)
		FROM page_analytics_daily
		WHERE date >= ? AND date < ?
	`, startDateStr, today).Scan(&aggregatedUnique)

	var todayUnique int64
	_ = m.ctx.DB.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT visitor_hash)
		FROM page_analytics_views
		WHERE DATE(created_at) = ?
	`, today).Scan(&todayUnique)

	stats.UniqueVisitors = aggregatedUnique + todayUnique

	// Calculate bounce rate from daily aggregates
	var totalBounces int64
	_ = m.ctx.DB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(bounces), 0)
		FROM page_analytics_daily
		WHERE date >= ? AND date <= ?
	`, startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).Scan(&totalBounces)

	if stats.TotalViews > 0 {
		stats.BounceRate = float64(totalBounces) / float64(stats.TotalViews) * 100
	}

	// Views today (already calculated above)
	stats.ViewsToday = todayViews

	// Views yesterday
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	_ = m.ctx.DB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(views), 0)
		FROM page_analytics_daily
		WHERE date = ?
	`, yesterday).Scan(&stats.ViewsYesterday)

	// Calculate trend
	if stats.ViewsYesterday > 0 {
		stats.TrendPercent = (float64(stats.ViewsToday) - float64(stats.ViewsYesterday)) / float64(stats.ViewsYesterday) * 100
	}

	// Real-time visitors (last 5 minutes)
	stats.RealTimeVisitors = m.GetRealTimeVisitorCount(5)

	return stats
}

// getTopPages returns the top pages by views.
func (m *Module) getTopPages(ctx context.Context, startDate, endDate time.Time, limit int) []TopPage {
	endDateStr := endDate.Format("2006-01-02")

	// Combine aggregated daily data with today's raw views (with proper unique visitor count)
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT path, SUM(views) as total_views, SUM(unique_visitors) as total_unique, SUM(bounces) as total_bounces
		FROM (
			-- Aggregated daily data (excluding end date)
			SELECT path, views, unique_visitors, bounces
			FROM page_analytics_daily
			WHERE date >= ? AND date < ?
			UNION ALL
			-- End date's raw views with unique visitor count per path
			SELECT path, COUNT(*) as views, COUNT(DISTINCT visitor_hash) as unique_visitors, 0 as bounces
			FROM page_analytics_views
			WHERE DATE(created_at) = ?
			GROUP BY path
		)
		GROUP BY path
		ORDER BY total_views DESC
		LIMIT ?
	`, startDate.Format("2006-01-02"), endDateStr, endDateStr, limit)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var pages []TopPage
	for rows.Next() {
		var p TopPage
		var bounces int64
		if err := rows.Scan(&p.Path, &p.Views, &p.UniqueVisitors, &bounces); err != nil {
			continue
		}
		if p.Views > 0 {
			p.BounceRate = float64(bounces) / float64(p.Views) * 100
		}
		// Try to get page title from database
		p.PageTitle = m.getPageTitle(ctx, p.Path)
		pages = append(pages, p)
	}
	return pages
}

// getPageTitle attempts to get a page title for a path.
func (m *Module) getPageTitle(ctx context.Context, path string) string {
	// Remove leading slash and try as slug
	slug := path
	if len(slug) > 0 && slug[0] == '/' {
		slug = slug[1:]
	}

	var title string
	err := m.ctx.DB.QueryRowContext(ctx, `
		SELECT title FROM pages WHERE slug = ? AND status = 'published' LIMIT 1
	`, slug).Scan(&title)
	if err != nil {
		// Return formatted path as fallback
		if path == "/" {
			return "Home"
		}
		return path
	}
	return title
}

// getTopReferrers returns the top referrers by views.
func (m *Module) getTopReferrers(ctx context.Context, startDate, endDate time.Time, limit int) []TopReferrer {
	endDateStr := endDate.Format("2006-01-02")

	// Combine aggregated data with end date's raw views
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT referrer_domain, SUM(views) as total_views, SUM(unique_visitors) as total_unique
		FROM (
			SELECT referrer_domain, views, unique_visitors FROM page_analytics_referrers
			WHERE date >= ? AND date < ?
			UNION ALL
			SELECT referrer_domain, 1 as views, 0 as unique_visitors FROM page_analytics_views
			WHERE DATE(created_at) = ? AND referrer_domain != ''
		)
		GROUP BY referrer_domain
		ORDER BY total_views DESC
		LIMIT ?
	`, startDate.Format("2006-01-02"), endDateStr, endDateStr, limit)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	return scanRows(rows, func(r *TopReferrer) []any {
		return []any{&r.Domain, &r.Views, &r.UniqueVisitors}
	})
}

// getBrowserStats returns browser breakdown.
func (m *Module) getBrowserStats(ctx context.Context, startDate, endDate time.Time) []BrowserStat {
	endDateStr := endDate.Format("2006-01-02")

	// Combine aggregated data with end date's raw views
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT browser, SUM(views) as total_views
		FROM (
			SELECT browser, views FROM page_analytics_tech
			WHERE date >= ? AND date < ?
			UNION ALL
			SELECT browser, 1 as views FROM page_analytics_views
			WHERE DATE(created_at) = ?
		)
		GROUP BY browser
		ORDER BY total_views DESC
		LIMIT 10
	`, startDate.Format("2006-01-02"), endDateStr, endDateStr)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	return scanViewStats[BrowserStat](rows, func(b *BrowserStat) []any {
		return []any{&b.Browser, &b.Views}
	})
}

// getDeviceStats returns device type breakdown.
func (m *Module) getDeviceStats(ctx context.Context, startDate, endDate time.Time) []DeviceStat {
	endDateStr := endDate.Format("2006-01-02")

	// Combine aggregated data with end date's raw views
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT device_type, SUM(views) as total_views
		FROM (
			SELECT device_type, views FROM page_analytics_tech
			WHERE date >= ? AND date < ?
			UNION ALL
			SELECT device_type, 1 as views FROM page_analytics_views
			WHERE DATE(created_at) = ?
		)
		GROUP BY device_type
		ORDER BY total_views DESC
	`, startDate.Format("2006-01-02"), endDateStr, endDateStr)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	return scanViewStats[DeviceStat](rows, func(d *DeviceStat) []any {
		return []any{&d.DeviceType, &d.Views}
	})
}

// getCountryStats returns country breakdown.
func (m *Module) getCountryStats(ctx context.Context, startDate, endDate time.Time, limit int) []CountryStat {
	endDateStr := endDate.Format("2006-01-02")

	// Combine aggregated data with end date's raw views
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT country_code, SUM(views) as total_views
		FROM (
			SELECT country_code, views FROM page_analytics_geo
			WHERE date >= ? AND date < ?
			UNION ALL
			SELECT country_code, 1 as views FROM page_analytics_views
			WHERE DATE(created_at) = ? AND country_code != ''
		)
		GROUP BY country_code
		ORDER BY total_views DESC
		LIMIT ?
	`, startDate.Format("2006-01-02"), endDateStr, endDateStr, limit)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var countries []CountryStat
	var totalViews int64
	for rows.Next() {
		var c CountryStat
		if err := rows.Scan(&c.CountryCode, &c.Views); err != nil {
			continue
		}
		c.CountryName = CountryName(c.CountryCode)
		totalViews += c.Views
		countries = append(countries, c)
	}

	// Calculate percentages
	for i := range countries {
		if totalViews > 0 {
			countries[i].Percent = float64(countries[i].Views) / float64(totalViews) * 100
		}
	}
	return countries
}

// getTimeSeries returns views/visitors per day for charts.
func (m *Module) getTimeSeries(ctx context.Context, startDate, endDate time.Time) []TimeSeriesPoint {
	endDateStr := endDate.Format("2006-01-02")

	// Combine aggregated data with end date's raw views
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT date, SUM(views) as total_views, SUM(unique_visitors) as total_unique
		FROM (
			SELECT date, views, unique_visitors FROM page_analytics_daily
			WHERE date >= ? AND date < ?
			UNION ALL
			SELECT DATE(created_at) as date, 1 as views, 0 as unique_visitors FROM page_analytics_views
			WHERE DATE(created_at) = ?
		)
		GROUP BY date
		ORDER BY date
	`, startDate.Format("2006-01-02"), endDateStr, endDateStr)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	return scanRows(rows, func(p *TimeSeriesPoint) []any {
		return []any{&p.Date, &p.Views, &p.Unique}
	})
}

// parseDateRange converts a date range string to start and end times.
func parseDateRange(rangeStr string) (time.Time, time.Time) {
	now := time.Now()
	endDate := now

	var startDate time.Time
	switch rangeStr {
	case "7d":
		startDate = now.AddDate(0, 0, -7)
	case "30d":
		startDate = now.AddDate(0, 0, -30)
	case "90d":
		startDate = now.AddDate(0, 0, -90)
	case "1y":
		startDate = now.AddDate(-1, 0, 0)
	default:
		startDate = now.AddDate(0, 0, -30)
	}

	return startDate, endDate
}

// viewStat represents a stat item with views and percentage.
type viewStat interface {
	getViews() int64
	setPercent(float64)
}

// Implement viewStat for BrowserStat
func (b *BrowserStat) getViews() int64      { return b.Views }
func (b *BrowserStat) setPercent(p float64) { b.Percent = p }

// Implement viewStat for DeviceStat
func (d *DeviceStat) getViews() int64      { return d.Views }
func (d *DeviceStat) setPercent(p float64) { d.Percent = p }

// calculatePercents calculates percentage for each item based on total views.
func calculatePercents[T viewStat](items []T) {
	var totalViews int64
	for _, item := range items {
		totalViews += item.getViews()
	}
	if totalViews > 0 {
		for i := range items {
			items[i].setPercent(float64(items[i].getViews()) / float64(totalViews) * 100)
		}
	}
}

// scanViewStats scans rows into a slice of viewStat items and calculates percentages.
func scanViewStats[T any, PT interface {
	*T
	viewStat
}](rows rowScanner, scanFunc func(*T) []any) []T {
	if rows == nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var items []T
	for rows.Next() {
		var item T
		if err := rows.Scan(scanFunc(&item)...); err != nil {
			continue
		}
		items = append(items, item)
	}

	// Calculate percentages using pointer slice
	ptrs := make([]PT, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}
	calculatePercents(ptrs)

	return items
}

// rowScanner abstracts sql.Rows for scanning.
type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
}

// scanRows scans rows into a slice using the provided scan function.
func scanRows[T any](rows rowScanner, scanFunc func(*T) []any) []T {
	if rows == nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var items []T
	for rows.Next() {
		var item T
		if err := rows.Scan(scanFunc(&item)...); err != nil {
			continue
		}
		items = append(items, item)
	}
	return items
}
