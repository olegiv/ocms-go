// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package util provides general-purpose utility functions.
package util

import (
	"database/sql"
	"strconv"
)

// NullInt64FromPtr converts a pointer to int64 into sql.NullInt64.
// Returns a valid NullInt64 if the pointer is non-nil, otherwise returns an invalid one.
func NullInt64FromPtr(ptr *int64) sql.NullInt64 {
	if ptr != nil {
		return sql.NullInt64{Int64: *ptr, Valid: true}
	}
	return sql.NullInt64{}
}

// NullInt64FromValue creates a valid sql.NullInt64 from an int64 value.
func NullInt64FromValue(val int64) sql.NullInt64 {
	return sql.NullInt64{Int64: val, Valid: true}
}

// ParseNullInt64 parses a string into sql.NullInt64.
// Returns an invalid NullInt64 if the string is empty, "0", or cannot be parsed.
func ParseNullInt64(s string) sql.NullInt64 {
	if s == "" || s == "0" {
		return sql.NullInt64{}
	}
	if val, err := strconv.ParseInt(s, 10, 64); err == nil {
		return sql.NullInt64{Int64: val, Valid: true}
	}
	return sql.NullInt64{}
}

// ParseNullInt64Positive parses a string into sql.NullInt64, requiring positive values.
// Returns an invalid NullInt64 if the string is empty, cannot be parsed, or value is <= 0.
func ParseNullInt64Positive(s string) sql.NullInt64 {
	if s == "" {
		return sql.NullInt64{}
	}
	if val, err := strconv.ParseInt(s, 10, 64); err == nil && val > 0 {
		return sql.NullInt64{Int64: val, Valid: true}
	}
	return sql.NullInt64{}
}

// NullStringFromValue creates a sql.NullString from a string value.
// Returns a valid NullString if the string is non-empty, otherwise returns an invalid one.
func NullStringFromValue(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// NullStringFromPtr converts a pointer to string into sql.NullString.
// Returns a valid NullString if the pointer is non-nil, otherwise returns an invalid one.
func NullStringFromPtr(ptr *string) sql.NullString {
	if ptr != nil {
		return sql.NullString{String: *ptr, Valid: true}
	}
	return sql.NullString{}
}
