// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Pagination holds pagination data for frontend templates.
type Pagination struct {
	CurrentPage int
	TotalPages  int
	TotalItems  int64
	PerPage     int
	HasPrev     bool
	HasNext     bool
	HasFirst    bool
	HasLast     bool
	PrevURL     string
	NextURL     string
	FirstURL    string
	LastURL     string
	Pages       []PaginationPage
}

// PaginationPage represents a single page link in pagination.
type PaginationPage struct {
	Number     int
	URL        string
	IsCurrent  bool
	IsEllipsis bool
}

// AdminPagination holds pagination data for admin templates.
type AdminPagination struct {
	CurrentPage int
	TotalPages  int
	TotalItems  int64
	PerPage     int
	HasFirst    bool
	HasPrev     bool
	HasNext     bool
	HasLast     bool
	FirstPage   int
	PrevPage    int
	NextPage    int
	LastPage    int
	Pages       []AdminPaginationPage
	BaseURL     string
	QueryString string
}

// AdminPaginationPage represents a single page link in admin pagination.
type AdminPaginationPage struct {
	Number     int
	URL        string
	IsCurrent  bool
	IsEllipsis bool
}

// BuildAdminPagination creates pagination data for admin templates.
// baseURL is the path without query string (e.g., "/admin/events").
// queryParams are the current query parameters to preserve (e.g., filters).
func BuildAdminPagination(currentPage, totalItems, perPage int, baseURL string, queryParams url.Values) AdminPagination {
	totalPages := (totalItems + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	pagination := AdminPagination{
		CurrentPage: currentPage,
		TotalPages:  totalPages,
		TotalItems:  int64(totalItems),
		PerPage:     perPage,
		HasFirst:    currentPage > 1,
		HasPrev:     currentPage > 1,
		HasNext:     currentPage < totalPages,
		HasLast:     currentPage < totalPages,
		FirstPage:   1,
		PrevPage:    currentPage - 1,
		NextPage:    currentPage + 1,
		LastPage:    totalPages,
		BaseURL:     baseURL,
	}

	// Build query string without page parameter
	if queryParams != nil {
		params := make(url.Values)
		for k, v := range queryParams {
			if k != "page" && len(v) > 0 && v[0] != "" {
				params[k] = v
			}
		}
		if len(params) > 0 {
			pagination.QueryString = params.Encode()
		}
	}

	// Build URL helper
	buildURL := func(page int) string {
		if pagination.QueryString != "" {
			return fmt.Sprintf("%s?%s&page=%d", baseURL, pagination.QueryString, page)
		}
		return fmt.Sprintf("%s?page=%d", baseURL, page)
	}

	// Build page links using shared helper
	pagination.Pages = BuildPaginationPages(currentPage, totalPages, buildURL,
		func(number int, pageURL string, isCurrent, isEllipsis bool) AdminPaginationPage {
			return AdminPaginationPage{Number: number, URL: pageURL, IsCurrent: isCurrent, IsEllipsis: isEllipsis}
		})

	return pagination
}

// PageURL returns the URL for a specific page number.
func (p AdminPagination) PageURL(page int) string {
	if p.QueryString != "" {
		return fmt.Sprintf("%s?%s&page=%d", p.BaseURL, p.QueryString, page)
	}
	return fmt.Sprintf("%s?page=%d", p.BaseURL, page)
}

// FirstURL returns the URL for the first page.
func (p AdminPagination) FirstURL() string {
	return p.PageURL(1)
}

// PrevURL returns the URL for the previous page.
func (p AdminPagination) PrevURL() string {
	return p.PageURL(p.PrevPage)
}

// NextURL returns the URL for the next page.
func (p AdminPagination) NextURL() string {
	return p.PageURL(p.NextPage)
}

// LastURL returns the URL for the last page.
func (p AdminPagination) LastURL() string {
	return p.PageURL(p.TotalPages)
}

// ShouldShow returns true if pagination should be displayed (more than 1 page).
func (p AdminPagination) ShouldShow() bool {
	return p.TotalPages > 1
}

// PageRange returns a description of the current page range.
func (p AdminPagination) PageRange() string {
	start := (p.CurrentPage-1)*p.PerPage + 1
	end := p.CurrentPage * p.PerPage
	if end > int(p.TotalItems) {
		end = int(p.TotalItems)
	}
	return strings.TrimSpace(fmt.Sprintf("%d-%d", start, end))
}

// BuildPaginationPages generates page links with ellipsis for any pagination type.
// It shows 5 page numbers centered on the current page, with "..." for gaps,
// and always includes the first and last pages.
func BuildPaginationPages[T any](
	currentPage, totalPages int,
	buildURL func(int) string,
	makePage func(number int, pageURL string, isCurrent, isEllipsis bool) T,
) []T {
	var pages []T

	start := currentPage - 2
	end := currentPage + 2
	if start < 1 {
		start = 1
		end = 5
	}
	if end > totalPages {
		end = totalPages
		start = end - 4
		if start < 1 {
			start = 1
		}
	}

	// Add first page and ellipsis if needed
	if start > 1 {
		pages = append(pages, makePage(1, buildURL(1), false, false))
		if start > 2 {
			pages = append(pages, makePage(0, "", false, true))
		}
	}

	// Add page numbers
	for i := start; i <= end; i++ {
		pages = append(pages, makePage(i, buildURL(i), i == currentPage, false))
	}

	// Add ellipsis and last page if needed
	if end < totalPages {
		if end < totalPages-1 {
			pages = append(pages, makePage(0, "", false, true))
		}
		pages = append(pages, makePage(totalPages, buildURL(totalPages), false, false))
	}

	return pages
}

// CalculateTotalPages calculates the number of pages for the given total items and items per page.
func CalculateTotalPages(totalItems, perPage int) int {
	if perPage <= 0 {
		return 1
	}
	totalPages := (totalItems + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	return totalPages
}

// ClampPage ensures the page number is within the valid range [1, totalPages].
func ClampPage(page, totalPages int) int {
	if page < 1 {
		return 1
	}
	if page > totalPages {
		return totalPages
	}
	return page
}

// NormalizePagination calculates total pages and clamps the current page to a valid range.
// Returns the normalized page number and total pages.
func NormalizePagination(page, totalItems, perPage int) (normalizedPage, totalPages int) {
	totalPages = CalculateTotalPages(totalItems, perPage)
	normalizedPage = ClampPage(page, totalPages)
	return normalizedPage, totalPages
}

// ParsePageParam parses the "page" query parameter from the request.
// Returns 1 if the parameter is missing, empty, or invalid.
func ParsePageParam(r *http.Request) int {
	return ParseIntParam(r, "page", 1, 1, 0)
}

// ParsePerPageParam parses the "per_page" query parameter from the request.
// Returns the default value if the parameter is missing, empty, or invalid.
// The value is clamped to the range [1, maxPerPage].
func ParsePerPageParam(r *http.Request, defaultPerPage, maxPerPage int) int {
	return ParseIntParam(r, "per_page", defaultPerPage, 1, maxPerPage)
}

// ParseIntParam parses an integer query parameter from the request.
// Returns defaultVal if the parameter is missing, empty, or invalid.
// If minVal > 0, values below minVal return defaultVal.
// If maxVal > 0, values above maxVal return defaultVal.
func ParseIntParam(r *http.Request, param string, defaultVal, minVal, maxVal int) int {
	str := r.URL.Query().Get(param)
	if str == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(str)
	if err != nil {
		return defaultVal
	}
	if minVal > 0 && val < minVal {
		return defaultVal
	}
	if maxVal > 0 && val > maxVal {
		return defaultVal
	}
	return val
}

// ParseQueryInt64 parses a named query parameter as a positive int64.
// Returns 0 if the parameter is missing, empty, invalid, or not positive.
func ParseQueryInt64(r *http.Request, name string) int64 {
	str := r.URL.Query().Get(name)
	if str == "" {
		return 0
	}
	val, err := strconv.ParseInt(str, 10, 64)
	if err != nil || val <= 0 {
		return 0
	}
	return val
}
