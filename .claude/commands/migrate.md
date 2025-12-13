Manage database migrations using goose.

Steps:
1. Check current migration status with `make migrate-status`
2. If migrations are pending, apply them with `make migrate-up`
3. Report migration results:
   - Current version
   - Migrations applied
   - Any errors encountered
4. If errors occur, analyze and provide troubleshooting steps

Available migration commands:
- `make migrate-up` - Apply all pending migrations
- `make migrate-down` - Rollback last migration
- `make migrate-status` - Show current migration status
- `make migrate-create` - Create a new migration (interactive)

Database location: `./data/ocms.db` (configurable via OCMS_DB_PATH)
