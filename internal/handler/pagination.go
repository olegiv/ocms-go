package handler

import (
	"fmt"
	"net/url"
	"strings"
)

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
// baseURL is the path without query string (e.g., "/admin/events")
// queryParams are the current query parameters to preserve (e.g., filters)
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

	// Build page links (show max 5 pages around current with ellipsis)
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
		pagination.Pages = append(pagination.Pages, AdminPaginationPage{
			Number:    1,
			URL:       buildURL(1),
			IsCurrent: false,
		})
		if start > 2 {
			pagination.Pages = append(pagination.Pages, AdminPaginationPage{
				IsEllipsis: true,
			})
		}
	}

	// Add page numbers
	for i := start; i <= end; i++ {
		pagination.Pages = append(pagination.Pages, AdminPaginationPage{
			Number:    i,
			URL:       buildURL(i),
			IsCurrent: i == currentPage,
		})
	}

	// Add ellipsis and last page if needed
	if end < totalPages {
		if end < totalPages-1 {
			pagination.Pages = append(pagination.Pages, AdminPaginationPage{
				IsEllipsis: true,
			})
		}
		pagination.Pages = append(pagination.Pages, AdminPaginationPage{
			Number:    totalPages,
			URL:       buildURL(totalPages),
			IsCurrent: false,
		})
	}

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
