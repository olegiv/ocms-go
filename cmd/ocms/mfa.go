// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/mfa"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// newMFAService builds the two-factor service from the configuration and
// tells the session checks whether a second factor is required. Demo mode
// turns two-factor authentication off: its seeded accounts are shared by
// every visitor, so one visitor's authenticator would lock the rest out.
func newMFAService(db *sql.DB, cfg *config.Config) (*mfa.Service, error) {
	key, required := cfg.MFAKey, cfg.RequireMFA
	if middleware.IsDemoMode() {
		key, required = "", false
	}
	svc, err := mfa.NewService(db, key, required)
	if err != nil {
		return nil, fmt.Errorf("initializing two-factor authentication: %w", err)
	}
	middleware.SetMFARequired(required)
	slog.Info("two-factor authentication initialized",
		"authenticator_apps", svc.Available(),
		"required", required,
		"demo_mode", middleware.IsDemoMode(),
	)
	return svc, nil
}

// runMFAReset removes the second factor of one account and signs it out
// everywhere, without starting the server. It is the way back in for an
// administrator who lost both the authenticator and the recovery codes when
// no other administrator can reset it from the user page. Like migrate, it
// reads .env or the file OCMS_ENV_FILE names.
func runMFAReset(args []string) error {
	if err := loadEnvFile(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("mfa-reset", flag.ContinueOnError)
	dbPath := dbPathFlag(fs)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: ocms mfa-reset [options] <email>\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Remove the two-factor authentication of one account and sign it out everywhere.\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("mfa-reset takes one email address")
	}
	email := fs.Arg(0)
	if err := requireDatabase(*dbPath, "reset two-factor authentication in"); err != nil {
		return err
	}

	db, err := store.NewDB(*dbPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	user, err := store.New(db).GetUserByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("no user with email %s", email)
	}
	if err != nil {
		return fmt.Errorf("loading user %s: %w", email, err)
	}
	// Removing a second factor needs no key: only enrolling and checking do.
	svc, err := mfa.NewService(db, "", false)
	if err != nil {
		return err
	}
	if _, err := svc.Disable(ctx, user.ID); err != nil {
		return fmt.Errorf("resetting two-factor authentication for %s: %w", email, err)
	}
	if err := service.NewEventService(db).LogAuthEvent(ctx, model.EventLevelWarning, "Two-factor authentication reset from the command line", &user.ID, "", "", map[string]any{"email": user.Email}); err != nil {
		slog.WarnContext(service.WithoutEventLog(ctx), "failed to record the reset in the event log", "error", err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "Removed two-factor authentication for %s and signed the account out everywhere.\n"+
		"Where OCMS_REQUIRE_MFA is on, they set up a new authenticator at their next sign-in.\n", user.Email)
	return nil
}
