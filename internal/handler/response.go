package handler

import (
	"log/slog"
	"net/http"

	"ocms-go/internal/render"
)

// flashAndRedirect sets a flash message and redirects to the given URL.
// Uses http.StatusSeeOther (303) for POST/PUT/DELETE redirects.
func flashAndRedirect(w http.ResponseWriter, r *http.Request, renderer *render.Renderer, url, message, messageType string) {
	renderer.SetFlash(r, message, messageType)
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// flashError sets an error flash message and redirects to the given URL.
func flashError(w http.ResponseWriter, r *http.Request, renderer *render.Renderer, url, message string) {
	flashAndRedirect(w, r, renderer, url, message, "error")
}

// flashSuccess sets a success flash message and redirects to the given URL.
func flashSuccess(w http.ResponseWriter, r *http.Request, renderer *render.Renderer, url, message string) {
	flashAndRedirect(w, r, renderer, url, message, "success")
}

// parseFormOrRedirect parses the request form and redirects with an error message on failure.
// Returns true if parsing succeeded, false if it failed (and redirect was performed).
func parseFormOrRedirect(w http.ResponseWriter, r *http.Request, renderer *render.Renderer, redirectURL string) bool {
	if err := r.ParseForm(); err != nil {
		flashError(w, r, renderer, redirectURL, "Invalid form data")
		return false
	}
	return true
}

// logAndHTTPError logs an error and writes an HTTP error response.
func logAndHTTPError(w http.ResponseWriter, message string, statusCode int, logMsg string, args ...any) {
	slog.Error(logMsg, args...)
	http.Error(w, message, statusCode)
}

// logAndInternalError logs an error and writes a 500 Internal Server Error response.
func logAndInternalError(w http.ResponseWriter, logMsg string, args ...any) {
	logAndHTTPError(w, "Internal Server Error", http.StatusInternalServerError, logMsg, args...)
}
