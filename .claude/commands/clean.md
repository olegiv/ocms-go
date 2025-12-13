Clean build artifacts and temporary files.

Steps:
1. Run `make clean` to remove:
   - `bin/` directory (compiled binaries)
   - `data/*.db` (development databases)
2. Optionally clean additional artifacts:
   - `web/static/dist/` (compiled CSS - will be regenerated on next build)
   - Go build cache: `go clean -cache`
   - Go module cache: `go clean -modcache` (use with caution)
3. Report what was cleaned and disk space freed
4. Note that cleaned files will be regenerated on next build

Warning: This will delete your local development database.
Production databases (if using custom OCMS_DB_PATH) are not affected.

To rebuild after cleaning:
1. `make assets` - Regenerate CSS
2. `make migrate-up` - Recreate database and apply migrations
3. `make build` or `make dev` - Build/run the application

Use this command when:
- Build artifacts are corrupted
- Need to free disk space
- Starting fresh with a clean database
- Troubleshooting build issues
