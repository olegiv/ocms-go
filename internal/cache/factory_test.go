// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import "testing"

func TestSanitizeRedisURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "no password",
			url:  "redis://localhost:6379/0",
			want: "redis://localhost:6379/0",
		},
		{
			name: "with password only",
			url:  "redis://:secret@localhost:6379/0",
			want: "redis://:%2A%2A%2A@localhost:6379/0",
		},
		{
			name: "with user and password",
			url:  "redis://admin:secret@redis.example.com:6379/1",
			want: "redis://admin:%2A%2A%2A@redis.example.com:6379/1",
		},
		{
			name: "invalid url",
			url:  "://bad",
			want: "[invalid URL]",
		},
		{
			name: "empty string",
			url:  "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeRedisURL(tt.url)
			if got != tt.want {
				t.Errorf("SanitizeRedisURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}
