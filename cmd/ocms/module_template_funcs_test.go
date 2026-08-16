// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/render"

	"github.com/olegiv/ocms-go/modules/analytics_ext"
	"github.com/olegiv/ocms-go/modules/analytics_int"
	"github.com/olegiv/ocms-go/modules/dbmanager"
	"github.com/olegiv/ocms-go/modules/developer"
	"github.com/olegiv/ocms-go/modules/embed"
	"github.com/olegiv/ocms-go/modules/example"
	"github.com/olegiv/ocms-go/modules/hcaptcha"
	"github.com/olegiv/ocms-go/modules/informer"
	"github.com/olegiv/ocms-go/modules/migrator"
	"github.com/olegiv/ocms-go/modules/privacy"
	"github.com/olegiv/ocms-go/modules/sentinel"
)

// allModules mirrors registerModules() so this test sees exactly the set that
// ships. Constructing with New() is enough: TemplateFuncs() returns closures
// over the module value and does not require Init.
func allModules() []module.Module {
	return []module.Module{
		example.New(),
		developer.New(),
		analytics_ext.New(),
		embed.New(),
		hcaptcha.New(),
		privacy.New(),
		informer.New(),
		sentinel.New(),
		migrator.New(),
		dbmanager.New(),
		analytics_int.New(),
	}
}

// TestEveryModuleTemplateFuncHasRendererPlaceholder is the guard for a failure
// mode that is silent, deferred, and therefore very easy to ship.
//
// Registry.AllTemplateFuncs() only returns funcs from ACTIVE modules, and Go's
// html/template resolves function names when the template is PARSED. So a theme
// that calls a module func has a hard dependency on that module being active at
// boot: deactivate it, restart, and the theme fails to parse. A theme that fails
// to parse is not a 500 — internal/theme/manager.go logs one warning, drops the
// theme, and the site quietly falls back to whichever theme sorts first.
//
// The established fix is a no-op placeholder in the renderer's base func map,
// which AddTemplateFuncs overwrites when the module is active. This test asserts
// every module func has one, so the next module to add a template func cannot
// reintroduce the trap by omission.
//
// Deliberately no allowlist: a func with no placeholder is a latent outage even
// if no shipped theme happens to call it today, because site-specific themes
// live outside this repo and cannot be audited from here.
func TestEveryModuleTemplateFuncHasRendererPlaceholder(t *testing.T) {
	base := (&render.Renderer{}).TemplateFuncs()

	checked := 0
	for _, mod := range allModules() {
		for name := range mod.TemplateFuncs() {
			checked++
			if _, ok := base[name]; !ok {
				t.Errorf("module %q supplies template func %q with no no-op placeholder in "+
					"render.templateFuncs().\n"+
					"Add one next to the other module placeholders in internal/render/render.go, "+
					"or any theme calling %q will fail to parse — and be silently dropped — "+
					"whenever %q is deactivated.",
					mod.Name(), name, name, mod.Name())
			}
		}
	}

	if checked == 0 {
		t.Fatal("no module template funcs were discovered; the enumeration is broken " +
			"and this test is vacuous")
	}
}
