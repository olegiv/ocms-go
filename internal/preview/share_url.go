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
// fields. Reveal and JSON marshaling return the raw URL.
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

// MarshalJSON preserves the URL in the explicitly requested issuance response.
func (u ShareURL) MarshalJSON() ([]byte, error) { return json.Marshal(u.Reveal()) }

// UnmarshalJSON decodes the string in an issuance response.
func (u *ShareURL) UnmarshalJSON(data []byte) error {
	var raw *string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw != nil {
		u.raw = raw
	}
	return nil
}

// LogValue prevents slog's JSON fallback from serializing the bearer when the
// entire issuance result is logged. Deliberate JSON serialization still exposes
// it for the response and must not be used as a logging representation.
func (l Link) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int64("page_id", l.PageID),
		slog.Int64("revision", l.Revision),
		slog.Any("url", l.URL),
		slog.Time("expires_at", l.ExpiresAt),
		slog.String("warning", l.Warning),
	)
}
