// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package dbmanager

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// QueryResult contains the result of a SQL query execution.
type QueryResult struct {
	Columns      []string
	Rows         [][]string
	RowsAffected int64
	ExecutionMs  int64
	Error        string
	IsSelect     bool
}

// QueryHistoryItem represents a single query execution in history.
type QueryHistoryItem struct {
	ID            int64
	Query         string
	UserID        int64
	ExecutedAt    time.Time
	RowsAffected  int
	ExecutionTime int
	Error         sql.NullString
}

// DashboardData contains data for the database manager dashboard template.
type DashboardData struct {
	Query   string
	Result  *QueryResult
	History []QueryHistoryItem
}

// renderDashboard renders the database manager dashboard with the given data.
func (m *Module) renderDashboard(w http.ResponseWriter, r *http.Request, lang string, user *store.User, data DashboardData) {
	if err := m.ctx.Render.Render(w, r, "admin/module_dbmanager", render.TemplateData{
		Title: i18n.T(lang, "dbmanager.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "dbmanager.title"), URL: "/admin/dbmanager", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleDashboard handles GET /admin/dbmanager - shows the database manager dashboard.
func (m *Module) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil || user.Role != middleware.RoleAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	lang := m.ctx.Render.GetAdminLang(r)

	history, err := m.getQueryHistory(r.Context(), 10)
	if err != nil {
		m.ctx.Logger.Error("failed to get query history", "error", err)
	}

	data := DashboardData{
		History: history,
	}

	m.renderDashboard(w, r, lang, user, data)
}

// handleExecute handles POST /admin/dbmanager/execute - executes a SQL query.
func (m *Module) handleExecute(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil || user.Role != middleware.RoleAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if middleware.IsDemoMode() {
		m.setFlashAndRedirect(w, r, "error",
			middleware.DemoModeMessageDetailed(middleware.RestrictionSQLExecution))
		return
	}

	lang := m.ctx.Render.GetAdminLang(r)

	query := strings.TrimSpace(r.FormValue("query"))
	if query == "" {
		m.setFlashAndRedirect(w, r, "error", i18n.T(lang, "dbmanager.error_empty_query"))
		return
	}

	m.ctx.Logger.Info("executing SQL query",
		"user", user.Email,
		"query_length", len(query))

	result := m.executeQuery(r.Context(), query, user.ID)

	history, err := m.getQueryHistory(r.Context(), 10)
	if err != nil {
		m.ctx.Logger.Error("failed to get query history", "error", err)
	}

	data := DashboardData{
		Query:   query,
		Result:  result,
		History: history,
	}

	m.renderDashboard(w, r, lang, user, data)
}

// executeQuery executes a SQL query and returns the result.
func (m *Module) executeQuery(ctx context.Context, query string, userID int64) *QueryResult {
	start := time.Now()
	result := &QueryResult{
		IsSelect: isSelectQuery(query),
	}

	if result.IsSelect {
		rows, err := m.ctx.DB.QueryContext(ctx, query)
		if err != nil {
			result.Error = err.Error()
			m.logQueryExecution(ctx, query, userID, 0, time.Since(start), err)
			return result
		}
		defer func() { _ = rows.Close() }()

		columns, err := rows.Columns()
		if err != nil {
			result.Error = err.Error()
			m.logQueryExecution(ctx, query, userID, 0, time.Since(start), err)
			return result
		}
		result.Columns = columns

		var resultRows [][]string
		for rows.Next() {
			values := make([]any, len(columns))
			valuePtrs := make([]any, len(columns))
			for i := range values {
				valuePtrs[i] = &values[i]
			}

			if err := rows.Scan(valuePtrs...); err != nil {
				result.Error = err.Error()
				m.logQueryExecution(ctx, query, userID, 0, time.Since(start), err)
				return result
			}

			row := make([]string, len(columns))
			for i, v := range values {
				row[i] = formatValue(v)
			}
			resultRows = append(resultRows, row)
		}

		if err := rows.Err(); err != nil {
			result.Error = err.Error()
			m.logQueryExecution(ctx, query, userID, 0, time.Since(start), err)
			return result
		}

		result.Rows = resultRows
		result.RowsAffected = int64(len(resultRows))
	} else {
		res, err := m.ctx.DB.ExecContext(ctx, query)
		if err != nil {
			result.Error = err.Error()
			m.logQueryExecution(ctx, query, userID, 0, time.Since(start), err)
			return result
		}

		affected, _ := res.RowsAffected()
		result.RowsAffected = affected
	}

	result.ExecutionMs = time.Since(start).Milliseconds()
	m.logQueryExecution(ctx, query, userID, int(result.RowsAffected), time.Since(start), nil)

	return result
}

// logQueryExecution logs a query execution to the history table.
func (m *Module) logQueryExecution(ctx context.Context, query string, userID int64, rowsAffected int, duration time.Duration, execErr error) {
	var errStr sql.NullString
	if execErr != nil {
		errStr = sql.NullString{String: execErr.Error(), Valid: true}
	}

	_, err := m.ctx.DB.ExecContext(ctx, `
		INSERT INTO dbmanager_query_history (query, user_id, rows_affected, execution_time_ms, error)
		VALUES (?, ?, ?, ?, ?)
	`, query, userID, rowsAffected, duration.Milliseconds(), errStr)
	if err != nil {
		m.ctx.Logger.Error("failed to log query execution", "error", err)
	}
}

// getQueryHistory retrieves the most recent query executions.
func (m *Module) getQueryHistory(ctx context.Context, limit int) ([]QueryHistoryItem, error) {
	rows, err := m.ctx.DB.QueryContext(ctx, `
		SELECT id, query, user_id, executed_at, rows_affected, execution_time_ms, error
		FROM dbmanager_query_history
		ORDER BY executed_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var history []QueryHistoryItem
	for rows.Next() {
		var item QueryHistoryItem
		if err := rows.Scan(
			&item.ID,
			&item.Query,
			&item.UserID,
			&item.ExecutedAt,
			&item.RowsAffected,
			&item.ExecutionTime,
			&item.Error,
		); err != nil {
			return nil, err
		}
		history = append(history, item)
	}

	return history, rows.Err()
}

// setFlashAndRedirect sets a flash message and redirects to the dashboard.
func (m *Module) setFlashAndRedirect(w http.ResponseWriter, r *http.Request, msgType, message string) {
	m.ctx.Render.SetFlash(r, message, msgType)
	http.Redirect(w, r, "/admin/dbmanager", http.StatusSeeOther)
}

// isSelectQuery checks if the query is a SELECT statement.
func isSelectQuery(query string) bool {
	q := strings.TrimSpace(strings.ToUpper(query))
	return strings.HasPrefix(q, "SELECT") ||
		strings.HasPrefix(q, "PRAGMA") ||
		strings.HasPrefix(q, "EXPLAIN") ||
		strings.HasPrefix(q, "WITH")
}

// formatValue converts a database value to a string representation.
func formatValue(v any) string {
	if v == nil {
		return "NULL"
	}

	switch val := v.(type) {
	case []byte:
		return string(val)
	case time.Time:
		return val.Format("2006-01-02 15:04:05")
	default:
		return strings.ReplaceAll(strings.TrimSpace(
			strings.ReplaceAll(
				strings.ReplaceAll(
					strings.ReplaceAll(
						formatAny(v), "\n", " ",
					), "\r", "",
				), "\t", " ",
			),
		), "  ", " ")
	}
}

// formatAny formats any value as a string.
func formatAny(v any) string {
	switch val := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if val {
			return "true"
		}
		return "false"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return strings.TrimSpace(strings.ReplaceAll(
			strings.ReplaceAll(
				strings.ReplaceAll(
					formatInt(v), "\n", " ",
				), "\r", "",
			), "\t", " ",
		))
	case float32, float64:
		return strings.TrimSpace(strings.ReplaceAll(
			strings.ReplaceAll(
				strings.ReplaceAll(
					formatFloat(v), "\n", " ",
				), "\r", "",
			), "\t", " ",
		))
	case string:
		return val
	case []byte:
		return string(val)
	case time.Time:
		return val.Format("2006-01-02 15:04:05")
	default:
		return strings.TrimSpace(strings.ReplaceAll(
			strings.ReplaceAll(
				strings.ReplaceAll(
					formatDefault(v), "\n", " ",
				), "\r", "",
			), "\t", " ",
		))
	}
}

func formatInt(v any) string {
	switch val := v.(type) {
	case int:
		return intToString(int64(val))
	case int8:
		return intToString(int64(val))
	case int16:
		return intToString(int64(val))
	case int32:
		return intToString(int64(val))
	case int64:
		return intToString(val)
	case uint:
		return uintToString(uint64(val))
	case uint8:
		return uintToString(uint64(val))
	case uint16:
		return uintToString(uint64(val))
	case uint32:
		return uintToString(uint64(val))
	case uint64:
		return uintToString(val)
	default:
		return ""
	}
}

func intToString(v int64) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var buf [20]byte
	i := len(buf) - 1
	for v > 0 {
		buf[i] = byte('0' + v%10)
		v /= 10
		i--
	}
	if negative {
		buf[i] = '-'
		i--
	}
	return string(buf[i+1:])
}

func uintToString(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf) - 1
	for v > 0 {
		buf[i] = byte('0' + v%10)
		v /= 10
		i--
	}
	return string(buf[i+1:])
}

func formatFloat(v any) string {
	switch val := v.(type) {
	case float32:
		return floatToString(float64(val))
	case float64:
		return floatToString(val)
	default:
		return ""
	}
}

func floatToString(v float64) string {
	// Simple float formatting without importing strconv
	intPart := int64(v)
	fracPart := v - float64(intPart)
	if fracPart < 0 {
		fracPart = -fracPart
	}

	result := intToString(intPart)
	if fracPart > 0.000001 {
		result += "."
		fracPart *= 1000000
		fracStr := uintToString(uint64(fracPart))
		// Pad with leading zeros
		for len(fracStr) < 6 {
			fracStr = "0" + fracStr
		}
		// Trim trailing zeros
		fracStr = strings.TrimRight(fracStr, "0")
		result += fracStr
	}
	return result
}

func formatDefault(v any) string {
	// Use type assertion for common types
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	default:
		// For other types, return a type description
		return "[complex value]"
	}
}
