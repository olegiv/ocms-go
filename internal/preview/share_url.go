// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

// ShareURL holds a bearer URL for an issuance response. The pointer keeps the
// bearer out of fmt diagnostics even when formatting skips methods on private
// fields. Only Reveal returns the raw URL.
type ShareURL struct {
	raw *string
}

// Reveal returns the URL for delivery to the issuer.
func (u ShareURL) Reveal() string {
	if u.raw == nil {
		return ""
	}
	return *u.raw
}

func (ShareURL) String() string { return "[REDACTED]" }

// Format also covers Go-syntax, numeric and hexadecimal verbs, which must not
// expose the underlying string when a diagnostic uses an unexpected format.
func (u ShareURL) Format(state fmt.State, _ rune) {
	// fmt.Formatter cannot return a write error to the formatting caller.
	_, _ = io.WriteString(state, u.String())
}

func (u ShareURL) LogValue() slog.Value { return slog.StringValue(u.String()) }

// MarshalJSON redacts the bearer even when nested in a JSON diagnostic.
func (u ShareURL) MarshalJSON() ([]byte, error) {
	if u.raw == nil {
		return json.Marshal("")
	}
	return json.Marshal(u.String())
}

// LogValue keeps the issuance metadata available in structured diagnostics.
func (l Link) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int64("page_id", l.PageID),
		slog.Int64("revision", l.Revision),
		slog.Any("url", l.URL),
		slog.Time("expires_at", l.ExpiresAt),
		slog.String("warning", l.Warning),
	)
}
