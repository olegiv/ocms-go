// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/oauth"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestFlyStagingBootstrapHasNoPublicService(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "fly.staging.bootstrap.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		header := strings.Trim(strings.TrimSpace(line), "[]")
		if strings.HasPrefix(header, "http_service") || strings.HasPrefix(header, "services") {
			t.Fatal("private bootstrap must not expose Fly Proxy services")
		}
	}
	for key, want := range map[string]string{
		"OCMS_ENV": "development", "OCMS_DO_SEED": "true", "OCMS_REQUIRE_MFA": "true",
		"OCMS_OAUTH_ENABLED": "false", "OCMS_AI_DISABLED": "true",
	} {
		if got := readFlyEnv(t, "fly.staging.bootstrap.toml")[key]; got != want {
			t.Errorf("private bootstrap %s=%q, want %q", key, got, want)
		}
	}
}

func TestFlyStagingRequiresMFAAndPreservesBootstrapState(t *testing.T) {
	public := readFlyEnv(t, "fly.staging.toml")
	private := readFlyEnv(t, "fly.staging.bootstrap.toml")
	for key, want := range map[string]string{
		"OCMS_ENV": "staging", "OCMS_DO_SEED": "false", "OCMS_DEMO_MODE": "false",
		"OCMS_REQUIRE_MFA": "true", "OCMS_OAUTH_ENABLED": "true",
		"OCMS_AI_DISABLED": "false", "OCMS_OAUTH_ALLOW_DCR": "false",
		"OCMS_SITE_URL": "https://ocms-mcp-staging.fly.dev", "OCMS_DB_PATH": "/app/data/ocms.db",
		"OCMS_UPLOADS_DIR": "/app/data/uploads", "OCMS_SERVER_HOST": "[::]",
	} {
		if got := public[key]; got != want {
			t.Errorf("public staging %s=%q, want %q", key, got, want)
		}
	}
	differences := map[string]bool{
		"OCMS_ENV": true, "OCMS_DO_SEED": true, "OCMS_OAUTH_ENABLED": true,
		"OCMS_AI_DISABLED": true,
	}
	for key, value := range public {
		if !differences[key] && private[key] != value {
			t.Errorf("bootstrap/public state differs for %s", key)
		}
	}
	for key := range private {
		if _, exists := public[key]; !exists {
			t.Errorf("bootstrap-only env key %s", key)
		}
	}
	for _, name := range []string{"fly.staging.toml", "fly.staging.bootstrap.toml"} {
		for _, key := range []string{"OCMS_SESSION_SECRET", "OCMS_MFA_KEY", "OCMS_TRUSTED_PROXIES"} {
			if _, exists := readFlyEnv(t, name)[key]; exists {
				t.Errorf("%s must receive %s from owner-managed Fly secrets", name, key)
			}
		}
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{
			"app = 'ocms-mcp-staging'", "primary_region = 'fra'", "kill_timeout = 60",
			"source = 'ocms_staging_data'", "destination = '/app/data'", "strategy = 'rolling'",
		} {
			if !strings.Contains(string(data), required) {
				t.Errorf("%s missing state/identity contract %q", name, required)
			}
		}
		if strings.Count(string(data), "[[mounts]]") != 1 || strings.Contains(string(data), "release_command =") {
			t.Errorf("%s must use one persistent volume and startup migrations", name)
		}
	}
}

func TestFlyStagingPublicIngressAndOwnerCommands(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "fly.staging.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var active strings.Builder
	for line := range strings.SplitSeq(string(raw), "\n") {
		line, _, _ = strings.Cut(line, "#")
		active.WriteString(strings.TrimSpace(line))
		active.WriteByte('\n')
	}
	for _, required := range []string{
		"[http_service]", "force_https = true", "min_machines_running = 1",
		"auto_start_machines = true", "memory = '1gb'", "cpus = 2",
		"[[http_service.checks]]", "path = '/health/ready'",
	} {
		if !strings.Contains(active.String(), required) {
			t.Errorf("public staging missing %s", required)
		}
	}
	commands, err := os.ReadFile(filepath.Join("..", "..", "docs", "mcp-staging-fly.md"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for line := range strings.SplitSeq(strings.ReplaceAll(string(commands), "\\\n", " "), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "flyctl deploy ") {
			continue
		}
		found = true
		for _, required := range []string{"--ha=false", "--config ", "--app \"$STAGING_APP\"", "--strategy rolling"} {
			if !strings.Contains(line, required) {
				t.Errorf("staging deploy command missing %s", required)
			}
		}
	}
	if !found {
		t.Fatal("no owner deploy commands were checked")
	}
}

func TestFlyStagingEnvironmentsLoad(t *testing.T) {
	for _, name := range []string{"fly.staging.toml", "fly.staging.bootstrap.toml"} {
		t.Run(name, func(t *testing.T) {
			// Do not let a developer's unrelated OCMS environment override the fixture.
			typeOf := reflect.TypeFor[config.Config]()
			for i := range typeOf.NumField() {
				key, _, _ := strings.Cut(typeOf.Field(i).Tag.Get("env"), ",")
				if key != "" {
					t.Setenv(key, "")
				}
			}
			for key, value := range readFlyEnv(t, name) {
				t.Setenv(key, value)
			}
			t.Setenv("OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
			t.Setenv("OCMS_MFA_KEY", "k9Qw2xZt7Lp4Vn8sRb3Yh6Mc1Ud5Ef0Gj2Hx4Nz8Tq=")
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.RequireMFA || cfg.ServerAddr() != "[::]:8080" {
				t.Fatal("staging/bootstrap lost required MFA or dual-stack binding")
			}
			if cfg.OAuthEnabled {
				db, err := store.NewDB(filepath.Join(t.TempDir(), "staging.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				if err := store.Migrate(db); err != nil {
					t.Fatal(err)
				}
				service, err := oauth.New(db, oauth.Options{Issuer: cfg.SiteURL,
					AllowedHosts: strings.Split(cfg.OAuthCIMDAllowedHosts, ","), PreRegisteredClients: cfg.OAuthPreRegisteredClients})
				if err != nil {
					t.Fatal(err)
				}
				// The actual profile pin resolves without live metadata discovery.
				request, err := service.Prepare(t.Context(), url.Values{
					"client_id":    {"https://claude.ai/oauth/mcp-oauth-client-metadata"},
					"redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "response_type": {"code"},
					"code_challenge": {strings.Repeat("A", 43)}, "code_challenge_method": {"S256"},
					"resource": {cfg.SiteURL + "/api/mcp"}, "scope": {"mcp:access"},
				})
				if err != nil || request.ClientName != "Claude" {
					t.Fatalf("staging pin cannot prepare Claude authorization: %v", err)
				}
			}
			t.Setenv("OCMS_MFA_KEY", "")
			if _, err := config.Load(); err == nil {
				t.Fatal("staging starts without its required MFA sealing key")
			}
		})
	}
}
