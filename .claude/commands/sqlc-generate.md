Regenerate SQLC Go code from SQL queries.

Steps:
1. Run `sqlc generate` to regenerate database access code
2. Verify generated files in `internal/store/` directory
3. Report which files were regenerated
4. Check for any SQLC errors or warnings
5. If errors occur, analyze the SQL queries and suggest fixes

This command should be run after:
- Adding new SQL queries in `internal/store/queries/`
- Modifying existing SQL queries
- Updating database migrations that affect query schemas

SQLC configuration: `sqlc.yaml`
Generated code location: `internal/store/*.sql.go`
