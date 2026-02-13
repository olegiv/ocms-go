Scan the project for code quality issues.

Steps:
1. Run `go vet ./...` to check for common issues
2. Run `staticcheck ./...` for extended static analysis (if available)
3. Run `errcheck ./...` to detect unhandled errors (if available)
4. Check for duplicate code patterns
5. Check for empty slice declarations using literals (`x := []Type{}` instead of `var x []Type`)
6. Check for variables that collide with imported package names
7. Check for potential resource leaks (missing defer rows.Close())
8. Report all findings with file locations and suggested fixes

If staticcheck or errcheck are not installed, install them:
- `go install honnef.co/go/tools/cmd/staticcheck@latest`
- `go install github.com/kisielk/errcheck@latest`

Note: All Go commands require OCMS_SESSION_SECRET environment variable to be set.
Use: `OCMS_SESSION_SECRET=test-secret-key-32-bytes-long!!!` as prefix when needed.
