# Repository Guidelines

## Git Safety (Hard Rule)

Never run any git-history or remote mutation — `commit`, `tag`, `push`,
`reset`, `rebase`, `revert`, `merge`, `cherry-pick`, `am`, force-push, or
`remote add/set-url/remove` and branch/tag deletion — without a separate,
explicit "yes" from the human in the current conversation for that exact
action. Approving a larger task (for example "cut the release") is not that
"yes".

- Local edits stay uncommitted working-tree changes until the human asks to
  commit them.
- When the human explicitly asks to commit ("commit these changes"), that
  request approves the commit — and only that commit. Use `--no-verify` only
  for a commit the human explicitly requested, because the repo pre-commit hook
  blocks non-interactive commits.
- Never push, tag, or otherwise touch the remote without a separate explicit
  go-ahead for that exact push/tag.
- A `PreToolUse` hook enforces this mechanically in new sessions; this section
  is the instruction-level statement of the same rule.
- Per-machine setup: run `make install-git-guard` and install the
  `dsh-git-guard` bundle (Web GUI Plugins page, or via an agent) — see
  `docs/dsh-onboarding.md`.

## Code Review Rules

### Scope
- Review only lines changed in this pull request. Report pre-existing problems in unchanged code as a
  single note at the end, not as inline findings.
- Report P0 and P1 only when you can name the concrete input, environment or call path that triggers
  the failure. Do not report style, naming, formatting, speculative hardening or alternative designs.
- One finding per root cause. If a guard or validator has a class of bypasses, report the class once
  with two examples. Do not report a root cause that already has a reply on this pull request.

### Best-effort guards
- Components documented as best-effort tripwires (not security boundaries) are reviewed against their
  documented coverage list, not against every possible spelling of an input. Safe path: add the missing
  case to the coverage list and to its test table.
- Best-effort components (each applies once its PR merges): `dsh-git-guard/`, pending PR #170
  (contract: `docs/dsh-onboarding.md`, "Behavior notes"). Ordinary Git spellings (`--delete`, option order, `remote rm`, global
  options such as `-C` and `--no-pager`) are in scope; shell-string evasions (wrappers, here-strings,
  command substitutions, dashed `git-*` executables) are documented limitations.

## Project Structure & Module Organization
- `cmd/ocms/`: application entrypoint (`main.go`).
- `internal/`: core runtime code (handlers, middleware, services, store, views, cache, scheduler).
- `modules/`: built-in pluggable modules (analytics, embed, privacy, migrator, etc.).
- `custom/`: user-defined modules/themes loaded at runtime.
- `web/`: shared templates and frontend assets (`static/js`, `static/scss`, `static/dist`).
- `internal/store/migrations/` and `internal/store/queries/`: DB migrations and SQL source for generated `*.sql.go` files.
- `docs/`: feature, deployment, and security documentation.
- `scripts/`: asset builds, deployment helpers, and code-quality/security wrappers.
- `site.mk`: shared Make targets for site-instance repos using `include core/site.mk`.

## Build, Test, and Development Commands
- `make dev`: build assets, generate templ files, and run the app.
- `make run`: run server only (fast local backend iteration).
- `make build` or `make build-prod`: build binaries into `bin/`.
- `make build-all-platforms`: cross-build Linux AMD64/ARM64 and macOS ARM64 binaries with CGO disabled; individual `build-linux-amd64`, `build-linux-arm64`, and `build-darwin-arm64` targets are also available.
- `make test`: run all Go tests (`go test -v ./...`).
- `make coverage` / `make coverage-html`: print test coverage or generate `coverage.out` and `coverage.html`.
- `make sqlc` / `make templ`: regenerate SQL query code (with row-close cleanup) or templ Go files.
- `make code-quality-local`: check Go toolchain consistency, run installed golangci-lint/nilaway, and run tests with a fallback session secret.
- `make security-audit-local`: run available govulncheck/npm audit scanners and write reports under `.audit/`.
- `make code-quality` / `make security-audit`: invoke the corresponding Claude command through the proxy wrapper.
- `make assets`: install npm deps, copy JS libs, compile SCSS/Tailwind.
- `make migrate-up` / `make migrate-down` / `make migrate-status`: manage SQLite migrations.
- `make migrate-create`: prompt for a migration name and create a SQL migration using goose.
- `make install-hooks`: enable repo hook(s) from `.githooks/`.
- In site-instance repos, `site.mk` automatically syncs custom modules before run/build/test targets. After a fresh core clone, run `make sync-modules` from every site owning modules before building a shared binary.

## Coding Style & Naming Conventions
- Go only: follow idiomatic Go, `gofmt`, and package-oriented structure.
- Linting is defined in `.golangci.yml`; run `golangci-lint run ./...` before PRs.
- Use `CamelCase` for exported identifiers, `mixedCaps` for internal names, and concise package names.
- Test files must end with `_test.go`; keep tests adjacent to implementation.
- Do not hand-edit generated files (`*_templ.go`, `*.sql.go`) without regenerating sources.

## Testing Guidelines
- Primary framework: Go `testing` package.
- Run full suite with `OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! go test ./...` (or `OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!! make test`); `make test` does not supply a fallback secret.
- Add tests for new handlers, middleware, store queries, and module behavior.
- Prefer deterministic unit tests; cover edge cases and permission/security paths.

## Commit & Pull Request Guidelines
- Commit style in history is imperative and concise (e.g., `Add CSP nonce wiring`, `Fix code quality issues`).
- Keep subject lines short and specific; group related changes per commit.
- PRs should include: purpose, key changes, test evidence (`make test`/lint output), and linked issues.
- For UI/template/theme changes, attach screenshots or short recordings.
- Ensure no absolute local paths are committed (`make check-no-absolute-paths`).
- Before tagging a release, run `make assets` and commit any regenerated sources; release CI rejects changes outside `web/static/dist`.
