Scan the project for code quality issues and warnings.

## Checks Performed

1. **Go Toolchain Version Mismatch**
   - Check if `go version` matches the compiler version
   - If mismatch found, STOP and report the issue

2. **Static Analysis**
   - Run `go vet ./...` for common issues
   - Run `staticcheck ./...` for extended analysis
   - Run `errcheck ./...` for unhandled errors

3. **Semantic Analysis** (manual checks)
   - Condition is always false/true (constant comparisons)
   - Empty slice declaration using literal
   - Variable collides with imported package name
   - Duplicate code patterns

## Steps

1. **Check Go toolchain:**
   ```bash
   go version
   $(go env GOTOOLDIR)/compile -V
   ```
   If versions don't match, report error and provide fix instructions.

2. **Run static analysis tools:**
   ```bash
   go vet ./...
   staticcheck ./...
   errcheck ./...
   ```

3. **Check for constant comparisons (condition always false):**
   - Find constant definitions and their values
   - Find tests comparing constants to literal values
   - Report any useless comparisons

4. **Check for empty slice literals:**
   ```bash
   grep -rn ":= \[\][a-zA-Z.]*{}" --include="*.go" .
   ```
   Report any `x := []Type{}` that should be `var x []Type`

5. **Check for package name collisions:**
   - Find files importing common packages (url, http, json, errors, etc.)
   - Check if those package names are used as variables
   - Report collisions

6. **Report results:**
   - List all issues found with file:line references
   - Provide fix suggestions for each issue
   - Summary of total issues by category

## Package Names to Check for Collisions

Standard library: `bytes`, `context`, `crypto`, `encoding`, `errors`, `fmt`, `hash`, `html`, `http`, `io`, `json`, `log`, `math`, `net`, `os`, `path`, `reflect`, `regexp`, `runtime`, `sort`, `sql`, `strconv`, `strings`, `sync`, `template`, `testing`, `time`, `unicode`, `url`, `xml`

## Expected Output

```
Code Quality Report
==================

Go Toolchain: OK (go1.25.5)

Static Analysis:
  go vet:      0 issues
  staticcheck: 0 issues
  errcheck:    0 issues

Semantic Analysis:
  Constant comparisons:     0 issues
  Empty slice literals:     0 issues (excluding generated files)
  Package name collisions:  0 issues

Total: 0 issues found
```

## If Issues Found

For each issue, provide:
1. File path and line number
2. Description of the issue
3. How to fix it
4. Code example (before/after)

Note: Generated files (*.sql.go, etc.) are excluded from empty slice literal checks.
