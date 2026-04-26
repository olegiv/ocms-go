// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
)

func TestSetGlobalConfigGetter(t *testing.T) {
	// Reset after test
	original := globalConfigGetter
	t.Cleanup(func() { globalConfigGetter = original })

	// Initially may be nil; set it to a known function
	called := false
	fn := func(ctx context.Context, key string) (string, error) {
		called = true
		return "10.0.0.1", nil
	}
	SetGlobalConfigGetter(fn)

	if globalConfigGetter == nil {
		t.Fatal("SetGlobalConfigGetter did not set globalConfigGetter")
	}

	// Call it to confirm it's the function we set
	_, _ = globalConfigGetter(context.Background(), "test_key")
	if !called {
		t.Error("globalConfigGetter should have called the function we set")
	}
}

func TestGetExcludedIPs(t *testing.T) {
	original := globalConfigGetter
	t.Cleanup(func() { globalConfigGetter = original })

	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	tests := []struct {
		name      string
		configVal string
		configErr bool
		wantLen   int
		wantFirst string
	}{
		{
			name:      "nil getter returns nil",
			configVal: "",
			configErr: false,
			wantLen:   0,
		},
		{
			name:      "single IP",
			configVal: "192.168.1.1",
			wantLen:   1,
			wantFirst: "192.168.1.1",
		},
		{
			name:      "multiple IPs with blank lines",
			configVal: "10.0.0.1\n\n10.0.0.2\n  10.0.0.3  ",
			wantLen:   3,
			wantFirst: "10.0.0.1",
		},
		{
			name:      "empty value returns nil",
			configVal: "",
			wantLen:   0,
		},
		{
			name:      "only whitespace lines",
			configVal: "   \n\n   ",
			wantLen:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "nil getter returns nil" {
				globalConfigGetter = nil
				ips := svc.getExcludedIPs(ctx)
				if len(ips) != 0 {
					t.Errorf("getExcludedIPs with nil getter = %v, want empty", ips)
				}
				return
			}

			globalConfigGetter = func(_ context.Context, _ string) (string, error) {
				return tt.configVal, nil
			}

			ips := svc.getExcludedIPs(ctx)
			if len(ips) != tt.wantLen {
				t.Errorf("len(ips) = %d, want %d (ips = %v)", len(ips), tt.wantLen, ips)
			}
			if tt.wantFirst != "" && (len(ips) == 0 || ips[0] != tt.wantFirst) {
				t.Errorf("ips[0] = %q, want %q", ips[0], tt.wantFirst)
			}
		})
	}
}

func TestLogEvent_IPExclusion(t *testing.T) {
	original := globalConfigGetter
	t.Cleanup(func() { globalConfigGetter = original })

	tests := []struct {
		name       string
		category   string
		ip         string
		excludedIP string
		wantLogged bool
	}{
		{
			name:       "excluded IP skips non-auth event",
			category:   model.EventCategoryPage,
			ip:         "10.0.0.1",
			excludedIP: "10.0.0.1",
			wantLogged: false,
		},
		{
			name:       "excluded IP still logs auth event",
			category:   model.EventCategoryAuth,
			ip:         "10.0.0.1",
			excludedIP: "10.0.0.1",
			wantLogged: true,
		},
		{
			name:       "excluded IP still logs security event",
			category:   model.EventCategorySecurity,
			ip:         "10.0.0.1",
			excludedIP: "10.0.0.1",
			wantLogged: true,
		},
		{
			name:       "non-excluded IP is logged",
			category:   model.EventCategoryPage,
			ip:         "192.168.1.1",
			excludedIP: "10.0.0.1",
			wantLogged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupEventTestDB(t)
			defer func() { _ = db.Close() }()

			globalConfigGetter = func(_ context.Context, _ string) (string, error) {
				return tt.excludedIP, nil
			}

			svc := NewEventService(db)
			ctx := context.Background()

			err := svc.LogEvent(ctx, model.EventLevelInfo, tt.category, "test", nil, tt.ip, "/test", nil)
			if err != nil {
				t.Fatalf("LogEvent failed: %v", err)
			}

			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count); err != nil {
				t.Fatalf("failed to count events: %v", err)
			}

			logged := count > 0
			if logged != tt.wantLogged {
				t.Errorf("event logged = %v, want %v", logged, tt.wantLogged)
			}
		})
	}
}

func TestLogSchedulerEvent(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogSchedulerEvent(ctx, model.EventLevelInfo, "Job completed", nil, "", "/admin/scheduler", nil)
	if err != nil {
		t.Fatalf("LogSchedulerEvent failed: %v", err)
	}

	var category string
	if err := db.QueryRow("SELECT category FROM events").Scan(&category); err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if category != model.EventCategoryScheduler {
		t.Errorf("category = %q, want %q", category, model.EventCategoryScheduler)
	}
}

func TestLogSecurityEvent(t *testing.T) {
	db := setupEventTestDB(t)
	defer func() { _ = db.Close() }()

	svc := NewEventService(db)
	ctx := context.Background()

	err := svc.LogSecurityEvent(ctx, model.EventLevelWarning, "Suspicious activity", nil, "1.2.3.4", "/login", nil)
	if err != nil {
		t.Fatalf("LogSecurityEvent failed: %v", err)
	}

	var category, level string
	if err := db.QueryRow("SELECT category, level FROM events").Scan(&category, &level); err != nil {
		t.Fatalf("failed to read event: %v", err)
	}
	if category != model.EventCategorySecurity {
		t.Errorf("category = %q, want %q", category, model.EventCategorySecurity)
	}
	if level != model.EventLevelWarning {
		t.Errorf("level = %q, want %q", level, model.EventLevelWarning)
	}
}
