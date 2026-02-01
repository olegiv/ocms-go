// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/model"
)

// Default admin credentials
const (
	DefaultAdminEmail    = "admin@example.com"
	DefaultAdminPassword = "changeme1234" // Must be at least 12 characters
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
	{model.ConfigKeyPoweredBy, "Powered by oCMS", model.ConfigTypeString, "Footer powered by text"},
	{model.ConfigKeyCopyright, "", model.ConfigTypeString, "Footer copyright text (leave empty for automatic)"},
}

// Seed creates initial data in the database if doSeed is true.
// When doSeed is false, seeding is skipped to prevent automatic recreation
// of deleted data (e.g., default admin user) on application restart.
func Seed(ctx context.Context, db *sql.DB, doSeed bool) error {
	if !doSeed {
		slog.Info("seeding disabled (set OCMS_DO_SEED=true to enable)")
		return nil
	}

	queries := New(db)

	// Check if admin user already exists
	_, err := queries.GetUserByEmail(ctx, DefaultAdminEmail)
	if err == nil {
		slog.Info("admin user already exists, skipping user seed")
		// Still seed config and menus if needed
		if err := seedConfig(ctx, queries); err != nil {
			return err
		}
		return seedMenus(ctx, queries)
	}
	if !errors.Is(err, sql.ErrNoRows) {
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

	// Seed default menus
	if err := seedMenus(ctx, queries); err != nil {
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

	// Get default language for config creation
	defaultLang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		return fmt.Errorf("getting default language for config: %w", err)
	}

	now := time.Now()
	for _, cfg := range DefaultConfig {
		_, err := queries.UpsertConfig(ctx, UpsertConfigParams{
			Key:          cfg.Key,
			Value:        cfg.Value,
			Type:         cfg.Type,
			Description:  cfg.Description,
			LanguageCode: defaultLang.Code,
			UpdatedAt:    now,
			UpdatedBy:    sql.NullInt64{Valid: false},
		})
		if err != nil {
			return fmt.Errorf("seeding config %s: %w", cfg.Key, err)
		}
	}

	slog.Info("seeded default config values", "count", len(DefaultConfig))
	return nil
}

// DefaultMenus holds default menu definitions.
var DefaultMenus = []struct {
	Name string
	Slug string
}{
	{Name: "Main Menu", Slug: model.MenuMain},
	{Name: "Footer Menu", Slug: model.MenuFooter},
}

// seedMenus creates default menus.
func seedMenus(ctx context.Context, queries *Queries) error {
	// Check if any menus exist
	count, err := queries.CountMenus(ctx)
	if err != nil {
		return fmt.Errorf("counting menus: %w", err)
	}

	if count > 0 {
		slog.Info("menus already exist, skipping seed")
		return nil
	}

	// Get default language for menu creation
	defaultLang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		return fmt.Errorf("getting default language for menus: %w", err)
	}

	now := time.Now()
	for _, menu := range DefaultMenus {
		_, err := queries.CreateMenu(ctx, CreateMenuParams{
			Name:         menu.Name,
			Slug:         menu.Slug,
			LanguageCode: defaultLang.Code,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			return fmt.Errorf("seeding menu %s: %w", menu.Slug, err)
		}
	}

	slog.Info("seeded default menus", "count", len(DefaultMenus))
	return nil
}
