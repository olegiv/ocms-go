# DB Manager Module

The DB Manager module is an administrative tool for executing SQL queries directly against the database. It provides a web interface for running SELECT, INSERT, UPDATE, DELETE, and other SQL statements with query history tracking.

## Overview

This module allows administrators to:

- Execute arbitrary SQL queries against the database
- View query results in a tabular format
- Track query execution history with timing and row count metrics
- Re-run previous queries from history

### Features

- Direct SQL query execution
- Support for SELECT, INSERT, UPDATE, DELETE, PRAGMA, EXPLAIN, and WITH statements
- Tabular display of SELECT query results
- Row count and execution time metrics
- Query history with click-to-reload functionality
- Keyboard shortcut (Ctrl+Enter) for quick execution

## Installation

The DB Manager module is registered in `cmd/ocms/main.go`:

```go
import "github.com/olegiv/ocms-go/modules/dbmanager"

// In main()
if err := moduleRegistry.Register(dbmanager.New()); err != nil {
    return fmt.Errorf("registering dbmanager module: %w", err)
}
```

## Admin Interface

Access the DB Manager module at **Admin > Modules > DB Manager** or directly at `/admin/dbmanager`.

### Query Execution

1. Enter your SQL query in the textarea
2. Click **Execute Query** or press **Ctrl+Enter**
3. View results below the query form

For SELECT queries, results are displayed in a table with column headers. For DML statements (INSERT, UPDATE, DELETE), the number of affected rows is shown.

### Query History

The module maintains a history of executed queries showing:

| Column | Description |
|--------|-------------|
| Query | The SQL statement (truncated preview) |
| Time | When the query was executed |
| Duration | Execution time in milliseconds |
| Rows | Number of rows returned/affected |
| Status | Success or Failed |

Click on any history row to load that query back into the editor.

## How It Works

### Query Classification

Queries are classified as "read" queries (returning results) based on their prefix:

- `SELECT` - Standard select queries
- `PRAGMA` - SQLite pragma commands
- `EXPLAIN` - Query plan explanations
- `WITH` - Common table expression queries

All other queries are treated as "write" operations and return affected row counts.

### Query History Tracking

Every executed query is logged to a history table:

```sql
CREATE TABLE dbmanager_query_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    query TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    executed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    rows_affected INTEGER DEFAULT 0,
    execution_time_ms INTEGER DEFAULT 0,
    error TEXT
);
```

This provides an audit trail of database operations performed through the module.

## Database Schema

### History Table

| Column | Type | Description |
|--------|------|-------------|
| id | INTEGER | Primary key |
| query | TEXT | The SQL query executed |
| user_id | INTEGER | ID of the admin who ran the query |
| executed_at | DATETIME | Timestamp of execution |
| rows_affected | INTEGER | Number of rows returned/affected |
| execution_time_ms | INTEGER | Execution duration in milliseconds |
| error | TEXT | Error message if query failed (NULL on success) |

## Module Structure

```
modules/dbmanager/
├── module.go           # Module definition, lifecycle, migrations
├── handlers.go         # HTTP handlers and query execution logic
├── dbmanager_test.go   # Unit tests
└── locales/            # Embedded i18n translations
    ├── en/
    │   └── messages.json
    └── ru/
        └── messages.json
```

## Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | /admin/dbmanager | Dashboard with query form and history |
| POST | /admin/dbmanager/execute | Execute a SQL query |

## Internationalization (i18n)

The DB Manager module includes i18n support with embedded translations:

### Supported Languages
- **English (en)**: Default language
- **Russian (ru)**: Full translation

### Translation Keys

All translation keys are prefixed with `dbmanager.`:

| Key | Description |
|-----|-------------|
| `dbmanager.title` | Page title |
| `dbmanager.description` | Module description |
| `dbmanager.warning_*` | Caution message components |
| `dbmanager.query_*` | Query form labels |
| `dbmanager.results_*` | Results section labels |
| `dbmanager.history_*` | History table labels |
| `dbmanager.error_*` | Error messages |

## Security

The module is protected by:

- Admin authentication middleware (admin users only)
- CSRF token validation on POST requests
- Module active status toggle for quick disable
- Complete query history logging for audit purposes

### Best Practices

1. **Backup before modifications**: Always backup your database before running UPDATE or DELETE queries
2. **Test queries first**: Use SELECT to verify your WHERE clause before running UPDATE/DELETE
3. **Use transactions**: For complex modifications, consider wrapping in transactions
4. **Review history**: Check the query history to audit database operations

## Module Active Status

The DB Manager module can be enabled or disabled from **Admin > Modules**:

- **Active**: All routes are accessible, module appears in sidebar (if enabled)
- **Inactive**: Routes return 404 (public) or redirect to modules list (admin)

### Enabling/Disabling

1. Navigate to **Admin > Modules**
2. Find "DB Manager" in the list
3. Toggle the switch to enable/disable
4. Changes take effect immediately (no restart required)

## Troubleshooting

### "Please enter a SQL query"

The query textarea was empty. Enter a valid SQL statement before clicking Execute.

### Query returns error

Check the error message displayed in red. Common issues:
- Table name typos
- Syntax errors in SQL
- Permission issues (SQLite doesn't have user permissions, but check file access)

### History not showing

Ensure the module migration has run. The history table should be created automatically on first module initialization.

## Example Queries

### View table structure
```sql
PRAGMA table_info(pages);
```

### List all tables
```sql
SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;
```

### Count records
```sql
SELECT COUNT(*) as total FROM pages WHERE status = 'published';
```

### View recent pages
```sql
SELECT id, title, slug, status, created_at
FROM pages
ORDER BY created_at DESC
LIMIT 10;
```
