# Repository Guidelines

## Project Structure & Module Organization
- `cmd/ocms/`: application entrypoint (`main.go`).
- `internal/`: core runtime code (handlers, middleware, services, store, views, cache, scheduler).
- `modules/`: built-in pluggable modules (analytics, embed, privacy, migrator, etc.).
- `custom/`: user-defined modules/themes loaded at runtime.
- `web/`: shared templates and frontend assets (`static/js`, `static/scss`, `static/dist`).
- `internal/store/migrations/` and `internal/store/queries/`: DB migrations and SQL source for generated `*.sql.go` files.
- `docs/`: feature, deployment, and security documentation.

## Build, Test, and Development Commands
- `make dev`: build assets, generate templ files, and run the app.
- `make run`: run server only (fast local backend iteration).
- `make all`: build the default local/dev binary.
- `make build`: build the fast local/dev binary into `bin/`.
- `make build-prod`: build the optimized host production binary into `bin/`.
- `make build-linux-amd64`: build the optimized static Linux AMD64 production binary.
- `make build-darwin-arm64`: build the optimized Darwin ARM64 production binary.
- `make build-all-platforms`: build Linux AMD64 + Darwin ARM64 production binaries.
- `make test`: run all Go tests.
- `make test-race`: run tests with race detector.
- `make coverage` / `make coverage-html`: run coverage summary or write `coverage.html`.
- `make fmt` / `make fmt-check`: format with gofumpt or fail if formatting is needed.
- `make vet`, `make lint`, and `make check`: run Go vet, linters, or the full local quality gate.
- `make deps` / `make tidy`: download or tidy Go modules.
- `make install-tools`: install pinned `golangci-lint` and `gofumpt`.
- `make help`: show Makefile targets.
- `make assets`: install npm deps, copy JS libs, compile SCSS/Tailwind.
- `make migrate-up` / `make migrate-down` / `make migrate-status`: manage SQLite migrations.
- `make install-hooks`: enable repo hook(s) from `.githooks/`.

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
