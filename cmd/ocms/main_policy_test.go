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
		CREATE TABLE webhooks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL,
			is_active INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE scheduled_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL,
			is_active INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			is_active INTEGER NOT NULL DEFAULT 1,
			expires_at DATETIME
		);
		CREATE TABLE api_key_source_cidrs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			api_key_id INTEGER NOT NULL,
			cidr TEXT NOT NULL
		);
		CREATE TABLE embed_settings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider TEXT NOT NULL,
			settings TEXT NOT NULL DEFAULT '{}',
			is_enabled INTEGER NOT NULL DEFAULT 0
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

	if err := auditRequiredFormCaptchaPosture(context.Background(), db, hooks, false); err == nil {
		t.Fatal("expected error when captcha verifier hook is missing")
	}
}

func TestAuditRequiredFormCaptchaPosture_RejectsActiveFormsWithoutCaptcha(t *testing.T) {
	db := newPolicyTestDB(t)
	hooks := newHookRegistryWithCaptcha(true)

	if _, err := db.Exec(`INSERT INTO forms (is_active) VALUES (1)`); err != nil {
		t.Fatalf("inserting form: %v", err)
	}

	if err := auditRequiredFormCaptchaPosture(context.Background(), db, hooks, true); err == nil {
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

	if err := auditRequiredFormCaptchaPosture(context.Background(), db, hooks, true); err != nil {
		t.Fatalf("expected compliant posture to pass, got: %v", err)
	}
}

func TestAuditRequiredFormCaptchaPosture_RejectsDisabledVerifier(t *testing.T) {
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

	if err := auditRequiredFormCaptchaPosture(context.Background(), db, hooks, false); err == nil {
		t.Fatal("expected error when captcha verifier is not fully enabled")
	}
}

func TestAuditRequiredHTTPSOutboundPosture_RejectsNonHTTPSWebhook(t *testing.T) {
	db := newPolicyTestDB(t)
	if _, err := db.Exec(`INSERT INTO webhooks (url, is_active) VALUES ('http://example.com/webhook', 1)`); err != nil {
		t.Fatalf("inserting webhook: %v", err)
	}

	if err := auditRequiredHTTPSOutboundPosture(context.Background(), db); err == nil {
		t.Fatal("expected error when active webhook URL is non-HTTPS")
	}
}

func TestAuditRequiredHTTPSOutboundPosture_RejectsNonHTTPSTask(t *testing.T) {
	db := newPolicyTestDB(t)
	if _, err := db.Exec(`INSERT INTO scheduled_tasks (url, is_active) VALUES ('http://example.com/health', 1)`); err != nil {
		t.Fatalf("inserting scheduled task: %v", err)
	}

	if err := auditRequiredHTTPSOutboundPosture(context.Background(), db); err == nil {
		t.Fatal("expected error when active scheduled task URL is non-HTTPS")
	}
}

func TestAuditRequiredHTTPSOutboundPosture_AllowsCompliantConfig(t *testing.T) {
	db := newPolicyTestDB(t)
	_, err := db.Exec(`
		INSERT INTO webhooks (url, is_active) VALUES
			('https://example.com/webhook', 1),
			('http://example.com/inactive-webhook', 0);
		INSERT INTO scheduled_tasks (url, is_active) VALUES
			('https://example.com/health', 1),
			('http://example.com/inactive-task', 0);
	`)
	if err != nil {
		t.Fatalf("inserting policy fixtures: %v", err)
	}

	if err := auditRequiredHTTPSOutboundPosture(context.Background(), db); err != nil {
		t.Fatalf("expected compliant HTTPS posture to pass, got: %v", err)
	}
}

func TestAuditRequiredAPIKeyExpiryPosture_RejectsNonExpiringActiveKeys(t *testing.T) {
	db := newPolicyTestDB(t)
	if _, err := db.Exec(`INSERT INTO api_keys (is_active, expires_at) VALUES (1, NULL)`); err != nil {
		t.Fatalf("inserting api key: %v", err)
	}

	if err := auditRequiredAPIKeyExpiryPosture(context.Background(), db); err == nil {
		t.Fatal("expected error when active API key has no expiry")
	}
}

func TestAuditRequiredAPIKeyExpiryPosture_AllowsCompliantConfig(t *testing.T) {
	db := newPolicyTestDB(t)
	_, err := db.Exec(`
		INSERT INTO api_keys (is_active, expires_at) VALUES
			(1, '2030-01-01T00:00:00Z'),
			(0, NULL);
	`)
	if err != nil {
		t.Fatalf("inserting api key fixtures: %v", err)
	}

	if err := auditRequiredAPIKeyExpiryPosture(context.Background(), db); err != nil {
		t.Fatalf("expected compliant API key expiry posture to pass, got: %v", err)
	}
}

func TestAuditRequiredAPIKeySourceCIDRPosture_RejectsMissingCIDRs(t *testing.T) {
	db := newPolicyTestDB(t)
	if _, err := db.Exec(`INSERT INTO api_keys (is_active, expires_at) VALUES (1, '2030-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("inserting api key: %v", err)
	}

	if err := auditRequiredAPIKeySourceCIDRPosture(context.Background(), db); err == nil {
		t.Fatal("expected error when active API key has no source CIDR")
	}
}

func TestAuditRequiredAPIKeySourceCIDRPosture_AllowsCompliantConfig(t *testing.T) {
	db := newPolicyTestDB(t)

	res, err := db.Exec(`INSERT INTO api_keys (is_active, expires_at) VALUES (1, '2030-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("inserting api key: %v", err)
	}
	keyID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading api key id: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO api_key_source_cidrs (api_key_id, cidr) VALUES (?, '203.0.113.0/24')`, keyID); err != nil {
		t.Fatalf("inserting source cidr: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO api_keys (is_active, expires_at) VALUES (0, NULL)`); err != nil {
		t.Fatalf("inserting inactive api key: %v", err)
	}

	if err := auditRequiredAPIKeySourceCIDRPosture(context.Background(), db); err != nil {
		t.Fatalf("expected compliant API key source CIDR posture to pass, got: %v", err)
	}
}

func TestAuditRequiredEmbedHTTPSPosture_RejectsNonHTTPSEndpoint(t *testing.T) {
	db := newPolicyTestDB(t)

	_, err := db.Exec(`
		INSERT INTO embed_settings (provider, settings, is_enabled)
		VALUES ('dify', '{"api_endpoint":"http://example.com/v1"}', 1)
	`)
	if err != nil {
		t.Fatalf("inserting embed settings: %v", err)
	}

	if err := auditRequiredEmbedHTTPSPosture(context.Background(), db); err == nil {
		t.Fatal("expected error when active embed endpoint is non-HTTPS")
	}
}

func TestAuditRequiredEmbedHTTPSPosture_AllowsCompliantConfig(t *testing.T) {
	db := newPolicyTestDB(t)

	_, err := db.Exec(`
		INSERT INTO embed_settings (provider, settings, is_enabled)
		VALUES
			('dify', '{"api_endpoint":"https://example.com/v1"}', 1),
			('dify', '{"api_endpoint":"http://example.com/v1"}', 0)
	`)
	if err != nil {
		t.Fatalf("inserting embed settings fixtures: %v", err)
	}

	if err := auditRequiredEmbedHTTPSPosture(context.Background(), db); err != nil {
		t.Fatalf("expected compliant embed HTTPS posture to pass, got: %v", err)
	}
}

func TestParseAllowedEmbedUpstreamHosts(t *testing.T) {
	t.Run("valid entries", func(t *testing.T) {
		hosts, err := parseAllowedEmbedUpstreamHosts("api.dify.ai,dify.internal.example")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hosts) != 2 {
			t.Fatalf("expected 2 hosts, got %d", len(hosts))
		}
	})

	t.Run("reject host with scheme", func(t *testing.T) {
		if _, err := parseAllowedEmbedUpstreamHosts("https://api.dify.ai"); err == nil {
			t.Fatal("expected error for host containing scheme")
		}
	})
}

func TestAuditEmbedUpstreamHostPosture_RejectsDisallowedHost(t *testing.T) {
	db := newPolicyTestDB(t)

	_, err := db.Exec(`
		INSERT INTO embed_settings (provider, settings, is_enabled)
		VALUES ('dify', '{"api_endpoint":"https://evil.example/v1"}', 1)
	`)
	if err != nil {
		t.Fatalf("inserting embed settings: %v", err)
	}

	allowed := map[string]struct{}{"api.dify.ai": {}}
	if err := auditEmbedUpstreamHostPosture(context.Background(), db, allowed); err == nil {
		t.Fatal("expected error when active embed endpoint host is not allowlisted")
	}
}

func TestAuditEmbedUpstreamHostPosture_AllowsCompliantHost(t *testing.T) {
	db := newPolicyTestDB(t)

	_, err := db.Exec(`
		INSERT INTO embed_settings (provider, settings, is_enabled)
		VALUES
			('dify', '{"api_endpoint":"https://api.dify.ai/v1"}', 1),
			('dify', '{"api_endpoint":"https://evil.example/v1"}', 0)
	`)
	if err != nil {
		t.Fatalf("inserting embed settings fixtures: %v", err)
	}

	allowed := map[string]struct{}{"api.dify.ai": {}}
	if err := auditEmbedUpstreamHostPosture(context.Background(), db, allowed); err != nil {
		t.Fatalf("expected compliant host posture to pass, got: %v", err)
	}
}

func TestAuditRequiredEmbedUpstreamHostPolicyPosture_RejectsMissingAllowlist(t *testing.T) {
	db := newPolicyTestDB(t)
	_, err := db.Exec(`
		INSERT INTO embed_settings (provider, settings, is_enabled)
		VALUES ('dify', '{"api_endpoint":"https://api.dify.ai/v1"}', 1)
	`)
	if err != nil {
		t.Fatalf("inserting embed settings fixture: %v", err)
	}

	if err := auditRequiredEmbedUpstreamHostPolicyPosture(context.Background(), db, ""); err == nil {
		t.Fatal("expected error when upstream host allowlist is required but missing")
	}
}

func TestAuditRequiredEmbedUpstreamHostPolicyPosture_AllowsConfiguredAllowlist(t *testing.T) {
	db := newPolicyTestDB(t)
	_, err := db.Exec(`
		INSERT INTO embed_settings (provider, settings, is_enabled)
		VALUES ('dify', '{"api_endpoint":"https://api.dify.ai/v1"}', 1)
	`)
	if err != nil {
		t.Fatalf("inserting embed settings fixture: %v", err)
	}

	if err := auditRequiredEmbedUpstreamHostPolicyPosture(context.Background(), db, "api.dify.ai"); err != nil {
		t.Fatalf("expected configured allowlist to pass, got: %v", err)
	}
}
