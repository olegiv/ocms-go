// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package embed

import (
	"strings"
	"testing"
)

func TestValidateDifyIdentifier(t *testing.T) {
	t.Run("valid identifier", func(t *testing.T) {
		err := validateDifyIdentifier("user_123-abc@example.com", maxDifyUserIDLen, "user")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("missing identifier", func(t *testing.T) {
		err := validateDifyIdentifier("", maxDifyUserIDLen, "user")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("too long identifier", func(t *testing.T) {
		err := validateDifyIdentifier(strings.Repeat("a", maxDifyUserIDLen+1), maxDifyUserIDLen, "user")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid characters", func(t *testing.T) {
		err := validateDifyIdentifier("user with spaces", maxDifyUserIDLen, "user")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestExtractAndValidateDifyChatUser(t *testing.T) {
	t.Run("valid body", func(t *testing.T) {
		user, err := extractAndValidateDifyChatUser([]byte(`{"user":"user-1","query":"hello"}`))
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if user != "user-1" {
			t.Fatalf("user = %q, want %q", user, "user-1")
		}
	})

	t.Run("missing user", func(t *testing.T) {
		_, err := extractAndValidateDifyChatUser([]byte(`{"query":"hello"}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid user format", func(t *testing.T) {
		_, err := extractAndValidateDifyChatUser([]byte(`{"user":"bad user","query":"hello"}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("query too long", func(t *testing.T) {
		body := `{"user":"user-1","query":"` + strings.Repeat("x", maxDifyQueryLen+1) + `"}`
		_, err := extractAndValidateDifyChatUser([]byte(body))
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
