package handler

import (
	"testing"

	"ocms-go/internal/cache"
)

func TestNewCacheHandler(t *testing.T) {
	sm := testSessionManager(t)

	h := NewCacheHandler(nil, sm, nil, nil)
	if h == nil {
		t.Fatal("NewCacheHandler returned nil")
	}
}

func TestCacheStatsData(t *testing.T) {
	data := CacheStatsData{
		Caches:      []cache.ManagerCacheStats{},
		TotalStats:  cache.Stats{},
		Info:        cache.ManagerInfo{},
		IsRedis:     false,
		HealthError: "",
	}

	if data.Caches == nil {
		t.Error("Caches should not be nil")
	}
	if data.IsRedis {
		t.Error("IsRedis should be false by default")
	}
	if data.HealthError != "" {
		t.Error("HealthError should be empty")
	}
}

func TestCacheStatsDataWithError(t *testing.T) {
	data := CacheStatsData{
		Caches:      []cache.ManagerCacheStats{},
		HealthError: "connection refused",
	}

	if data.HealthError != "connection refused" {
		t.Errorf("HealthError = %q, want %q", data.HealthError, "connection refused")
	}
}

func TestCacheStatsDataWithRedis(t *testing.T) {
	data := CacheStatsData{
		Caches:  []cache.ManagerCacheStats{},
		IsRedis: true,
	}

	if !data.IsRedis {
		t.Error("IsRedis should be true")
	}
}
