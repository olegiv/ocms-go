# Repository Guidelines

## Project Structure & Module Organization
- `cmd/ocms/`: application entrypoint (`main.go`).
- `internal/`: core runtime code (handlers, middleware, services, store, views, cache, scheduler).
- `modules/`: built-in pluggable modules (analytics, embed, privacy, migrator, etc.).
- `custom/`: user-defined modules/themes loaded at runtime.
- `web/`: shared templates and frontend assets (`static/js`, `static/scss`, `static/dist`).
- `internal/store/migrations/` and `internal/store/queries/`: DB migrations and SQL source for generated `*.sql.go` files.
- `scripts/`: asset build, deploy, path-check, and Codex helper entrypoints used by the Make targets.
- `site.mk`: shared Make targets for per-site repos that include this core checkout via `core/site.mk`.
- `docs/`: feature, deployment, and security documentation.

## Build, Test, and Development Commands
- `make dev`: build assets, generate templ files, and run the app.
- `make run`: run server only (fast local backend iteration).
- `make build` or `make build-prod`: build binaries into `bin/`.
- `make coverage` or `make coverage-html`: generate Go coverage summaries/reports.
- `make test`: run all Go tests (`go test -v ./...`).
- `make assets`: install npm deps, copy JS libs, compile SCSS/Tailwind.
- `make sqlc` / `make templ`: regenerate SQLC and templ generated Go files.
- `make migrate-up` / `make migrate-down` / `make migrate-status`: manage SQLite migrations.
- `make code-quality` / `make security-audit`: run repo Codex helper workflows from `scripts/codex-commands`.
- `make install-hooks`: enable repo hook(s) from `.githooks/`.
- In site instance repos that `include core/site.mk`, use `make sync-modules` before builds/tests when custom modules changed.

## Coding Style & Naming Conventions
- Go only: follow idiomatic Go, `gofmt`, and package-oriented structure.
- Linting is defined in `.golangci.yml`; run `golangci-lint run ./...` before PRs.
- Use `CamelCase` for exported identifiers, `mixedCaps` for internal names, and concise package names.
- Test files must end with `_test.go`; keep tests adjacent to implementation.
- Do not hand-edit generated files (`*_templ.go`, `*.sql.go`) without regenerating sources.

## Testing Guidelines
- Primary framework: Go `testing` package.
- Run full suite with `OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test ./...` (or just `make test`).
- Add tests for new handlers, middleware, store queries, and module behavior.
- Prefer deterministic unit tests; cover edge cases and permission/security paths.

## Commit & Pull Request Guidelines
- Commit style in history is imperative and concise (e.g., `Add CSP nonce wiring`, `Fix code quality issues`).
- Keep subject lines short and specific; group related changes per commit.
- PRs should include: purpose, key changes, test evidence (`make test`/lint output), and linked issues.
- For UI/template/theme changes, attach screenshots or short recordings.
- Ensure no absolute local paths are committed (`make check-no-absolute-paths`).
