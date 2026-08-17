# oCMS site-instance make targets.
#
# A site instance is a small repository holding only that site's content —
# custom/themes/, custom/modules/, .env, deploy wrappers — with `core` a symlink
# to a checkout of oCMS. All of its build logic lives here, so every site shares
# one definition and `git pull` in core updates them all.
#
# Usage — the whole site Makefile is normally this:
#
#     include core/site.mk
#
# Override variables BEFORE the include; every variable below uses ?=, so a
# later ?= is a silent no-op (a plain = after the include still wins).
# CORE_DIR in particular MUST be set before the include: VERSION/GIT_COMMIT are
# resolved against it at parse time.
# .DEFAULT_GOAL is the exception: override it AFTER the include.
#
#     BINARY_NAME ?= mysite
#     include core/site.mk
#
# Overriding BINARY_NAME also requires exporting it for deploy-binary.sh, which
# otherwise looks for bin/ocms-linux-amd64.
#
# Provides: help, dev, run, stop, restart, build, build-prod, build-linux-amd64,
# build-linux-arm64, build-darwin-arm64, build-all-platforms, assets, test, clean,
# sync-modules,
# clean-modules, migrate-up, migrate-down, migrate-status.
#
# Reads OCMS_SERVER_PORT and OCMS_DB_PATH out of the site .env. OCMS_DB_PATH
# must be absolute — the migrate targets refuse a relative one, because the
# server resolves it against core/ while make would resolve it against the site.
#
# sync-modules/clean-modules need CORE_DIR to be a git checkout: git is how they
# tell oCMS's own modules from a site's copies. They abort rather than guess if
# it is not one.
#
# Deploy wrappers stay in the site repo: they carry the host, instance id and
# vhost path, which are the only genuinely site-specific values.

.PHONY: help sync-modules clean-modules dev run stop restart build build-prod \
        build-linux-amd64 build-linux-arm64 build-darwin-arm64 build-all-platforms \
        assets clean \
        test require-db-path migrate-up migrate-down migrate-status

# ── Paths ────────────────────────────────────────────────────────────────────
SITE_DIR         ?= $(shell pwd)
CORE_DIR         ?= $(SITE_DIR)/core
# Every recipe below cds into CORE_DIR, so a relative override would resolve
# against the wrong tree: `BUILD_DIR=bin` would mkdir <site>/bin and then have
# go write <core>/bin, leaving the deploy scripts to upload a stale binary.
# Resolve relative values against SITE_DIR once, here.
abspath_site = $(if $(patsubst /%,,$(1)),$(SITE_DIR)/$(1),$(1))
override CORE_DIR := $(call abspath_site,$(CORE_DIR))
MIGRATIONS_DIR   ?= $(CORE_DIR)/internal/store/migrations
SITE_MODULES_DIR ?= $(SITE_DIR)/custom/modules
CORE_MODULES_DIR ?= $(CORE_DIR)/custom/modules

# Shell text (NOT a $(shell ...) call) that lists the names under
# custom/modules/ which ship with oCMS. git is the only source: it is what
# distinguishes core's own files from a site's copies, and it needs no
# hand-maintained list to drift or be mis-supplied.
#
# Shell text rather than a $(shell ...) call for two reasons. It keeps git out
# of `make -n`, and it cannot be flattened by accident: a $(shell ...) assigned
# with := — as VERSION/GIT_COMMIT below are, for their own reasons — resolves at
# parse time and freezes against whatever CORE_DIR held then. A site that
# reassigns CORE_DIR after the include would then have the recipe operate on a
# different core than the list describes, every module there would look stale,
# and git-tracked files would be deleted. (A recursive `=` $(shell ...) would
# re-expand correctly, but nothing in the file would stop a later edit from
# turning it into `:=`.)
#
# core.quotePath=false keeps non-ASCII names literal — octal-escaped output
# would not match the real directory and it would be deleted as stale.
core_names_cmd = cd "$(CORE_DIR)" 2>/dev/null && git -c core.quotePath=false ls-files custom/modules 2>/dev/null | cut -d/ -f3 | sort -u | tr '\n' ' '

# ── Build ────────────────────────────────────────────────────────────────────
BINARY_NAME ?= ocms
BUILD_DIR   ?= $(SITE_DIR)/bin
# Resolved at point of use, not with `override ... :=`. An override directive
# outranks a later plain assignment, so `BUILD_DIR = dist` after the include
# would be silently ignored — which the docs promise still works. Recursive
# expansion normalises whatever BUILD_DIR holds when a recipe runs.
build_dir = $(call abspath_site,$(BUILD_DIR))
GO          ?= go
GOFLAGS     ?= -v
MAIN_DIR    ?= ./cmd/ocms

# Version metadata comes from the core checkout — that is where the Go code is.
VERSION    ?= $(shell git -C "$(CORE_DIR)" describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT ?= $(shell git -C "$(CORE_DIR)" rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
# Flatten after the ?= override hook: left recursive, each is re-expanded once
# per recipe line that references it — extra git/date calls, and BUILD_TIME
# could stamp two different timestamps into one build-all-platforms run.
VERSION    := $(VERSION)
GIT_COMMIT := $(GIT_COMMIT)
BUILD_TIME := $(BUILD_TIME)
LDFLAGS_VERSION ?= -X main.appVersion=$(VERSION) -X main.appGitCommit=$(GIT_COMMIT) -X main.appBuildTime=$(BUILD_TIME)

# The site .env is NOT included as makefile syntax. GNU make would parse it with
# its own rules and silently corrupt ordinary shell values: quotes are kept
# verbatim (systemd's EnvironmentFile= strips them, so make dev and production
# would derive different session secrets from one file), $HOME expands as the
# make variable $H followed by OME, trailing comments become trailing spaces,
# and $$ collapses to $. The app reads the file itself via OCMS_ENV_FILE
# (godotenv in cmd/ocms/main.go), which handles all of that correctly.
#
# make dev runs from inside core/, so without this the core's own .env would win.
# Normalised at point of use (see env_file): a relative override would be read
# correctly here — make runs in the site — but exported verbatim and then
# resolved against CORE_DIR by the server, which would silently miss the file.
OCMS_ENV_FILE ?= $(SITE_DIR)/.env

# Read one key out of the .env with sed rather than include. Matches godotenv,
# which is what the server itself uses: an optional `export ` prefix, surrounding
# single or double quotes stripped, an unquoted trailing `# comment` removed,
# and — verified against godotenv v1.5.1 — the LAST assignment winning when a key
# appears twice, hence `tail -1`. Taking the first would have `make stop` aim at
# a port the server is not listening on. The `t` branches stop a quoted value's
# contents from being mistaken for a comment.
env_get = $(shell sed -n 's/^[[:space:]]*\(export[[:space:]][[:space:]]*\)\{0,1\}$(1)[[:space:]]*=[[:space:]]*//p' "$(env_file)" 2>/dev/null | tail -1 | sed -e 's/^"\([^"]*\)".*$$/\1/; t' -e "s/^'\([^']*\)'.*\$$/\1/; t" -e 's/[[:space:]][[:space:]]*\#.*$$//; s/[[:space:]]*$$//')

# godotenv expands ${VAR} references inside .env values; this sed reader cannot,
# and silently prefixing SITE_DIR to a literal "${ROOT}/data.db" would create a
# real directory of that name and open a database the server never uses. Refuse
# instead — the values make reads are short enough to spell out literally.
env_no_refs = $(if $(findstring $$,$(1)),$(error $(2) in $(env_file) uses a $${...} reference, which site.mk cannot expand — write the value literally),$(1))

# Raw inputs, straight from the .env unless the site overrides them.
OCMS_SERVER_PORT ?= $(call env_get,OCMS_SERVER_PORT)
OCMS_DB_PATH     ?= $(call env_get,OCMS_DB_PATH)
OCMS_CUSTOM_DIR  ?= $(call env_get,OCMS_CUSTOM_DIR)
OCMS_UPLOADS_DIR ?= $(call env_get,OCMS_UPLOADS_DIR)

# Normalised at point of use, never with `override ... :=`. An override directive
# outranks a later plain assignment, so a documented post-include
# `OCMS_DB_PATH = tenant.db` would be silently ignored. Recursive expansion
# normalises whatever the variable holds when a recipe actually runs.
#
# These are paths the server resolves against its WORKING DIRECTORY, and dev/run
# cd into CORE_DIR to run `go run` — so a site's ./custom and ./uploads would
# otherwise resolve inside the shared core: the site's themes would not load and
# every site would read and write one uploads directory.
env_file    = $(call abspath_site,$(OCMS_ENV_FILE))
db_path     = $(call abspath_site,$(patsubst ./%,%,$(call env_no_refs,$(or $(OCMS_DB_PATH),data/ocms.db),OCMS_DB_PATH)))
custom_dir  = $(call abspath_site,$(patsubst ./%,%,$(call env_no_refs,$(or $(OCMS_CUSTOM_DIR),custom),OCMS_CUSTOM_DIR)))
uploads_dir = $(call abspath_site,$(patsubst ./%,%,$(call env_no_refs,$(or $(OCMS_UPLOADS_DIR),uploads),OCMS_UPLOADS_DIR)))
# 8080 mirrors internal/config's own envDefault: with no OCMS_SERVER_PORT set,
# the server still starts on 8080, so `stop` and `restart` must know that too.
server_port = $(call env_no_refs,$(or $(OCMS_SERVER_PORT),8080),OCMS_SERVER_PORT)

# Passed explicitly to anything that runs the server from inside CORE_DIR.
# godotenv.Load does not overwrite variables already in the environment, so these
# win over the .env while still being derived from it.
site_env = OCMS_ENV_FILE="$(env_file)" OCMS_DB_PATH="$(db_path)" OCMS_CUSTOM_DIR="$(custom_dir)" OCMS_UPLOADS_DIR="$(uploads_dir)"$(if $(server_port), OCMS_SERVER_PORT="$(server_port)")

# Self-documenting: scrapes the "## " descriptions off the target lines below.
# The character class needs digits, or build-linux-amd64 and build-darwin-arm64
# are omitted from the listing.
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# Must be := — GNU make sets .DEFAULT_GOAL to the first target as soon as it is
# parsed, so by this line it already holds `help` and ?= could never fire.
# A site overrides it by assigning AFTER the include.
.DEFAULT_GOAL := dev

# ── Shared-core lock ─────────────────────────────────────────────────────────
# Sites share one CORE_DIR, so one site writes into a tree another site's
# compiler may be reading. A prerequisite is not enough: it finishes before the
# build recipe starts, leaving a window for a second site to swap modules
# mid-compile. So every target that mutates or reads the shared tree holds one
# lock across ALL of its steps — assets, sync and the build alike. Assets matter
# because web/static/dist is embedded into the binary.
#
# Re-entrant: the holder exports OCMS_CORE_LOCK, so a sub-make invoked inside the
# lock (build -> assets, sync-modules) does not deadlock on itself.
#
# mkdir is the atomic test-and-set; flock is absent on macOS. The trap releases
# it on normal exit, Ctrl-C and SIGTERM — which is why nothing below `exec`s:
# exec would replace the shell that owns the trap and strand the lock. Only
# SIGKILL or a hard reboot can strand it, hence the hint in the timeout message.
LOCK_DIR  ?= $(CORE_DIR)/.site-build.lock
LOCK_WAIT ?= 900

define with_core_lock
set -eu; \
if [ -z "$${OCMS_CORE_LOCK:-}" ]; then \
	waited=0; \
	[ -d "$(CORE_DIR)" ] || { echo "site.mk: CORE_DIR does not exist: $(CORE_DIR) (missing core symlink?)" >&2; exit 1; }; \
	until mkdir "$(LOCK_DIR)" 2>/dev/null; do \
		[ -d "$(LOCK_DIR)" ] || { echo "site.mk: cannot create $(LOCK_DIR) — core not writable?" >&2; exit 1; }; \
		waited=$$((waited + 1)); \
		[ $$waited -lt $(LOCK_WAIT) ] || { echo "site.mk: timed out after $(LOCK_WAIT)s waiting for $(LOCK_DIR)." >&2; echo "site.mk: if no other build is running, remove it: rmdir '$(LOCK_DIR)'" >&2; exit 1; }; \
		[ $$waited -ne 1 ] || echo "site.mk: another site is using $(CORE_DIR); waiting..." >&2; \
		sleep 1; \
	done; \
	trap 'rmdir "$(LOCK_DIR)" 2>/dev/null || true' EXIT; \
	trap 'rmdir "$(LOCK_DIR)" 2>/dev/null || true; exit 130' INT; \
	trap 'rmdir "$(LOCK_DIR)" 2>/dev/null || true; exit 143' TERM; \
	OCMS_CORE_LOCK=1; export OCMS_CORE_LOCK; \
fi;
endef

# ── Custom modules ───────────────────────────────────────────────────────────
# Copy this site's Go modules into the core tree so the compiler can see them.
#
# Go resolves packages only under the core module path and there is no go.work,
# so a site-owned module cannot be compiled from the site repo directly. The
# source of truth is the SITE's custom/modules/; the copy in core is a
# disposable build artifact, gitignored there, referenced by nothing tracked in
# core — so a core checkout still builds standalone.
#
# A copy, not a symlink: Go's wildcards do not descend into symlinked
# directories, so `go test ./...` would silently skip the module and report
# success with failing tests inside it.
#
# This ADDS another site's modules; it does not evict them. One binary serves
# every instance (scripts/deploy/ocmsctl and ocms@.service both exec
# /opt/ocms/bin/ocms, and a site's deploy-binary.sh pushes it to all of them), so
# that binary must carry the UNION of every site's modules. Evicting another
# site's would build a binary without them and ship it to the instance that
# needs them. Registry.InitAll migrates every registered module regardless of
# active status, so those tables appear everywhere either way — gate a module per
# instance with EnvironmentChecker/AllowedEnvs, not by leaving it uncompiled.
#
# .templ files are generated in the SITE tree, before the copy. The core's
# `make assets` also runs templ generate, but only against the core copy — and
# the next sync's `rsync --delete` then overwrites that freshly generated
# _templ.go with the site's committed one. A developer editing views.templ would
# see the new output under `make dev` and ship the old output from `make build`.
# Generating at the source of truth keeps both consistent.
#
# OWNERS records which site each copied name came from. It is what lets this
# target tell "my module, now deleted from my repo" (remove it) from "another
# site's module" (leave it alone, and refuse to overwrite it). Without it two
# sites owning the same module name would silently clobber each other, and a
# module deleted from a site repo could never be found to remove.
#
# The union exists ONLY in this tree: a build sees its own site plus whatever
# earlier syncs left behind, and nothing else. After a fresh clone or a
# `git clean -fdx` here, a binary built from one site omits every other site's
# modules — and deploy-binary.sh ships it to every instance. Re-run
# `make sync-modules` from each site that owns modules first. Giving each
# instance its own binary path is the structural fix; see docs/custom-modules.md.
#
# Whole recipe in ONE shell per target. Each line of a make recipe otherwise
# gets its own shell, so `|| exit 0` on its own line exits that line only and
# the loops below still run — with an empty SITE_MODULES_DIR the glob becomes
# /*/ and the loop would rsync top-level system directories into core.
OWNERS = $(CORE_MODULES_DIR)/.owners

sync-modules: ## Copy custom/modules/ into core (runs before builds)
	@$(with_core_lock) \
	site="$(SITE_MODULES_DIR)"; core="$(CORE_MODULES_DIR)"; me="$(SITE_DIR)"; \
	[ -n "$$site" ] && [ -n "$$core" ] && [ -n "$$me" ] || { echo "sync-modules: SITE_MODULES_DIR/CORE_MODULES_DIR/SITE_DIR must be set" >&2; exit 1; }; \
	mkdir -p "$$core"; \
	core_names="$$($(core_names_cmd) || true)"; \
	core_names="$$(echo $$core_names)"; \
	[ -n "$$core_names" ] || { echo "sync-modules: cannot list oCMS's own modules in $(CORE_DIR) (not a git checkout?); refusing to touch $$core" >&2; exit 1; }; \
	touch "$(OWNERS)"; \
	mine=" "; \
	if [ -d "$$site" ]; then \
		for path in "$$site"/*/ "$$site"/imports_*.go; do \
			[ -e "$$path" ] || continue; \
			name="$$(basename "$$path")"; \
			case " $$core_names " in *" $$name "*) \
				echo "sync-modules: $$name ships with oCMS; rename the site module" >&2; exit 1;; \
			esac; \
			owner="$$(awk -F'\t' -v n="$$name" '$$1 == n { print $$2; exit }' "$(OWNERS)")"; \
			if [ -n "$$owner" ] && [ "$$owner" != "$$me" ]; then \
				if [ -d "$$owner" ]; then \
					echo "sync-modules: $$name is already provided by $$owner; rename one of them" >&2; exit 1; \
				fi; \
				echo "sync-modules: reclaiming $$name from $$owner (that site no longer exists)"; \
			fi; \
			mine="$$mine$$name "; \
		done; \
	fi; \
	while IFS="$$(printf '\t')" read -r name owner; do \
		[ -n "$$name" ] || continue; \
		[ "$$owner" = "$$me" ] || continue; \
		case "$$mine" in *" $$name "*) continue;; esac; \
		case " $$core_names " in *" $$name "*) continue;; esac; \
		echo "sync-modules: removing $$name (deleted from $$me)"; rm -rf "$$core/$$name"; \
	done < "$(OWNERS)"; \
	if [ -d "$$site" ] && [ -n "$$(find "$$site" -name '*.templ' -print -quit 2>/dev/null)" ]; then \
		if command -v templ >/dev/null 2>&1; then \
			out="$$(templ generate -path "$$site" 2>&1)" || { echo "sync-modules: templ generate failed in $$site" >&2; printf '%s\n' "$$out" >&2; exit 1; }; \
		else \
			echo "sync-modules: $$site has .templ files but templ is not installed; using the committed *_templ.go as-is" >&2; \
		fi; \
	fi; \
	if [ -d "$$site" ]; then \
		for dir in "$$site"/*/; do \
			[ -d "$$dir" ] || continue; \
			dest="$$core/$$(basename "$$dir")"; \
			if [ -L "$$dest" ]; then \
				echo "sync-modules: replacing symlink $$dest with a real directory"; rm -f "$$dest"; \
			fi; \
			rsync -a --delete "$$dir" "$$dest/"; \
		done; \
		for file in "$$site"/imports_*.go; do \
			[ -f "$$file" ] || continue; \
			dest="$$core/$$(basename "$$file")"; \
			[ -L "$$dest" ] && rm -f "$$dest"; \
			cp "$$file" "$$dest"; \
		done; \
	fi; \
	: > "$(OWNERS).tmp"; \
	while IFS="$$(printf '\t')" read -r name owner; do \
		[ -n "$$name" ] || continue; \
		[ "$$owner" != "$$me" ] || continue; \
		case "$$mine" in *" $$name "*) continue;; esac; \
		printf '%s\t%s\n' "$$name" "$$owner" >> "$(OWNERS).tmp"; \
	done < "$(OWNERS)"; \
	for name in $$mine; do printf '%s\t%s\n' "$$name" "$$me" >> "$(OWNERS).tmp"; done; \
	mv "$(OWNERS).tmp" "$(OWNERS)"

# Removes what OWNERS says this site put there — including modules it has since
# deleted from its own repo, which no glob over the site tree could find. Another
# site's copies and oCMS's own tracked modules are never touched.
clean-modules: ## Remove this site's module copies from core
	@$(with_core_lock) \
	core="$(CORE_MODULES_DIR)"; me="$(SITE_DIR)"; \
	[ -n "$$core" ] && [ -n "$$me" ] || { echo "clean-modules: CORE_MODULES_DIR/SITE_DIR must be set" >&2; exit 1; }; \
	[ -f "$(OWNERS)" ] || { echo "Core module copies removed"; exit 0; }; \
	core_names="$$($(core_names_cmd) || true)"; \
	core_names="$$(echo $$core_names)"; \
	[ -n "$$core_names" ] || { echo "clean-modules: cannot list oCMS's own modules in $(CORE_DIR) (not a git checkout?); refusing to touch $$core" >&2; exit 1; }; \
	while IFS="$$(printf '\t')" read -r name owner; do \
		[ -n "$$name" ] || continue; \
		[ "$$owner" = "$$me" ] || continue; \
		case " $$core_names " in *" $$name "*) continue;; esac; \
		rm -rf "$$core/$$name"; \
	done < "$(OWNERS)"; \
	awk -F'\t' -v me="$$me" '$$2 != me' "$(OWNERS)" > "$(OWNERS).tmp"; \
	mv "$(OWNERS).tmp" "$(OWNERS)"; \
	echo "Core module copies removed"

# ── Server ───────────────────────────────────────────────────────────────────
dev: ## Sync modules, build assets, run the dev server
	@$(with_core_lock) \
	$(MAKE) --no-print-directory sync-modules; \
	$(MAKE) --no-print-directory assets; \
	cd "$(CORE_DIR)" && $(site_env) $(GO) run $(MAIN_DIR)

run: ## Run the dev server without rebuilding assets
	@$(with_core_lock) \
	$(MAKE) --no-print-directory sync-modules; \
	cd "$(CORE_DIR)" && $(site_env) $(GO) run $(MAIN_DIR)

stop: ## Kill the server on the configured port
	@[ -n "$(server_port)" ] || { echo "stop: OCMS_SERVER_PORT is not set in $(env_file)" >&2; exit 1; }
	@lsof -ti:$(server_port) -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
	@echo "Server stopped on port $(server_port)"

restart: ## Restart the dev server
	@$(MAKE) --no-print-directory stop
	@$(MAKE) --no-print-directory dev

# ── Build ────────────────────────────────────────────────────────────────────
# Cross-builds set CGO_ENABLED=0 to match core's own Makefile: the sqlite driver
# is pure Go, and a stray CGO_ENABLED=1 in the environment would otherwise
# silently change what ships to production.
build: ## Build the binary with debug symbols
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	@$(with_core_lock) \
	$(MAKE) --no-print-directory sync-modules; \
	mkdir -p "$(build_dir)"; \
	cd "$(CORE_DIR)" && $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS_VERSION)" -o "$(build_dir)/$(BINARY_NAME)" $(MAIN_DIR)

build-prod: ## Build an optimised, stripped binary
	@echo "Building $(BINARY_NAME) $(VERSION) for production..."
	@$(with_core_lock) \
	$(MAKE) --no-print-directory sync-modules; \
	mkdir -p "$(build_dir)"; \
	cd "$(CORE_DIR)" && $(GO) build -ldflags="-s -w $(LDFLAGS_VERSION)" -trimpath -o "$(build_dir)/$(BINARY_NAME)" $(MAIN_DIR)

build-linux-amd64: ## Cross-build for Linux AMD64
	@echo "Building $(BINARY_NAME) $(VERSION) for Linux AMD64..."
	@$(with_core_lock) \
	$(MAKE) --no-print-directory sync-modules; \
	mkdir -p "$(build_dir)"; \
	cd "$(CORE_DIR)" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w $(LDFLAGS_VERSION)" -trimpath -o "$(build_dir)/$(BINARY_NAME)-linux-amd64" $(MAIN_DIR)

build-darwin-arm64: ## Cross-build for macOS ARM64
	@echo "Building $(BINARY_NAME) $(VERSION) for macOS ARM64..."
	@$(with_core_lock) \
	$(MAKE) --no-print-directory sync-modules; \
	mkdir -p "$(build_dir)"; \
	cd "$(CORE_DIR)" && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="-s -w $(LDFLAGS_VERSION)" -trimpath -o "$(build_dir)/$(BINARY_NAME)-darwin-arm64" $(MAIN_DIR)

build-linux-arm64: ## Cross-build for Linux ARM64
	@echo "Building $(BINARY_NAME) $(VERSION) for Linux ARM64..."
	@$(with_core_lock) \
	$(MAKE) --no-print-directory sync-modules; \
	mkdir -p "$(build_dir)"; \
	cd "$(CORE_DIR)" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -ldflags="-s -w $(LDFLAGS_VERSION)" -trimpath -o "$(build_dir)/$(BINARY_NAME)-linux-arm64" $(MAIN_DIR)

build-all-platforms: build-linux-amd64 build-linux-arm64 build-darwin-arm64 ## Cross-build every platform
	@echo "All platform builds complete!"
	@ls -lh "$(build_dir)/$(BINARY_NAME)"-*

assets: ## Compile SCSS and copy JS dependencies
	@$(with_core_lock) \
	cd "$(CORE_DIR)" && $(MAKE) assets

test: ## Run the full Go test suite
	@$(with_core_lock) \
	$(MAKE) --no-print-directory sync-modules; \
	cd "$(CORE_DIR)" && $(site_env) $(GO) test -v ./...

clean: ## Remove build artifacts
	@[ -n "$(SITE_DIR)" ] && [ -n "$(build_dir)" ] || { echo "clean: SITE_DIR/BUILD_DIR must be set" >&2; exit 1; }
	rm -rf "$(build_dir)"

# ── Database ─────────────────────────────────────────────────────────────────
# OCMS_DB_PATH comes from the site .env, the same file the server reads, and is
# resolved against SITE_DIR above so goose and the server always agree. Without
# that, internal/config's ./data/ocms.db default plus `dev`/`run` cd-ing into
# core/ would have goose migrating <site>/data/ocms.db while the server opened
# <core>/data/ocms.db — shared with every other site using that checkout, and
# reporting success against a database the app never opens.
#
# The guard below is a backstop against a broken resolution, not a user-facing
# constraint: a relative path in the .env is fine and gets resolved.
require-db-path:
	@[ -n "$(db_path)" ] || { echo "OCMS_DB_PATH is empty (SITE_DIR unset?)" >&2; exit 1; }
	@case "$(db_path)" in /*) ;; *) echo "OCMS_DB_PATH did not resolve to an absolute path: $(db_path)" >&2; exit 1;; esac

migrate-up: require-db-path ## Apply pending database migrations
	goose -dir "$(MIGRATIONS_DIR)" sqlite3 "$(db_path)" up

migrate-down: require-db-path ## Roll back the last migration
	goose -dir "$(MIGRATIONS_DIR)" sqlite3 "$(db_path)" down

migrate-status: require-db-path ## Show migration status
	goose -dir "$(MIGRATIONS_DIR)" sqlite3 "$(db_path)" status
