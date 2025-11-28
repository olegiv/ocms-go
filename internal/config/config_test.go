package config

import (
	"os"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear environment and set only required var
	os.Clearenv()
	os.Setenv("OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!")

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
	os.Setenv("OCMS_SESSION_SECRET", "my-secret")
	os.Setenv("OCMS_DB_PATH", "/custom/path.db")
	os.Setenv("OCMS_SERVER_HOST", "0.0.0.0")
	os.Setenv("OCMS_SERVER_PORT", "3000")
	os.Setenv("OCMS_ENV", "production")
	os.Setenv("OCMS_LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.SessionSecret != "my-secret" {
		t.Errorf("SessionSecret = %q, want %q", cfg.SessionSecret, "my-secret")
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
