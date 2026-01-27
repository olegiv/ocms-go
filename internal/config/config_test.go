// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"testing"
)

func setEnv(t *testing.T, key, value string) {
	t.Helper()
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("failed to set %s: %v", key, err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	// Clear environment and set only required var
	os.Clearenv()
	setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Check defaults
	if cfg.DBPath != "./data/ocms.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "./data/ocms.db")
	}
	if cfg.ServerHost != "localhost" {
		t.Errorf("ServerHost = %q, want %q", cfg.ServerHost, "localhost")
	}
	if cfg.ServerPort != 8080 {
		t.Errorf("ServerPort = %d, want %d", cfg.ServerPort, 8080)
	}
	if cfg.Env != "development" {
		t.Errorf("Env = %q, want %q", cfg.Env, "development")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
}

func TestLoad_CustomValues(t *testing.T) {
	os.Clearenv()
	customSecret := "custom-secret-key-32-bytes-long!"
	setEnv(t, "OCMS_SESSION_SECRET", customSecret)
	setEnv(t, "OCMS_DB_PATH", "/custom/path.db")
	setEnv(t, "OCMS_SERVER_HOST", "0.0.0.0")
	setEnv(t, "OCMS_SERVER_PORT", "3000")
	setEnv(t, "OCMS_ENV", "production")
	setEnv(t, "OCMS_LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.SessionSecret != customSecret {
		t.Errorf("SessionSecret = %q, want %q", cfg.SessionSecret, customSecret)
	}
	if cfg.DBPath != "/custom/path.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/custom/path.db")
	}
	if cfg.ServerHost != "0.0.0.0" {
		t.Errorf("ServerHost = %q, want %q", cfg.ServerHost, "0.0.0.0")
	}
	if cfg.ServerPort != 3000 {
		t.Errorf("ServerPort = %d, want %d", cfg.ServerPort, 3000)
	}
	if cfg.Env != "production" {
		t.Errorf("Env = %q, want %q", cfg.Env, "production")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestLoad_RequiredSessionSecret(t *testing.T) {
	os.Clearenv()
	// Don't set OCMS_SESSION_SECRET

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when OCMS_SESSION_SECRET is not set")
	}
}

func TestLoad_SessionSecretTooShort(t *testing.T) {
	tests := []struct {
		name   string
		secret string
	}{
		{"empty", ""},
		{"short", "short"},
		{"15_bytes", "123456789012345"}, // 15 bytes (below dev minimum of 16)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Clearenv()
			setEnv(t, "OCMS_SESSION_SECRET", tt.secret)
			// Default env is development

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() should fail with %d-byte secret", len(tt.secret))
			}
		})
	}
}

func TestLoad_SessionSecretMinimumLength_Dev(t *testing.T) {
	os.Clearenv()
	// Exactly 16 bytes should work in development
	secret16 := "1234567890123456"
	setEnv(t, "OCMS_SESSION_SECRET", secret16)
	// OCMS_ENV defaults to "development"

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should succeed with 16-byte secret in dev: %v", err)
	}
	if cfg.SessionSecret != secret16 {
		t.Errorf("SessionSecret = %q, want %q", cfg.SessionSecret, secret16)
	}
}

func TestLoad_SessionSecretMinimumLength_Prod(t *testing.T) {
	os.Clearenv()
	// 31 bytes should fail in production
	secret31 := "1234567890123456789012345678901"
	setEnv(t, "OCMS_SESSION_SECRET", secret31)
	setEnv(t, "OCMS_ENV", "production")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail with 31-byte secret in production")
	}

	// 32 bytes should work in production
	os.Clearenv()
	secret32 := "12345678901234567890123456789012"
	setEnv(t, "OCMS_SESSION_SECRET", secret32)
	setEnv(t, "OCMS_ENV", "production")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should succeed with 32-byte secret in production: %v", err)
	}
	if cfg.SessionSecret != secret32 {
		t.Errorf("SessionSecret = %q, want %q", cfg.SessionSecret, secret32)
	}
}

func TestConfig_IsDevelopment(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"development", true},
		{"production", false},
		{"staging", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			cfg := Config{Env: tt.env}
			if got := cfg.IsDevelopment(); got != tt.want {
				t.Errorf("IsDevelopment() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_ServerAddr(t *testing.T) {
	tests := []struct {
		host string
		port int
		want string
	}{
		{"localhost", 8080, "localhost:8080"},
		{"0.0.0.0", 3000, "0.0.0.0:3000"},
		{"127.0.0.1", 443, "127.0.0.1:443"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			cfg := Config{ServerHost: tt.host, ServerPort: tt.port}
			if got := cfg.ServerAddr(); got != tt.want {
				t.Errorf("ServerAddr() = %q, want %q", got, tt.want)
			}
		})
	}
}
