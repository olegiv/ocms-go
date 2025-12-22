Build the oCMS production binary.

Steps:
1. First build assets by running `make assets`
2. Build the production binary with `make build`
3. Verify the binary was created in `bin/ocms`
4. Report the build status and binary location
5. If build fails, analyze error messages and suggest fixes

The build process:
- Installs npm dependencies (htmx, alpine.js) from `package.json`
- Copies JS deps to `web/static/dist/js/`
- Compiles SCSS from `web/static/scss/main.scss` to `web/static/dist/main.css`
- Compiles Go code from `cmd/ocms` to `bin/ocms` binary
- Embeds static assets and templates into the binary

Note: After updating npm dependencies, use `go build -a` to force re-embedding.
