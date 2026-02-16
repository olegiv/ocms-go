// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"testing"
)

func setEnv(t *testing.T, key, value string) {
	t.Helper()
	t.Setenv(key, value)
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
	if cfg.RequireFormCaptcha {
		t.Error("RequireFormCaptcha = true, want false")
	}
	if cfg.WebhookFormDataMode != "redacted" {
		t.Errorf("WebhookFormDataMode = %q, want %q", cfg.WebhookFormDataMode, "redacted")
	}
	if cfg.RequireWebhookFormDataMinimization {
		t.Error("RequireWebhookFormDataMinimization = true, want false")
	}
	if cfg.RequireTrustedProxies {
		t.Error("RequireTrustedProxies = true, want false")
	}
	if cfg.APIAllowedCIDRs != "" {
		t.Errorf("APIAllowedCIDRs = %q, want empty", cfg.APIAllowedCIDRs)
	}
	if cfg.RequireAPIAllowedCIDRs {
		t.Error("RequireAPIAllowedCIDRs = true, want false")
	}
	if cfg.RequireAPIKeyExpiry {
		t.Error("RequireAPIKeyExpiry = true, want false")
	}
	if cfg.RequireAPIKeySourceCIDRs {
		t.Error("RequireAPIKeySourceCIDRs = true, want false")
	}
	if cfg.RevokeAPIKeyOnSourceIPChange {
		t.Error("RevokeAPIKeyOnSourceIPChange = true, want false")
	}
	if cfg.APIKeyMaxTTLDays != 0 {
		t.Errorf("APIKeyMaxTTLDays = %d, want 0", cfg.APIKeyMaxTTLDays)
	}
	if cfg.EmbedAllowedOrigins != "" {
		t.Errorf("EmbedAllowedOrigins = %q, want empty", cfg.EmbedAllowedOrigins)
	}
	if cfg.EmbedAllowedUpstreamHosts != "" {
		t.Errorf("EmbedAllowedUpstreamHosts = %q, want empty", cfg.EmbedAllowedUpstreamHosts)
	}
	if cfg.RequireEmbedAllowedOrigins {
		t.Error("RequireEmbedAllowedOrigins = true, want false")
	}
	if cfg.RequireEmbedProxyToken {
		t.Error("RequireEmbedProxyToken = true, want false")
	}
	if cfg.EmbedProxyToken != "" {
		t.Errorf("EmbedProxyToken = %q, want empty", cfg.EmbedProxyToken)
	}
	if cfg.RequireHTTPSOutbound {
		t.Error("RequireHTTPSOutbound = true, want false")
	}
	if cfg.SanitizePageHTML {
		t.Error("SanitizePageHTML = true, want false")
	}
	if cfg.RequireSanitizePageHTML {
		t.Error("RequireSanitizePageHTML = true, want false")
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
	setEnv(t, "OCMS_TRUSTED_PROXIES", "127.0.0.1/32,10.0.0.0/8")
	setEnv(t, "OCMS_REQUIRE_TRUSTED_PROXIES", "true")
	setEnv(t, "OCMS_REQUIRE_FORM_CAPTCHA", "true")
	setEnv(t, "OCMS_WEBHOOK_FORM_DATA_MODE", "none")
	setEnv(t, "OCMS_REQUIRE_WEBHOOK_FORM_DATA_MINIMIZATION", "true")
	setEnv(t, "OCMS_API_ALLOWED_CIDRS", "10.0.0.0/8,192.168.1.10")
	setEnv(t, "OCMS_REQUIRE_API_ALLOWED_CIDRS", "true")
	setEnv(t, "OCMS_REQUIRE_API_KEY_EXPIRY", "true")
	setEnv(t, "OCMS_REQUIRE_API_KEY_SOURCE_CIDRS", "true")
	setEnv(t, "OCMS_REVOKE_API_KEY_ON_SOURCE_IP_CHANGE", "true")
	setEnv(t, "OCMS_API_KEY_MAX_TTL_DAYS", "90")
	setEnv(t, "OCMS_EMBED_ALLOWED_ORIGINS", "https://example.com,https://app.example.com")
	setEnv(t, "OCMS_EMBED_ALLOWED_UPSTREAM_HOSTS", "api.dify.ai,dify.internal.example")
	setEnv(t, "OCMS_REQUIRE_EMBED_ALLOWED_ORIGINS", "true")
	setEnv(t, "OCMS_EMBED_PROXY_TOKEN", "embed-token-test")
	setEnv(t, "OCMS_REQUIRE_EMBED_PROXY_TOKEN", "true")
	setEnv(t, "OCMS_REQUIRE_HTTPS_OUTBOUND", "true")
	setEnv(t, "OCMS_SANITIZE_PAGE_HTML", "true")
	setEnv(t, "OCMS_REQUIRE_SANITIZE_PAGE_HTML", "true")

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
	if cfg.TrustedProxies != "127.0.0.1/32,10.0.0.0/8" {
		t.Errorf("TrustedProxies = %q, want %q", cfg.TrustedProxies, "127.0.0.1/32,10.0.0.0/8")
	}
	if !cfg.RequireTrustedProxies {
		t.Error("RequireTrustedProxies = false, want true")
	}
	if !cfg.RequireFormCaptcha {
		t.Error("RequireFormCaptcha = false, want true")
	}
	if cfg.WebhookFormDataMode != "none" {
		t.Errorf("WebhookFormDataMode = %q, want %q", cfg.WebhookFormDataMode, "none")
	}
	if !cfg.RequireWebhookFormDataMinimization {
		t.Error("RequireWebhookFormDataMinimization = false, want true")
	}
	if cfg.APIAllowedCIDRs != "10.0.0.0/8,192.168.1.10" {
		t.Errorf("APIAllowedCIDRs = %q, want %q", cfg.APIAllowedCIDRs, "10.0.0.0/8,192.168.1.10")
	}
	if !cfg.RequireAPIAllowedCIDRs {
		t.Error("RequireAPIAllowedCIDRs = false, want true")
	}
	if !cfg.RequireAPIKeyExpiry {
		t.Error("RequireAPIKeyExpiry = false, want true")
	}
	if !cfg.RequireAPIKeySourceCIDRs {
		t.Error("RequireAPIKeySourceCIDRs = false, want true")
	}
	if !cfg.RevokeAPIKeyOnSourceIPChange {
		t.Error("RevokeAPIKeyOnSourceIPChange = false, want true")
	}
	if cfg.APIKeyMaxTTLDays != 90 {
		t.Errorf("APIKeyMaxTTLDays = %d, want %d", cfg.APIKeyMaxTTLDays, 90)
	}
	if cfg.EmbedAllowedOrigins != "https://example.com,https://app.example.com" {
		t.Errorf("EmbedAllowedOrigins = %q, want %q", cfg.EmbedAllowedOrigins, "https://example.com,https://app.example.com")
	}
	if cfg.EmbedAllowedUpstreamHosts != "api.dify.ai,dify.internal.example" {
		t.Errorf("EmbedAllowedUpstreamHosts = %q, want %q", cfg.EmbedAllowedUpstreamHosts, "api.dify.ai,dify.internal.example")
	}
	if !cfg.RequireEmbedAllowedOrigins {
		t.Error("RequireEmbedAllowedOrigins = false, want true")
	}
	if cfg.EmbedProxyToken != "embed-token-test" {
		t.Errorf("EmbedProxyToken = %q, want %q", cfg.EmbedProxyToken, "embed-token-test")
	}
	if !cfg.RequireEmbedProxyToken {
		t.Error("RequireEmbedProxyToken = false, want true")
	}
	if !cfg.RequireHTTPSOutbound {
		t.Error("RequireHTTPSOutbound = false, want true")
	}
	if !cfg.SanitizePageHTML {
		t.Error("SanitizePageHTML = false, want true")
	}
	if !cfg.RequireSanitizePageHTML {
		t.Error("RequireSanitizePageHTML = false, want true")
	}
}

func TestLoad_RejectSeedInProduction(t *testing.T) {
	os.Clearenv()
	setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
	setEnv(t, "OCMS_ENV", "production")
	setEnv(t, "OCMS_DO_SEED", "true")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when OCMS_DO_SEED=true in production")
	}
}

func TestLoad_RequireSanitizePageHTMLInProduction(t *testing.T) {
	os.Clearenv()
	setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
	setEnv(t, "OCMS_ENV", "production")
	setEnv(t, "OCMS_REQUIRE_SANITIZE_PAGE_HTML", "true")
	setEnv(t, "OCMS_SANITIZE_PAGE_HTML", "false")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when OCMS_REQUIRE_SANITIZE_PAGE_HTML=true and OCMS_SANITIZE_PAGE_HTML=false in production")
	}
}

func TestLoad_RequireAPIAllowedCIDRsInProduction(t *testing.T) {
	os.Clearenv()
	setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
	setEnv(t, "OCMS_ENV", "production")
	setEnv(t, "OCMS_REQUIRE_API_ALLOWED_CIDRS", "true")
	setEnv(t, "OCMS_API_ALLOWED_CIDRS", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when OCMS_REQUIRE_API_ALLOWED_CIDRS=true and OCMS_API_ALLOWED_CIDRS is empty in production")
	}
}

func TestLoad_RequireTrustedProxiesInProduction(t *testing.T) {
	os.Clearenv()
	setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
	setEnv(t, "OCMS_ENV", "production")
	setEnv(t, "OCMS_REQUIRE_TRUSTED_PROXIES", "true")
	setEnv(t, "OCMS_TRUSTED_PROXIES", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when OCMS_REQUIRE_TRUSTED_PROXIES=true and OCMS_TRUSTED_PROXIES is empty in production")
	}
}

func TestLoad_RequireEmbedProxyTokenInProduction(t *testing.T) {
	os.Clearenv()
	setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
	setEnv(t, "OCMS_ENV", "production")
	setEnv(t, "OCMS_REQUIRE_EMBED_PROXY_TOKEN", "true")
	setEnv(t, "OCMS_EMBED_PROXY_TOKEN", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when OCMS_REQUIRE_EMBED_PROXY_TOKEN=true and OCMS_EMBED_PROXY_TOKEN is empty in production")
	}
}

func TestLoad_InvalidWebhookFormDataMode(t *testing.T) {
	os.Clearenv()
	setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
	setEnv(t, "OCMS_WEBHOOK_FORM_DATA_MODE", "invalid")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when OCMS_WEBHOOK_FORM_DATA_MODE is invalid")
	}
}

func TestLoad_InvalidAPIKeyMaxTTLDays(t *testing.T) {
	os.Clearenv()
	setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
	setEnv(t, "OCMS_API_KEY_MAX_TTL_DAYS", "-1")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when OCMS_API_KEY_MAX_TTL_DAYS is negative")
	}
}

func TestLoad_RequireWebhookFormDataMinimizationInProduction(t *testing.T) {
	os.Clearenv()
	setEnv(t, "OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
	setEnv(t, "OCMS_ENV", "production")
	setEnv(t, "OCMS_WEBHOOK_FORM_DATA_MODE", "full")
	setEnv(t, "OCMS_REQUIRE_WEBHOOK_FORM_DATA_MINIMIZATION", "true")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when OCMS_WEBHOOK_FORM_DATA_MODE=full and OCMS_REQUIRE_WEBHOOK_FORM_DATA_MINIMIZATION=true in production")
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

func TestLoad_RejectsKnownWeakSecrets(t *testing.T) {
	for _, weak := range knownWeakSecrets {
		t.Run(weak[:16], func(t *testing.T) {
			os.Clearenv()
			setEnv(t, "OCMS_SESSION_SECRET", weak)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() should reject known weak secret")
			}
		})
	}
}

func TestHasMinimumEntropy(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"all lowercase", "abcdefghijklmnopqrstuvwxyz123456", false},
		{"lower+upper only", "abcdefGHIJKLmnopQRSTuvwx12345678", true},
		{"base64 output", "K7gNU3sdo+OL0wNhqoVWhr3g6s1xYv72ol/pe/Unols=", true},
		{"all same char", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
		{"mixed good secret", "MyS3cr3t!K3y-F0r_Pr0duct10n!!!", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasMinimumEntropy(tt.s); got != tt.want {
				t.Errorf("hasMinimumEntropy(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
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
