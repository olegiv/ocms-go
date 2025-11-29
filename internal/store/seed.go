package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"ocms-go/internal/auth"
	"ocms-go/internal/model"
)

// Default admin credentials
const (
	DefaultAdminEmail    = "admin@example.com"
	DefaultAdminPassword = "changeme"
	DefaultAdminName     = "Administrator"
)

// DefaultConfig holds default configuration values.
var DefaultConfig = []struct {
	Key         string
	Value       string
	Type        string
	Description string
}{
	{model.ConfigKeySiteName, "Opossum CMS", model.ConfigTypeString, "The name of your site"},
	{model.ConfigKeySiteDescription, "", model.ConfigTypeString, "A short description of your site"},
	{model.ConfigKeyAdminEmail, "admin@example.com", model.ConfigTypeString, "Administrator email address"},
	{model.ConfigKeyPostsPerPage, "10", model.ConfigTypeInt, "Number of posts to display per page"},
}

// Seed creates initial data in the database.
func Seed(ctx context.Context, db *sql.DB) error {
	queries := New(db)

	// Check if admin user already exists
	_, err := queries.GetUserByEmail(ctx, DefaultAdminEmail)
	if err == nil {
		slog.Info("admin user already exists, skipping seed")
		// Still seed config if needed
		return seedConfig(ctx, queries)
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("checking for admin user: %w", err)
	}

	// Hash the default password
	passwordHash, err := auth.HashPassword(DefaultAdminPassword)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	// Create admin user
	now := time.Now()
	user, err := queries.CreateUser(ctx, CreateUserParams{
		Email:        DefaultAdminEmail,
		PasswordHash: passwordHash,
		Role:         "admin",
		Name:         DefaultAdminName,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		return fmt.Errorf("creating admin user: %w", err)
	}

	slog.Info("created default admin user",
		"id", user.ID,
		"email", user.Email,
		"password", DefaultAdminPassword,
	)

	// Seed config values
	if err := seedConfig(ctx, queries); err != nil {
		return err
	}

	return nil
}

// seedConfig creates default configuration values.
func seedConfig(ctx context.Context, queries *Queries) error {
	// Check if any config exists
	count, err := queries.CountConfig(ctx)
	if err != nil {
		return fmt.Errorf("counting config: %w", err)
	}

	if count > 0 {
		slog.Info("config already exists, skipping seed")
		return nil
	}

	now := time.Now()
	for _, cfg := range DefaultConfig {
		_, err := queries.UpsertConfig(ctx, UpsertConfigParams{
			Key:         cfg.Key,
			Value:       cfg.Value,
			Type:        cfg.Type,
			Description: cfg.Description,
			UpdatedAt:   now,
			UpdatedBy:   sql.NullInt64{Valid: false},
		})
		if err != nil {
			return fmt.Errorf("seeding config %s: %w", cfg.Key, err)
		}
	}

	slog.Info("seeded default config values", "count", len(DefaultConfig))
	return nil
}
