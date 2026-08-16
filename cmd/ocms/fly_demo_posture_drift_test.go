// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// demoDisposition records why the Fly demo survives a production posture gate.
type demoDisposition int

const (
	// disabledInDemo — fly.toml sets the gate itself to 'false'.
	disabledInDemo demoDisposition = iota
	// satisfiedInDemo — fly.toml supplies the companion value the gate demands.
	satisfiedInDemo
	// harmlessInDemo — the gate stays on and the demo meets it as configured.
	harmlessInDemo
)

// demoGate is the classification of one production posture gate.
type demoGate struct {
	how demoDisposition
	// companion is the fly.toml key that satisfies the gate. Required for
	// satisfiedInDemo, unused otherwise.
	companion string
	// why explains the classification. Required for harmlessInDemo, because
	// "we left it on" is the claim most likely to be wrong.
	why string
}

// demoGates classifies every gate applyProductionSecurityDefaults turns on.
//
// The Fly demo runs OCMS_ENV=production, so each of these is live there. A gate
// missing from this map fails TestFlyDemoClassifiesEveryProductionGate, which is
// the step that was skipped when OCMS_REQUIRE_MIGRATOR_ALLOWED_DB_HOSTS landed.
var demoGates = map[string]demoGate{
	"OCMS_REQUIRE_FORM_CAPTCHA":                 {how: disabledInDemo},
	"OCMS_REQUIRE_TRUSTED_PROXIES":              {how: disabledInDemo},
	"OCMS_REQUIRE_API_ALLOWED_CIDRS":            {how: disabledInDemo},
	"OCMS_REQUIRE_API_KEY_EXPIRY":               {how: disabledInDemo},
	"OCMS_REQUIRE_API_KEY_SOURCE_CIDRS":         {how: disabledInDemo},
	"OCMS_REQUIRE_WEBHOOK_ALLOWED_HOSTS":        {how: disabledInDemo},
	"OCMS_REQUIRE_EMBED_ALLOWED_UPSTREAM_HOSTS": {how: disabledInDemo},
	"OCMS_REQUIRE_BLOCK_SUSPICIOUS_PAGE_HTML":   {how: disabledInDemo},

	"OCMS_REQUIRE_MIGRATOR_ALLOWED_DB_HOSTS": {
		how:       satisfiedInDemo,
		companion: "OCMS_MIGRATOR_ALLOWED_DB_HOSTS",
		why: "the migrator module is active in the demo database, so the gate " +
			"bites; the allowlist points at a host running no database",
	},

	"OCMS_REQUIRE_WEBHOOK_FORM_DATA_MINIMIZATION": {
		how: harmlessInDemo,
		why: "OCMS_WEBHOOK_FORM_DATA_MODE is unset and defaults to 'redacted'; " +
			"the gate only refuses startup on mode 'full'",
	},
	"OCMS_REVOKE_API_KEY_ON_SOURCE_IP_CHANGE": {
		how: harmlessInDemo,
		why: "runtime API key behaviour, not a startup gate",
	},
	"OCMS_REQUIRE_EMBED_ALLOWED_ORIGINS": {
		how: harmlessInDemo,
		why: "the audit is inert unless the embed module is active with proxy " +
			"routes, which the demo does not enable",
	},
	"OCMS_REQUIRE_HTTPS_OUTBOUND": {
		how: harmlessInDemo,
		why: "the demo configures no webhooks, scheduler targets or embed " +
			"endpoints, so no outbound URL is checked",
	},
	"OCMS_SANITIZE_PAGE_HTML": {
		how: harmlessInDemo,
		why: "behaviour flag; sanitising demo page HTML is wanted",
	},
	"OCMS_REQUIRE_SANITIZE_PAGE_HTML": {
		how: harmlessInDemo,
		why: "satisfied because OCMS_SANITIZE_PAGE_HTML is left on",
	},
	"OCMS_BLOCK_SUSPICIOUS_PAGE_HTML": {
		how: harmlessInDemo,
		why: "behaviour flag; its requirement gate is disabled separately so " +
			"demo content that trips the heuristic is not fatal",
	},
	"OCMS_API_KEY_MAX_TTL_DAYS": {
		how: harmlessInDemo,
		why: "defaults to a 90 day ceiling; the demo issues no long-lived keys",
	},
}

// TestFlyDemoClassifiesEveryProductionGate fails when a gate in
// applyProductionSecurityDefaults has no entry in demoGates.
//
// The Fly demo sets OCMS_ENV=production, so every gate added there goes live on
// the demo at the next deploy. When OCMS_REQUIRE_MIGRATOR_ALLOWED_DB_HOSTS was
// added, fly.toml was not revisited, and the failure surfaced as a machine that
// built, shipped, and then refused to start — the demo was down until someone
// read the logs.
//
// The invariant deliberately is not "the demo disables every gate": several are
// correctly left on, and two only bite when their module is active, which is
// not decidable from source. What is mechanical is that every gate must be
// consciously classified here.
//
// Bug state: add a setBoolIfUnset("OCMS_REQUIRE_ANYTHING", ...) line to
// applyProductionSecurityDefaults and this names it.
func TestFlyDemoClassifiesEveryProductionGate(t *testing.T) {
	gates := productionGates(t)
	if len(gates) == 0 {
		t.Fatal("no gates parsed from applyProductionSecurityDefaults; the " +
			"function moved or was renamed and this test is now vacuous")
	}

	for _, key := range gates {
		entry, ok := demoGates[key]
		if !ok {
			t.Errorf("%s is applied in production but is not classified in "+
				"demoGates; decide whether the Fly demo must disable it, satisfy "+
				"it with a companion value, or can leave it on, then record that "+
				"here and in fly.toml", key)
			continue
		}
		if entry.how == satisfiedInDemo && entry.companion == "" {
			t.Errorf("%s is classified satisfiedInDemo without a companion key", key)
		}
		if entry.how == harmlessInDemo && strings.TrimSpace(entry.why) == "" {
			t.Errorf("%s is classified harmlessInDemo without saying why", key)
		}
	}

	// A gate deleted from config.go must not linger here pretending to guard
	// something.
	live := make(map[string]bool, len(gates))
	for _, key := range gates {
		live[key] = true
	}
	for key := range demoGates {
		if !live[key] {
			t.Errorf("demoGates classifies %s but applyProductionSecurityDefaults "+
				"no longer applies it; drop the entry", key)
		}
	}
}

// TestFlyDemoEnvMatchesClassification fails when fly.toml does not actually do
// what demoGates claims, so a classification cannot rot into a comment.
func TestFlyDemoEnvMatchesClassification(t *testing.T) {
	env := flyEnv(t)
	if len(env) == 0 {
		t.Fatal("parsed no [env] entries from fly.toml")
	}

	keys := make([]string, 0, len(demoGates))
	for key := range demoGates {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		entry := demoGates[key]
		switch entry.how {
		case disabledInDemo:
			if env[key] != "false" {
				t.Errorf("demoGates says fly.toml disables %s, but its [env] value "+
					"is %q; the demo will run with that gate enforced", key, env[key])
			}
		case satisfiedInDemo:
			if strings.TrimSpace(env[entry.companion]) == "" {
				t.Errorf("demoGates says %s is satisfied by %s, but fly.toml does "+
					"not set it; the demo will refuse to start", key, entry.companion)
			}
			if env[key] == "false" {
				t.Errorf("%s is classified satisfiedInDemo yet fly.toml also "+
					"disables it; pick one, and prefer keeping enforcement on", key)
			}
		case harmlessInDemo:
			if env[key] == "false" {
				t.Errorf("%s is classified harmlessInDemo yet fly.toml disables "+
					"it; reclassify as disabledInDemo so the reason is recorded", key)
			}
		}
	}
}

// productionGates returns the env var names applyProductionSecurityDefaults
// turns on, read from the source so the list cannot drift from the code.
func productionGates(t *testing.T) []string {
	t.Helper()

	path := filepath.Join("..", "..", "internal", "config", "config.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	var gates []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "applyProductionSecurityDefaults" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || (ident.Name != "setBoolIfUnset" && ident.Name != "setIntIfUnset") {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			key, uerr := strconv.Unquote(lit.Value)
			if uerr == nil {
				gates = append(gates, key)
			}
			return true
		})
	}
	sort.Strings(gates)
	return gates
}

// flyEnv parses the [env] table of fly.toml into a key/value map. The file uses
// one `KEY = 'value'` pair per line, so a full TOML parser would be a
// dependency bought for nothing.
func flyEnv(t *testing.T) map[string]string {
	t.Helper()

	path := filepath.Join("..", "..", "fly.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	env := map[string]string{}
	inEnv := false
	for line := range strings.SplitSeq(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inEnv = trimmed == "[env]"
			continue
		}
		if !inEnv || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		if idx := strings.Index(value, " #"); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}
		value = strings.Trim(value, "'\"")
		env[strings.TrimSpace(key)] = value
	}
	return env
}
