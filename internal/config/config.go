// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/caarlos0/env/v11"
)

// knownWeakSecrets contains default/example secrets that must be rejected in production.
var knownWeakSecrets = []string{
	"change-me-to-32-byte-secret-key!",
	"REPLACE_WITH_YOUR_OWN_SECRET_KEY!",
}

// Config holds the application configuration loaded from environment variables.
type Config struct {
	DBPath        string `env:"OCMS_DB_PATH" envDefault:"./data/ocms.db"`
	SessionSecret string `env:"OCMS_SESSION_SECRET,required"`
	ServerHost    string `env:"OCMS_SERVER_HOST" envDefault:"localhost"`
	ServerPort    int    `env:"OCMS_SERVER_PORT" envDefault:"8080"`
	Env           string `env:"OCMS_ENV" envDefault:"development"`
	LogLevel    string `env:"OCMS_LOG_LEVEL" envDefault:"info"`
	CustomDir   string `env:"OCMS_CUSTOM_DIR" envDefault:"./custom"`
	UploadsDir  string `env:"OCMS_UPLOADS_DIR" envDefault:"./uploads"`
	ActiveTheme string `env:"OCMS_ACTIVE_THEME" envDefault:"default"`

	// Cache configuration
	RedisURL     string `env:"OCMS_REDIS_URL"`                         // Optional Redis URL for distributed caching
	CachePrefix  string `env:"OCMS_CACHE_PREFIX" envDefault:"ocms:"`   // Redis key prefix
	CacheTTL     int    `env:"OCMS_CACHE_TTL" envDefault:"3600"`       // Default cache TTL in seconds
	CacheMaxSize int    `env:"OCMS_CACHE_MAX_SIZE" envDefault:"10000"` // Max memory cache entries

	// hCaptcha configuration
	HCaptchaSiteKey   string `env:"OCMS_HCAPTCHA_SITE_KEY"`   // hCaptcha site key
	HCaptchaSecretKey string `env:"OCMS_HCAPTCHA_SECRET_KEY"` // hCaptcha secret key

	// GeoIP configuration
	GeoIPDBPath string `env:"OCMS_GEOIP_DB_PATH"` // Path to GeoLite2-Country.mmdb file

	// Seeding configuration
	DoSeed bool `env:"OCMS_DO_SEED" envDefault:"false"` // Enable database seeding
}

// IsDevelopment returns true if the application is running in development mode.
func (c Config) IsDevelopment() bool {
	return c.Env == "development"
}

// ServerAddr returns the full server address in host:port format.
func (c Config) ServerAddr() string {
	return fmt.Sprintf("%s:%d", c.ServerHost, c.ServerPort)
}

// UseRedisCache returns true if Redis caching is configured.
func (c Config) UseRedisCache() bool {
	return c.RedisURL != ""
}

// HCaptchaEnabled returns true if hCaptcha is configured.
func (c Config) HCaptchaEnabled() bool {
	return c.HCaptchaSiteKey != "" && c.HCaptchaSecretKey != ""
}

// GeoIPEnabled returns true if GeoIP database is configured.
func (c Config) GeoIPEnabled() bool {
	return c.GeoIPDBPath != ""
}

// MinSessionSecretLength is the minimum required length for the session secret.
// AES-256 requires 32 bytes minimum for secure encryption.
const MinSessionSecretLength = 32

// Load parses environment variables and returns a Config struct.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Validate session secret length
	if len(cfg.SessionSecret) < MinSessionSecretLength {
		return nil, fmt.Errorf("OCMS_SESSION_SECRET must be at least %d bytes long, got %d bytes; "+
			"generate a secure secret with: openssl rand -base64 32",
			MinSessionSecretLength, len(cfg.SessionSecret))
	}

	// Reject known weak/default secrets
	for _, weak := range knownWeakSecrets {
		if cfg.SessionSecret == weak {
			return nil, fmt.Errorf("OCMS_SESSION_SECRET is a known default value and must not be used; "+
				"generate a secure secret with: openssl rand -base64 32")
		}
	}

	// Warn about low-entropy secrets
	if !hasMinimumEntropy(cfg.SessionSecret) {
		slog.Warn("OCMS_SESSION_SECRET has low character diversity; " +
			"consider generating a random secret with: openssl rand -base64 32")
	}

	return cfg, nil
}

// hasMinimumEntropy checks that a secret contains at least 3 character classes
// (lowercase, uppercase, digits, special characters).
func hasMinimumEntropy(s string) bool {
	charTypes := 0
	if strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyz") {
		charTypes++
	}
	if strings.ContainsAny(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		charTypes++
	}
	if strings.ContainsAny(s, "0123456789") {
		charTypes++
	}
	if strings.ContainsAny(s, "!@#$%^&*()-_=+[]{}|;:,.<>?/~`'\"\\") {
		charTypes++
	}
	return charTypes >= 3
}
