package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// AixAdapter syncs AI session events from aix's SQLite database (Cursor sessions, etc.)
type AixAdapter struct {
	source string // cursor, codex, opencode, ...
	dbPath string
}

// NewAixAdapter creates a new Aix adapter for a given source.
// Currently supported: source="cursor" (others can be added later).
func NewAixAdapter(source string) (*AixAdapter, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("aix adapter requires source (e.g. cursor)")
	}

	dbPath, err := defaultAixDBPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("aix database not found at %s (run aix sync --all first): %w", dbPath, err)
	}

	return &AixAdapter{
		source: source,
		dbPath: dbPath,
	}, nil
}

func (a *AixAdapter) Name() string {
	// Keep this stable + human friendly; also used as source_adapter and watermark key.
	// If we add more AI sources later, they'll get their own adapter names (codex, opencode, etc.).
	return a.source
}

func (a *AixAdapter) Sync(ctx context.Context, commsDB *sql.DB, full bool) (SyncResult, error) {
	start := time.Now()
	var result SyncResult

	// Open aix database (read-only)
	aixDB, err := sql.Open("sqlite", "file:"+a.dbPath+"?mode=ro")
	if err != nil {
		return result, fmt.Errorf("failed to open aix database: %w", err)
	}
	defer aixDB.Close()

	// Enable foreign keys on comms DB
	if _, err := commsDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return result, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Get sync watermark (seconds)
	var lastSync int64
	if !full {
		row := commsDB.QueryRow("SELECT last_sync_at FROM sync_watermarks WHERE adapter = ?", a.Name())
		if err := row.Scan(&lastSync); err != nil && err != sql.ErrNoRows {
			return result, fmt.Errorf("failed to get sync watermark: %w", err)
		}
	}

	// Look up me person if present (optional).
	var mePersonID string
	_ = commsDB.QueryRow("SELECT id FROM persons WHERE is_me = 1 LIMIT 1").Scan(&mePersonID)

	// Ensure "me" has an identity that indicates presence on this source (helps cross-platform views).
	if mePersonID != "" {
		if err := a.ensureMeIdentity(commsDB, mePersonID); err != nil {
			return result, err
		}
	}

	// Cache AI persons per model to avoid repeated DB round-trips.
	aiByModel := make(map[string]string) // modelKey -> personID

	eventsCreated, eventsUpdated, maxTS, personsCreated, err := a.syncMessages(ctx, aixDB, commsDB, lastSync, mePersonID, aiByModel)
	if err != nil {
		return result, err
	}
	result.EventsCreated = eventsCreated
	result.EventsUpdated = eventsUpdated
	result.PersonsCreated += personsCreated

	// Update watermark to max imported event timestamp (seconds)
	watermark := lastSync
	if maxTS > watermark {
		watermark = maxTS
	}
	_, err = commsDB.Exec(`
		INSERT INTO sync_watermarks (adapter, last_sync_at, last_event_id)
		VALUES (?, ?, NULL)
		ON CONFLICT(adapter) DO UPDATE SET last_sync_at = excluded.last_sync_at
	`, a.Name(), watermark)
	if err != nil {
		return result, fmt.Errorf("failed to update sync watermark: %w", err)
	}

	result.Duration = time.Since(start)
	return result, nil
}

func (a *AixAdapter) ensureMeIdentity(commsDB *sql.DB, mePersonID string) error {
	// This is intentionally coarse; Eve whoami is the canonical rich identity seed.
	identityChannel := "aix"
	identityIdentifier := fmt.Sprintf("aix:%s:user", a.source)
	now := time.Now().Unix()
	identityID := uuid.New().String()
	_, err := commsDB.Exec(`
		INSERT INTO identities (id, person_id, channel, identifier, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel, identifier) DO UPDATE SET person_id = excluded.person_id
	`, identityID, mePersonID, identityChannel, identityIdentifier, now)
	if err != nil {
		return fmt.Errorf("upsert me aix identity: %w", err)
	}
	return nil
}

func (a *AixAdapter) getOrCreateAIPerson(commsDB *sql.DB, modelKey string) (personID string, created bool, err error) {
	// Map each (source, model) to a stable identity key.
	identityChannel := "ai"
	identityIdentifier := fmt.Sprintf("aix:%s:model:%s", a.source, modelKey)

	// Try lookup first
	row := commsDB.QueryRow(`SELECT person_id FROM identities WHERE channel = ? AND identifier = ?`, identityChannel, identityIdentifier)
	if err := row.Scan(&personID); err == nil {
		return personID, false, nil
	} else if err != sql.ErrNoRows {
		return "", false, fmt.Errorf("failed to query ai identity: %w", err)
	}

	// Create person + identity
	personID = uuid.New().String()
	now := time.Now().Unix()
	canonicalName := "AI Assistant"
	sourceTitle := strings.ToUpper(a.source[:1]) + a.source[1:]
	displayName := fmt.Sprintf("%s AI (%s)", sourceTitle, modelKey)

	tx, err := commsDB.Begin()
	if err != nil {
		return "", false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO persons (id, canonical_name, display_name, is_me, created_at, updated_at)
		VALUES (?, ?, ?, 0, ?, ?)
	`, personID, canonicalName, displayName, now, now)
	if err != nil {
		return "", false, fmt.Errorf("insert ai person: %w", err)
	}

	identityID := uuid.New().String()
	_, err = tx.Exec(`
		INSERT INTO identities (id, person_id, channel, identifier, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel, identifier) DO NOTHING
	`, identityID, personID, identityChannel, identityIdentifier, now)
	if err != nil {
		return "", false, fmt.Errorf("insert ai identity: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("commit tx: %w", err)
	}
	return personID, true, nil
}

func (a *AixAdapter) syncMessages(
	ctx context.Context,
	aixDB *sql.DB,
	commsDB *sql.DB,
	lastSyncSeconds int64,
	mePersonID string,
	aiByModel map[string]string,
) (created int, updated int, maxImportedTS int64, personsCreated int, err error) {
	_ = ctx

	query := `
		SELECT
			m.id as message_id,
			m.session_id,
			m.role,
			m.content,
			CAST(COALESCE(m.timestamp, s.created_at) / 1000 AS INTEGER) as ts_sec,
			s.model
		FROM messages m
		JOIN sessions s ON m.session_id = s.id
		WHERE s.source = ?
		  AND CAST(COALESCE(m.timestamp, s.created_at) / 1000 AS INTEGER) > ?
		ORDER BY ts_sec ASC
	`

	rows, err := aixDB.Query(query, a.source, lastSyncSeconds)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("failed to query aix messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			messageID string
			sessionID string
			role      string
			content   sql.NullString
			tsSec     int64
			model     sql.NullString
		)
		if err := rows.Scan(&messageID, &sessionID, &role, &content, &tsSec, &model); err != nil {
			return created, updated, maxImportedTS, personsCreated, fmt.Errorf("scan aix message: %w", err)
		}
		if tsSec > maxImportedTS {
			maxImportedTS = tsSec
		}

		modelKey := "unknown"
		if model.Valid && strings.TrimSpace(model.String) != "" {
			modelKey = strings.TrimSpace(model.String)
		}

		aiPersonID, ok := aiByModel[modelKey]
		if !ok {
			pid, createdPerson, err := a.getOrCreateAIPerson(commsDB, modelKey)
			if err != nil {
				return created, updated, maxImportedTS, personsCreated, err
			}
			aiPersonID = pid
			aiByModel[modelKey] = pid
			if createdPerson {
				personsCreated++
			}
		}

		// Map to comms event semantics
		direction := "observed"
		switch role {
		case "user":
			direction = "sent"
		case "assistant":
			direction = "received"
		case "tool":
			direction = "observed"
		}

		contentTypesJSON, _ := json.Marshal([]string{"text"})
		threadID := fmt.Sprintf("aix_session:%s", sessionID)

		eventID := uuid.New().String()
		_, err = commsDB.Exec(`
			INSERT INTO events (
				id, timestamp, channel, content_types, content,
				direction, thread_id, reply_to, source_adapter, source_id
			) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)
			ON CONFLICT(source_adapter, source_id) DO UPDATE SET
				content = excluded.content,
				content_types = excluded.content_types,
				thread_id = excluded.thread_id
		`, eventID, tsSec, "cursor", string(contentTypesJSON), content.String, direction, threadID, a.Name(), messageID)
		if err != nil {
			return created, updated, maxImportedTS, personsCreated, fmt.Errorf("upsert event: %w", err)
		}

		// Determine if insert or update
		var existingEventID string
		row := commsDB.QueryRow("SELECT id FROM events WHERE source_adapter = ? AND source_id = ?", a.Name(), messageID)
		if err := row.Scan(&existingEventID); err == nil {
			if existingEventID == eventID {
				created++
			} else {
				updated++
				eventID = existingEventID
			}
		}

		// Participants
		if mePersonID != "" && aiPersonID != "" {
			switch role {
			case "user":
				_ = insertParticipant(commsDB, eventID, mePersonID, "sender")
				_ = insertParticipant(commsDB, eventID, aiPersonID, "recipient")
			case "assistant":
				_ = insertParticipant(commsDB, eventID, aiPersonID, "sender")
				_ = insertParticipant(commsDB, eventID, mePersonID, "recipient")
			default:
				_ = insertParticipant(commsDB, eventID, mePersonID, "observer")
				_ = insertParticipant(commsDB, eventID, aiPersonID, "observer")
			}
		}
	}

	if err := rows.Err(); err != nil {
		return created, updated, maxImportedTS, personsCreated, err
	}
	return created, updated, maxImportedTS, personsCreated, nil
}

func insertParticipant(db *sql.DB, eventID, personID, role string) error {
	_, err := db.Exec(`
		INSERT INTO event_participants (event_id, person_id, role)
		VALUES (?, ?, ?)
		ON CONFLICT(event_id, person_id, role) DO NOTHING
	`, eventID, personID, role)
	return err
}

func defaultAixDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "aix", "aix.db"), nil
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "aix", "aix.db"), nil
	}
	return filepath.Join(home, ".local", "share", "aix", "aix.db"), nil
}

