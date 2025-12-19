// Package page_analytics provides built-in server-side analytics for oCMS.
// It tracks page views, unique visitors, referrers, browser/device stats,
// and geographic data with privacy-focused anonymization.
package page_analytics

import "time"

// PageView represents a single page view event stored in the database.
type PageView struct {
	ID             int64
	VisitorHash    string    // Anonymized visitor fingerprint (daily rotating)
	Path           string    // URL path (e.g., "/about")
	PageID         *int64    // FK to pages.id (nullable for non-page routes)
	ReferrerDomain string    // Extracted referrer domain
	CountryCode    string    // 2-letter ISO country code
	Browser        string    // Browser name (Chrome, Firefox, Safari, etc.)
	OS             string    // Operating system
	DeviceType     string    // desktop, mobile, tablet
	Language       string    // Accept-Language primary language
	SessionHash    string    // Session proxy (IP+UA+date hash)
	CreatedAt      time.Time // When the view occurred
}

// HourlyStat represents hourly aggregated statistics.
type HourlyStat struct {
	ID             int64
	HourStart      time.Time
	Path           string
	Views          int
	UniqueVisitors int
}

// DailyStat represents daily aggregated statistics.
type DailyStat struct {
	ID             int64
	Date           time.Time
	Path           string
	Views          int
	UniqueVisitors int
	Bounces        int
}

// ReferrerStat represents daily referrer statistics.
type ReferrerStat struct {
	ID             int64
	Date           time.Time
	ReferrerDomain string
	Views          int
	UniqueVisitors int
}

// TechStat represents daily browser/device statistics.
type TechStat struct {
	ID         int64
	Date       time.Time
	Browser    string
	OS         string
	DeviceType string
	Views      int
}

// GeoStat represents daily geographic statistics.
type GeoStat struct {
	ID             int64
	Date           time.Time
	CountryCode    string
	Views          int
	UniqueVisitors int
}

// Settings holds module configuration.
type Settings struct {
	Enabled           bool
	RetentionDays     int
	ExcludePaths      []string
	CurrentSalt       string
	SaltCreatedAt     time.Time
	SaltRotationHours int
}

// OverviewStats contains summary statistics for the dashboard.
type OverviewStats struct {
	TotalViews       int64
	UniqueVisitors   int64
	BounceRate       float64 // percentage
	ViewsToday       int64
	ViewsYesterday   int64
	TrendPercent     float64 // change from yesterday
	RealTimeVisitors int     // visitors in last 5 minutes
}

// TopPage represents a page in the top pages list.
type TopPage struct {
	Path           string
	PageID         *int64
	PageTitle      string
	Views          int64
	UniqueVisitors int64
	BounceRate     float64
}

// TopReferrer represents a referrer in the top referrers list.
type TopReferrer struct {
	Domain         string
	Views          int64
	UniqueVisitors int64
}

// BrowserStat represents browser breakdown for dashboard.
type BrowserStat struct {
	Browser string
	Views   int64
	Percent float64
}

// DeviceStat represents device type breakdown for dashboard.
type DeviceStat struct {
	DeviceType string
	Views      int64
	Percent    float64
}

// CountryStat represents country breakdown for dashboard.
type CountryStat struct {
	CountryCode string
	CountryName string
	Views       int64
	Percent     float64
}

// TimeSeriesPoint represents a data point for time series charts.
type TimeSeriesPoint struct {
	Date   string // YYYY-MM-DD
	Views  int64
	Unique int64
}

// DashboardData contains all data for the analytics dashboard.
type DashboardData struct {
	Overview     OverviewStats
	TopPages     []TopPage
	TopReferrers []TopReferrer
	Browsers     []BrowserStat
	Devices      []DeviceStat
	Countries    []CountryStat
	TimeSeries   []TimeSeriesPoint
	DateRange    string // "7d", "30d", "90d", "1y"
	Settings     Settings
}

// ParsedUA holds parsed user agent information.
type ParsedUA struct {
	Browser    string
	OS         string
	DeviceType string // "desktop", "mobile", "tablet"
}
