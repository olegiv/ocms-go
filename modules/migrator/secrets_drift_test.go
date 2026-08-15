// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/modules/migrator/sources/shared"
)

// TestSessionConfigOmitsPasswordFields fails if any password-typed config
// field survives into the map that is written to the session store.
//
// The session store is SQLite-backed and gob-encoded, not encrypted, so a
// secret placed there is written in plaintext into the CMS database and is
// included in any backup of that file.
//
// It is registry-driven: a new source with a new password field is covered
// automatically.
func TestSessionConfigOmitsPasswordFields(t *testing.T) {
	// Init registers every built-in source into the shared registry.
	_ = testModule(t)

	sources := ListSources()
	if len(sources) == 0 {
		t.Fatal("no migrator sources registered; this test would vacuously pass")
	}

	checkedAtLeastOneSecret := false

	for _, src := range sources {
		fields := src.ConfigFields()

		// Simulate a fully populated form submission.
		cfg := make(map[string]string, len(fields))
		for _, field := range fields {
			cfg[field.Name] = "submitted-value-" + field.Name
		}

		safe := withoutSecrets(cfg, fields)

		for _, field := range fields {
			if field.Type != "password" {
				if safe[field.Name] != cfg[field.Name] {
					t.Errorf("source %q: non-secret field %q was dropped from session config",
						src.Name(), field.Name)
				}
				continue
			}
			checkedAtLeastOneSecret = true
			if _, present := safe[field.Name]; present {
				t.Errorf("source %q: password field %q is persisted to the session store; "+
					"it must be stripped by withoutSecrets", src.Name(), field.Name)
			}
		}
	}

	if !checkedAtLeastOneSecret {
		t.Fatal("no password-typed config field found across any source; test is vacuous")
	}
}

// TestSessionWritesStripSecrets fails if any handler writes the raw form
// config to the session instead of routing it through withoutSecrets.
//
// TestSessionConfigOmitsPasswordFields proves the helper is correct; this
// proves it is actually used. Without this, reverting a single call site to
// pass `cfg` directly reintroduces the plaintext-password write with every
// other test still green.
func TestSessionWritesStripSecrets(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	var offenders []string
	checked := 0

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "SetSessionData" || len(call.Args) != 3 {
				return true
			}
			// Only the migrator config key carries source credentials.
			keyIdent, ok := call.Args[1].(*ast.Ident)
			if !ok || keyIdent.Name != "sessionKeyMigratorConfig" {
				return true
			}
			checked++

			valueCall, ok := call.Args[2].(*ast.CallExpr)
			if ok {
				if fn, ok := valueCall.Fun.(*ast.Ident); ok && fn.Name == "withoutSecrets" {
					return true
				}
			}
			offenders = append(offenders, name+":"+
				strconv.Itoa(fset.Position(call.Pos()).Line))
			return true
		})
	}

	if checked == 0 {
		t.Fatal("no SetSessionData(sessionKeyMigratorConfig, ...) call found; test is vacuous")
	}
	if len(offenders) > 0 {
		t.Errorf("session writes of the migrator config must be wrapped in withoutSecrets(...) "+
			"so the source database password is never persisted in plaintext.\nUnwrapped writes:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// passwordInputRe matches a rendered password input carrying a value attribute.
var passwordInputRe = regexp.MustCompile(`(?is)<input[^>]*type=\\?"password\\?"[^>]*\bvalue=`)

// TestPasswordInputsRenderNoValue fails if the generated admin form writes a
// secret back into the DOM. Even though templ escapes the attribute (so this
// is not XSS), rendering the source database password into the page puts it in
// the browser, in any intermediary cache, and in the page source.
func TestPasswordInputsRenderNoValue(t *testing.T) {
	for _, name := range []string{"views.templ", "views_templ.go"} {
		path := filepath.Join(".", name)
		data, err := os.ReadFile(path) //nolint:gosec // fixed, in-repo path
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if loc := passwordInputRe.FindIndex(data); loc != nil {
			snippet := string(data[loc[0]:min(loc[1]+40, len(data))])
			t.Errorf("%s renders a password input with a value attribute: %s", name, snippet)
		}
	}
}

// TestMigratorAllowlistEnvNameMatchesConfig fails if the env var the module
// enforces at runtime drifts from the one the production startup gate reads.
// They are declared in different packages, so nothing else couples them.
func TestMigratorAllowlistEnvNameMatchesConfig(t *testing.T) {
	field, ok := reflect.TypeFor[config.Config]().FieldByName("MigratorAllowedDBHosts")
	if !ok {
		t.Fatal("config.Config has no MigratorAllowedDBHosts field; the production startup gate cannot see the allowlist")
	}
	if got := field.Tag.Get("env"); got != shared.EnvAllowedDBHosts {
		t.Errorf("config env tag = %q, shared.EnvAllowedDBHosts = %q; the startup gate reads a different variable than the module enforces",
			got, shared.EnvAllowedDBHosts)
	}

	if _, ok := reflect.TypeFor[config.Config]().FieldByName("RequireMigratorAllowedDBHosts"); !ok {
		t.Error("config.Config has no RequireMigratorAllowedDBHosts field; " +
			"every outbound allowlist in this codebase has an OCMS_REQUIRE_* production twin")
	}
}

// TestRequireMigratorAllowedDBHostsDefaultsOnInProduction locks in that the
// production default matches every other OCMS_REQUIRE_* flag: off in
// development, on in production unless explicitly disabled.
func TestRequireMigratorAllowedDBHostsDefaultsOnInProduction(t *testing.T) {
	t.Setenv("OCMS_ENV", "production")
	t.Setenv("OCMS_SESSION_SECRET", "test-secret-key-32-bytes-long!!!")
	// Satisfy the other production gates so Load reaches ours.
	t.Setenv("OCMS_TRUSTED_PROXIES", "127.0.0.1/32")
	t.Setenv("OCMS_API_ALLOWED_CIDRS", "127.0.0.1/32")
	t.Setenv("OCMS_SANITIZE_PAGE_HTML", "true")
	t.Setenv("OCMS_BLOCK_SUSPICIOUS_PAGE_HTML", "true")

	cfg, err := config.Load()
	if err != nil {
		if strings.Contains(err.Error(), "OCMS_REQUIRE_MIGRATOR_ALLOWED_DB_HOSTS") {
			t.Fatalf("unexpected migrator gate failure during Load: %v", err)
		}
		t.Skipf("config.Load blocked by an unrelated production gate: %v", err)
	}

	if !cfg.RequireMigratorAllowedDBHosts {
		t.Error("RequireMigratorAllowedDBHosts should default to true in production, " +
			"matching OCMS_REQUIRE_WEBHOOK_ALLOWED_HOSTS and the other outbound allowlist twins")
	}
}
