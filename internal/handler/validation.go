package handler

import (
	"log/slog"

	"ocms-go/internal/util"
)

// SlugExistsFunc is a function type for checking if a slug exists.
// Returns count of matching slugs and any error.
type SlugExistsFunc func() (int64, error)

// ValidateSlugWithChecker validates a slug using a custom existence checker.
// Returns an error message string if validation fails, or empty string if valid.
func ValidateSlugWithChecker(slug string, checkExists SlugExistsFunc) string {
	if slug == "" {
		return "Slug is required"
	}
	if !util.IsValidSlug(slug) {
		return "Invalid slug format (use lowercase letters, numbers, and hyphens)"
	}
	exists, err := checkExists()
	if err != nil {
		slog.Error("database error checking slug", "error", err)
		return "Error checking slug"
	}
	if exists != 0 {
		return "Slug already exists"
	}
	return ""
}

// ValidateSlugForUpdate validates a slug for update operations.
// Skips validation if the slug hasn't changed from the current value.
func ValidateSlugForUpdate(slug, currentSlug string, checkExists SlugExistsFunc) string {
	if slug == currentSlug {
		return "" // No change, no validation needed
	}
	return ValidateSlugWithChecker(slug, checkExists)
}

// ValidateSlugFormat validates only the slug format without checking existence.
// Use this when uniqueness checking is not required.
func ValidateSlugFormat(slug string) string {
	if slug == "" {
		return "Slug is required"
	}
	if !util.IsValidSlug(slug) {
		return "Invalid slug format"
	}
	return ""
}
