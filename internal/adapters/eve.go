package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// EveAdapter syncs iMessage events from Eve's database
type EveAdapter struct {
	eveDBPath string
}

// NewEveAdapter creates a new Eve adapter
func NewEveAdapter() (*EveAdapter, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	eveDBPath := filepath.Join(home, "Library", "Application Support", "Eve", "eve.db")
	if _, err := os.Stat(eveDBPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("Eve database not found at %s", eveDBPath)
	}

	return &EveAdapter{
		eveDBPath: eveDBPath,
	}, nil
}

func (e *EveAdapter) Name() string {
	return "imessage"
}

func (e *EveAdapter) Sync(ctx context.Context, commsDB *sql.DB, full bool) (SyncResult, error) {
	startTime := time.Now()
	result := SyncResult{}

	// Open Eve database (read-only)
	eveDB, err := sql.Open("sqlite", "file:"+e.eveDBPath+"?mode=ro")
	if err != nil {
		return result, fmt.Errorf("failed to open Eve database: %w", err)
	}
	defer eveDB.Close()

	// Enable foreign keys on comms DB
	if _, err := commsDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return result, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Get sync watermark (last synced timestamp)
	var lastSyncTimestamp int64
	if !full {
		row := commsDB.QueryRow("SELECT last_sync_at FROM sync_watermarks WHERE adapter = ?", e.Name())
		if err := row.Scan(&lastSyncTimestamp); err != nil && err != sql.ErrNoRows {
			return result, fmt.Errorf("failed to get sync watermark: %w", err)
		}
	}

	// Sync contacts first (to establish person/identity mappings)
	personsCreated, err := e.syncContacts(ctx, eveDB, commsDB)
	if err != nil {
		return result, fmt.Errorf("failed to sync contacts: %w", err)
	}
	result.PersonsCreated = personsCreated

	// Sync messages
	eventsCreated, eventsUpdated, maxImportedTimestamp, err := e.syncMessages(ctx, eveDB, commsDB, lastSyncTimestamp)
	if err != nil {
		return result, fmt.Errorf("failed to sync messages: %w", err)
	}
	result.EventsCreated = eventsCreated
	result.EventsUpdated = eventsUpdated

	// Update sync watermark
	// IMPORTANT: use the max imported event timestamp, NOT wall-clock time.
	// This avoids skipping late-arriving/backfilled messages whose timestamp is older than "now".
	watermark := lastSyncTimestamp
	if maxImportedTimestamp > watermark {
		watermark = maxImportedTimestamp
	}
	_, err = commsDB.Exec(`
		INSERT INTO sync_watermarks (adapter, last_sync_at)
		VALUES (?, ?)
		ON CONFLICT(adapter) DO UPDATE SET last_sync_at = excluded.last_sync_at
	`, e.Name(), watermark)
	if err != nil {
		return result, fmt.Errorf("failed to update sync watermark: %w", err)
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

// syncContacts syncs Eve contacts to comms persons and identities
func (e *EveAdapter) syncContacts(ctx context.Context, eveDB, commsDB *sql.DB) (int, error) {
	// Query contacts and their identifiers from Eve
	rows, err := eveDB.Query(`
		SELECT
			c.id,
			c.name,
			c.nickname,
			ci.identifier,
			ci.type
		FROM contacts c
		LEFT JOIN contact_identifiers ci ON c.id = ci.contact_id
		WHERE c.is_me = 0
		ORDER BY c.id
	`)
	if err != nil {
		return 0, fmt.Errorf("failed to query Eve contacts: %w", err)
	}
	defer rows.Close()

	personsCreated := 0
	contactMap := make(map[int64]string) // Eve contact_id -> comms person_id

	for rows.Next() {
		var eveContactID int64
		var name, nickname sql.NullString
		var identifier, identifierType sql.NullString

		if err := rows.Scan(&eveContactID, &name, &nickname, &identifier, &identifierType); err != nil {
			return personsCreated, fmt.Errorf("failed to scan contact row: %w", err)
		}

		// Determine canonical name
		canonicalName := "Unknown"
		if name.Valid && name.String != "" {
			canonicalName = name.String
		} else if nickname.Valid && nickname.String != "" {
			canonicalName = nickname.String
		} else if identifier.Valid && identifier.String != "" {
			canonicalName = identifier.String
		}

		// Check if person already exists (by identifier or by name)
		var personID string
		if identifier.Valid && identifierType.Valid {
			// Try to find person by identifier
			row := commsDB.QueryRow(`
				SELECT person_id FROM identities
				WHERE channel = ? AND identifier = ?
			`, identifierType.String, identifier.String)
			if err := row.Scan(&personID); err != nil && err != sql.ErrNoRows {
				return personsCreated, fmt.Errorf("failed to query identity: %w", err)
			}
		}

		// If not found, create new person
		if personID == "" {
			personID = uuid.New().String()
			now := time.Now().Unix()

			_, err := commsDB.Exec(`
				INSERT INTO persons (id, canonical_name, is_me, created_at, updated_at)
				VALUES (?, ?, 0, ?, ?)
			`, personID, canonicalName, now, now)
			if err != nil {
				// Person might already exist, try to find by name
				row := commsDB.QueryRow("SELECT id FROM persons WHERE canonical_name = ?", canonicalName)
				if err := row.Scan(&personID); err != nil {
					return personsCreated, fmt.Errorf("failed to insert/find person: %w", err)
				}
			} else {
				personsCreated++
			}
		}

		contactMap[eveContactID] = personID

		// Add identity if we have an identifier
		if identifier.Valid && identifierType.Valid {
			identityID := uuid.New().String()
			now := time.Now().Unix()

			_, err := commsDB.Exec(`
				INSERT INTO identities (id, person_id, channel, identifier, created_at)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(channel, identifier) DO NOTHING
			`, identityID, personID, identifierType.String, identifier.String, now)
			if err != nil {
				return personsCreated, fmt.Errorf("failed to insert identity: %w", err)
			}
		}
	}

	return personsCreated, rows.Err()
}

// syncMessages syncs Eve messages to comms events
func (e *EveAdapter) syncMessages(ctx context.Context, eveDB, commsDB *sql.DB, lastSyncTimestamp int64) (int, int, int64, error) {
	// Query messages from Eve
	query := `
		SELECT
			m.id,
			m.guid,
			m.chat_id,
			m.sender_id,
			m.content,
			CAST(strftime('%s', m.timestamp) AS INTEGER) as timestamp_unix,
			m.is_from_me,
			m.service_name,
			m.reply_to_guid,
			COALESCE(c.chat_identifier, printf('chat_id:%d', m.chat_id)) as thread_id,
			(SELECT COUNT(*) FROM attachments a WHERE a.message_id = m.id) as attachment_count
		FROM messages m
		LEFT JOIN chats c ON m.chat_id = c.id
		WHERE CAST(strftime('%s', m.timestamp) AS INTEGER) > ?
		ORDER BY timestamp_unix ASC
	`

	rows, err := eveDB.Query(query, lastSyncTimestamp)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to query Eve messages: %w", err)
	}
	defer rows.Close()

	eventsCreated := 0
	eventsUpdated := 0
	maxImportedTimestamp := int64(0)

	for rows.Next() {
		var messageID int64
		var guid, chatIdentifier string
		var chatID, senderID sql.NullInt64
		var content, serviceName, replyToGuid sql.NullString
		var timestamp int64
		var isFromMe bool
		var attachmentCount int

		if err := rows.Scan(&messageID, &guid, &chatID, &senderID, &content, &timestamp, &isFromMe, &serviceName, &replyToGuid, &chatIdentifier, &attachmentCount); err != nil {
			return eventsCreated, eventsUpdated, maxImportedTimestamp, fmt.Errorf("failed to scan message row: %w", err)
		}

		if timestamp > maxImportedTimestamp {
			maxImportedTimestamp = timestamp
		}

		// Build content types array
		contentTypes := []string{}
		if content.Valid && content.String != "" {
			contentTypes = append(contentTypes, "text")
		}
		if attachmentCount > 0 {
			contentTypes = append(contentTypes, "attachment")
		}
		if len(contentTypes) == 0 {
			contentTypes = append(contentTypes, "text") // Default
		}

		contentTypesJSON, err := json.Marshal(contentTypes)
		if err != nil {
			return eventsCreated, eventsUpdated, maxImportedTimestamp, fmt.Errorf("failed to marshal content types: %w", err)
		}

		// Determine direction
		direction := "received"
		if isFromMe {
			direction = "sent"
		}

		// Create event
		eventID := uuid.New().String()
		_, err = commsDB.Exec(`
			INSERT INTO events (
				id, timestamp, channel, content_types, content,
				direction, thread_id, reply_to, source_adapter, source_id
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_adapter, source_id) DO UPDATE SET
				content = excluded.content,
				content_types = excluded.content_types,
				thread_id = excluded.thread_id,
				reply_to = excluded.reply_to
		`, eventID, timestamp, "imessage", string(contentTypesJSON), content.String,
			direction, chatIdentifier, replyToGuid.String, e.Name(), guid)

		if err != nil {
			return eventsCreated, eventsUpdated, maxImportedTimestamp, fmt.Errorf("failed to insert/update event: %w", err)
		}

		// Check if this was an insert or update
		var existingEventID string
		row := commsDB.QueryRow("SELECT id FROM events WHERE source_adapter = ? AND source_id = ?", e.Name(), guid)
		if err := row.Scan(&existingEventID); err == nil {
			if existingEventID == eventID {
				eventsCreated++
			} else {
				eventsUpdated++
				eventID = existingEventID
			}
		}

		// Add event participants
		if senderID.Valid {
			// Find person_id for this Eve contact
			var personID string

			// Try to find via contact_identifiers
			row := eveDB.QueryRow(`
				SELECT ci.identifier, ci.type
				FROM contact_identifiers ci
				WHERE ci.contact_id = ?
				LIMIT 1
			`, senderID.Int64)

			var identifier, identifierType sql.NullString
			if err := row.Scan(&identifier, &identifierType); err == nil && identifier.Valid && identifierType.Valid {
				// Look up person by identifier in comms
				row := commsDB.QueryRow(`
					SELECT person_id FROM identities
					WHERE channel = ? AND identifier = ?
				`, identifierType.String, identifier.String)
				if err := row.Scan(&personID); err == nil {
					// Determine role based on direction
					role := "sender"
					if !isFromMe {
						role = "sender" // They sent it to me
					} else {
						role = "recipient" // I sent it to them (though in Eve this is less clear)
					}

					_, err = commsDB.Exec(`
						INSERT INTO event_participants (event_id, person_id, role)
						VALUES (?, ?, ?)
						ON CONFLICT(event_id, person_id, role) DO NOTHING
					`, eventID, personID, role)
					if err != nil {
						return eventsCreated, eventsUpdated, maxImportedTimestamp, fmt.Errorf("failed to insert event participant: %w", err)
					}
				}
			}
		}

		// If message is from me, add me as sender
		if isFromMe {
			var mePersonID string
			row := commsDB.QueryRow("SELECT id FROM persons WHERE is_me = 1")
			if err := row.Scan(&mePersonID); err == nil {
				_, err = commsDB.Exec(`
					INSERT INTO event_participants (event_id, person_id, role)
					VALUES (?, ?, ?)
					ON CONFLICT(event_id, person_id, role) DO NOTHING
				`, eventID, mePersonID, "sender")
				if err != nil {
					return eventsCreated, eventsUpdated, maxImportedTimestamp, fmt.Errorf("failed to insert me as sender: %w", err)
				}
			}
		}
	}

	if err := rows.Err(); err != nil {
		return eventsCreated, eventsUpdated, maxImportedTimestamp, err
	}
	return eventsCreated, eventsUpdated, maxImportedTimestamp, nil
}
