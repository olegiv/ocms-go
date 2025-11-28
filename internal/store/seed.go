package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"ocms-go/internal/auth"
)

// Default admin credentials
const (
	DefaultAdminEmail    = "admin@example.com"
	DefaultAdminPassword = "changeme"
	DefaultAdminName     = "Administrator"
)

// Seed creates initial data in the database.
func Seed(ctx context.Context, db *sql.DB) error {
	queries := New(db)

	// Check if admin user already exists
	_, err := queries.GetUserByEmail(ctx, DefaultAdminEmail)
	if err == nil {
		slog.Info("admin user already exists, skipping seed")
		return nil
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

	return nil
}
