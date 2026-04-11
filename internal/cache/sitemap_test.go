// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSitemapCache_GetCachesBySiteURL(t *testing.T) {
	q := newTestDB(t)
	c := NewSitemapCache(q, time.Hour)
	ctx := context.Background()

	xmlA, err := c.Get(ctx, "https://first.example")
	if err != nil {
		t.Fatalf("Get(first) error: %v", err)
	}
	if !strings.Contains(string(xmlA), "https://first.example") {
		t.Fatalf("expected first sitemap to contain first host, got: %s", string(xmlA))
	}

	statsAfterFirst := c.Stats()
	if statsAfterFirst.Misses != 1 {
		t.Fatalf("after first Get misses=%d, want 1", statsAfterFirst.Misses)
	}
	if statsAfterFirst.Hits != 0 {
		t.Fatalf("after first Get hits=%d, want 0", statsAfterFirst.Hits)
	}

	xmlB, err := c.Get(ctx, "https://second.example")
	if err != nil {
		t.Fatalf("Get(second) error: %v", err)
	}
	if !strings.Contains(string(xmlB), "https://second.example") {
		t.Fatalf("expected second sitemap to contain second host, got: %s", string(xmlB))
	}
	if strings.Contains(string(xmlB), "https://first.example") {
		t.Fatalf("second sitemap should not contain first host, got: %s", string(xmlB))
	}

	statsAfterSecond := c.Stats()
	if statsAfterSecond.Misses != 2 {
		t.Fatalf("after second Get misses=%d, want 2", statsAfterSecond.Misses)
	}
	if statsAfterSecond.Hits != 0 {
		t.Fatalf("after second Get hits=%d, want 0", statsAfterSecond.Hits)
	}

	xmlB2, err := c.Get(ctx, "https://second.example")
	if err != nil {
		t.Fatalf("Get(second cached) error: %v", err)
	}
	if !strings.Contains(string(xmlB2), "https://second.example") {
		t.Fatalf("expected cached second sitemap to contain second host, got: %s", string(xmlB2))
	}

	statsAfterThird := c.Stats()
	if statsAfterThird.Misses != 2 {
		t.Fatalf("after third Get misses=%d, want 2", statsAfterThird.Misses)
	}
	if statsAfterThird.Hits != 1 {
		t.Fatalf("after third Get hits=%d, want 1", statsAfterThird.Hits)
	}
}
