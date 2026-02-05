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
		{"31_bytes", "1234567890123456789012345678901"}, // 31 bytes
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Clearenv()
			setEnv(t, "OCMS_SESSION_SECRET", tt.secret)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() should fail with %d-byte secret", len(tt.secret))
			}
		})
	}
}

func TestLoad_SessionSecretMinimumLength(t *testing.T) {
	os.Clearenv()
	// Exactly 32 bytes should work
	secret32 := "12345678901234567890123456789012"
	setEnv(t, "OCMS_SESSION_SECRET", secret32)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should succeed with 32-byte secret: %v", err)
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

func TestConfig_GeoIPEnabled(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		enabled bool
	}{
		{"empty path", "", false},
		{"path set", "/path/to/GeoLite2-Country.mmdb", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{GeoIPDBPath: tt.path}
			if got := cfg.GeoIPEnabled(); got != tt.enabled {
				t.Errorf("GeoIPEnabled() = %v, want %v", got, tt.enabled)
			}
		})
	}
}

func TestLoad_GeoIPDBPath(t *testing.T) {
	os.Clearenv()
	setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
	setEnv(t, "OCMS_GEOIP_DB_PATH", "/path/to/GeoLite2-Country.mmdb")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.GeoIPDBPath != "/path/to/GeoLite2-Country.mmdb" {
		t.Errorf("GeoIPDBPath = %q, want %q", cfg.GeoIPDBPath, "/path/to/GeoLite2-Country.mmdb")
	}
	if !cfg.GeoIPEnabled() {
		t.Error("GeoIPEnabled() = false, want true")
	}
}

func TestLoad_UploadsDir(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		if cfg.UploadsDir != "./uploads" {
			t.Errorf("UploadsDir = %q, want %q", cfg.UploadsDir, "./uploads")
		}
	})

	t.Run("custom_value", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
		setEnv(t, "OCMS_UPLOADS_DIR", "/var/www/uploads")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		if cfg.UploadsDir != "/var/www/uploads" {
			t.Errorf("UploadsDir = %q, want %q", cfg.UploadsDir, "/var/www/uploads")
		}
	})
}

func TestLoad_CustomDir(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		if cfg.CustomDir != "./custom" {
			t.Errorf("CustomDir = %q, want %q", cfg.CustomDir, "./custom")
		}
	})

	t.Run("custom_value", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
		setEnv(t, "OCMS_CUSTOM_DIR", "/var/www/custom")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		if cfg.CustomDir != "/var/www/custom" {
			t.Errorf("CustomDir = %q, want %q", cfg.CustomDir, "/var/www/custom")
		}
	})
}

