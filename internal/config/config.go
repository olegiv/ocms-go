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
}

// IsDevelopment returns true if the application is running in development mode.
func (c Config) IsDevelopment() bool {
	return c.Env == "development"
}

// ServerAddr returns the full server address in host:port format.
func (c Config) ServerAddr() string {
	return fmt.Sprintf("%s:%d", c.ServerHost, c.ServerPort)
}

// Load parses environment variables and returns a Config struct.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}
