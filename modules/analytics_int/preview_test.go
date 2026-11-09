// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
)

func TestPrivatePreviewDoesNotRecordPageview(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	m := testModule(t, db)
	m.settings.Enabled = true
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(200)
	})
	r := httptest.NewRequest("GET", "/ordinary-draft-slug", nil)
	r.RemoteAddr = "203.0.113.1:1234"
	r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130.0 Safari/537.36")
	m.TrackingMiddleware()(next).ServeHTTP(httptest.NewRecorder(), r)
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	} // Wait for every async tracker before observing storage.
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM page_analytics_views").Scan(&n); err != nil || n != 0 {
		t.Fatalf("private response tracked: count=%d err=%v", n, err)
	}
}
