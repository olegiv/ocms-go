// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package preview

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

// ShareURL redacts formatting and structured logging. Explicit string
// conversion or JSON serialization reveals the bearer for issuance responses.
type ShareURL string

func (ShareURL) String() string { return "[REDACTED]" }

// Format also covers Go-syntax, numeric and hexadecimal verbs, which must not
// expose the underlying string when a diagnostic uses an unexpected format.
func (u ShareURL) Format(state fmt.State, _ rune) {
	// fmt.Formatter cannot return a write error to the formatting caller.
	_, _ = io.WriteString(state, u.String())
}

func (u ShareURL) LogValue() slog.Value { return slog.StringValue(u.String()) }

// MarshalJSON preserves the URL in the explicitly requested issuance response.
func (u ShareURL) MarshalJSON() ([]byte, error) { return json.Marshal(string(u)) }

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
