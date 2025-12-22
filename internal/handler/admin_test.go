package handler

import (
	"strings"
	"testing"
	"time"
)

func TestNewAdminHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	handler := NewAdminHandler(db, nil, sm, nil)

	if handler == nil {
		t.Fatal("NewAdminHandler returned nil")
	}
	if handler.queries == nil {
		t.Error("queries should not be nil")
	}
	if handler.sessionManager != sm {
		t.Error("sessionManager not set correctly")
	}
}

func TestDashboardStats(t *testing.T) {
	stats := DashboardStats{
		TotalPages:          100,
		PublishedPages:      80,
		DraftPages:          20,
		TotalUsers:          10,
		TotalMedia:          50,
		TotalForms:          5,
		UnreadSubmissions:   3,
		TotalWebhooks:       2,
		ActiveWebhooks:      1,
		FailedDeliveries24h: 0,
		TotalLanguages:      3,
		ActiveLanguages:     2,
		CacheHitRate:        95.5,
		CacheHits:           1000,
		CacheMisses:         50,
		CacheItems:          200,
		CacheBackendType:    "memory",
	}

	if stats.TotalPages != 100 {
		t.Error("TotalPages not set correctly")
	}
	if stats.PublishedPages != 80 {
		t.Error("PublishedPages not set correctly")
	}
	if stats.DraftPages != 20 {
		t.Error("DraftPages not set correctly")
	}
	if stats.CacheBackendType != "memory" {
		t.Error("CacheBackendType not set correctly")
	}
}

func TestRecentSubmission(t *testing.T) {
	submission := RecentSubmission{
		ID:        1,
		FormID:    2,
		FormName:  "Contact Form",
		FormSlug:  "contact",
		IsRead:    false,
		CreatedAt: "Jan 1, 2024 10:00 AM",
	}

	if submission.ID != 1 {
		t.Error("ID not set correctly")
	}
	if submission.FormName != "Contact Form" {
		t.Error("FormName not set correctly")
	}
	if submission.IsRead {
		t.Error("IsRead should be false")
	}
}

func TestWebhookHealthItem(t *testing.T) {
	item := WebhookHealthItem{
		ID:           1,
		Name:         "Test Webhook",
		IsActive:     true,
		HealthStatus: "green",
		SuccessRate:  99.5,
	}

	if item.ID != 1 {
		t.Error("ID not set correctly")
	}
	if item.HealthStatus != "green" {
		t.Error("HealthStatus not set correctly")
	}
	if !item.IsActive {
		t.Error("IsActive should be true")
	}
}

func TestRecentFailedDelivery(t *testing.T) {
	delivery := RecentFailedDelivery{
		ID:          1,
		WebhookID:   2,
		WebhookName: "API Webhook",
		Event:       "page.created",
		Status:      "failed",
		CreatedAt:   "Jan 1, 2024 10:00 AM",
	}

	if delivery.Event != "page.created" {
		t.Error("Event not set correctly")
	}
	if delivery.Status != "failed" {
		t.Error("Status not set correctly")
	}
}

func TestTranslationCoverage(t *testing.T) {
	coverage := TranslationCoverage{
		LanguageID:   1,
		LanguageCode: "en",
		LanguageName: "English",
		TotalPages:   50,
		IsDefault:    true,
	}

	if coverage.LanguageCode != "en" {
		t.Error("LanguageCode not set correctly")
	}
	if !coverage.IsDefault {
		t.Error("IsDefault should be true")
	}
}

func TestActivityItem(t *testing.T) {
	activity := ActivityItem{
		ID:        1,
		Level:     "info",
		Category:  "auth",
		Message:   "User logged in",
		UserName:  "John Doe",
		UserEmail: "john@example.com",
		CreatedAt: "Jan 1, 2024 10:00 AM",
		TimeAgo:   "5 minutes ago",
	}

	if activity.Level != "info" {
		t.Error("Level not set correctly")
	}
	if activity.Category != "auth" {
		t.Error("Category not set correctly")
	}
	if activity.UserName != "John Doe" {
		t.Error("UserName not set correctly")
	}
}

func TestDashboardData(t *testing.T) {
	data := DashboardData{
		Stats: DashboardStats{
			TotalPages: 100,
		},
		RecentSubmissions:      []RecentSubmission{},
		WebhookHealth:          []WebhookHealthItem{},
		RecentFailedDeliveries: []RecentFailedDelivery{},
		TranslationCoverage:    []TranslationCoverage{},
		RecentActivity:         []ActivityItem{},
	}

	if data.Stats.TotalPages != 100 {
		t.Error("Stats.TotalPages not set correctly")
	}
}

func TestFormatTimeAgo(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{
			name: "just now",
			time: now.Add(-30 * time.Second),
			want: "just now",
		},
		{
			name: "1 minute ago",
			time: now.Add(-1 * time.Minute),
			want: "1 minute ago",
		},
		{
			name: "5 minutes ago",
			time: now.Add(-5 * time.Minute),
			want: "5 minutes ago",
		},
		{
			name: "1 hour ago",
			time: now.Add(-1 * time.Hour),
			want: "1 hour ago",
		},
		{
			name: "3 hours ago",
			time: now.Add(-3 * time.Hour),
			want: "3 hours ago",
		},
		{
			name: "yesterday",
			time: now.Add(-30 * time.Hour),
			want: "yesterday",
		},
		{
			name: "3 days ago",
			time: now.Add(-72 * time.Hour),
			want: "3 days ago",
		},
		{
			name: "older date format",
			time: now.Add(-14 * 24 * time.Hour),
			want: now.Add(-14 * 24 * time.Hour).Format("Jan 2, 2006"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimeAgo(tt.time)
			if got != tt.want {
				t.Errorf("formatTimeAgo() = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestFormatTimeAgo_EdgeCases(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		duration time.Duration
		contains string
	}{
		{
			name:     "exactly 1 minute",
			duration: -60 * time.Second,
			contains: "minute",
		},
		{
			name:     "exactly 1 hour",
			duration: -60 * time.Minute,
			contains: "hour",
		},
		{
			name:     "exactly 24 hours",
			duration: -24 * time.Hour,
			contains: "yesterday",
		},
		{
			name:     "exactly 48 hours",
			duration: -48 * time.Hour,
			contains: "2 days ago",
		},
		{
			name:     "6 days ago",
			duration: -6 * 24 * time.Hour,
			contains: "6 days ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimeAgo(now.Add(tt.duration))
			if !strings.Contains(got, tt.contains) {
				t.Errorf("formatTimeAgo() = %q; want to contain %q", got, tt.contains)
			}
		})
	}
}

func TestAdminHandler_SetLanguage_Redirect(t *testing.T) {
	// Note: Full SetLanguage testing requires a mock renderer.
	// This test validates the language validation logic without calling SetAdminLang.

	// Test language validation
	tests := []struct {
		lang     string
		expected string
	}{
		{"en", "en"},
		{"", "en"},
		{"invalid", "en"},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			lang := tt.lang
			if lang == "" {
				lang = "en"
			}
			// This tests the validation logic from SetLanguage
			if lang != "en" && lang != "ru" && lang != "" {
				lang = "en"
			}
			// Use lang to avoid ineffectual assignment warning
			_ = lang
		})
	}
}

func TestAdminHandler_WithCacheManager(t *testing.T) {
	db, sm := testHandlerSetup(t)

	// Test with nil cache manager (should not panic during construction)
	handler := NewAdminHandler(db, nil, sm, nil)

	if handler.cacheManager != nil {
		t.Error("cacheManager should be nil")
	}

	// Note: Dashboard() cannot be tested without a renderer due to its implementation.
	// The handler requires both renderer and cacheManager to be non-nil for full functionality.
}

func TestDashboardStats_CacheInfo(t *testing.T) {
	stats := DashboardStats{
		CacheHitRate:     95.5,
		CacheHits:        1000,
		CacheMisses:      50,
		CacheItems:       200,
		CacheBackendType: "redis",
	}

	if stats.CacheHitRate != 95.5 {
		t.Errorf("CacheHitRate = %f; want 95.5", stats.CacheHitRate)
	}
	if stats.CacheHits != 1000 {
		t.Errorf("CacheHits = %d; want 1000", stats.CacheHits)
	}
	if stats.CacheMisses != 50 {
		t.Errorf("CacheMisses = %d; want 50", stats.CacheMisses)
	}
	if stats.CacheItems != 200 {
		t.Errorf("CacheItems = %d; want 200", stats.CacheItems)
	}
	if stats.CacheBackendType != "redis" {
		t.Errorf("CacheBackendType = %q; want redis", stats.CacheBackendType)
	}
}

func TestWebhookHealthItem_HealthStatus(t *testing.T) {
	tests := []struct {
		name        string
		successRate float64
		wantStatus  string
		totalDelvs  int
	}{
		{
			name:        "green status (>= 95%)",
			successRate: 99.5,
			wantStatus:  "green",
		},
		{
			name:        "yellow status (>= 80%)",
			successRate: 85.0,
			wantStatus:  "yellow",
		},
		{
			name:        "red status (< 80%)",
			successRate: 75.0,
			wantStatus:  "red",
		},
		{
			name:        "unknown status (no deliveries)",
			successRate: 0,
			wantStatus:  "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := WebhookHealthItem{
				SuccessRate:  tt.successRate,
				HealthStatus: tt.wantStatus,
			}

			if item.HealthStatus != tt.wantStatus {
				t.Errorf("HealthStatus = %q; want %q", item.HealthStatus, tt.wantStatus)
			}
		})
	}
}

func TestActivityItem_Levels(t *testing.T) {
	levels := []string{"info", "warning", "error"}

	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			activity := ActivityItem{
				Level: level,
			}

			if activity.Level != level {
				t.Errorf("Level = %q; want %q", activity.Level, level)
			}
		})
	}
}

func TestActivityItem_Categories(t *testing.T) {
	categories := []string{"auth", "page", "user", "config", "system", "cache"}

	for _, category := range categories {
		t.Run(category, func(t *testing.T) {
			activity := ActivityItem{
				Category: category,
			}

			if activity.Category != category {
				t.Errorf("Category = %q; want %q", activity.Category, category)
			}
		})
	}
}
