package store

import (
	"database/sql"
	"embed"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"

	_ "modernc.org/sqlite" // SQLite driver for database/sql
)

//go:embed migrations/*.sql
var migrations embed.FS

// DBConfig holds database configuration options.
type DBConfig struct {
	// MaxOpenConns is the maximum number of open connections to the database.
	// For SQLite, this is typically 1 for writes but can be higher for reads with WAL mode.
	MaxOpenConns int
	// MaxIdleConns is the maximum number of connections in the idle connection pool.
	MaxIdleConns int
	// ConnMaxLifetime is the maximum amount of time a connection may be reused.
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime is the maximum amount of time a connection may be idle.
	ConnMaxIdleTime time.Duration
}

// DefaultDBConfig returns sensible defaults for SQLite.
func DefaultDBConfig() DBConfig {
	return DBConfig{
		// SQLite with WAL mode supports multiple readers but single writer
		// Setting higher for read-heavy workloads
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 30 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}
}

// NewDB opens a SQLite database connection and configures it for optimal performance.
func NewDB(path string) (*sql.DB, error) {
	return NewDBWithConfig(path, DefaultDBConfig())
}

// NewDBWithConfig opens a SQLite database connection with custom configuration.
func NewDBWithConfig(path string, cfg DBConfig) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	// Configure SQLite for better performance and concurrency
	pragmas := []string{
		"PRAGMA journal_mode=WAL",        // Write-Ahead Logging for better concurrency
		"PRAGMA busy_timeout=5000",       // Wait 5s when database is locked
		"PRAGMA synchronous=NORMAL",      // Good balance of safety and speed
		"PRAGMA cache_size=-64000",       // 64MB cache
		"PRAGMA foreign_keys=ON",         // Enforce foreign key constraints
		"PRAGMA temp_store=MEMORY",       // Store temp tables in memory
		"PRAGMA mmap_size=268435456",     // 256MB memory-mapped I/O
		"PRAGMA page_size=4096",          // 4KB page size (standard)
		"PRAGMA wal_autocheckpoint=1000", // Auto checkpoint every 1000 pages
		"PRAGMA optimize",                // Run query planner optimizations
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("setting pragma %q: %w", pragma, err)
		}
	}

	// Verify connection
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return db, nil
}

// Migrate runs all pending database migrations.
func Migrate(db *sql.DB) error {
	goose.SetBaseFS(migrations)

	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("setting dialect: %w", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	return nil
}
