// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

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
	// Pass pragmas via DSN so they apply to every connection in the pool,
	// not just the first one. Without this, new pooled connections lack
	// busy_timeout and journal_mode, causing SQLITE_BUSY under load.
	dsn := path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=cache_size(-64000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=temp_store(MEMORY)" +
		"&_pragma=mmap_size(268435456)" +
		"&_pragma=page_size(4096)" +
		"&_pragma=wal_autocheckpoint(1000)" +
		"&_txlock=immediate"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	// Run one-time optimizations on the first connection
	if _, err := db.Exec("PRAGMA optimize"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("running PRAGMA optimize: %w", err)
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
