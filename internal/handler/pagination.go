// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/uikit"
)

// Type aliases for uikit pagination types.
// These allow all handler code to continue using handler.AdminPagination etc.
// while the canonical definitions live in the reusable uikit package.
type (
	AdminPagination     = uikit.AdminPagination
	AdminPaginationPage = uikit.AdminPaginationPage
	Pagination          = uikit.Pagination
	PaginationPage      = uikit.PaginationPage
)

// BuildAdminPagination delegates to uikit.BuildAdminPagination.
var BuildAdminPagination = uikit.BuildAdminPagination

// CalculateTotalPages delegates to uikit.CalculateTotalPages.
var CalculateTotalPages = uikit.CalculateTotalPages

// ClampPage delegates to uikit.ClampPage.
var ClampPage = uikit.ClampPage

// NormalizePagination delegates to uikit.NormalizePagination.
var NormalizePagination = uikit.NormalizePagination

// ParsePageParam delegates to uikit.ParsePageParam.
var ParsePageParam = uikit.ParsePageParam

// ParsePerPageParam delegates to uikit.ParsePerPageParam.
var ParsePerPageParam = uikit.ParsePerPageParam

// ParseIntParam delegates to uikit.ParseIntParam.
var ParseIntParam = uikit.ParseIntParam

// ParseQueryInt64 delegates to uikit.ParseQueryInt64.
var ParseQueryInt64 = uikit.ParseQueryInt64

// ParseIDParam parses the "id" URL parameter from the request as int64.
// This is chi-specific and stays in the handler package.
func ParseIDParam(r *http.Request) (int64, error) {
	return ParseURLParamInt64(r, "id")
}

// ParseURLParamInt64 parses a named URL parameter from the request as int64.
// This is chi-specific and stays in the handler package.
func ParseURLParamInt64(r *http.Request, name string) (int64, error) {
	str := chi.URLParam(r, name)
	return strconv.ParseInt(str, 10, 64)
}

// buildPaginationPages delegates to uikit.BuildPaginationPages.
// Used by frontend.go's buildPagination method.
func buildPaginationPages[T any](
	currentPage, totalPages int,
	buildURL func(int) string,
	makePage func(number int, pageURL string, isCurrent, isEllipsis bool) T,
) []T {
	return uikit.BuildPaginationPages(currentPage, totalPages, buildURL, makePage)
}
