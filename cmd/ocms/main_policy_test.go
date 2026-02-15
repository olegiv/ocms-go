// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/modules/hcaptcha"
)

func newPolicyTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("opening sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE forms (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			is_active INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE form_fields (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			form_id INTEGER NOT NULL,
			type TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("creating schema: %v", err)
	}

	return db
}

func newHookRegistryWithCaptcha(enabled bool) *module.HookRegistry {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hooks := module.NewHookRegistry(logger)
	if !enabled {
		return hooks
	}

	hooks.Register(hcaptcha.HookFormCaptchaVerify, module.HookHandler{
		Name:     "test-captcha-verify",
		Module:   "hcaptcha",
		Priority: 0,
		Fn: func(ctx context.Context, data any) (any, error) {
			return data, nil
		},
	})

	return hooks
}

func TestAuditRequiredFormCaptchaPosture_RejectsMissingVerifier(t *testing.T) {
	db := newPolicyTestDB(t)
	hooks := newHookRegistryWithCaptcha(false)

	if err := auditRequiredFormCaptchaPosture(context.Background(), db, hooks); err == nil {
		t.Fatal("expected error when captcha verifier hook is missing")
	}
}

func TestAuditRequiredFormCaptchaPosture_RejectsActiveFormsWithoutCaptcha(t *testing.T) {
	db := newPolicyTestDB(t)
	hooks := newHookRegistryWithCaptcha(true)

	if _, err := db.Exec(`INSERT INTO forms (is_active) VALUES (1)`); err != nil {
		t.Fatalf("inserting form: %v", err)
	}

	if err := auditRequiredFormCaptchaPosture(context.Background(), db, hooks); err == nil {
		t.Fatal("expected error when active form lacks captcha field")
	}
}

func TestAuditRequiredFormCaptchaPosture_AllowsWhenCompliant(t *testing.T) {
	db := newPolicyTestDB(t)
	hooks := newHookRegistryWithCaptcha(true)

	res, err := db.Exec(`INSERT INTO forms (is_active) VALUES (1)`)
	if err != nil {
		t.Fatalf("inserting form: %v", err)
	}
	formID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading form id: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO form_fields (form_id, type) VALUES (?, 'captcha')`, formID); err != nil {
		t.Fatalf("inserting captcha field: %v", err)
	}

	if err := auditRequiredFormCaptchaPosture(context.Background(), db, hooks); err != nil {
		t.Fatalf("expected compliant posture to pass, got: %v", err)
	}
}
