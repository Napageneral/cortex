package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/Napageneral/comms/internal/config"
)

//go:embed schema.sql
var schemaSQL string

// Init initializes the database and creates tables if needed
func Init() error {
	dataDir, err := config.GetDataDir()
	if err != nil {
		return err
	}

	dbPath := filepath.Join(dataDir, "comms.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

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

	dbPath := filepath.Join(dataDir, "comms.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable foreign keys
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
	return filepath.Join(dataDir, "comms.db"), nil
}
