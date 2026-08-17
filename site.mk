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
# build-darwin-arm64, build-all-platforms, assets, test, clean, sync-modules,
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
        build-linux-amd64 build-darwin-arm64 build-all-platforms assets clean \
        test require-db-path migrate-up migrate-down migrate-status

# ── Paths ────────────────────────────────────────────────────────────────────
SITE_DIR         ?= $(shell pwd)
CORE_DIR         ?= $(SITE_DIR)/core
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
OCMS_ENV_FILE ?= $(SITE_DIR)/.env
export OCMS_ENV_FILE

# Read one key out of the .env with sed rather than include. Accepts an optional
# `export ` prefix (godotenv does) and takes the first match only. Deliberately
# does not strip quotes: make only uses these for a port and a file path.
env_get = $(shell sed -n 's/^[[:space:]]*\(export[[:space:]][[:space:]]*\)\{0,1\}$(1)[[:space:]]*=[[:space:]]*//p' "$(OCMS_ENV_FILE)" 2>/dev/null | head -1 | sed 's/[[:space:]][[:space:]]*\#.*$$//; s/[[:space:]]*$$//')

# The values make itself needs: OCMS_SERVER_PORT for `stop`, OCMS_DB_PATH for the
# migrate targets. Both must agree with what the server actually uses, so they
# come from the same .env the app reads — not from an independent default.
OCMS_SERVER_PORT ?= $(call env_get,OCMS_SERVER_PORT)
OCMS_DB_PATH     ?= $(call env_get,OCMS_DB_PATH)

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

# ── Custom modules ───────────────────────────────────────────────────────────
# Mirror this site's Go modules into the core tree so the compiler can see them.
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
# This MIRRORS, it does not merely add. Several sites share one core checkout,
# so a purely additive copy would leave the previous site's modules in the tree
# and compile them into this site's binary — and because Registry.InitAll runs
# migrations for every registered module before it reads active status, their
# tables would be created in this site's database. Modules a site has since
# deleted would likewise persist forever with no target able to remove them.
# So: anything in core's custom/modules that is neither tracked by core nor
# present in this site's tree is removed first.
#
# Whole recipe in ONE shell per target. Each line of a make recipe otherwise
# gets its own shell, so `|| exit 0` on its own line exits that line only and
# the loops below still run — with an empty SITE_MODULES_DIR the glob becomes
# /*/ and the loop would rsync top-level system directories into core.
#
# Note the ordering: stale cleanup runs even when this site has no
# custom/modules/ at all. Returning early there would leave the *previous*
# site's modules in the tree and compile them into a site that has none, which
# is the exact failure this target exists to prevent.
#
# core_names is the list of module names that ship with oCMS, and everything
# hinges on it: it decides both what may not be overwritten and what counts as
# stale. If it cannot be determined the recipe aborts rather than guessing —
# an empty list would make every core module look stale and delete it.
sync-modules: ## Mirror custom/modules/ into core (runs before builds)
	@set -eu; \
	site="$(SITE_MODULES_DIR)"; core="$(CORE_MODULES_DIR)"; \
	[ -n "$$site" ] && [ -n "$$core" ] || { echo "sync-modules: SITE_MODULES_DIR/CORE_MODULES_DIR must be set" >&2; exit 1; }; \
	mkdir -p "$$core"; \
	core_names="$$($(core_names_cmd) || true)"; \
	core_names="$$(echo $$core_names)"; \
	[ -n "$$core_names" ] || { echo "sync-modules: cannot list oCMS's own modules in $(CORE_DIR) (not a git checkout?); refusing to touch $$core" >&2; exit 1; }; \
	owned=" "; \
	if [ -d "$$site" ]; then \
		for path in "$$site"/*/ "$$site"/imports_*.go; do \
			[ -e "$$path" ] || continue; \
			name="$$(basename "$$path")"; \
			case " $$core_names " in *" $$name "*) \
				echo "sync-modules: $$name ships with oCMS; rename the site module" >&2; exit 1;; \
			esac; \
			owned="$$owned$$name "; \
		done; \
	fi; \
	for path in "$$core"/*/ "$$core"/imports_*.go; do \
		[ -e "$$path" ] || continue; \
		name="$$(basename "$$path")"; \
		case " $$core_names $$owned" in *" $$name "*) continue;; esac; \
		echo "sync-modules: removing stale $$name"; rm -rf "$$core/$$name"; \
	done; \
	[ -d "$$site" ] || exit 0; \
	for dir in "$$site"/*/; do \
		[ -d "$$dir" ] || continue; \
		rsync -a --delete "$$dir" "$$core/$$(basename "$$dir")/"; \
	done; \
	for file in "$$site"/imports_*.go; do \
		[ -f "$$file" ] || continue; \
		cp "$$file" "$$core/"; \
	done

# Removes only what this site owns, by name. A blanket `rm imports_*.go` would
# take core's own tracked imports_bookmarks.go with it, and a site module named
# `bookmarks` would take core's tracked directory — hence the same abort-rather-
# than-guess rule as sync-modules when the core list cannot be determined.
clean-modules: ## Remove this site's module copies from core
	@set -eu; \
	site="$(SITE_MODULES_DIR)"; core="$(CORE_MODULES_DIR)"; \
	[ -n "$$site" ] && [ -n "$$core" ] || { echo "clean-modules: SITE_MODULES_DIR/CORE_MODULES_DIR must be set" >&2; exit 1; }; \
	[ -d "$$site" ] || exit 0; \
	core_names="$$($(core_names_cmd) || true)"; \
	core_names="$$(echo $$core_names)"; \
	[ -n "$$core_names" ] || { echo "clean-modules: cannot list oCMS's own modules in $(CORE_DIR) (not a git checkout?); refusing to touch $$core" >&2; exit 1; }; \
	for path in "$$site"/*/ "$$site"/imports_*.go; do \
		[ -e "$$path" ] || continue; \
		name="$$(basename "$$path")"; \
		case " $$core_names " in *" $$name "*) continue;; esac; \
		rm -rf "$$core/$$name"; \
	done; \
	echo "Core module copies removed"

# ── Server ───────────────────────────────────────────────────────────────────
dev: assets sync-modules ## Build assets, sync modules, run the dev server
	cd "$(CORE_DIR)" && $(GO) run $(MAIN_DIR)

run: sync-modules ## Run the dev server without rebuilding assets
	cd "$(CORE_DIR)" && $(GO) run $(MAIN_DIR)

stop: ## Kill the server on the configured port
	@[ -n "$(OCMS_SERVER_PORT)" ] || { echo "stop: OCMS_SERVER_PORT is not set" >&2; exit 1; }
	@lsof -ti:$(OCMS_SERVER_PORT) -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
	@echo "Server stopped on port $(OCMS_SERVER_PORT)"

restart: stop dev ## Restart the dev server

# ── Build ────────────────────────────────────────────────────────────────────
# Cross-builds set CGO_ENABLED=0 to match core's own Makefile: the sqlite driver
# is pure Go, and a stray CGO_ENABLED=1 in the environment would otherwise
# silently change what ships to production.
build: sync-modules ## Build the binary with debug symbols
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	@mkdir -p "$(BUILD_DIR)"
	cd "$(CORE_DIR)" && $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS_VERSION)" -o "$(BUILD_DIR)/$(BINARY_NAME)" $(MAIN_DIR)

build-prod: sync-modules ## Build an optimised, stripped binary
	@echo "Building $(BINARY_NAME) $(VERSION) for production..."
	@mkdir -p "$(BUILD_DIR)"
	cd "$(CORE_DIR)" && $(GO) build -ldflags="-s -w $(LDFLAGS_VERSION)" -trimpath -o "$(BUILD_DIR)/$(BINARY_NAME)" $(MAIN_DIR)

build-linux-amd64: sync-modules ## Cross-build for Linux AMD64
	@echo "Building $(BINARY_NAME) $(VERSION) for Linux AMD64..."
	@mkdir -p "$(BUILD_DIR)"
	cd "$(CORE_DIR)" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w $(LDFLAGS_VERSION)" -trimpath -o "$(BUILD_DIR)/$(BINARY_NAME)-linux-amd64" $(MAIN_DIR)

build-darwin-arm64: sync-modules ## Cross-build for macOS ARM64
	@echo "Building $(BINARY_NAME) $(VERSION) for macOS ARM64..."
	@mkdir -p "$(BUILD_DIR)"
	cd "$(CORE_DIR)" && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="-s -w $(LDFLAGS_VERSION)" -trimpath -o "$(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64" $(MAIN_DIR)

build-all-platforms: build-linux-amd64 build-darwin-arm64 ## Cross-build every platform
	@echo "All platform builds complete!"
	@ls -lh "$(BUILD_DIR)/$(BINARY_NAME)"-*

assets: ## Compile SCSS and copy JS dependencies
	cd "$(CORE_DIR)" && $(MAKE) assets

test: sync-modules ## Run the full Go test suite
	cd "$(CORE_DIR)" && $(GO) test -v ./...

clean: ## Remove build artifacts
	@[ -n "$(SITE_DIR)" ] && [ -n "$(BUILD_DIR)" ] || { echo "clean: SITE_DIR/BUILD_DIR must be set" >&2; exit 1; }
	rm -rf "$(BUILD_DIR)"

# ── Database ─────────────────────────────────────────────────────────────────
# OCMS_DB_PATH comes from the site .env, the same file the server reads. It must
# be ABSOLUTE: internal/config defaults to ./data/ocms.db relative to the working
# directory, and `dev`/`run` cd into core/ — so a relative path would have goose
# migrating <site>/data/ocms.db while the server opens <core>/data/ocms.db,
# shared with every other site using that checkout. Migrations would report
# success against a database the app never opens.
require-db-path:
	@[ -n "$(OCMS_DB_PATH)" ] || { echo "OCMS_DB_PATH is not set in $(OCMS_ENV_FILE) — set it to an absolute path" >&2; exit 1; }
	@case "$(OCMS_DB_PATH)" in /*) ;; *) echo "OCMS_DB_PATH must be absolute, got: $(OCMS_DB_PATH)" >&2; exit 1;; esac

migrate-up: require-db-path ## Apply pending database migrations
	goose -dir "$(MIGRATIONS_DIR)" sqlite3 "$(OCMS_DB_PATH)" up

migrate-down: require-db-path ## Roll back the last migration
	goose -dir "$(MIGRATIONS_DIR)" sqlite3 "$(OCMS_DB_PATH)" down

migrate-status: require-db-path ## Show migration status
	goose -dir "$(MIGRATIONS_DIR)" sqlite3 "$(OCMS_DB_PATH)" status
