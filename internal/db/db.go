package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Napageneral/cortex/internal/config"
	"github.com/Napageneral/cortex/internal/contacts"
	"github.com/google/uuid"
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

	// Ensure legacy columns exist before applying schema (prevents index errors).
	if err := ensureLegacyColumns(db); err != nil {
		return err
	}

	// Execute schema
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	if err := migrateContactPersonSplit(db); err != nil {
		return err
	}
	if err := ensureEventParticipantIndexes(db); err != nil {
		return err
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

func ensureLegacyColumns(db *sql.DB) error {
	// Columns added after earlier schema versions.
	if err := ensureColumn(db, "person_facts", "source_episode_id", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(db, "unattributed_facts", "source_episode_id", "TEXT REFERENCES episodes(id)"); err != nil {
		return err
	}
	if err := ensureColumn(db, "candidate_mentions", "source_episode_id", "TEXT REFERENCES episodes(id)"); err != nil {
		return err
	}
	// Add is_group column to threads table (for group vs 1:1 chat detection)
	if err := ensureColumn(db, "threads", "is_group", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return nil
}

func ensureColumn(db *sql.DB, table, column, definition string) error {
	if !tableExists(db, table) {
		return nil
	}
	has, err := columnExists(db, table, column)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	if err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
}

func ensureEventParticipantIndexes(db *sql.DB) error {
	if !tableExists(db, "event_participants") {
		return nil
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_event_participants_event ON event_participants(event_id)`); err != nil {
		return fmt.Errorf("create event_participants event index: %w", err)
	}
	hasContactID, err := columnExists(db, "event_participants", "contact_id")
	if err != nil {
		return fmt.Errorf("check event_participants contact_id: %w", err)
	}
	if !hasContactID {
		return nil
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_event_participants_contact ON event_participants(contact_id)`); err != nil {
		return fmt.Errorf("create event_participants contact index: %w", err)
	}
	return nil
}

func tableExists(db *sql.DB, table string) bool {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	return err == nil
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func migrateContactPersonSplit(db *sql.DB) error {
	if !tableExists(db, "event_participants") {
		return nil
	}
	hasContactID, err := columnExists(db, "event_participants", "contact_id")
	if err != nil {
		return fmt.Errorf("check event_participants columns: %w", err)
	}
	if hasContactID {
		return nil
	}
	hasPersonID, err := columnExists(db, "event_participants", "person_id")
	if err != nil {
		return fmt.Errorf("check event_participants legacy columns: %w", err)
	}
	if !hasPersonID {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin contact migration: %w", err)
	}
	defer tx.Rollback()
	_, _ = tx.Exec("PRAGMA defer_foreign_keys = ON")

	_, err = tx.Exec(`CREATE TEMP TABLE IF NOT EXISTS person_contact_map (
		person_id TEXT PRIMARY KEY,
		contact_id TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create temp contact map: %w", err)
	}

	personToContact := make(map[string]string)

	rows, err := tx.Query(`
		SELECT i.person_id, i.channel, i.identifier, p.canonical_name, p.display_name, p.is_me
		FROM identities i
		JOIN persons p ON p.id = i.person_id
		ORDER BY i.created_at
	`)
	if err != nil {
		return fmt.Errorf("load identities: %w", err)
	}
	for rows.Next() {
		var personID, channel, identifier, canonicalName string
		var displayName sql.NullString
		var isMe int
		if err := rows.Scan(&personID, &channel, &identifier, &canonicalName, &displayName, &isMe); err != nil {
			rows.Close()
			return fmt.Errorf("scan identity row: %w", err)
		}
		name := canonicalName
		if displayName.Valid && strings.TrimSpace(displayName.String) != "" {
			name = displayName.String
		}
		contactType := channel
		switch channel {
		case "aix":
			contactType = "human"
		case "ai":
			contactType = "ai"
		}
		contactID, _, err := contacts.GetOrCreateContact(tx, contactType, identifier, name, "migration")
		if err != nil {
			rows.Close()
			return fmt.Errorf("create contact for identity: %w", err)
		}
		if _, ok := personToContact[personID]; !ok {
			personToContact[personID] = contactID
		}
		if isMe == 1 || contacts.IsMeaningfulPersonName(name) {
			if err := contacts.EnsurePersonContactLink(tx, personID, contactID, "migration", 1.0); err != nil {
				rows.Close()
				return fmt.Errorf("link person to contact: %w", err)
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate identities: %w", err)
	}

	personRows, err := tx.Query(`
		SELECT id, canonical_name, display_name, is_me
		FROM persons
	`)
	if err != nil {
		return fmt.Errorf("load persons: %w", err)
	}
	for personRows.Next() {
		var personID, canonicalName string
		var displayName sql.NullString
		var isMe int
		if err := personRows.Scan(&personID, &canonicalName, &displayName, &isMe); err != nil {
			personRows.Close()
			return fmt.Errorf("scan person row: %w", err)
		}
		if _, ok := personToContact[personID]; ok {
			continue
		}
		name := canonicalName
		if displayName.Valid && strings.TrimSpace(displayName.String) != "" {
			name = displayName.String
		}
		if strings.TrimSpace(name) == "" {
			name = "Unknown Contact"
		}
		contactID := uuid.New().String()
		now := time.Now().Unix()
		if _, err := tx.Exec(`
			INSERT INTO contacts (id, display_name, source, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
		`, contactID, name, "migration", now, now); err != nil {
			personRows.Close()
			return fmt.Errorf("insert fallback contact: %w", err)
		}
		personToContact[personID] = contactID
		if isMe == 1 || contacts.IsMeaningfulPersonName(name) {
			if err := contacts.EnsurePersonContactLink(tx, personID, contactID, "migration", 1.0); err != nil {
				personRows.Close()
				return fmt.Errorf("link fallback contact: %w", err)
			}
		}
	}
	personRows.Close()
	if err := personRows.Err(); err != nil {
		return fmt.Errorf("iterate persons: %w", err)
	}

	for personID, contactID := range personToContact {
		if _, err := tx.Exec(`
			INSERT INTO person_contact_map (person_id, contact_id)
			VALUES (?, ?)
			ON CONFLICT(person_id) DO NOTHING
		`, personID, contactID); err != nil {
			return fmt.Errorf("seed contact map: %w", err)
		}
	}

	if _, err := tx.Exec(`ALTER TABLE event_participants RENAME TO event_participants_old`); err != nil {
		return fmt.Errorf("rename event_participants: %w", err)
	}
	if _, err := tx.Exec(`
		CREATE TABLE event_participants (
			event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
			contact_id TEXT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			PRIMARY KEY (event_id, contact_id, role)
		)
	`); err != nil {
		return fmt.Errorf("create new event_participants: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO event_participants (event_id, contact_id, role)
		SELECT ep.event_id, pcm.contact_id, ep.role
		FROM event_participants_old ep
		JOIN person_contact_map pcm ON pcm.person_id = ep.person_id
		ON CONFLICT(event_id, contact_id, role) DO NOTHING
	`); err != nil {
		return fmt.Errorf("backfill event_participants: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE event_participants_old`); err != nil {
		return fmt.Errorf("drop old event_participants: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_event_participants_event ON event_participants(event_id)`); err != nil {
		return fmt.Errorf("create event_participants index: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_event_participants_contact ON event_participants(contact_id)`); err != nil {
		return fmt.Errorf("create event_participants index: %w", err)
	}

	if _, err := tx.Exec(`
		DELETE FROM entity_aliases
		WHERE alias_type IN ('email', 'phone', 'handle', 'username')
	`); err != nil {
		return fmt.Errorf("delete contact aliases: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit contact migration: %w", err)
	}
	return nil
}
