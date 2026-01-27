// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Config holds the application configuration loaded from environment variables.
type Config struct {
	DBPath        string `env:"OCMS_DB_PATH" envDefault:"./data/ocms.db"`
	SessionSecret string `env:"OCMS_SESSION_SECRET,required"`
	ServerHost    string `env:"OCMS_SERVER_HOST" envDefault:"localhost"`
	ServerPort    int    `env:"OCMS_SERVER_PORT" envDefault:"8080"`
	Env           string `env:"OCMS_ENV" envDefault:"development"`
	LogLevel      string `env:"OCMS_LOG_LEVEL" envDefault:"info"`
	ThemesDir     string `env:"OCMS_THEMES_DIR" envDefault:"./themes"`
	ActiveTheme   string `env:"OCMS_ACTIVE_THEME" envDefault:"default"`

	// Cache configuration
	RedisURL     string `env:"OCMS_REDIS_URL"`                         // Optional Redis URL for distributed caching
	CachePrefix  string `env:"OCMS_CACHE_PREFIX" envDefault:"ocms:"`   // Redis key prefix
	CacheTTL     int    `env:"OCMS_CACHE_TTL" envDefault:"3600"`       // Default cache TTL in seconds
	CacheMaxSize int    `env:"OCMS_CACHE_MAX_SIZE" envDefault:"10000"` // Max memory cache entries

	// hCaptcha configuration
	HCaptchaSiteKey   string `env:"OCMS_HCAPTCHA_SITE_KEY"`   // hCaptcha site key
	HCaptchaSecretKey string `env:"OCMS_HCAPTCHA_SECRET_KEY"` // hCaptcha secret key
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

// Session secret length requirements
const (
	// MinSessionSecretLengthProd is the minimum for production (AES-256).
	MinSessionSecretLengthProd = 32
	// MinSessionSecretLengthDev is the minimum for development (AES-128).
	MinSessionSecretLengthDev = 16
)

// Load parses environment variables and returns a Config struct.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Validate session secret length (stricter in production)
	minLength := MinSessionSecretLengthDev
	if !cfg.IsDevelopment() {
		minLength = MinSessionSecretLengthProd
	}

	if len(cfg.SessionSecret) < minLength {
		return nil, fmt.Errorf("OCMS_SESSION_SECRET must be at least %d bytes long, got %d bytes; "+
			"generate a secure secret with: openssl rand -base64 32",
			minLength, len(cfg.SessionSecret))
	}

	return cfg, nil
}
