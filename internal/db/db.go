package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/Napageneral/cortex/internal/config"
)

//go:embed schema.sql
var schemaSQL string

// Init initializes the database and creates tables if needed
func Init() error {
	dataDir, err := config.GetDataDir()
	if err != nil {
		return err
	}

	dbPath := filepath.Join(dataDir, "cortex.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	// SQLite behaves best with a single connection per process.
	// Multiple connections can contend for the write lock and cause SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Pragmas for performance + concurrency.
	// WAL allows concurrent readers while a writer is active.
	// busy_timeout reduces SQLITE_BUSY errors under contention.
	_, _ = db.Exec("PRAGMA journal_mode = WAL")
	_, _ = db.Exec("PRAGMA synchronous = NORMAL")
	_, _ = db.Exec("PRAGMA busy_timeout = 30000")
	_, _ = db.Exec("PRAGMA foreign_keys = ON")

	// Execute schema
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	return nil
}

// Open opens a connection to the database
func Open() (*sql.DB, error) {
	dataDir, err := config.GetDataDir()
	if err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dataDir, "cortex.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// SQLite behaves best with a single connection per process.
	// Multiple connections can contend for the write lock and cause SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Pragmas for performance + concurrency.
	// WAL allows concurrent readers while a writer is active.
	// busy_timeout reduces SQLITE_BUSY errors under contention.
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA synchronous = NORMAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set synchronous: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 30000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set busy_timeout: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	return db, nil
}

// GetPath returns the path to the database file
func GetPath() (string, error) {
	dataDir, err := config.GetDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, "cortex.db"), nil
}
