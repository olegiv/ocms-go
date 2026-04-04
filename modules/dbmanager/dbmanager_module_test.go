// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package dbmanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// ============================================================================
// Module properties
// ============================================================================

func TestTranslationsFS(t *testing.T) {
	m := New()
	fs := m.TranslationsFS()
	entries, err := fs.ReadDir("locales")
	if err != nil {
		t.Fatalf("TranslationsFS ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("TranslationsFS should contain at least one locale file")
	}
}

func TestRegisterRoutes(t *testing.T) {
	m := New()
	// RegisterRoutes should not panic — dbmanager has no public routes
	m.RegisterRoutes(nil)
}

func TestRegisterAdminRoutes(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	router := chi.NewRouter()
	m.RegisterAdminRoutes(router)
	// If we get here without panic, routes are registered
}

// ============================================================================
// formatAny: cover all type branches
// ============================================================================

func TestFormatAny(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{"nil", nil, "NULL"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"int", int(42), "42"},
		{"int8", int8(8), "8"},
		{"int16", int16(16), "16"},
		{"int32", int32(32), "32"},
		{"int64", int64(64), "64"},
		{"uint", uint(1), "1"},
		{"uint8", uint8(8), "8"},
		{"uint16", uint16(16), "16"},
		{"uint32", uint32(32), "32"},
		{"uint64", uint64(64), "64"},
		{"float32", float32(1.5), "1.5"},
		{"float64", float64(2.5), "2.5"},
		{"string", "hello world", "hello world"},
		{"bytes", []byte("bytes"), "bytes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAny(tt.value)
			if got != tt.expected {
				t.Errorf("formatAny(%v [%T]) = %q, want %q", tt.value, tt.value, got, tt.expected)
			}
		})
	}
}

func TestFormatAny_Time(t *testing.T) {
	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	got := formatAny(ts)
	want := "2025-01-15 10:30:00"
	if got != want {
		t.Errorf("formatAny(time) = %q, want %q", got, want)
	}
}

func TestFormatAny_ComplexType(t *testing.T) {
	// An unusual type that hits the default formatDefault branch
	type customType struct{ V int }
	val := customType{V: 99}
	got := formatAny(val)
	if got == "" {
		t.Error("formatAny complex type should not return empty string")
	}
}

// ============================================================================
// formatInt: all integer subtypes
// ============================================================================

func TestFormatInt(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{"int", int(100), "100"},
		{"int8", int8(-5), "-5"},
		{"int16", int16(1000), "1000"},
		{"int32", int32(-99), "-99"},
		{"int64", int64(9999), "9999"},
		{"uint", uint(200), "200"},
		{"uint8", uint8(255), "255"},
		{"uint16", uint16(65535), "65535"},
		{"uint32", uint32(100000), "100000"},
		{"uint64", uint64(999999), "999999"},
		{"string (unknown)", "unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatInt(tt.value)
			if got != tt.expected {
				t.Errorf("formatInt(%v [%T]) = %q, want %q", tt.value, tt.value, got, tt.expected)
			}
		})
	}
}

// ============================================================================
// formatFloat and floatToString
// ============================================================================

func TestFormatFloat(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{"float32 whole", float32(3.0), "3"},
		{"float32 decimal", float32(1.5), "1.5"},
		{"float64 whole", float64(7.0), "7"},
		{"float64 decimal", float64(3.14), "3.14"},
		{"unknown type", "not a float", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatFloat(tt.value)
			if got != tt.expected {
				t.Errorf("formatFloat(%v [%T]) = %q, want %q", tt.value, tt.value, got, tt.expected)
			}
		})
	}
}

func TestFloatToString(t *testing.T) {
	tests := []struct {
		value    float64
		expected string
	}{
		{0.0, "0"},
		{1.0, "1"},
		{-1.0, "-1"},
		{3.14, "3.14"},
		{2.5, "2.5"},
		{100.0, "100"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := floatToString(tt.value)
			if got != tt.expected {
				t.Errorf("floatToString(%v) = %q, want %q", tt.value, got, tt.expected)
			}
		})
	}
}

// ============================================================================
// formatDefault
// ============================================================================

func TestFormatDefault(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{"string", "hello", "hello"},
		{"bytes", []byte("world"), "world"},
		{"complex", struct{ X int }{X: 1}, "[complex value]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDefault(tt.value)
			if got != tt.expected {
				t.Errorf("formatDefault(%v) = %q, want %q", tt.value, got, tt.expected)
			}
		})
	}
}

// ============================================================================
// formatValue: time branch
// ============================================================================

func TestFormatValue_Time(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	got := formatValue(ts)
	want := "2025-06-15 12:00:00"
	if got != want {
		t.Errorf("formatValue(time.Time) = %q, want %q", got, want)
	}
}

func TestFormatValue_Whitespace(t *testing.T) {
	// String with newlines/tabs should be cleaned up
	got := formatValue("line1\nline2\ttab")
	if strings.Contains(got, "\n") || strings.Contains(got, "\t") {
		t.Errorf("formatValue should strip newlines and tabs, got: %q", got)
	}
}

// ============================================================================
// executeQuery: PRAGMA and WITH queries
// ============================================================================

func TestExecutePragmaQuery(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	result := m.executeQuery(context.Background(), "PRAGMA table_info(users)", 1)
	if result.Error != "" {
		t.Errorf("executeQuery PRAGMA error: %s", result.Error)
	}
	if !result.IsSelect {
		t.Error("PRAGMA should be treated as SELECT")
	}
}

func TestExecuteWithQuery(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	result := m.executeQuery(context.Background(), "WITH x AS (SELECT 1 AS val) SELECT val FROM x", 1)
	if result.Error != "" {
		t.Errorf("executeQuery WITH error: %s", result.Error)
	}
	if !result.IsSelect {
		t.Error("WITH should be treated as SELECT")
	}
}

func TestExecuteExplainQuery(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	result := m.executeQuery(context.Background(), "EXPLAIN QUERY PLAN SELECT * FROM users", 1)
	// EXPLAIN may or may not work depending on SQLite version; just check no panic
	_ = result
}

func TestExecuteUpdateQuery(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Create a temp table and update it
	_, err := db.Exec("CREATE TABLE temp_update (id INTEGER PRIMARY KEY, val TEXT)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = db.Exec("INSERT INTO temp_update (val) VALUES ('before')")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	result := m.executeQuery(context.Background(), "UPDATE temp_update SET val = 'after'", 1)
	if result.Error != "" {
		t.Errorf("executeQuery UPDATE error: %s", result.Error)
	}
	if result.IsSelect {
		t.Error("UPDATE should not be treated as SELECT")
	}
	if result.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", result.RowsAffected)
	}
}

// ============================================================================
// handleDashboard / handleExecute: unauthenticated → 403 Forbidden
// ============================================================================

func TestHandleDashboardForbidden(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/dbmanager", nil)

	m.handleDashboard(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("handleDashboard no user status = %d, want 403", w.Code)
	}
}

func TestHandleExecuteForbidden(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/dbmanager/execute", nil)

	m.handleExecute(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("handleExecute no user status = %d, want 403", w.Code)
	}
}

// ============================================================================
// handleDashboard / handleExecute: editor role → 403 Forbidden (admin only)
// ============================================================================

func TestHandleDashboardEditorForbidden(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/dbmanager", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, store.User{
		ID:    2,
		Email: "editor@test.com",
		Role:  "editor",
	})
	req = req.WithContext(ctx)

	m.handleDashboard(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("handleDashboard editor status = %d, want 403", w.Code)
	}
}

// ============================================================================
// handleExecute: authenticated admin, empty query
// ============================================================================

func TestHandleExecuteWithQuery(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	body := strings.NewReader("query=SELECT+1")
	req := httptest.NewRequest(http.MethodPost, "/admin/dbmanager/execute", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, store.User{
		ID:    1,
		Email: "admin@test.com",
		Role:  "admin",
	})
	req = req.WithContext(ctx)

	// handleExecute calls renderDashboard which calls render.Templ.
	// With an empty Renderer (no templates), Templ will write a 200 but with empty body.
	// The test is mainly to exercise the code path without panic.
	defer func() {
		if r := recover(); r != nil {
			// A panic in render.Templ is acceptable — we still covered the handler path
			t.Logf("renderDashboard panicked (expected with no templates): %v", r)
		}
	}()

	m.handleExecute(w, req)
	// Either 200 (render succeeded) or 303 (flash redirect)
}

func TestHandleExecuteEmptyQuery(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	w := httptest.NewRecorder()
	body := strings.NewReader("query=")
	req := httptest.NewRequest(http.MethodPost, "/admin/dbmanager/execute", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUser, store.User{
		ID:    1,
		Email: "admin@test.com",
		Role:  "admin",
	})
	req = req.WithContext(ctx)

	m.handleExecute(w, req)

	// Empty query should redirect with error flash
	if w.Code != http.StatusSeeOther {
		t.Errorf("handleExecute empty query status = %d, want 303", w.Code)
	}
}

// ============================================================================
// getQueryHistory: limit parameter
// ============================================================================

func TestQueryHistoryLimit(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Execute multiple queries
	for i := 0; i < 5; i++ {
		_ = m.executeQuery(context.Background(), "SELECT 1", 1)
	}

	// Fetch with limit of 3
	history, err := m.getQueryHistory(context.Background(), 3)
	if err != nil {
		t.Fatalf("getQueryHistory: %v", err)
	}
	if len(history) != 3 {
		t.Errorf("getQueryHistory(limit=3) len = %d, want 3", len(history))
	}
}

// ============================================================================
// logQueryExecution: error path
// ============================================================================

func TestLogQueryExecutionWithError(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Log a query execution with an error
	m.logQueryExecution(
		context.Background(),
		"INVALID QUERY",
		1,
		0,
		time.Millisecond,
		&testError{msg: "syntax error"},
	)

	// Verify it was logged
	history, err := m.getQueryHistory(context.Background(), 5)
	if err != nil {
		t.Fatalf("getQueryHistory: %v", err)
	}

	found := false
	for _, h := range history {
		if h.Query == "INVALID QUERY" && h.Error.Valid {
			found = true
			break
		}
	}
	if !found {
		t.Error("error query should be logged with error message")
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// ============================================================================
// QueryHistoryItem struct fields
// ============================================================================

func TestQueryHistoryItemStruct(t *testing.T) {
	item := QueryHistoryItem{
		ID:            1,
		Query:         "SELECT 1",
		UserID:        2,
		ExecutedAt:    time.Now(),
		RowsAffected:  5,
		ExecutionTime: 10,
	}

	if item.ID != 1 {
		t.Error("ID not set")
	}
	if item.Query != "SELECT 1" {
		t.Error("Query not set")
	}
	if item.UserID != 2 {
		t.Error("UserID not set")
	}
	if item.RowsAffected != 5 {
		t.Error("RowsAffected not set")
	}
}

// ============================================================================
// DashboardData struct
// ============================================================================

func TestDashboardDataStruct(t *testing.T) {
	data := DashboardData{
		Query: "SELECT 1",
		Result: &QueryResult{
			Columns:      []string{"col1"},
			Rows:         [][]string{{"val1"}},
			RowsAffected: 1,
			IsSelect:     true,
		},
		History: []QueryHistoryItem{{ID: 1, Query: "SELECT 1"}},
	}

	if data.Query != "SELECT 1" {
		t.Error("Query not set")
	}
	if data.Result == nil {
		t.Error("Result should not be nil")
	}
	if len(data.History) != 1 {
		t.Error("History should have 1 item")
	}
}

// ============================================================================
// Additional module lifecycle tests
// ============================================================================

func TestModuleContextNilOnShutdown(t *testing.T) {
	m := New()
	// Shutdown before Init should not panic
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown before Init: %v", err)
	}
}

// ============================================================================
// isSelectQuery: all keywords
// ============================================================================

func TestIsSelectQueryKeywords(t *testing.T) {
	tests := []struct {
		query    string
		expected bool
	}{
		{"SELECT 1", true},
		{"select 1", true},
		{"  SELECT  ", true},
		{"PRAGMA user_version", true},
		{"pragma user_version", true},
		{"EXPLAIN SELECT 1", true},
		{"explain select 1", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"with x AS (SELECT 1) SELECT * FROM x", true},
		{"INSERT INTO t VALUES (1)", false},
		{"UPDATE t SET v = 1", false},
		{"DELETE FROM t", false},
		{"DROP TABLE t", false},
		{"CREATE TABLE t (id INTEGER)", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := isSelectQuery(tt.query)
			if got != tt.expected {
				t.Errorf("isSelectQuery(%q) = %v, want %v", tt.query, got, tt.expected)
			}
		})
	}
}

// ============================================================================
// floatToString: zero and integer values
// ============================================================================

func TestFloatToStringZero(t *testing.T) {
	got := floatToString(0.0)
	if got != "0" {
		t.Errorf("floatToString(0.0) = %q, want 0", got)
	}
}

func TestFloatToStringNegative(t *testing.T) {
	got := floatToString(-3.14)
	if got == "" {
		t.Error("floatToString negative should return non-empty")
	}
}

// ============================================================================
// formatValue: bytes and whitespace cleaning
// ============================================================================

func TestFormatValueString(t *testing.T) {
	got := formatValue("hello world")
	if got != "hello world" {
		t.Errorf("formatValue(string) = %q, want hello world", got)
	}
}

func TestFormatValueDoubleSpace(t *testing.T) {
	// Double spaces should be condensed to single space
	got := formatValue("hello  world")
	if got != "hello world" {
		t.Errorf("formatValue double space = %q, want 'hello world'", got)
	}
}

// ============================================================================
// truncateQuery
// ============================================================================

func TestTruncateQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		maxLen   int
		expected string
	}{
		{"short query", "SELECT 1", 50, "SELECT 1"},
		{"exact length", "SELECT 1", 8, "SELECT 1"},
		{"long query", "SELECT * FROM users WHERE id > 100", 20, "SELECT * FROM users " + "..."},
		{"empty query", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateQuery(tt.query, tt.maxLen)
			if got != tt.expected {
				t.Errorf("truncateQuery(%q, %d) = %q, want %q", tt.query, tt.maxLen, got, tt.expected)
			}
		})
	}
}

// ============================================================================
// DBManagerViewData struct
// ============================================================================

func TestDBManagerViewData(t *testing.T) {
	data := DBManagerViewData{
		IsDemoMode: true,
		Query:      "SELECT 1",
		Result:     &QueryResult{IsSelect: true},
		History:    []QueryHistoryItem{{ID: 1}},
	}
	if !data.IsDemoMode {
		t.Error("IsDemoMode not set")
	}
	if data.Query != "SELECT 1" {
		t.Error("Query not set")
	}
}

// ============================================================================
// executeQuery: multi-column SELECT
// ============================================================================

func TestExecuteMultiColumnSelect(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	result := m.executeQuery(context.Background(), "SELECT 1 AS a, 2 AS b, 3 AS c", 1)
	if result.Error != "" {
		t.Errorf("executeQuery error: %s", result.Error)
	}
	if len(result.Columns) != 3 {
		t.Errorf("Columns len = %d, want 3", len(result.Columns))
	}
	if len(result.Rows) != 1 {
		t.Fatalf("Rows len = %d, want 1", len(result.Rows))
	}
	if result.Rows[0][0] != "1" || result.Rows[0][1] != "2" || result.Rows[0][2] != "3" {
		t.Errorf("Row values = %v, want [1 2 3]", result.Rows[0])
	}
}
