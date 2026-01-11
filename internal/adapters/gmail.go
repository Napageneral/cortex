package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// GmailAdapter syncs Gmail events via gogcli
type GmailAdapter struct {
	account string
}

// NewGmailAdapter creates a new Gmail adapter
func NewGmailAdapter(account string) (*GmailAdapter, error) {
	if account == "" {
		return nil, fmt.Errorf("account email is required for Gmail adapter")
	}

	// Verify gogcli is available
	if _, err := exec.LookPath("gog"); err != nil {
		return nil, fmt.Errorf("gogcli (gog) not found in PATH. Install with: brew install steipete/tap/gogcli")
	}

	return &GmailAdapter{
		account: account,
	}, nil
}

func (g *GmailAdapter) Name() string {
	return "gmail"
}

func (g *GmailAdapter) Sync(ctx context.Context, commsDB *sql.DB, full bool) (SyncResult, error) {
	startTime := time.Now()
	result := SyncResult{}

	// Enable foreign keys on comms DB
	if _, err := commsDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return result, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Get sync watermark (last synced date)
	var lastSyncTimestamp int64
	if !full {
		row := commsDB.QueryRow("SELECT last_sync_at FROM sync_watermarks WHERE adapter = ?", g.Name())
		if err := row.Scan(&lastSyncTimestamp); err != nil && err != sql.ErrNoRows {
			return result, fmt.Errorf("failed to get sync watermark: %w", err)
		}
	}

	// Build search query based on watermark
	searchQuery := "in:anywhere"
	if lastSyncTimestamp > 0 {
		// Convert Unix timestamp to date format for Gmail search
		lastSyncDate := time.Unix(lastSyncTimestamp, 0).Format("2006/01/02")
		searchQuery = fmt.Sprintf("after:%s", lastSyncDate)
	}

	// Fetch emails from gogcli
	threads, err := g.fetchThreads(ctx, searchQuery)
	if err != nil {
		return result, fmt.Errorf("failed to fetch threads: %w", err)
	}

	// Sync threads and messages
	for _, thread := range threads {
		eventsCreated, eventsUpdated, personsCreated, err := g.syncThread(ctx, commsDB, thread)
		if err != nil {
			// Log error but continue with other threads
			fmt.Printf("Warning: failed to sync thread %s: %v\n", thread.ID, err)
			continue
		}
		result.EventsCreated += eventsCreated
		result.EventsUpdated += eventsUpdated
		result.PersonsCreated += personsCreated
	}

	// Update sync watermark
	now := time.Now().Unix()
	_, err = commsDB.Exec(`
		INSERT INTO sync_watermarks (adapter, last_sync_at)
		VALUES (?, ?)
		ON CONFLICT(adapter) DO UPDATE SET last_sync_at = excluded.last_sync_at
	`, g.Name(), now)
	if err != nil {
		return result, fmt.Errorf("failed to update sync watermark: %w", err)
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

// GmailThread represents a Gmail thread from gogcli
type GmailThread struct {
	ID       string          `json:"id"`
	Snippet  string          `json:"snippet"`
	Messages []GmailMessage  `json:"messages"`
}

// GmailMessage represents a single message in a thread
type GmailMessage struct {
	ID            string            `json:"id"`
	ThreadID      string            `json:"threadId"`
	LabelIDs      []string          `json:"labelIds"`
	Snippet       string            `json:"snippet"`
	InternalDate  string            `json:"internalDate"` // Unix timestamp in milliseconds as string
	Payload       GmailPayload      `json:"payload"`
	SizeEstimate  int               `json:"sizeEstimate"`
}

// GmailPayload contains the message headers and body
type GmailPayload struct {
	PartID   string            `json:"partId"`
	MimeType string            `json:"mimeType"`
	Filename string            `json:"filename"`
	Headers  []GmailHeader     `json:"headers"`
	Body     GmailBody         `json:"body"`
	Parts    []GmailPayload    `json:"parts"`
}

// GmailHeader represents an email header
type GmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// GmailBody contains the message body
type GmailBody struct {
	Size int    `json:"size"`
	Data string `json:"data"`
}

// gogcliResponse wraps the response from gog gmail search
type gogcliResponse struct {
	Threads []GmailThread `json:"threads"`
}

// fetchThreads executes gogcli to fetch Gmail threads
func (g *GmailAdapter) fetchThreads(ctx context.Context, query string) ([]GmailThread, error) {
	// Execute gog gmail search with JSON output
	cmd := exec.CommandContext(ctx, "gog", "gmail", "search", query, "--json", "--max", "100")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gogcli command failed: %w (output: %s)", err, string(output))
	}

	// Parse JSON response
	var response gogcliResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("failed to parse gogcli JSON output: %w", err)
	}

	return response.Threads, nil
}

// syncThread syncs a single Gmail thread into the comms database
func (g *GmailAdapter) syncThread(ctx context.Context, commsDB *sql.DB, thread GmailThread) (int, int, int, error) {
	eventsCreated := 0
	eventsUpdated := 0
	personsCreated := 0

	for _, message := range thread.Messages {
		// Parse message timestamp (internalDate is Unix timestamp in milliseconds)
		var timestamp int64
		if _, err := fmt.Sscanf(message.InternalDate, "%d", &timestamp); err == nil {
			timestamp = timestamp / 1000 // Convert milliseconds to seconds
		} else {
			timestamp = time.Now().Unix()
		}

		// Extract headers
		headers := make(map[string]string)
		for _, h := range message.Payload.Headers {
			headers[strings.ToLower(h.Name)] = h.Value
		}

		from := headers["from"]
		to := headers["to"]
		cc := headers["cc"]
		subject := headers["subject"]

		// Extract body content
		body := g.extractBody(message.Payload)
		content := subject
		if body != "" {
			content = fmt.Sprintf("Subject: %s\n\n%s", subject, body)
		}

		// Build content types
		contentTypes := []string{"text"}
		if g.hasAttachments(message.Payload) {
			contentTypes = append(contentTypes, "attachment")
		}
		contentTypesJSON, _ := json.Marshal(contentTypes)

		// Determine direction based on SENT label
		direction := "received"
		for _, label := range message.LabelIDs {
			if label == "SENT" {
				direction = "sent"
				break
			}
		}

		// Create event
		eventID := uuid.New().String()
		_, err := commsDB.Exec(`
			INSERT INTO events (
				id, timestamp, channel, content_types, content,
				direction, thread_id, reply_to, source_adapter, source_id
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_adapter, source_id) DO UPDATE SET
				content = excluded.content,
				content_types = excluded.content_types
		`, eventID, timestamp, "gmail", string(contentTypesJSON), content,
			direction, thread.ID, "", g.Name(), message.ID)

		if err != nil {
			return eventsCreated, eventsUpdated, personsCreated, fmt.Errorf("failed to insert/update event: %w", err)
		}

		// Check if this was an insert or update
		var existingEventID string
		row := commsDB.QueryRow("SELECT id FROM events WHERE source_adapter = ? AND source_id = ?", g.Name(), message.ID)
		if err := row.Scan(&existingEventID); err == nil {
			if existingEventID == eventID {
				eventsCreated++
			} else {
				eventsUpdated++
				eventID = existingEventID
			}
		}

		// Process participants
		participantsCreated, err := g.syncParticipants(commsDB, eventID, from, to, cc, direction)
		if err != nil {
			return eventsCreated, eventsUpdated, personsCreated, fmt.Errorf("failed to sync participants: %w", err)
		}
		personsCreated += participantsCreated
	}

	return eventsCreated, eventsUpdated, personsCreated, nil
}

// extractBody extracts the text body from Gmail message payload
func (g *GmailAdapter) extractBody(payload GmailPayload) string {
	// Try to get body from main payload
	if payload.Body.Size > 0 && payload.Body.Data != "" {
		return payload.Body.Data
	}

	// Check parts for text/plain or text/html
	for _, part := range payload.Parts {
		if strings.HasPrefix(part.MimeType, "text/") && part.Body.Size > 0 {
			return part.Body.Data
		}
		// Recursively check nested parts
		if len(part.Parts) > 0 {
			body := g.extractBody(part)
			if body != "" {
				return body
			}
		}
	}

	return ""
}

// hasAttachments checks if the message has attachments
func (g *GmailAdapter) hasAttachments(payload GmailPayload) bool {
	if payload.Filename != "" {
		return true
	}
	for _, part := range payload.Parts {
		if part.Filename != "" || g.hasAttachments(part) {
			return true
		}
	}
	return false
}

// syncParticipants creates persons and identities for email participants
func (g *GmailAdapter) syncParticipants(commsDB *sql.DB, eventID, from, to, cc, direction string) (int, error) {
	personsCreated := 0

	// Parse and add sender
	if from != "" {
		fromEmails := parseEmailAddresses(from)
		for _, email := range fromEmails {
			personID, created, err := g.getOrCreatePersonByEmail(commsDB, email)
			if err != nil {
				return personsCreated, err
			}
			if created {
				personsCreated++
			}

			// Add as sender
			role := "sender"
			_, err = commsDB.Exec(`
				INSERT INTO event_participants (event_id, person_id, role)
				VALUES (?, ?, ?)
				ON CONFLICT(event_id, person_id, role) DO NOTHING
			`, eventID, personID, role)
			if err != nil {
				return personsCreated, fmt.Errorf("failed to insert sender participant: %w", err)
			}
		}
	}

	// Parse and add recipients
	if to != "" {
		toEmails := parseEmailAddresses(to)
		for _, email := range toEmails {
			personID, created, err := g.getOrCreatePersonByEmail(commsDB, email)
			if err != nil {
				return personsCreated, err
			}
			if created {
				personsCreated++
			}

			// Add as recipient
			_, err = commsDB.Exec(`
				INSERT INTO event_participants (event_id, person_id, role)
				VALUES (?, ?, ?)
				ON CONFLICT(event_id, person_id, role) DO NOTHING
			`, eventID, personID, "recipient")
			if err != nil {
				return personsCreated, fmt.Errorf("failed to insert recipient participant: %w", err)
			}
		}
	}

	// Parse and add CC recipients
	if cc != "" {
		ccEmails := parseEmailAddresses(cc)
		for _, email := range ccEmails {
			personID, created, err := g.getOrCreatePersonByEmail(commsDB, email)
			if err != nil {
				return personsCreated, err
			}
			if created {
				personsCreated++
			}

			// Add as CC
			_, err = commsDB.Exec(`
				INSERT INTO event_participants (event_id, person_id, role)
				VALUES (?, ?, ?)
				ON CONFLICT(event_id, person_id, role) DO NOTHING
			`, eventID, personID, "cc")
			if err != nil {
				return personsCreated, fmt.Errorf("failed to insert CC participant: %w", err)
			}
		}
	}

	return personsCreated, nil
}

// getOrCreatePersonByEmail finds or creates a person by email address
func (g *GmailAdapter) getOrCreatePersonByEmail(commsDB *sql.DB, email string) (string, bool, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", false, fmt.Errorf("empty email address")
	}

	// Try to find existing person by email identity
	var personID string
	row := commsDB.QueryRow(`
		SELECT person_id FROM identities
		WHERE channel = 'email' AND identifier = ?
	`, email)
	if err := row.Scan(&personID); err == nil {
		return personID, false, nil
	} else if err != sql.ErrNoRows {
		return "", false, fmt.Errorf("failed to query identity: %w", err)
	}

	// Person doesn't exist, create new one
	personID = uuid.New().String()
	now := time.Now().Unix()

	// Use email as canonical name (user can update later with display name)
	_, err := commsDB.Exec(`
		INSERT INTO persons (id, canonical_name, is_me, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?)
	`, personID, email, now, now)
	if err != nil {
		return "", false, fmt.Errorf("failed to insert person: %w", err)
	}

	// Create identity
	identityID := uuid.New().String()
	_, err = commsDB.Exec(`
		INSERT INTO identities (id, person_id, channel, identifier, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel, identifier) DO NOTHING
	`, identityID, personID, "email", email, now)
	if err != nil {
		return "", false, fmt.Errorf("failed to insert identity: %w", err)
	}

	return personID, true, nil
}

// parseEmailAddresses parses a comma-separated list of email addresses
// Handles formats like: "Name <email@example.com>, email2@example.com"
func parseEmailAddresses(s string) []string {
	var emails []string
	parts := strings.Split(s, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Extract email from "Name <email>" format
		if idx := strings.Index(part, "<"); idx >= 0 {
			endIdx := strings.Index(part[idx:], ">")
			if endIdx > 0 {
				email := strings.TrimSpace(part[idx+1 : idx+endIdx])
				emails = append(emails, email)
				continue
			}
		}

		// Plain email address
		emails = append(emails, part)
	}
	return emails
}
